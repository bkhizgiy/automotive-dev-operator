package auth

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func GetOIDCConfigFromAPI(serverURL string) (*OIDCConfig, error) {
	insecureTLS := strings.EqualFold(os.Getenv("CAIB_INSECURE_TLS"), "true") || os.Getenv("CAIB_INSECURE_TLS") == "1"
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureTLS},
		},
	}

	configURL := strings.TrimSuffix(serverURL, "/") + "/v1/auth/config"
	req, err := http.NewRequest("GET", configURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var apiResponse struct {
		ClientID string `json:"clientId"`
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

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, nil
	}

	if len(apiResponse.JWT) == 0 {
		return nil, nil
	}

	jwtConfig := apiResponse.JWT[0]

	clientID := apiResponse.ClientID
	if clientID == "" {
		return nil, fmt.Errorf("OIDC client ID is required but not provided by the server")
	}

	return &OIDCConfig{
		IssuerURL: jwtConfig.Issuer.URL,
		ClientID:  clientID,
		Scopes:    []string{"openid", "profile", "email"},
	}, nil
}

// GetOIDCConfigFromLocalConfig tries to read from local config file
func GetOIDCConfigFromLocalConfig() (*OIDCConfig, error) {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, tokenCacheDir, "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config struct {
		IssuerURL string   `json:"issuer_url"`
		ClientID  string   `json:"client_id"`
		Scopes    []string `json:"scopes"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	if config.IssuerURL == "" || config.ClientID == "" {
		return nil, fmt.Errorf("invalid config: issuer_url and client_id required")
	}

	scopes := config.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}

	return &OIDCConfig{
		IssuerURL: config.IssuerURL,
		ClientID:  config.ClientID,
		Scopes:    scopes,
	}, nil
}

// SaveOIDCConfig saves OIDC config to local file
func SaveOIDCConfig(config *OIDCConfig) error {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, tokenCacheDir, "config.json")

	configData := map[string]interface{}{
		"issuer_url": config.IssuerURL,
		"client_id":  config.ClientID,
		"scopes":     config.Scopes,
	}

	data, err := json.MarshalIndent(configData, "", "  ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0600)
}
