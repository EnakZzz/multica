package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeSubjectField(t *testing.T) {
	long := strings.Repeat("a", 100)
	longRunes := strings.Repeat("深", 100)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii", "Acme", "Acme"},
		{"strips newline", "Acme\nEvil", "AcmeEvil"},
		{"strips crlf header-style", "Acme\r\nBcc: evil@example.com", "AcmeBcc: evil@example.com"},
		{"strips tab", "Acme\tTeam", "AcmeTeam"},
		{"strips unicode control", "Acme\x07Beep", "AcmeBeep"},
		{"preserves non-ascii", "深度学习工作区", "深度学习工作区"},
		{"preserves emoji", "Team 🚀", "Team 🚀"},
		{"truncates long ascii", long, strings.Repeat("a", maxSubjectFieldRunes-1) + "…"},
		{"truncates rune-aware", longRunes, strings.Repeat("深", maxSubjectFieldRunes-1) + "…"},
		{"empty stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeSubjectField(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeSubjectField(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildInvitationParams_EscapesHTMLInBody(t *testing.T) {
	tests := []struct {
		name          string
		inviter       string
		workspace     string
		wantInBody    []string
		wantNotInBody []string
	}{
		{
			name:      "escapes script tag in inviter",
			inviter:   "<script>alert(1)</script>",
			workspace: "Acme",
			wantInBody: []string{
				"&lt;script&gt;alert(1)&lt;/script&gt;",
			},
			wantNotInBody: []string{
				"<script>alert(1)</script>",
			},
		},
		{
			name:      "escapes attribute-break payload in inviter",
			inviter:   `Alice" onclick="evil()`,
			workspace: "Acme",
			wantNotInBody: []string{
				`Alice" onclick="evil()`,
			},
		},
		{
			name:      "escapes anchor tag in workspace",
			inviter:   "Alice",
			workspace: `<a href="https://evil.example">Click</a>`,
			wantInBody: []string{
				"&lt;a href=",
				"&gt;Click&lt;/a&gt;",
			},
			wantNotInBody: []string{
				`<a href="https://evil.example">Click</a>`,
			},
		},
		{
			name:      "benign text unchanged",
			inviter:   "Alice",
			workspace: "Acme",
			wantInBody: []string{
				"Alice",
				"Acme",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := buildInvitationParams(
				"noreply@multica.ai",
				"invitee@example.com",
				tt.inviter,
				tt.workspace,
				"https://app.multica.ai/invite/abc-123",
			)
			for _, needle := range tt.wantInBody {
				if !strings.Contains(p.Html, needle) {
					t.Errorf("body missing %q\nbody: %s", needle, p.Html)
				}
			}
			for _, needle := range tt.wantNotInBody {
				if strings.Contains(p.Html, needle) {
					t.Errorf("body should not contain raw %q\nbody: %s", needle, p.Html)
				}
			}
		})
	}
}

func TestBuildInvitationParams_SubjectStripsControls(t *testing.T) {
	p := buildInvitationParams(
		"noreply@multica.ai",
		"invitee@example.com",
		"Alice\r\n",
		"Acme\t",
		"https://app.multica.ai/invite/abc",
	)
	if strings.ContainsAny(p.Subject, "\r\n\t") {
		t.Errorf("subject still contains control characters: %q", p.Subject)
	}
	if p.Subject != "Alice invited you to Acme on Multica" {
		t.Errorf("unexpected subject: %q", p.Subject)
	}
}

func TestBuildInvitationParams_SubjectNotHTMLEscaped(t *testing.T) {
	// Subject is not HTML-rendered; entities would render literally in inboxes.
	p := buildInvitationParams(
		"noreply@multica.ai",
		"invitee@example.com",
		"Alice",
		"Acme & Co.",
		"https://app.multica.ai/invite/abc",
	)
	if strings.Contains(p.Subject, "&amp;") {
		t.Errorf("subject should not be HTML-escaped, got %q", p.Subject)
	}
	if !strings.Contains(p.Subject, "Acme & Co.") {
		t.Errorf("subject missing literal ampersand: %q", p.Subject)
	}
}

func TestBuildInvitationParams_SubjectTruncated(t *testing.T) {
	longWorkspace := strings.Repeat("A", 200)
	p := buildInvitationParams(
		"noreply@multica.ai",
		"invitee@example.com",
		"Alice",
		longWorkspace,
		"https://app.multica.ai/invite/abc",
	)
	// Template: "Alice invited you to <ws> on Multica"
	// ws is capped at maxSubjectFieldRunes; overall subject should also be bounded.
	maxExpected := len("Alice invited you to  on Multica") + maxSubjectFieldRunes
	if runes := len([]rune(p.Subject)); runes > maxExpected {
		t.Errorf("subject not bounded: %d runes, max %d: %q", runes, maxExpected, p.Subject)
	}
	if !strings.Contains(p.Subject, "…") {
		t.Errorf("truncated subject should contain ellipsis marker: %q", p.Subject)
	}
}

func TestBuildInvitationParams_ToAndFromPassedThrough(t *testing.T) {
	p := buildInvitationParams(
		"noreply@multica.ai",
		"invitee@example.com",
		"Alice",
		"Acme",
		"https://app.multica.ai/invite/abc",
	)
	if p.From != "noreply@multica.ai" {
		t.Errorf("From = %q", p.From)
	}
	if len(p.To) != 1 || p.To[0] != "invitee@example.com" {
		t.Errorf("To = %v", p.To)
	}
	if !strings.Contains(p.Html, "https://app.multica.ai/invite/abc") {
		t.Errorf("body missing invite URL: %s", p.Html)
	}
}

func TestFeishuSendVerificationCodeResolvesOpenIDFromLoginEmailByDefault(t *testing.T) {
	var tokenRequest map[string]string
	var lookupRequest map[string][]string
	var messageRequest map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			if err := json.NewDecoder(r.Body).Decode(&tokenRequest); err != nil {
				t.Fatalf("decode token request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "tenant-token",
				"expire":              7200,
			})
		case "/open-apis/contact/v3/users/batch_get_id":
			if got := r.URL.Query().Get("user_id_type"); got != "open_id" {
				t.Fatalf("user_id_type = %q, want open_id", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("lookup Authorization = %q", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&lookupRequest); err != nil {
				t.Fatalf("decode lookup request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"user_list": []map[string]string{
						{"email": "alice@example.com", "user_id": "ou_alice"},
					},
				},
			})
		case "/open-apis/im/v1/messages":
			if got := r.URL.Query().Get("receive_id_type"); got != "open_id" {
				t.Fatalf("receive_id_type = %q, want open_id", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("message Authorization = %q", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&messageRequest); err != nil {
				t.Fatalf("decode message request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("FEISHU_BASE_URL", srv.URL)
	t.Setenv("FEISHU_VERIFICATION_RECEIVE_ID", "")
	t.Setenv("FEISHU_VERIFICATION_RECEIVE_ID_TYPE", "")

	svc := NewEmailService()
	if err := svc.SendVerificationCode("alice@example.com", "123456"); err != nil {
		t.Fatalf("SendVerificationCode: %v", err)
	}

	if tokenRequest["app_id"] != "cli_test" || tokenRequest["app_secret"] != "secret" {
		t.Fatalf("unexpected token request: %#v", tokenRequest)
	}
	if got := lookupRequest["emails"]; len(got) != 1 || got[0] != "alice@example.com" {
		t.Fatalf("lookup emails = %#v", got)
	}
	if messageRequest["receive_id"] != "ou_alice" {
		t.Fatalf("receive_id = %q, want resolved open_id", messageRequest["receive_id"])
	}
	if messageRequest["msg_type"] != "text" {
		t.Fatalf("msg_type = %q", messageRequest["msg_type"])
	}
	if !strings.Contains(messageRequest["content"], "123456") {
		t.Fatalf("message content missing code: %s", messageRequest["content"])
	}
}

func TestFeishuSendVerificationCodeUsesConfiguredRecipientOverride(t *testing.T) {
	var messageRequest map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "tenant-token",
				"expire":              7200,
			})
		case "/open-apis/im/v1/messages":
			if got := r.URL.Query().Get("receive_id_type"); got != "chat_id" {
				t.Fatalf("receive_id_type = %q, want chat_id", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&messageRequest); err != nil {
				t.Fatalf("decode message request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("FEISHU_BASE_URL", srv.URL)
	t.Setenv("FEISHU_VERIFICATION_RECEIVE_ID_TYPE", "chat_id")
	t.Setenv("FEISHU_VERIFICATION_RECEIVE_ID", "oc_test_chat")

	svc := NewEmailService()
	if err := svc.SendVerificationCode("alice@example.com", "654321"); err != nil {
		t.Fatalf("SendVerificationCode: %v", err)
	}

	if messageRequest["receive_id"] != "oc_test_chat" {
		t.Fatalf("receive_id = %q, want configured override", messageRequest["receive_id"])
	}
}

func TestFeishuFailureWithoutEmailFallbackReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "tenant-token",
				"expire":              7200,
			})
		case "/open-apis/contact/v3/users/batch_get_id":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 99991672,
				"msg":  "Access denied. One of the following scopes is required: [contact:user.id:readonly]",
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("FEISHU_BASE_URL", srv.URL)
	t.Setenv("FEISHU_VERIFICATION_RECEIVE_ID", "")
	t.Setenv("SMTP_HOST", "")
	t.Setenv("RESEND_API_KEY", "")

	svc := NewEmailService()
	err := svc.SendVerificationCode("alice@example.com", "123456")
	if err == nil {
		t.Fatal("SendVerificationCode should fail when Feishu fails and no email backend is configured")
	}
	if !strings.Contains(err.Error(), "no email fallback") {
		t.Fatalf("error should mention missing fallback, got: %v", err)
	}
	if !strings.Contains(err.Error(), "contact:user.id:readonly") {
		t.Fatalf("error should preserve Feishu permission detail, got: %v", err)
	}
}

func TestFeishuFailureFallsBackToSMTP(t *testing.T) {
	host, port, received := startFakeSMTPServer(t)
	feishuCalledMessageAPI := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "tenant-token",
				"expire":              7200,
			})
		case "/open-apis/contact/v3/users/batch_get_id":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 99991672,
				"msg":  "Access denied. One of the following scopes is required: [contact:user.id:readonly]",
			})
		case "/open-apis/im/v1/messages":
			feishuCalledMessageAPI = true
			t.Fatalf("message API should not be called after lookup failure")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "secret")
	t.Setenv("FEISHU_BASE_URL", srv.URL)
	t.Setenv("FEISHU_VERIFICATION_RECEIVE_ID", "")
	t.Setenv("SMTP_HOST", host)
	t.Setenv("SMTP_PORT", port)
	t.Setenv("SMTP_USERNAME", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("SMTP_TLS_INSECURE", "false")
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("RESEND_FROM_EMAIL", "noreply@example.com")

	svc := NewEmailService()
	if err := svc.SendVerificationCode("alice@example.com", "654321"); err != nil {
		t.Fatalf("SendVerificationCode should fall back to SMTP: %v", err)
	}
	if feishuCalledMessageAPI {
		t.Fatal("message API was called unexpectedly")
	}

	select {
	case msg := <-received:
		if !strings.Contains(msg, "alice@example.com") {
			t.Fatalf("SMTP message missing recipient email: %s", msg)
		}
		if !strings.Contains(msg, "654321") {
			t.Fatalf("SMTP message missing code: %s", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SMTP message")
	}
}

func startFakeSMTPServer(t *testing.T) (string, string, <-chan string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake SMTP: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	received := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		write := func(format string, args ...any) {
			_, _ = fmt.Fprintf(conn, format, args...)
		}
		write("220 fake-smtp\r\n")

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			upper := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(upper, "EHLO"):
				write("250-fake-smtp\r\n250 8BITMIME\r\n")
			case strings.HasPrefix(upper, "HELO"):
				write("250 fake-smtp\r\n")
			case strings.HasPrefix(upper, "MAIL FROM:"),
				strings.HasPrefix(upper, "RCPT TO:"):
				write("250 ok\r\n")
			case strings.HasPrefix(upper, "DATA"):
				write("354 end with dot\r\n")
				var data strings.Builder
				for {
					part, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimSpace(part) == "." {
						break
					}
					data.WriteString(part)
				}
				received <- data.String()
				write("250 queued\r\n")
			case strings.HasPrefix(upper, "QUIT"):
				write("221 bye\r\n")
				return
			default:
				write("250 ok\r\n")
			}
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), fmt.Sprintf("%d", addr.Port), received
}
