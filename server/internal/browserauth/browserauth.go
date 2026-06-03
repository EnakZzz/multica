package browserauth

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

type BrowserKind string

const (
	BrowserChrome BrowserKind = "chrome"
	BrowserEdge   BrowserKind = "edge"
)

type ExtractOptions struct {
	Browser       BrowserKind
	ProfilePath   string
	Domain        string
	CookieEnvName string
	EnvPrefix     string
	EnvSuffix     string
	SetUserEnv    bool
	HeaderEnvs    []HeaderEnvBinding
}

type HeaderEnvBinding struct {
	HeaderName string `json:"header_name"`
	EnvName    string `json:"env_name"`
}

type BrowserProfile struct {
	Browser     BrowserKind `json:"browser"`
	UserDataDir string      `json:"user_data_dir"`
	ProfilePath string      `json:"profile_path"`
	ProfileName string      `json:"profile_name"`
}

type ExtractSummary struct {
	Kind        string `json:"kind"`
	EnvName     string `json:"env_name"`
	Source      string `json:"source"`
	Browser     string `json:"browser,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Found       bool   `json:"found"`
	ValueLength int    `json:"value_length"`
	Applied     bool   `json:"applied"`
	Description string `json:"description,omitempty"`
	HeaderName  string `json:"header_name,omitempty"`
	ProfilePath string `json:"profile_path,omitempty"`
	UserDataDir string `json:"user_data_dir,omitempty"`
}

type ExtractResult struct {
	Domain   string           `json:"domain"`
	Profiles []BrowserProfile `json:"profiles"`
	Entries  []ExtractSummary `json:"entries"`
	Missing  []string         `json:"missing"`
	Warnings []string         `json:"warnings,omitempty"`
}

type localState struct {
	OSCrypt struct {
		EncryptedKey string `json:"encrypted_key"`
	} `json:"os_crypt"`
}

type sqliteCookie struct {
	HostKey        string
	Name           string
	Path           string
	Value          string
	EncryptedValue []byte
}

func Extract(opts ExtractOptions) (ExtractResult, error) {
	domain, err := normalizeDomain(opts.Domain)
	if err != nil {
		return ExtractResult{}, err
	}
	cookieEnvName := strings.TrimSpace(opts.CookieEnvName)
	if cookieEnvName == "" {
		cookieEnvName = buildCookieEnvName(opts.EnvPrefix, opts.EnvSuffix, domain)
	}
	profiles, err := resolveProfiles(opts)
	if err != nil {
		return ExtractResult{}, err
	}
	result := ExtractResult{
		Domain:   domain,
		Profiles: profiles,
	}
	for _, binding := range opts.HeaderEnvs {
		value := strings.TrimSpace(os.Getenv(binding.EnvName))
		entry := ExtractSummary{
			Kind:        "header_env",
			HeaderName:  binding.HeaderName,
			EnvName:     binding.EnvName,
			Source:      "existing-env",
			Found:       value != "",
			ValueLength: len(value),
			Description: "Header envs are referenced as-is; the helper only extracts browser cookies.",
		}
		if !entry.Found {
			result.Missing = append(result.Missing, binding.EnvName)
		}
		result.Entries = append(result.Entries, entry)
	}

	var attemptErrors []string
	for _, profile := range profiles {
		cookieHeader, err := extractCookieHeader(profile, domain)
		if err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s/%s: %v", profile.Browser, profile.ProfileName, err))
			continue
		}
		entry := ExtractSummary{
			Kind:        "cookie_env",
			EnvName:     cookieEnvName,
			Source:      "browser-cookie-db",
			Browser:     string(profile.Browser),
			Profile:     profile.ProfileName,
			ProfilePath: profile.ProfilePath,
			UserDataDir: profile.UserDataDir,
			Found:       true,
			ValueLength: len(cookieHeader),
		}
		if opts.SetUserEnv {
			if err := setUserEnvVar(cookieEnvName, cookieHeader); err != nil {
				return result, fmt.Errorf("set user env %s: %w", cookieEnvName, err)
			}
			_ = os.Setenv(cookieEnvName, cookieHeader)
			entry.Applied = true
		}
		result.Entries = append(result.Entries, entry)
		return result, nil
	}

	result.Entries = append(result.Entries, ExtractSummary{
		Kind:        "cookie_env",
		EnvName:     cookieEnvName,
		Source:      "browser-cookie-db",
		Found:       false,
		Description: "No matching browser cookie set was extracted.",
	})
	result.Missing = append(result.Missing, cookieEnvName)
	if len(attemptErrors) > 0 {
		result.Warnings = append(result.Warnings, attemptErrors...)
	}
	if opts.ProfilePath != "" || opts.Browser != "" {
		return result, fmt.Errorf("failed to extract cookies for %s", domain)
	}
	return result, fmt.Errorf("no usable browser cookies found for %s", domain)
}

func normalizeDomain(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("domain is required")
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("parse domain: %w", err)
		}
		raw = parsed.Hostname()
	}
	raw = strings.Trim(strings.TrimPrefix(strings.ToLower(raw), "."), "/")
	if raw == "" {
		return "", errors.New("domain is required")
	}
	return raw, nil
}

func buildCookieEnvName(prefix, suffix, domain string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "AI_GATEWAY_CHATGPT"
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		suffix = domain
	}
	suffix = strings.ToUpper(suffix)
	suffix = strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		if r >= 'a' && r <= 'z' {
			return r - ('a' - 'A')
		}
		return '_'
	}, suffix)
	suffix = strings.Trim(suffix, "_")
	if suffix == "" {
		suffix = "MAIN"
	}
	return prefix + "_COOKIE_" + suffix
}

func resolveProfiles(opts ExtractOptions) ([]BrowserProfile, error) {
	if path := strings.TrimSpace(opts.ProfilePath); path != "" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("profile_path: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("profile_path %q is not a directory", path)
		}
		browser, err := inferBrowserKind(opts.Browser, path)
		if err != nil {
			return nil, err
		}
		return []BrowserProfile{{
			Browser:     browser,
			UserDataDir: filepath.Dir(path),
			ProfilePath: path,
			ProfileName: filepath.Base(path),
		}}, nil
	}
	profiles, err := discoverProfiles(opts.Browser)
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, errors.New("no Chrome/Edge profiles found; pass --profile-path explicitly")
	}
	return profiles, nil
}

func inferBrowserKind(explicit BrowserKind, profilePath string) (BrowserKind, error) {
	if explicit == BrowserChrome || explicit == BrowserEdge {
		return explicit, nil
	}
	lower := strings.ToLower(filepath.ToSlash(profilePath))
	switch {
	case strings.Contains(lower, "/google/chrome/"):
		return BrowserChrome, nil
	case strings.Contains(lower, "/microsoft/edge/"):
		return BrowserEdge, nil
	default:
		return "", errors.New("cannot infer browser from profile_path; pass --browser chrome|edge")
	}
}

func discoverProfiles(filter BrowserKind) ([]BrowserProfile, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return nil, errors.New("LOCALAPPDATA is not set")
	}
	type browserRoot struct {
		kind BrowserKind
		dir  string
	}
	var roots []browserRoot
	switch filter {
	case "":
		roots = []browserRoot{
			{kind: BrowserChrome, dir: filepath.Join(localAppData, "Google", "Chrome", "User Data")},
			{kind: BrowserEdge, dir: filepath.Join(localAppData, "Microsoft", "Edge", "User Data")},
		}
	case BrowserChrome:
		roots = []browserRoot{{kind: BrowserChrome, dir: filepath.Join(localAppData, "Google", "Chrome", "User Data")}}
	case BrowserEdge:
		roots = []browserRoot{{kind: BrowserEdge, dir: filepath.Join(localAppData, "Microsoft", "Edge", "User Data")}}
	default:
		return nil, fmt.Errorf("unsupported browser %q", filter)
	}
	var profiles []BrowserProfile
	for _, root := range roots {
		if info, err := os.Stat(root.dir); err != nil || !info.IsDir() {
			continue
		}
		candidates, err := os.ReadDir(root.dir)
		if err != nil {
			continue
		}
		for _, entry := range candidates {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if name != "Default" && !strings.HasPrefix(name, "Profile ") {
				continue
			}
			profiles = append(profiles, BrowserProfile{
				Browser:     root.kind,
				UserDataDir: root.dir,
				ProfilePath: filepath.Join(root.dir, name),
				ProfileName: name,
			})
		}
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].Browser != profiles[j].Browser {
			return profiles[i].Browser < profiles[j].Browser
		}
		if profiles[i].ProfileName == "Default" && profiles[j].ProfileName != "Default" {
			return true
		}
		if profiles[j].ProfileName == "Default" && profiles[i].ProfileName != "Default" {
			return false
		}
		return profiles[i].ProfileName < profiles[j].ProfileName
	})
	return profiles, nil
}

func extractCookieHeader(profile BrowserProfile, domain string) (string, error) {
	masterKey, err := loadMasterKey(filepath.Join(profile.UserDataDir, "Local State"))
	if err != nil {
		return "", err
	}
	cookiesPath := filepath.Join(profile.ProfilePath, "Network", "Cookies")
	if _, err := os.Stat(cookiesPath); err != nil {
		cookiesPath = filepath.Join(profile.ProfilePath, "Cookies")
		if _, err := os.Stat(cookiesPath); err != nil {
			return "", errors.New("cookies database not found")
		}
	}
	tempCopy, cleanup, err := copyFileToTemp(cookiesPath)
	if err != nil {
		return "", err
	}
	defer cleanup()

	db, err := sql.Open("sqlite", tempCopy)
	if err != nil {
		return "", fmt.Errorf("open cookies db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT host_key, name, path, COALESCE(value, ''), encrypted_value
		FROM cookies
		WHERE host_key = ? OR host_key = ? OR host_key LIKE ?
		ORDER BY
			CASE
				WHEN host_key = ? THEN 0
				WHEN host_key = ? THEN 1
				ELSE 2
			END,
			LENGTH(path) DESC,
			name ASC
	`, domain, "."+domain, "%."+domain, domain, "."+domain)
	if err != nil {
		return "", fmt.Errorf("query cookies: %w", err)
	}
	defer rows.Close()

	cookies := make([]sqliteCookie, 0, 16)
	for rows.Next() {
		var item sqliteCookie
		if err := rows.Scan(&item.HostKey, &item.Name, &item.Path, &item.Value, &item.EncryptedValue); err != nil {
			return "", fmt.Errorf("scan cookies: %w", err)
		}
		if !domainMatches(item.HostKey, domain) {
			continue
		}
		cookies = append(cookies, item)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read cookies: %w", err)
	}
	if len(cookies) == 0 {
		return "", errors.New("no matching cookies found; make sure the browser is logged in for this domain")
	}
	parts := make([]string, 0, len(cookies))
	seen := make(map[string]struct{}, len(cookies))
	for _, item := range cookies {
		if _, exists := seen[item.Name]; exists {
			continue
		}
		value := item.Value
		if value == "" {
			value, err = decryptCookieValue(item.EncryptedValue, masterKey)
			if err != nil {
				return "", fmt.Errorf("decrypt cookie %s: %w", item.Name, err)
			}
		}
		if value == "" {
			continue
		}
		seen[item.Name] = struct{}{}
		parts = append(parts, item.Name+"="+value)
	}
	if len(parts) == 0 {
		return "", errors.New("matching cookies were found but none could be decrypted")
	}
	return strings.Join(parts, "; "), nil
}

