package buildapi

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	apiserverv1beta1 "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	tokenunion "k8s.io/apiserver/pkg/authentication/token/union"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	oidcauth "k8s.io/apiserver/plugin/pkg/authenticator/token/oidc"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	automotivev1alpha1 "github.com/centos-automotive-suite/automotive-dev-operator/api/v1alpha1"
	apiserver "k8s.io/apiserver/pkg/apis/apiserver"
)

// AuthenticationConfiguration defines the authentication configuration structure.
type AuthenticationConfiguration struct {
	ClientID string                              `json:"clientId"`
	Internal InternalAuthConfig                  `json:"internal"`
	JWT      []apiserverv1beta1.JWTAuthenticator `json:"jwt"`
}

// InternalAuthConfig defines internal authentication configuration.
type InternalAuthConfig struct {
	Prefix string `json:"prefix"`
}

func loadAuthenticationConfigurationFromFile(ctx context.Context, path string) (*AuthenticationConfiguration, authenticator.Token, string, error) {
	if path == "" {
		return nil, nil, "", nil
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, nil, "", fmt.Errorf("path is a directory, not a file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, "", fmt.Errorf("file not found: %s", path)
		}
		return nil, nil, "", fmt.Errorf("failed to read file %s: %w", path, err)
	}

	var config AuthenticationConfiguration

	var wrapped struct {
		Authentication AuthenticationConfiguration `yaml:"authentication" json:"authentication"`
	}
	wrappedErr := yaml.Unmarshal(content, &wrapped)
	if wrappedErr == nil {
		if len(wrapped.Authentication.JWT) > 0 {
			config = wrapped.Authentication
		} else if wrapped.Authentication.Internal.Prefix != "" {
			config = wrapped.Authentication
		} else {
			if err := yaml.Unmarshal(content, &config); err != nil {
				return nil, nil, "", fmt.Errorf("failed to parse auth config: wrapped parse succeeded but JWT empty (jwt_count=0, internal_prefix=%q), direct parse failed: %w. This suggests YAML structure mismatch with Kubernetes JWTAuthenticator format", wrapped.Authentication.Internal.Prefix, err)
			}
		}
	} else {
		if err := yaml.Unmarshal(content, &config); err != nil {
			return nil, nil, "", fmt.Errorf("failed to parse auth config (tried wrapped and direct): wrapped_err=%v, direct_err=%w", wrappedErr, err)
		}
	}

	if len(config.JWT) == 0 {
		var raw struct {
			Authentication struct {
				ClientID string `yaml:"clientId"`
				Internal struct {
					Prefix string `yaml:"prefix"`
				} `yaml:"internal"`
				JWT []struct {
					Issuer struct {
						URL                  string   `yaml:"url"`
						Audiences            []string `yaml:"audiences"`
						CertificateAuthority string   `yaml:"certificateAuthority"`
					} `yaml:"issuer"`
					ClaimMappings struct {
						Username struct {
							Claim  string `yaml:"claim"`
							Prefix string `yaml:"prefix"`
						} `yaml:"username"`
					} `yaml:"claimMappings"`
				} `yaml:"jwt"`
			} `yaml:"authentication"`
		}
		if err := yaml.Unmarshal(content, &raw); err == nil && len(raw.Authentication.JWT) > 0 {
			if config.ClientID == "" {
				config.ClientID = raw.Authentication.ClientID
			}
			if config.Internal.Prefix == "" {
				config.Internal.Prefix = raw.Authentication.Internal.Prefix
			}
			for _, entry := range raw.Authentication.JWT {
				prefix := entry.ClaimMappings.Username.Prefix
				jwt := apiserverv1beta1.JWTAuthenticator{
					Issuer: apiserverv1beta1.Issuer{
						URL:                  entry.Issuer.URL,
						Audiences:            entry.Issuer.Audiences,
						CertificateAuthority: entry.Issuer.CertificateAuthority,
					},
					ClaimMappings: apiserverv1beta1.ClaimMappings{
						Username: apiserverv1beta1.PrefixedClaimOrExpression{
							Claim:  entry.ClaimMappings.Username.Claim,
							Prefix: &prefix,
						},
					},
				}
				config.JWT = append(config.JWT, jwt)
			}
		}
	}

	if config.Internal.Prefix == "" {
		config.Internal.Prefix = "internal:"
	}

	authn, err := newJWTAuthenticator(ctx, config)
	if err != nil {
		return nil, nil, "", err
	}
	return &config, authn, config.Internal.Prefix, nil
}

