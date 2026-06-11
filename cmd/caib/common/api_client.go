// Package caibcommon provides shared caib helpers.
package caibcommon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/auth"
	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/config"
	buildapiclient "github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi/client"
	"k8s.io/client-go/tools/clientcmd"
)

// CreateBuildAPIClient creates a build API client with auth token from flags/env/kubeconfig.
func CreateBuildAPIClient(serverURL string, authToken *string, insecureSkipTLS bool) (*buildapiclient.Client, error) {
	ctx := context.Background()

	tokenValue := ""
	if authToken != nil {
		tokenValue = sanitizeToken(*authToken)
	}
	setToken := func(token string) {
		tokenValue = sanitizeToken(token)
		if authToken != nil {
			*authToken = tokenValue
		}
	}

	// Token resolution order:
	//   1. --token flag (tokenValue already set by caller)
	//   2. saved_token from cli.json (set by caib login --token)
	//   3. CAIB_TOKEN env var
	//   4. OIDC cached / browser flow
	//   5. kubeconfig / oc whoami -t fallback
	envToken := sanitizeToken(os.Getenv("CAIB_TOKEN"))
	// Many commands bind --token with a default value from CAIB_TOKEN.
	// If the pointer value exactly matches the env var, treat it as implicit
	// and allow saved_token to take precedence for "subsequent commands"
	// after `caib login --token`.
	if tokenValue != "" && envToken != "" && tokenValue == envToken {
		tokenValue = ""
	}
	if tokenValue == "" {
		if saved := config.LoadSavedToken(); saved != "" {
			setToken(saved)
		}
	}
	if tokenValue == "" && envToken != "" {
		setToken(envToken)
	}

	if tokenValue == "" {
		token, didAuth, err := auth.GetTokenWithReauth(ctx, serverURL, "", insecureSkipTLS)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: OIDC authentication failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "Attempting kubeconfig fallback (this may use a different identity)")
			if tok, loadErr := LoadTokenFromKubeconfig(); loadErr == nil && strings.TrimSpace(tok) != "" {
				setToken(tok)
			} else {
				return nil, fmt.Errorf("OIDC authentication failed and no kubeconfig token available: %w", err)
			}
		} else if token != "" {
			setToken(token)
			if didAuth {
				fmt.Fprintln(os.Stderr, "OIDC authentication successful")
			}
		} else if tok, loadErr := LoadTokenFromKubeconfig(); loadErr == nil && strings.TrimSpace(tok) != "" {
			setToken(tok)
		}
	}

	var opts []buildapiclient.Option
	if tokenValue != "" {
		opts = append(opts, buildapiclient.WithAuthToken(tokenValue))
	}

	if insecureSkipTLS {
		opts = append(opts, buildapiclient.WithInsecureTLS())
	}
	if caCertFile := os.Getenv("SSL_CERT_FILE"); caCertFile != "" {
		opts = append(opts, buildapiclient.WithCACertificate(caCertFile))
	} else if caCertFile := os.Getenv("REQUESTS_CA_BUNDLE"); caCertFile != "" {
		opts = append(opts, buildapiclient.WithCACertificate(caCertFile))
	}

	return buildapiclient.New(serverURL, opts...)
}

