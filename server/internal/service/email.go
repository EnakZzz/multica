package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/resend/resend-go/v2"
)

// maxSubjectFieldRunes bounds how much user-controlled text (workspace name,
// inviter name) can land in an email Subject. Prevents attackers from stuffing
// a full phishing pitch into a workspace name that gets sent from our domain.
const maxSubjectFieldRunes = 60

var errNoEmailBackend = errors.New("no email backend configured")

type EmailService struct {
	client          *resend.Client
	fromEmail       string
	smtpHost        string
	smtpPort        string
	smtpUsername    string
	smtpPassword    string
	smtpTLSInsecure bool
	feishu          *feishuClient
}

type feishuClient struct {
	appID         string
	appSecret     string
	baseURL       string
	receiveIDType string
	receiveID     string
	httpClient    *http.Client

	mu          sync.Mutex
	tenantToken string
	tokenExpiry time.Time
}

type feishuTenantTokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int64  `json:"expire"`
}

type feishuMessageResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type feishuUserIDLookupResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		UserList []struct {
			UserID string `json:"user_id"`
			Email  string `json:"email"`
		} `json:"user_list"`
	} `json:"data"`
}

func NewEmailService() *EmailService {
	apiKey := os.Getenv("RESEND_API_KEY")
	from := strings.TrimSpace(os.Getenv("RESEND_FROM_EMAIL"))
	if from == "" {
		from = "noreply@multica.ai"
	}

	smtpHost := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	smtpPort := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	if smtpPort == "" {
		smtpPort = "25"
	}
	smtpUsername := os.Getenv("SMTP_USERNAME")
	smtpPassword := os.Getenv("SMTP_PASSWORD")
	smtpTLSInsecure := os.Getenv("SMTP_TLS_INSECURE") == "true"
	feishu := newFeishuClientFromEnv()

	var client *resend.Client
	if apiKey != "" {
		client = resend.NewClient(apiKey)
	}

	switch {
	case feishu != nil:
		fmt.Printf("EmailService: Feishu bot receive_id_type=%s\n", feishu.receiveIDType)
	case smtpHost != "":
		fmt.Printf("EmailService: SMTP relay %s:%s from=%s\n", smtpHost, smtpPort, from)
	case client != nil:
		fmt.Printf("EmailService: Resend API from=%s\n", from)
	default:
		fmt.Println("EmailService: DEV mode - codes printed to stdout (set MULTICA_DEV_VERIFICATION_CODE in .env for a fixed local code)")
	}

	return &EmailService{
		client:          client,
		fromEmail:       from,
		smtpHost:        smtpHost,
		smtpPort:        smtpPort,
		smtpUsername:    smtpUsername,
		smtpPassword:    smtpPassword,
		smtpTLSInsecure: smtpTLSInsecure,
		feishu:          feishu,
	}
}

func newFeishuClientFromEnv() *feishuClient {
	appID := strings.TrimSpace(os.Getenv("FEISHU_APP_ID"))
	appSecret := strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET"))
	if appID == "" || appSecret == "" {
		return nil
	}

	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("FEISHU_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = "https://open.feishu.cn"
	}
	receiveIDType := strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_RECEIVE_ID_TYPE"))
	if receiveIDType == "" {
		receiveIDType = "open_id"
	}

	return &feishuClient{
		appID:         appID,
		appSecret:     appSecret,
		baseURL:       baseURL,
		receiveIDType: receiveIDType,
		receiveID:     strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_RECEIVE_ID")),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *feishuClient) tenantAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.tenantToken != "" && time.Now().Before(c.tokenExpiry) {
		token := c.tenantToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	body, err := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("feishu tenant token request: %w", err)
	}
	defer resp.Body.Close()

	var parsed feishuTenantTokenResponse
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

	c.mu.Lock()
	c.tenantToken = parsed.TenantAccessToken
	c.tokenExpiry = time.Now().Add(expiresIn)
	c.mu.Unlock()

	return parsed.TenantAccessToken, nil
}

func (c *feishuClient) openIDByEmail(ctx context.Context, token, email string) (string, error) {
	body, err := json.Marshal(map[string][]string{
		"emails": []string{email},
	})
	if err != nil {
		return "", err
	}

	reqURL := c.baseURL + "/open-apis/contact/v3/users/batch_get_id?user_id_type=open_id"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("feishu user lookup request: %w", err)
	}
	defer resp.Body.Close()

	var parsed feishuUserIDLookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("feishu user lookup decode: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || parsed.Code != 0 {
		return "", fmt.Errorf("feishu user lookup failed: status=%d code=%d msg=%s", resp.StatusCode, parsed.Code, parsed.Msg)
	}
	for _, user := range parsed.Data.UserList {
		if strings.EqualFold(strings.TrimSpace(user.Email), email) && strings.TrimSpace(user.UserID) != "" {
			return user.UserID, nil
		}
	}
	if len(parsed.Data.UserList) == 1 && strings.TrimSpace(parsed.Data.UserList[0].UserID) != "" {
		return parsed.Data.UserList[0].UserID, nil
	}
	return "", fmt.Errorf("feishu user lookup found no open_id for email %s", email)
}

