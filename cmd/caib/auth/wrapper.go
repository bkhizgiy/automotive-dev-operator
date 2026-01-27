package auth

import (
	"context"
	"strings"

	buildapiclient "github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi/client"
)

// IsAuthError checks if an error is an authentication error (401/403)
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "forbidden")
}

// GetTokenWithReauth gets a token, triggering OIDC re-auth if needed.
// Returns empty string if no OIDC config is available (auth is optional).
// The boolean return indicates whether a fresh auth flow was performed.
func GetTokenWithReauth(ctx context.Context, serverURL string, currentToken string) (string, bool, error) {
	// Try to get OIDC config from local config first
	config, err := GetOIDCConfigFromLocalConfig()
	if err != nil {
		// Fallback to Build API
		config, err = GetOIDCConfigFromAPI(serverURL)
		if err != nil {
			// If we can't get config, return empty (auth is optional)
			return "", false, nil
		}
	}

	// If no config available, return empty (auth is optional)
	if config == nil {
		return "", false, nil
	}

	oidcAuth := NewOIDCAuth(config.IssuerURL, config.ClientID, config.Scopes)

	// If we have a current token, check if it's valid
	if currentToken != "" {
		if oidcAuth.IsTokenValid(currentToken) {
			return currentToken, false, nil
		}
	}

	// Get new token via OIDC flow
	token, fromCache, err := oidcAuth.GetTokenWithStatus(ctx)
	if err != nil {
		return "", false, err
	}
	return token, !fromCache, nil
}

// CreateClientWithReauth creates a client and handles re-authentication on auth errors
func CreateClientWithReauth(ctx context.Context, serverURL string, authToken *string) (*buildapiclient.Client, error) {
	// Try to get token from OIDC if needed
	if strings.TrimSpace(*authToken) == "" {
		// Try OIDC auth
		token, _, err := GetTokenWithReauth(ctx, serverURL, "")
		if err == nil && token != "" {
			*authToken = token
		}
	}

	return buildapiclient.New(serverURL, buildapiclient.WithAuthToken(strings.TrimSpace(*authToken)))
}
