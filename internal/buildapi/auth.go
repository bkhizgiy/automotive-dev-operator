package buildapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"k8s.io/apimachinery/pkg/types"
	apiserverv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	"k8s.io/client-go/kubernetes"

	automotivev1alpha1 "github.com/centos-automotive-suite/automotive-dev-operator/api/v1alpha1"
	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (a *APIServer) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		username, authType, authErr := a.authenticateRequest(c)
		if authErr != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"reason":  authErr.Reason,
				"details": authErr.Details,
			})
			c.Abort()
			return
		}
		if username != "" {
			c.Set("requester", username)
			c.Set("authType", authType)
		}
		c.Next()
	}
}

func (a *APIServer) refreshAuthConfigIfNeeded() {
	a.authConfigMu.Lock()
	defer a.authConfigMu.Unlock()

	// Check if it's time to refresh (every 60 seconds)
	if time.Since(a.lastAuthConfigCheck) < 60*time.Second {
		return
	}
	a.lastAuthConfigCheck = time.Now()

	namespace := resolveNamespace()
	k8sClient, err := getKubernetesClient()
	if err != nil {
		a.log.Error(err, "failed to get k8s client for auth config refresh", "namespace", namespace)
		return
	}

	// Get the OperatorConfig to check if it changed
	operatorConfig := &automotivev1alpha1.OperatorConfig{}
	key := types.NamespacedName{Name: "config", Namespace: namespace}
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer fetchCancel()
	if err := k8sClient.Get(fetchCtx, key, operatorConfig); err != nil {
		a.log.Error(err, "failed to get OperatorConfig during refresh", "namespace", namespace)
		return
	}

	// Build new config from OperatorConfig (without creating authenticator yet)
	var newConfig *AuthenticationConfiguration
	if operatorConfig.Spec.BuildAPI != nil && operatorConfig.Spec.BuildAPI.Authentication != nil {
		auth := operatorConfig.Spec.BuildAPI.Authentication
		// Deep copy JWT config with Prefix handling
		jwtCopy := make([]apiserverv1beta1.JWTAuthenticator, len(auth.JWT))
		for i, jwt := range auth.JWT {
			jwtCopy[i] = jwt
			if jwt.ClaimMappings.Username.Claim != "" && jwt.ClaimMappings.Username.Prefix == nil {
				emptyPrefix := ""
				jwtCopy[i].ClaimMappings.Username.Prefix = &emptyPrefix
			}
			if jwt.ClaimMappings.Groups.Claim != "" && jwt.ClaimMappings.Groups.Prefix == nil {
				emptyPrefix := ""
				jwtCopy[i].ClaimMappings.Groups.Prefix = &emptyPrefix
			}
		}
		newConfig = &AuthenticationConfiguration{
			ClientID: auth.ClientID,
			Internal: InternalAuthConfig{Prefix: "internal:"},
			JWT:      jwtCopy,
		}
		if auth.Internal != nil && auth.Internal.Prefix != "" {
			newConfig.Internal.Prefix = auth.Internal.Prefix
		}
	}

	// Compare with existing config, only recreate authenticator if config changed
	if authConfigsEqual(a.authConfig, newConfig) {
		return
	}

	// Config changed - need to recreate authenticator
	a.log.Info("auth config changed, recreating OIDC authenticator")

	if newConfig == nil {
		a.authConfig = nil
		a.externalJWT = nil
		a.internalPrefix = ""
		return
	}

	// Create new authenticator
	authn, err := newJWTAuthenticator(context.Background(), *newConfig)
	if err != nil {
		a.log.Error(err, "failed to create JWT authenticator during refresh, keeping existing config")
		return
	}

	// Update config fields
	a.authConfig = newConfig
	a.internalPrefix = newConfig.Internal.Prefix
	if newConfig.ClientID != "" {
		a.oidcClientID = newConfig.ClientID
	}

	a.externalJWT = authn
}

func (a *APIServer) authenticateRequest(c *gin.Context) (string, string, *authError) {
	a.refreshAuthConfigIfNeeded()

	token := extractBearerToken(c)
	if token == "" {
		return "", "", &authError{
			Reason:  "missing_token",
			Details: "No bearer token provided. Set Authorization header with 'Bearer <token>' or use CAIB_TOKEN environment variable.",
		}
	}

	// Track which auth methods were tried for error reporting
	var authAttempts []string
	var oidcError error

	a.authConfigMu.RLock()
	internalJWT := a.internalJWT
	internalPrefix := a.internalPrefix
	externalJWT := a.externalJWT
	a.authConfigMu.RUnlock()

	if internalJWT != nil {
		authAttempts = append(authAttempts, "internal_jwt")
		if subject, ok := validateInternalJWT(token, internalJWT); ok {
			username := subject
			if internalPrefix != "" {
				username = internalPrefix + username
			}
			return username, "internal", nil
		}
	}

	if externalJWT != nil {
		authAttempts = append(authAttempts, "oidc")
		result := a.authenticateExternalJWT(c, token, externalJWT)
		if result.ok {
			if internalJWT != nil {
				if err := a.ensureClientTokenSecret(c, result.username, token); err != nil {
					a.log.Error(err, "failed to ensure client token secret", "username", result.username)
				}
			}
			return result.username, "external", nil
		}
		oidcError = result.err
	}

	// Fallback to kubeconfig TokenReview authentication
	authAttempts = append(authAttempts, "k8s_token_review")

	cfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		a.log.Error(err, "Failed to get REST config for TokenReview fallback")
		return "", "", &authError{
			Reason:  "server_error",
			Details: "Failed to initialize Kubernetes client for token validation. Check build-api logs.",
		}
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		a.log.Error(err, "Failed to create Kubernetes client for TokenReview")
		return "", "", &authError{
			Reason:  "server_error",
			Details: "Failed to create Kubernetes client for token validation. Check build-api logs.",
		}
	}

	tr := &authnv1.TokenReview{Spec: authnv1.TokenReviewSpec{Token: token}}
	res, err := clientset.AuthenticationV1().TokenReviews().Create(c.Request.Context(), tr, metav1.CreateOptions{})
	if err != nil {
		a.log.Error(err, "TokenReview API call failed")
		return "", "", &authError{
			Reason:  "token_review_failed",
			Details: "Failed to validate token with Kubernetes API. The token may be malformed or the server may have connectivity issues.",
		}
	}
	if res.Status.Authenticated {
		username := res.Status.User.Username
		if username == "" {
			return "", "", &authError{
				Reason:  "invalid_token",
				Details: "Token was authenticated but no username was returned.",
			}
		}
		return username, "k8s", nil
	}

	return "", "", a.buildAuthFailureError(authAttempts, oidcError, res.Status.Error)
}