func loadMasterKey(localStatePath string) ([]byte, error) {
	data, err := os.ReadFile(localStatePath)
	if err != nil {
		return nil, fmt.Errorf("read Local State: %w", err)
	}
	var state localState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode Local State: %w", err)
	}
	if state.OSCrypt.EncryptedKey == "" {
		return nil, errors.New("Local State did not include os_crypt.encrypted_key")
	}
	encoded, err := base64.StdEncoding.DecodeString(state.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("decode os_crypt.encrypted_key: %w", err)
	}
	if bytesHasPrefix(encoded, []byte("DPAPI")) {
		encoded = encoded[5:]
	}
	key, err := decryptDPAPI(encoded)
	if err != nil {
		return nil, fmt.Errorf("decrypt master key: %w", err)
	}
	return key, nil
}

func decryptCookieValue(encryptedValue, masterKey []byte) (string, error) {
	if len(encryptedValue) == 0 {
		return "", nil
	}
	if strings.HasPrefix(string(encryptedValue), "v10") || strings.HasPrefix(string(encryptedValue), "v11") {
		if len(encryptedValue) < 3+12+16 {
			return "", errors.New("encrypted cookie blob is too short")
		}
		block, err := aes.NewCipher(masterKey)
		if err != nil {
			return "", err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return "", err
		}
		nonce := encryptedValue[3 : 3+12]
		ciphertext := encryptedValue[3+12:]
		plain, err := gcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
	plain, err := decryptDPAPI(encryptedValue)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func domainMatches(hostKey, domain string) bool {
	hostKey = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(hostKey)), ".")
	domain = strings.ToLower(strings.TrimSpace(domain))
	return hostKey == domain || strings.HasSuffix(hostKey, "."+domain)
}

func copyFileToTemp(path string) (string, func(), error) {
	src, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer src.Close()
	tmp, err := os.CreateTemp("", "multica-browserauth-*.sqlite")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("copy cookies db: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("close temp cookies db: %w", err)
	}
	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
}

func bytesHasPrefix(data, prefix []byte) bool {
	if len(prefix) > len(data) {
		return false
	}
	for i := range prefix {
		if data[i] != prefix[i] {
			return false
		}
	}
	return true
}