func (c *feishuClient) SendVerificationCode(to, code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return err
	}

	receiveIDType := c.receiveIDType
	receiveID := c.receiveID
	if receiveID == "" {
		openID, err := c.openIDByEmail(ctx, token, to)
		if err != nil {
			return err
		}
		receiveID = openID
		receiveIDType = "open_id"
	}
	content, err := json.Marshal(map[string]string{
		"text": fmt.Sprintf("Multica verification code: %s\nExpires in 10 minutes.\nLogin email: %s", code, to),
	})
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(content),
	})
	if err != nil {
		return err
	}

	reqURL := c.baseURL + "/open-apis/im/v1/messages?receive_id_type=" + url.QueryEscape(receiveIDType)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("feishu send message request: %w", err)
	}
	defer resp.Body.Close()

	var parsed feishuMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("feishu send message decode: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || parsed.Code != 0 {
		return fmt.Errorf("feishu send message failed: status=%d code=%d msg=%s", resp.StatusCode, parsed.Code, parsed.Msg)
	}
	return nil
}

// sendSMTP delivers an HTML email via an SMTP server.
// Supports unauthenticated relay (SMTP_USERNAME empty) and authenticated SMTP.
// Upgrades to STARTTLS when advertised by the server.
// Set SMTP_TLS_INSECURE=true for self-signed or private CA certificates.
func (s *EmailService) sendSMTP(to, subject, htmlBody string) error {
	addr := net.JoinHostPort(s.smtpHost, s.smtpPort)

	// Bounded dial + whole-session deadline: prevents a blackholed SMTP server
	// from hanging the auth handler (or a background goroutine) indefinitely.
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	if err = conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		conn.Close()
		return fmt.Errorf("smtp set deadline: %w", err)
	}

	c, err := smtp.NewClient(conn, s.smtpHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	// STARTTLS if advertised — refreshes the extension list for 8BITMIME check below.
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{
			ServerName:         s.smtpHost,
			InsecureSkipVerify: s.smtpTLSInsecure, //nolint:gosec // opt-in via SMTP_TLS_INSECURE=true
		}
		if err = c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}

	if s.smtpUsername != "" {
		auth := smtp.PlainAuth("", s.smtpUsername, s.smtpPassword, s.smtpHost)
		if err = c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	// Probe 8BITMIME after (possible) STARTTLS so the extension list is current.
	// Use quoted-printable for relays that don't advertise 8BITMIME — safer for
	// non-ASCII workspace/inviter names crossing strict or older SMTP hops.
	has8Bit, _ := c.Extension("8BITMIME")
	encodedSubject := mime.QEncoding.Encode("utf-8", subject)
	msgID := fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), s.smtpHost)

	var bodyBytes []byte
	var cte string
	if has8Bit {
		bodyBytes = []byte(htmlBody)
		cte = "8bit"
	} else {
		var buf strings.Builder
		qpw := quotedprintable.NewWriter(&buf)
		_, _ = qpw.Write([]byte(htmlBody))
		_ = qpw.Close()
		bodyBytes = []byte(buf.String())
		cte = "quoted-printable"
	}

	if err = c.Mail(s.fromEmail); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err = c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO <%s>: %w", to, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	headers := "From: " + s.fromEmail + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + encodedSubject + "\r\n" +
		"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n" +
		"Message-ID: " + msgID + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: " + cte + "\r\n" +
		"\r\n"
	if _, err = fmt.Fprintf(w, "%s%s", headers, bodyBytes); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("smtp end data: %w", err)
	}
	return c.Quit()
}

func (s *EmailService) hasEmailBackend() bool {
	return s.smtpHost != "" || s.client != nil
}

func (s *EmailService) sendVerificationEmail(to, htmlBody string) error {
	if s.smtpHost != "" {
		return s.sendSMTP(to, "Your Multica verification code", htmlBody)
	}
	if s.client != nil {
		params := &resend.SendEmailRequest{
			From:    s.fromEmail,
			To:      []string{to},
			Subject: "Your Multica verification code",
			Html:    htmlBody,
		}
		_, err := s.client.Emails.Send(params)
		return err
	}
	return errNoEmailBackend
}

