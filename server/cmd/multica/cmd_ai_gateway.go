package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/browserauth"
	"github.com/multica-ai/multica/server/internal/cli"
)

var aiGatewayCmd = &cobra.Command{
	Use:   "ai-gateway",
	Short: "AI gateway local tooling",
}

var aiGatewayBrowserAuthCmd = &cobra.Command{
	Use:   "browser-auth",
	Short: "Extract browser auth material for AI gateway env vars",
}

var aiGatewayBrowserAuthExtractCmd = &cobra.Command{
	Use:   "extract",
	Short: "Extract Cookie envs from local Chrome/Edge profiles",
	RunE:  runAIGatewayBrowserAuthExtract,
}

func init() {
	aiGatewayCmd.AddCommand(aiGatewayBrowserAuthCmd)
	aiGatewayBrowserAuthCmd.AddCommand(aiGatewayBrowserAuthExtractCmd)

	aiGatewayBrowserAuthExtractCmd.Flags().String("browser", "", "Restrict discovery to one browser: chrome or edge")
	aiGatewayBrowserAuthExtractCmd.Flags().String("profile-path", "", "Explicit browser profile path; bypasses auto discovery")
	aiGatewayBrowserAuthExtractCmd.Flags().String("domain", "", "Target domain or URL to extract cookies for")
	aiGatewayBrowserAuthExtractCmd.Flags().String("cookie-env-name", "", "Exact Cookie env var name to populate")
	aiGatewayBrowserAuthExtractCmd.Flags().String("env-prefix", "AI_GATEWAY_CHATGPT", "Cookie env prefix used when --cookie-env-name is omitted")
	aiGatewayBrowserAuthExtractCmd.Flags().String("env-suffix", "", "Cookie env suffix used when --cookie-env-name is omitted")
	aiGatewayBrowserAuthExtractCmd.Flags().Bool("set-user-env", false, "Persist the extracted Cookie value into HKCU environment variables")
	aiGatewayBrowserAuthExtractCmd.Flags().StringArray("header-env", nil, "Report a required header env in HEADER_NAME=ENV_NAME form")
	aiGatewayBrowserAuthExtractCmd.Flags().String("output", "table", "Output format: table or json")
}

func runAIGatewayBrowserAuthExtract(cmd *cobra.Command, _ []string) error {
	domain, _ := cmd.Flags().GetString("domain")
	if strings.TrimSpace(domain) == "" {
		return fmt.Errorf("--domain is required")
	}
	browserRaw, _ := cmd.Flags().GetString("browser")
	profilePath, _ := cmd.Flags().GetString("profile-path")
	cookieEnvName, _ := cmd.Flags().GetString("cookie-env-name")
	envPrefix, _ := cmd.Flags().GetString("env-prefix")
	envSuffix, _ := cmd.Flags().GetString("env-suffix")
	setUserEnv, _ := cmd.Flags().GetBool("set-user-env")
	output, _ := cmd.Flags().GetString("output")
	headerEnvFlags, _ := cmd.Flags().GetStringArray("header-env")

	headerEnvs, err := parseHeaderEnvBindings(headerEnvFlags)
	if err != nil {
		return err
	}
	result, extractErr := browserauth.Extract(browserauth.ExtractOptions{
		Browser:       browserauth.BrowserKind(strings.ToLower(strings.TrimSpace(browserRaw))),
		ProfilePath:   profilePath,
		Domain:        domain,
		CookieEnvName: cookieEnvName,
		EnvPrefix:     envPrefix,
		EnvSuffix:     envSuffix,
		SetUserEnv:    setUserEnv,
		HeaderEnvs:    headerEnvs,
	})

	switch strings.TrimSpace(strings.ToLower(output)) {
	case "", "table":
		printAIGatewayBrowserAuthTable(result)
	case "json":
		if err := cli.PrintJSON(os.Stdout, result); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported --output %q", output)
	}

	if extractErr != nil {
		return extractErr
	}
	return nil
}

func parseHeaderEnvBindings(values []string) ([]browserauth.HeaderEnvBinding, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]browserauth.HeaderEnvBinding, 0, len(values))
	for _, raw := range values {
		parts := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("--header-env must use HEADER_NAME=ENV_NAME, got %q", raw)
		}
		out = append(out, browserauth.HeaderEnvBinding{
			HeaderName: strings.TrimSpace(parts[0]),
			EnvName:    strings.TrimSpace(parts[1]),
		})
	}
	return out, nil
}

func printAIGatewayBrowserAuthTable(result browserauth.ExtractResult) {
	if len(result.Profiles) > 0 {
		fmt.Fprintf(os.Stdout, "Domain: %s\n", result.Domain)
	}
	headers := []string{"KIND", "ENV", "FOUND", "LENGTH", "SOURCE", "BROWSER", "PROFILE", "APPLIED"}
	rows := make([][]string, 0, len(result.Entries))
	for _, entry := range result.Entries {
		found := "no"
		if entry.Found {
			found = "yes"
		}
		applied := "no"
		if entry.Applied {
			applied = "yes"
		}
		rows = append(rows, []string{
			entry.Kind,
			entry.EnvName,
			found,
			fmt.Sprintf("%d", entry.ValueLength),
			entry.Source,
			entry.Browser,
			entry.Profile,
			applied,
		})
	}
	if len(rows) > 0 {
		cli.PrintTable(os.Stdout, headers, rows)
	}
	if len(result.Missing) > 0 {
		fmt.Fprintf(os.Stdout, "\nMissing:\n")
		for _, name := range result.Missing {
			fmt.Fprintf(os.Stdout, "  - %s\n", name)
		}
	}
	if len(result.Warnings) > 0 {
		fmt.Fprintf(os.Stdout, "\nWarnings:\n")
		for _, warning := range result.Warnings {
			fmt.Fprintf(os.Stdout, "  - %s\n", warning)
		}
	}
}