// ExecuteWithReauth executes an API call and retries once after re-auth on auth errors.
func ExecuteWithReauth(
	serverURL string,
	authToken *string,
	insecureSkipTLS bool,
	fn func(*buildapiclient.Client) error,
) error {
	ctx := context.Background()
	currentToken := ""
	if authToken != nil {
		currentToken = sanitizeToken(*authToken)
	}
	setToken := func(token string) {
		currentToken = sanitizeToken(token)
		if authToken != nil {
			*authToken = currentToken
		}
	}

	// Determine whether the token is explicitly user-provided before the first
	// call resolves it. CreateBuildAPIClient can fill authToken from kubeconfig,
	// so we must capture this upfront rather than checking currentToken after.
	savedToken := config.LoadSavedToken()
	isExplicitToken := currentToken != "" || savedToken != ""

	runWithFreshClient := func() error {
		client, err := CreateBuildAPIClient(serverURL, authToken, insecureSkipTLS)
		if err != nil {
			return err
		}
		if authToken != nil {
			currentToken = sanitizeToken(*authToken)
		}
		return fn(client)
	}

	err := runWithFreshClient()
	if err == nil {
		return nil
	}
	if !auth.IsAuthError(err) {
		return err
	}

	// If the token was explicitly provided by the user (via caib login --token or
	// the --token flag) and the server rejected it, give a clear actionable error
	// rather than silently falling through to OIDC with a different identity.
	if isExplicitToken {
		if savedToken != "" {
			return fmt.Errorf(
				"saved token was rejected by the server (expired or invalid)\n"+
					"Refresh it with:\n"+
					"  oc login   # re-authenticate with your cluster\n"+
					"  caib login --token $(oc whoami -t) %s", serverURL,
			)
		}
		return fmt.Errorf(
			"provided token was rejected by the server (401)\n"+
				"Verify the token is valid, or re-authenticate:\n"+
				"  oc login   # re-authenticate with your cluster\n"+
				"  caib login --token $(oc whoami -t) %s", serverURL,
		)
	}

	fmt.Fprintln(os.Stderr, "Authentication failed (401), re-authenticating...")
	newToken, _, err := auth.GetTokenWithReauth(ctx, serverURL, currentToken, insecureSkipTLS)
	if err != nil {
		return fmt.Errorf("re-authentication failed: %w", err)
	}
	setToken(newToken)

	fmt.Fprintln(os.Stderr, "Retrying request...")
	err = runWithFreshClient()
	if err == nil {
		return nil
	}
	if !auth.IsAuthError(err) {
		return err
	}

	if tok, loadErr := LoadTokenFromKubeconfig(); loadErr == nil && strings.TrimSpace(tok) != "" {
		setToken(tok)
		fmt.Fprintln(os.Stderr, "Attempting kubeconfig fallback...")
		return runWithFreshClient()
	}

	return err
}

// LoadTokenFromKubeconfig loads a bearer token from kubeconfig.
func LoadTokenFromKubeconfig() (string, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	deferred := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	if restCfg, err := deferred.ClientConfig(); err == nil && restCfg != nil {
		if t := strings.TrimSpace(restCfg.BearerToken); t != "" {
			return t, nil
		}
		if f := strings.TrimSpace(restCfg.BearerTokenFile); f != "" {
			if b, readErr := os.ReadFile(f); readErr == nil {
				if t := strings.TrimSpace(string(b)); t != "" {
					return t, nil
				}
			}
		}
	}

	rawCfg, err := loadingRules.Load()
	if err != nil || rawCfg == nil {
		return "", fmt.Errorf("cannot load kubeconfig: %w", err)
	}
	ctxName := rawCfg.CurrentContext
	if strings.TrimSpace(ctxName) == "" {
		return "", fmt.Errorf("no current kube context")
	}
	ctx := rawCfg.Contexts[ctxName]
	if ctx == nil {
		return "", fmt.Errorf("missing context %s", ctxName)
	}
	ai := rawCfg.AuthInfos[ctx.AuthInfo]
	if ai == nil {
		return "", fmt.Errorf("missing auth info for context %s", ctxName)
	}
	if strings.TrimSpace(ai.Token) != "" {
		return strings.TrimSpace(ai.Token), nil
	}
	if ai.AuthProvider != nil && ai.AuthProvider.Config != nil {
		if t := strings.TrimSpace(ai.AuthProvider.Config["access-token"]); t != "" {
			return t, nil
		}
		if t := strings.TrimSpace(ai.AuthProvider.Config["id-token"]); t != "" {
			return t, nil
		}
		if t := strings.TrimSpace(ai.AuthProvider.Config["token"]); t != "" {
			return t, nil
		}
	}
	if path, lookErr := exec.LookPath("oc"); lookErr == nil && path != "" {
		cmdCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, cmdErr := exec.CommandContext(cmdCtx, path, "whoami", "-t").Output()
		if cmdErr == nil {
			if t := strings.TrimSpace(string(out)); t != "" {
				return t, nil
			}
		}
	}
	return "", fmt.Errorf("no bearer token found in kubeconfig")
}

// sanitizeToken strips any "Bearer " prefix and surrounding whitespace from a
// token string. Mirrors config.normalizeToken (unexported) — kept here to avoid
// a circular import between the common and config packages.
func sanitizeToken(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parts := strings.Fields(trimmed)
	if len(parts) >= 2 && strings.EqualFold(parts[0], "bearer") {
		return strings.TrimSpace(strings.Join(parts[1:], " "))
	}
	return trimmed
}
