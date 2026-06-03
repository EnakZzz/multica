package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type feishuDB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type FeishuIssueService struct {
	queries *db.Queries
	db      feishuDB

	appID             string
	appSecret         string
	baseURL           string
	publicURL         string
	verificationToken string
	httpClient        *http.Client

	mu          sync.Mutex
	tenantToken string
	tokenExpiry time.Time
}

type FeishuMessageBinding struct {
	WorkspaceID string
	IssueID     string
	InboxItemID string
	RecipientID string
	MessageID   string
	RootID      string
	ChatID      string
}

type feishuAPIResponse[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

func NewFeishuIssueServiceFromEnv(queries *db.Queries, executor any) *FeishuIssueService {
	appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	appSecret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	if appID == "" || appSecret == "" {
		return nil
	}
	dbExec, _ := executor.(feishuDB)
	if queries == nil || dbExec == nil {
		return nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("FEISHU_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = "https://open.feishu.cn"
	}
	return &FeishuIssueService{
		queries:           queries,
		db:                dbExec,
		appID:             appID,
		appSecret:         appSecret,
		baseURL:           baseURL,
		publicURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("MULTICA_PUBLIC_URL")), "/"),
		verificationToken: strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_TOKEN")),
		httpClient:        &http.Client{Timeout: 12 * time.Second},
	}
}

func (s *FeishuIssueService) Enabled() bool { return s != nil }

func (s *FeishuIssueService) VerificationToken() string {
	if s == nil {
		return ""
	}
	return s.verificationToken
}

