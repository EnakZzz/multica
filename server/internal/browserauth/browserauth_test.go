package browserauth

import "testing"

func TestBuildCookieEnvName(t *testing.T) {
	got := buildCookieEnvName("AI_GATEWAY_CHATGPT", "", "chatgpt.com")
	if got != "AI_GATEWAY_CHATGPT_COOKIE_CHATGPT_COM" {
		t.Fatalf("unexpected env name: %s", got)
	}
}

func TestDomainMatches(t *testing.T) {
	if !domainMatches(".chatgpt.com", "chatgpt.com") {
		t.Fatal("expected exact domain match")
	}
	if !domainMatches("auth.chatgpt.com", "chatgpt.com") {
		t.Fatal("expected subdomain match")
	}
	if domainMatches("example.com", "chatgpt.com") {
		t.Fatal("did not expect unrelated domain to match")
	}
}

func TestNormalizeDomainAcceptsURL(t *testing.T) {
	got, err := normalizeDomain("https://chatgpt.com/backend-api/me")
	if err != nil {
		t.Fatalf("normalizeDomain: %v", err)
	}
	if got != "chatgpt.com" {
		t.Fatalf("normalized domain = %q", got)
	}
}