func (a *APIServer) buildAuthFailureError(authAttempts []string, oidcError error, tokenReviewError string) *authError {
	oidcAttempted := false
	for _, method := range authAttempts {
		if method == "oidc" {
			oidcAttempted = true
			break
		}
	}

	// Log full error details server-side for debugging
	if tokenReviewError != "" {
		a.log.Info("TokenReview authentication failed", "error", tokenReviewError)
	}
	if oidcError != nil {
		a.log.Info("OIDC authentication failed", "error", oidcError.Error())
	}

	if !oidcAttempted {
		return &authError{
			Reason:  "invalid_token",
			Details: "Token validation failed. The token may be expired or invalid. Try 'oc login' to refresh your session, then use 'oc whoami -t' for a fresh token.",
		}
	}

	var details strings.Builder
	details.WriteString("Authentication failed. OIDC is configured on this cluster. ")

	if oidcError != nil {
		details.WriteString("OIDC: token validation failed. ")
	} else {
		details.WriteString("OIDC: token not valid for configured issuer. ")
	}

	if tokenReviewError != "" {
		details.WriteString("Kubernetes fallback: token rejected. ")
	} else {
		details.WriteString("Kubernetes fallback: token rejected (may be expired or invalid). ")
	}

	details.WriteString("If using OIDC, ensure you have a valid OIDC token. Otherwise, try 'oc login' to refresh your session.")

	return &authError{
		Reason:  "invalid_token",
		Details: details.String(),
	}
}

func extractBearerToken(c *gin.Context) string {
	authHeader := c.Request.Header.Get("Authorization")
	token, _ := strings.CutPrefix(authHeader, "Bearer ")
	if token != "" {
		return strings.TrimSpace(token)
	}
	token = c.Request.Header.Get("X-Forwarded-Access-Token")
	if token != "" {
		return strings.TrimSpace(token)
	}
	return ""
}

func (a *APIServer) handleGetAuthConfig(c *gin.Context) {
	a.refreshAuthConfigIfNeeded()

	type OIDCConfigResponse struct {
		ClientID string `json:"clientId,omitempty"`
		JWT      []struct {
			Issuer struct {
				URL       string   `json:"url"`
				Audiences []string `json:"audiences,omitempty"`
			} `json:"issuer"`
			ClaimMappings struct {
				Username struct {
					Claim  string `json:"claim"`
					Prefix string `json:"prefix,omitempty"`
				} `json:"username"`
			} `json:"claimMappings"`
		} `json:"jwt"`
	}

	a.authConfigMu.RLock()
	clientID := a.oidcClientID
	authConfig := a.authConfig
	a.authConfigMu.RUnlock()

	response := OIDCConfigResponse{
		ClientID: clientID,
	}

	if clientID != "" && authConfig != nil {
		clientIDInAudience := false
		for _, jwtConfig := range authConfig.JWT {
			for _, audience := range jwtConfig.Issuer.Audiences {
				if audience == clientID {
					clientIDInAudience = true
					break
				}
			}
		}
		if !clientIDInAudience && len(authConfig.JWT) > 0 {
			a.log.Info("OIDC clientId does not match any JWT audience", "clientId", clientID)
		}
	}

	a.authConfigMu.RLock()
	externalJWTWorking := a.externalJWT != nil
	a.authConfigMu.RUnlock()

	if authConfig != nil && len(authConfig.JWT) > 0 && externalJWTWorking {
		for _, jwtConfig := range authConfig.JWT {
			prefix := ""
			if jwtConfig.ClaimMappings.Username.Prefix != nil {
				prefix = *jwtConfig.ClaimMappings.Username.Prefix
			}
			response.JWT = append(response.JWT, struct {
				Issuer struct {
					URL       string   `json:"url"`
					Audiences []string `json:"audiences,omitempty"`
				} `json:"issuer"`
				ClaimMappings struct {
					Username struct {
						Claim  string `json:"claim"`
						Prefix string `json:"prefix,omitempty"`
					} `json:"username"`
				} `json:"claimMappings"`
			}{
				Issuer: struct {
					URL       string   `json:"url"`
					Audiences []string `json:"audiences,omitempty"`
				}{
					URL:       jwtConfig.Issuer.URL,
					Audiences: jwtConfig.Issuer.Audiences,
				},
				ClaimMappings: struct {
					Username struct {
						Claim  string `json:"claim"`
						Prefix string `json:"prefix,omitempty"`
					} `json:"username"`
				}{
					Username: struct {
						Claim  string `json:"claim"`
						Prefix string `json:"prefix,omitempty"`
					}{
						Claim:  jwtConfig.ClaimMappings.Username.Claim,
						Prefix: prefix,
					},
				},
			})
		}
	}

	if len(response.JWT) == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, response)
}