func (s *FeishuIssueService) tenantAccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.tenantToken != "" && time.Now().Before(s.tokenExpiry) {
		token := s.tenantToken
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()

	body, err := json.Marshal(map[string]string{
		"app_id":     s.appID,
		"app_secret": s.appSecret,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("feishu tenant token request: %w", err)
	}
	defer resp.Body.Close()
	var parsed struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int64  `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("feishu tenant token decode: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || parsed.Code != 0 || parsed.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu tenant token failed: status=%d code=%d msg=%s", resp.StatusCode, parsed.Code, parsed.Msg)
	}
	expiresIn := time.Duration(parsed.Expire) * time.Second
	if expiresIn <= time.Minute {
		expiresIn = time.Minute
	} else {
		expiresIn -= time.Minute
	}
	s.mu.Lock()
	s.tenantToken = parsed.TenantAccessToken
	s.tokenExpiry = time.Now().Add(expiresIn)
	s.mu.Unlock()
	return parsed.TenantAccessToken, nil
}

func (s *FeishuIssueService) request(ctx context.Context, method, path string, query url.Values, in any, out any) error {
	token, err := s.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	var body []byte
	if in != nil {
		body, err = json.Marshal(in)
		if err != nil {
			return err
		}
	}
	reqURL := s.baseURL + "/open-apis/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("feishu request %s: %w", path, err)
	}
	defer resp.Body.Close()
	var raw struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("feishu response decode %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || raw.Code != 0 {
		return fmt.Errorf("feishu request failed: path=%s status=%d code=%d msg=%s", path, resp.StatusCode, raw.Code, raw.Msg)
	}
	if out != nil && len(raw.Data) > 0 {
		if err := json.Unmarshal(raw.Data, out); err != nil {
			return fmt.Errorf("feishu data decode %s: %w", path, err)
		}
	}
	return nil
}

func (s *FeishuIssueService) openIDByUser(ctx context.Context, user db.User) (string, error) {
	var openID string
	err := s.db.QueryRow(ctx, `
SELECT open_id
FROM feishu_user_identity
WHERE user_id = $1 AND open_id <> ''
`, user.ID).Scan(&openID)
	if err == nil && openID != "" {
		return openID, nil
	}

	var data struct {
		UserList []struct {
			UserID string `json:"user_id"`
			Email  string `json:"email"`
		} `json:"user_list"`
	}
	if err := s.request(ctx, http.MethodPost, "contact/v3/users/batch_get_id",
		url.Values{"user_id_type": []string{"open_id"}},
		map[string][]string{"emails": []string{user.Email}},
		&data,
	); err != nil {
		return "", err
	}
	for _, candidate := range data.UserList {
		if strings.EqualFold(strings.TrimSpace(candidate.Email), user.Email) && strings.TrimSpace(candidate.UserID) != "" {
			openID = strings.TrimSpace(candidate.UserID)
			break
		}
	}
	if openID == "" && len(data.UserList) == 1 {
		openID = strings.TrimSpace(data.UserList[0].UserID)
	}
	if openID == "" {
		return "", fmt.Errorf("feishu user lookup found no open_id for email %s", user.Email)
	}
	_, _ = s.db.Exec(ctx, `
INSERT INTO feishu_user_identity (user_id, email, open_id, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (user_id) DO UPDATE
SET email = EXCLUDED.email, open_id = EXCLUDED.open_id, updated_at = now()
`, user.ID, user.Email, openID)
	return openID, nil
}

func (s *FeishuIssueService) SendInboxCard(ctx context.Context, item db.InboxItem, issueStatus string) error {
	if s == nil || item.RecipientType != "member" || !item.IssueID.Valid {
		return nil
	}
	user, err := s.queries.GetUser(ctx, item.RecipientID)
	if err != nil {
		return fmt.Errorf("load recipient user: %w", err)
	}
	openID, err := s.openIDByUser(ctx, user)
	if err != nil {
		return err
	}
	content, err := json.Marshal(s.buildIssueCard(ctx, item, issueStatus))
	if err != nil {
		return err
	}
	var data struct {
		MessageID string `json:"message_id"`
		RootID    string `json:"root_id"`
		ChatID    string `json:"chat_id"`
	}
	err = s.request(ctx, http.MethodPost, "im/v1/messages",
		url.Values{"receive_id_type": []string{"open_id"}},
		map[string]string{
			"receive_id": openID,
			"msg_type":   "interactive",
			"content":    string(content),
		},
		&data,
	)
	if err != nil {
		return err
	}
	if data.MessageID == "" {
		return nil
	}
	rootID := data.RootID
	if rootID == "" {
		rootID = data.MessageID
	}
	_, err = s.db.Exec(ctx, `
INSERT INTO feishu_message_binding (
    workspace_id, issue_id, inbox_item_id, recipient_id,
    receive_id_type, receive_id, message_id, root_id, chat_id, card_action_value, updated_at
) VALUES ($1, $2, $3, $4, 'open_id', $5, $6, $7, $8, $9, now())
ON CONFLICT (message_id) DO UPDATE
SET inbox_item_id = EXCLUDED.inbox_item_id,
    root_id = EXCLUDED.root_id,
    chat_id = EXCLUDED.chat_id,
    card_action_value = EXCLUDED.card_action_value,
    updated_at = now()
`, item.WorkspaceID, item.IssueID, item.ID, item.RecipientID, openID, data.MessageID, rootID, data.ChatID, item.Details)
	return err
}

func (s *FeishuIssueService) buildIssueCard(ctx context.Context, item db.InboxItem, issueStatus string) map[string]any {
	status := issueStatus
	if status == "" {
		status = "unknown"
	}
	issueID := util.UUIDToString(item.IssueID)
	workspaceID := util.UUIDToString(item.WorkspaceID)
	inboxID := util.UUIDToString(item.ID)
	body := ""
	if bodyPtr := util.TextToPtr(item.Body); bodyPtr != nil {
		body = strings.TrimSpace(*bodyPtr)
	}
	if body == "" {
		body = "Issue status: " + status
	}
	body = truncateRunes(body, 600)

	actions := []any{
		feishuButton("开始", "primary", map[string]string{"multica_action": "set_status", "status": "in_progress", "issue_id": issueID, "workspace_id": workspaceID, "inbox_item_id": inboxID}),
		feishuButton("送审", "default", map[string]string{"multica_action": "set_status", "status": "in_review", "issue_id": issueID, "workspace_id": workspaceID, "inbox_item_id": inboxID}),
		feishuButton("完成", "primary", map[string]string{"multica_action": "set_status", "status": "done", "issue_id": issueID, "workspace_id": workspaceID, "inbox_item_id": inboxID}),
		feishuButton("阻塞", "danger", map[string]string{"multica_action": "set_status", "status": "blocked", "issue_id": issueID, "workspace_id": workspaceID, "inbox_item_id": inboxID}),
		feishuButton("拒绝/取消", "danger", map[string]string{"multica_action": "set_status", "status": "cancelled", "issue_id": issueID, "workspace_id": workspaceID, "inbox_item_id": inboxID}),
		feishuButton("已读", "default", map[string]string{"multica_action": "mark_read", "issue_id": issueID, "workspace_id": workspaceID, "inbox_item_id": inboxID}),
		feishuButton("归档", "default", map[string]string{"multica_action": "archive_inbox", "issue_id": issueID, "workspace_id": workspaceID, "inbox_item_id": inboxID}),
	}
	if link := s.issueLink(ctx, item.WorkspaceID, issueID); link != "" {
		actions = append([]any{map[string]any{
			"tag": "button",
			"text": map[string]string{
				"tag":     "plain_text",
				"content": "打开",
			},
			"type": "default",
			"url":  link,
		}}, actions...)
	}

	return map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": cardTemplate(item.Severity),
			"title":    map[string]string{"tag": "plain_text", "content": truncateRunes(item.Title, 80)},
		},
		"elements": []any{
			map[string]string{"tag": "markdown", "content": body},
			map[string]string{"tag": "markdown", "content": "**通知**: " + item.Type + "\n**状态**: " + status},
			map[string]any{"tag": "action", "actions": actions},
			map[string]string{"tag": "hr"},
			map[string]string{"tag": "markdown", "content": "直接回复这条飞书消息，会同步为 Multica issue 评论。"},
		},
	}
}

func (s *FeishuIssueService) issueLink(ctx context.Context, workspaceID pgtype.UUID, issueID string) string {
	if s.publicURL == "" {
		return ""
	}
	ws, err := s.queries.GetWorkspace(ctx, workspaceID)
	if err != nil || ws.Slug == "" {
		return s.publicURL
	}
	return s.publicURL + "/" + url.PathEscape(ws.Slug) + "/issues/" + url.PathEscape(issueID)
}

func feishuButton(text, typ string, value map[string]string) map[string]any {
	return map[string]any{
		"tag":   "button",
		"text":  map[string]string{"tag": "plain_text", "content": text},
		"type":  typ,
		"value": value,
	}
}

func cardTemplate(severity string) string {
	switch severity {
	case "action_required", "attention":
		return "red"
	default:
		return "blue"
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	if n <= 3 {
		return string(r[:n])
	}
	return string(r[:n-3]) + "..."
}

func (s *FeishuIssueService) ReserveEvent(ctx context.Context, eventID string) (bool, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return true, nil
	}
	tag, err := s.db.Exec(ctx, `
INSERT INTO feishu_event_delivery (event_id)
VALUES ($1)
ON CONFLICT (event_id) DO NOTHING
`, eventID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *FeishuIssueService) BindingForMessage(ctx context.Context, messageID, rootID, chatID string) (FeishuMessageBinding, error) {
	var b FeishuMessageBinding
	var workspaceID, issueID, inboxItemID, recipientID pgtype.UUID
	err := s.db.QueryRow(ctx, `
SELECT workspace_id, issue_id, inbox_item_id, recipient_id, message_id, COALESCE(root_id, ''), COALESCE(chat_id, '')
FROM feishu_message_binding
WHERE message_id = $1
   OR ($2 <> '' AND root_id = $2)
   OR ($3 <> '' AND chat_id = $3 AND root_id = $2)
ORDER BY created_at DESC
LIMIT 1
`, messageID, rootID, chatID).Scan(&workspaceID, &issueID, &inboxItemID, &recipientID, &b.MessageID, &b.RootID, &b.ChatID)
	if err != nil {
		return b, err
	}
	b.WorkspaceID = util.UUIDToString(workspaceID)
	b.IssueID = util.UUIDToString(issueID)
	b.InboxItemID = util.UUIDToString(inboxItemID)
	b.RecipientID = util.UUIDToString(recipientID)
	return b, nil
}

func (s *FeishuIssueService) UserIDForOpenID(ctx context.Context, openID string) (string, error) {
	openID = strings.TrimSpace(openID)
	if openID == "" {
		return "", fmt.Errorf("missing feishu open_id")
	}
	var userID pgtype.UUID
	err := s.db.QueryRow(ctx, `
SELECT user_id
FROM feishu_user_identity
WHERE open_id = $1
`, openID).Scan(&userID)
	if err == nil {
		return util.UUIDToString(userID), nil
	}

	var data struct {
		User struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := s.request(ctx, http.MethodGet, "contact/v3/users/"+url.PathEscape(openID),
		url.Values{"user_id_type": []string{"open_id"}},
		nil,
		&data,
	); err != nil {
		return "", err
	}
	email := strings.TrimSpace(data.User.Email)
	if email == "" {
		return "", fmt.Errorf("feishu user %s has no visible email", openID)
	}
	user, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		return "", err
	}
	_, _ = s.db.Exec(ctx, `
INSERT INTO feishu_user_identity (user_id, email, open_id, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (user_id) DO UPDATE
SET email = EXCLUDED.email, open_id = EXCLUDED.open_id, updated_at = now()
`, user.ID, user.Email, openID)
	return util.UUIDToString(user.ID), nil
}

func (s *FeishuIssueService) LogSendFailure(err error, item db.InboxItem) {
	if err == nil {
		return
	}
	slog.Warn("feishu issue card send failed",
		"inbox_item_id", util.UUIDToString(item.ID),
		"issue_id", util.UUIDToPtr(item.IssueID),
		"recipient_id", util.UUIDToString(item.RecipientID),
		"error", err)
}