// SendVerificationCode sends a one-time login code. The code is server-generated
// (6-digit numeric); recipient routing still comes from the requested login email
// unless FEISHU_VERIFICATION_RECEIVE_ID overrides it.
// Delivery priority: Feishu bot -> SMTP relay -> Resend API -> DEV stdout.
func (s *EmailService) SendVerificationCode(to, code string) error {
	body := fmt.Sprintf(
		`<div style="font-family: sans-serif; max-width: 400px; margin: 0 auto;">
			<h2>Your verification code</h2>
			<p style="font-size: 32px; font-weight: bold; letter-spacing: 8px; margin: 24px 0;">%s</p>
			<p>This code expires in 10 minutes.</p>
			<p style="color: #666; font-size: 14px;">If you didn't request this code, you can safely ignore this email.</p>
		</div>`, code)

	if s.feishu != nil {
		if err := s.feishu.SendVerificationCode(to, code); err == nil {
			return nil
		} else if !s.hasEmailBackend() {
			return fmt.Errorf("feishu verification delivery failed and no email fallback is configured: %w", err)
		} else if fallbackErr := s.sendVerificationEmail(to, body); fallbackErr != nil {
			return fmt.Errorf("feishu verification delivery failed (%v); email fallback also failed: %w", err, fallbackErr)
		}
		return nil
	}

	if err := s.sendVerificationEmail(to, body); err != nil {
		if errors.Is(err, errNoEmailBackend) {
			fmt.Printf("[DEV] Verification code for %s: %s\n", to, code)
			return nil
		}
		return err
	}
	return nil
}

// SendInvitationEmail notifies the invitee that they have been invited to a workspace.
// invitationID is included in the URL so the email deep-links to /invite/{id}.
func (s *EmailService) SendInvitationEmail(to, inviterName, workspaceName, invitationID string) error {
	appURL := strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN"))
	if appURL == "" {
		appURL = "https://app.multica.ai"
	}
	inviteURL := fmt.Sprintf("%s/invite/%s", appURL, invitationID)

	if s.smtpHost != "" {
		params := buildInvitationParams(s.fromEmail, to, inviterName, workspaceName, inviteURL)
		return s.sendSMTP(to, params.Subject, params.Html)
	}
	if s.client == nil {
		fmt.Printf("[DEV] Invitation email to %s: %s invited you to %s — %s\n", to, inviterName, workspaceName, inviteURL)
		return nil
	}
	params := buildInvitationParams(s.fromEmail, to, inviterName, workspaceName, inviteURL)
	_, err := s.client.Emails.Send(params)
	return err
}

// buildInvitationParams assembles the email request for an invitation.
// Separated from SendInvitationEmail so the sanitization behavior is unit-testable
// without needing to mock the Resend SDK or an SMTP server.
func buildInvitationParams(from, to, inviterName, workspaceName, inviteURL string) *resend.SendEmailRequest {
	safeWorkspace := html.EscapeString(workspaceName)
	safeInviter := html.EscapeString(inviterName)
	subjectInviter := sanitizeSubjectField(inviterName)
	subjectWorkspace := sanitizeSubjectField(workspaceName)

	return &resend.SendEmailRequest{
		From:    from,
		To:      []string{to},
		Subject: fmt.Sprintf("%s invited you to %s on Multica", subjectInviter, subjectWorkspace),
		Html: fmt.Sprintf(
			`<div style="font-family: sans-serif; max-width: 480px; margin: 0 auto;">
				<h2>You're invited to join %s</h2>
				<p><strong>%s</strong> invited you to collaborate in the <strong>%s</strong> workspace on Multica.</p>
				<p style="margin: 24px 0;">
					<a href="%s" style="display: inline-block; padding: 12px 24px; background: #000; color: #fff; text-decoration: none; border-radius: 6px; font-weight: 500;">Accept invitation</a>
				</p>
				<p style="color: #666; font-size: 14px;">You'll need to log in to accept or decline the invitation.</p>
			</div>`, safeWorkspace, safeInviter, safeWorkspace, inviteURL),
	}
}

// sanitizeSubjectField prepares user-controlled text for the email Subject line.
// Subject is not HTML-rendered, so HTML-escaping would leak literal entities
// (e.g. &lt;script&gt;) into the recipient's inbox. Instead strip control
// characters (defense in depth against header-injection-adjacent abuse even
// though Resend also filters CR/LF) and cap length so attackers can't stuff
// a full phishing subject into a workspace name.
func sanitizeSubjectField(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	cleaned := b.String()
	if utf8.RuneCountInString(cleaned) <= maxSubjectFieldRunes {
		return cleaned
	}
	runes := []rune(cleaned)
	return string(runes[:maxSubjectFieldRunes-1]) + "…"
}
