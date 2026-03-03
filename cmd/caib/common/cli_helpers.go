package caibcommon

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// SupportsColorOutput returns true when terminal supports color output.
func SupportsColorOutput() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if term == "" || term == "dumb" {
		return false
	}
	ci := strings.TrimSpace(os.Getenv("CI")) != ""
	if ci {
		if os.Getenv("GITHUB_ACTIONS") == "" &&
			os.Getenv("GITLAB_CI") == "" &&
			os.Getenv("CIRCLECI") == "" &&
			os.Getenv("TRAVIS") == "" &&
			os.Getenv("BUILDKITE") == "" {
			return false
		}
	}
	return true
}

// WriteRegistryCredentialsFile writes registry credentials to a mode-0600 temp file.
func WriteRegistryCredentialsFile(token string) (string, error) {
	creds, err := json.Marshal(map[string]string{
		"username": "serviceaccount",
		"token":    token,
	})
	if err != nil {
		return "", err
	}

	f, err := os.CreateTemp("", "caib-registry-creds-*.json")
	if err != nil {
		return "", err
	}
	name := f.Name()

	if _, err := f.Write(creds); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Chmod(name, 0600); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// ValidateOutputRequiresPush validates --output usage with a push flag.
func ValidateOutputRequiresPush(output, pushRef, flagName string) error {
	if output == "" {
		return nil
	}
	if pushRef == "" {
		return fmt.Errorf("--output requires %s to download from registry", flagName)
	}
	return nil
}