func newJWTAuthenticator(ctx context.Context, config AuthenticationConfiguration) (authenticator.Token, error) {
	if len(config.JWT) == 0 {
		return nil, nil
	}

	scheme := runtime.NewScheme()
	_ = apiserver.AddToScheme(scheme)
	_ = apiserverv1beta1.AddToScheme(scheme)

	jwtAuthenticators := make([]authenticator.Token, 0, len(config.JWT))
	for _, jwtAuthenticator := range config.JWT {
		var oidcCAContent oidcauth.CAContentProvider
		if jwtAuthenticator.Issuer.CertificateAuthority != "" {
			var oidcCAError error
			// Try to read CA from file, or use it as inline PEM
			if _, err := os.Stat(jwtAuthenticator.Issuer.CertificateAuthority); err == nil {
				oidcCAContent, oidcCAError = dynamiccertificates.NewDynamicCAContentFromFile(
					"oidc-authenticator",
					jwtAuthenticator.Issuer.CertificateAuthority,
				)
				jwtAuthenticator.Issuer.CertificateAuthority = ""
			} else {
				oidcCAContent, oidcCAError = dynamiccertificates.NewStaticCAContent(
					"oidc-authenticator",
					[]byte(jwtAuthenticator.Issuer.CertificateAuthority),
				)
			}
			if oidcCAError != nil {
				return nil, oidcCAError
			}
		}

		var jwtAuthenticatorUnversioned apiserver.JWTAuthenticator
		if err := scheme.Convert(&jwtAuthenticator, &jwtAuthenticatorUnversioned, nil); err != nil {
			return nil, err
		}

		oidcAuth, err := oidcauth.New(ctx, oidcauth.Options{
			JWTAuthenticator:     jwtAuthenticatorUnversioned,
			CAContentProvider:    oidcCAContent,
			SupportedSigningAlgs: oidcauth.AllValidSigningAlgorithms(),
		})
		if err != nil {
			return nil, err
		}
		jwtAuthenticators = append(jwtAuthenticators, oidcAuth)
	}
	return tokenunion.NewFailOnError(jwtAuthenticators...), nil
}

// loadAuthenticationConfigurationFromOperatorConfig loads authentication configuration directly from OperatorConfig CRD.
func loadAuthenticationConfigurationFromOperatorConfig(ctx context.Context, k8sClient client.Client, namespace string) (*AuthenticationConfiguration, authenticator.Token, string, error) {
	operatorConfig := &automotivev1alpha1.OperatorConfig{}
	key := types.NamespacedName{Name: "config", Namespace: namespace}

	if err := k8sClient.Get(ctx, key, operatorConfig); err != nil {
		return nil, nil, "", fmt.Errorf("failed to get OperatorConfig %s/%s: %w", namespace, "config", err)
	}

	// Check if authentication is configured
	if operatorConfig.Spec.BuildAPI == nil || operatorConfig.Spec.BuildAPI.Authentication == nil {
		return nil, nil, "", nil // No authentication configured
	}

	auth := operatorConfig.Spec.BuildAPI.Authentication
	config := &AuthenticationConfiguration{
		ClientID: auth.ClientID,
		Internal: InternalAuthConfig{
			Prefix: "internal:",
		},
		JWT: auth.JWT,
	}

	// Set internal prefix if provided
	if auth.Internal != nil && auth.Internal.Prefix != "" {
		config.Internal.Prefix = auth.Internal.Prefix
	}

	// Build authenticator from JWT configuration
	authn, err := newJWTAuthenticator(ctx, *config)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create JWT authenticator: %w", err)
	}

	return config, authn, config.Internal.Prefix, nil
}
