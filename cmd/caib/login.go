package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/auth"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/clilog"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/config"
	"github.com/spf13/cobra"
)

var loginToken string

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login [server-url]",
		Short: "Save server endpoint and authenticate for subsequent commands",
		Long: `Login saves the Build API server URL in XDG config (typically ~/.config/caib/cli.json) so you do not need
to pass --server or set CAIB_SERVER for later commands. If the server uses OIDC,
this command also performs authentication and caches the token.

If no URL is provided, the server endpoint is attempted to be derived from the current
Jumpstarter client config (~/.config/jumpstarter/clients/<alias>.yaml).

Use --token to provide a bearer token explicitly (e.g. from 'oc whoami -t' or a
ServiceAccount token). The token is saved to config and used by all subsequent commands,
bypassing OIDC. This is the recommended approach for machine users and CI/CD pipelines.

Examples:
  caib login https://build-api.my-cluster.example.com
  caib login --token "$(oc whoami -t)" https://build-api.my-cluster.example.com
  caib login  # derive endpoint from Jumpstarter config (if available)`,
		Args: cobra.MaximumNArgs(1),
		Run:  runLogin,
	}

	cmd.Flags().StringVar(&loginToken, "token", "", "bearer token to save and use for all API calls (bypasses OIDC)")

	return cmd
}

// normalizeServerURL parses and normalizes a raw server URL argument.
// It prepends "https://" if no scheme is present, and rejects URLs with
// invalid schemes, credentials, query parameters, fragments, or non-root paths.
func normalizeServerURL(raw string) (string, error) {
	server := strings.TrimSpace(raw)
	if server == "" {
		return "", fmt.Errorf("server URL is required")
	}
	if strings.Contains(server, "://") {
		if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
			return "", fmt.Errorf("invalid server URL %q: scheme must be http or https", raw)
		}
	} else {
		server = "https://" + server
	}
	parsedURL, err := url.Parse(server)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return "", fmt.Errorf("invalid server URL %q", raw)
	}
	if parsedURL.User != nil {
		return "", fmt.Errorf("server URL must not include credentials")
	}
	if parsedURL.RawQuery != "" {
		return "", fmt.Errorf("server URL must not include query parameters")
	}
	if parsedURL.Fragment != "" {
		return "", fmt.Errorf("server URL must not include fragments")
	}
	if parsedURL.Path != "" && parsedURL.Path != "/" {
		return "", fmt.Errorf("server URL must not include a non-root path")
	}
	return parsedURL.Scheme + "://" + parsedURL.Host, nil
}

// checkServerReachable performs a lightweight GET against /v1/healthz to confirm
// the server exists and is reachable before the URL is persisted to config.
// Any HTTP response (even an error status) is accepted — only a connection
// failure causes an error.
func checkServerReachable(serverURL string, insecureSkipTLS bool) error {
	transport := &http.Transport{}
	if insecureSkipTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
	}
	resp, err := client.Get(strings.TrimSuffix(serverURL, "/") + "/v1/healthz")
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// runLogin saves the server URL and optionally performs OIDC authentication.
func runLogin(_ *cobra.Command, args []string) {
	var server string

	if len(args) == 0 {
		server = config.DeriveServerFromJumpstarter()
		if server == "" {
			handleError(fmt.Errorf("no Jumpstarter config found or derived endpoint unreachable; provide a server URL explicitly"))
		}
	} else {
		var err error
		server, err = normalizeServerURL(args[0])
		if err != nil {
			handleError(err)
		}
	}

	if err := checkServerReachable(server, insecureSkipTLS); err != nil {
		handleError(fmt.Errorf("cannot connect to server %q: %w", server, err))
	}

	if err := config.SaveServerURL(server); err != nil {
		handleError(fmt.Errorf("failed to save server URL: %w", err))
	}
	clilog.Infof("Server saved: %s\n", server)

	// --token: save to config so all subsequent commands use it directly,
	// bypassing OIDC. Works for both opaque tokens (oc whoami -t) and JWTs.
	if loginToken != "" {
		if err := config.SaveToken(loginToken); err != nil {
			handleError(fmt.Errorf("failed to save token: %w", err))
		}
		clilog.Infoln("Token saved. Subsequent commands will use it without re-authentication.")
		return
	}

	ctx := context.Background()
	token, didAuth, err := auth.GetTokenWithReauth(ctx, server, "", insecureSkipTLS)
	if err != nil {
		fmt.Printf("Warning: authentication failed (you may need --token or kubeconfig for API calls): %v\n", err)
		return
	}
	if token != "" && didAuth {
		clilog.Infoln("OIDC authentication successful. Token cached for subsequent commands.")
	} else if token != "" {
		clilog.Infoln("Using existing or kubeconfig token. You can run build/list/disk commands without --server.")
	}
}
