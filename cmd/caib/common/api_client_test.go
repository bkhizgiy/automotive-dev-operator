package caibcommon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/centos-automotive-suite/automotive-dev-operator/cmd/caib/config"
	buildapiclient "github.com/centos-automotive-suite/automotive-dev-operator/internal/buildapi/client"
)

// setupTempConfig redirects config reads/writes to a temp HOME directory.
// Returns a cleanup function.
func setupTempConfig(t *testing.T) func() {
	t.Helper()
	dir, err := os.MkdirTemp("", "caib-api-client-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	origHome := os.Getenv("HOME")
	origXDG := os.Getenv("XDG_CONFIG_HOME")
	_ = os.Setenv("HOME", dir)
	_ = os.Unsetenv("XDG_CONFIG_HOME")
	return func() {
		_ = os.Setenv("HOME", origHome)
		if origXDG != "" {
			_ = os.Setenv("XDG_CONFIG_HOME", origXDG)
		} else {
			_ = os.Unsetenv("XDG_CONFIG_HOME")
		}
		_ = os.RemoveAll(dir)
	}
}

// always401Handler is a handler that unconditionally returns 401.
var always401Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
})

// authErrorFn is an ExecuteWithReauth callback that always returns a 401 error.
func authErrorFn(_ *buildapiclient.Client) error {
	return &fakeAuthError{}
}

// fakeAuthError satisfies the auth.IsAuthError check (its message contains "401").
type fakeAuthError struct{}

func (e *fakeAuthError) Error() string { return "401 Unauthorized" }

func TestExecuteWithReauth_SavedTokenRejected(t *testing.T) {
	cleanup := setupTempConfig(t)
	defer cleanup()

	srv := httptest.NewServer(always401Handler)
	defer srv.Close()

	if err := config.SaveToken("sha256~fakesavedtoken"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	// Empty --token flag (zero value) so CreateBuildAPIClient loads the saved token.
	tok := ""
	err := ExecuteWithReauth(srv.URL, &tok, false, authErrorFn)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "saved token was rejected") {
		t.Errorf("expected 'saved token was rejected' in error, got: %v", err)
	}
}

func TestExecuteWithReauth_ExplicitTokenRejected(t *testing.T) {
	cleanup := setupTempConfig(t)
	defer cleanup()

	srv := httptest.NewServer(always401Handler)
	defer srv.Close()

	// No saved token — user passed an explicit --token flag value.
	tok := "sha256~explicit-flag-token"
	err := ExecuteWithReauth(srv.URL, &tok, false, authErrorFn)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "provided token was rejected") {
		t.Errorf("expected 'provided token was rejected' in error, got: %v", err)
	}
}

func TestExecuteWithReauth_NoTokenTriesOIDCFallback(t *testing.T) {
	cleanup := setupTempConfig(t)
	defer cleanup()

	// Server returns 401 for API calls and 404 for OIDC config (no OIDC configured).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "authconfig") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	// No saved token, no explicit token — should attempt OIDC (non-interactively
	// since no OIDC config) and return some error rather than panicking or opening
	// a browser.
	tok := ""
	err := ExecuteWithReauth(srv.URL, &tok, false, authErrorFn)
	if err == nil {
		t.Fatal("expected error when no token and no OIDC available, got nil")
	}
}

func TestSanitizeToken(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"sha256~abc", "sha256~abc"},
		{"Bearer sha256~abc", "sha256~abc"},
		{"bearer sha256~abc", "sha256~abc"},
		{"BEARER sha256~abc", "sha256~abc"},
		{"  Bearer  sha256~abc  ", "sha256~abc"},
		{"eyJhbGciOiJSUzI1NiJ9.e.sig", "eyJhbGciOiJSUzI1NiJ9.e.sig"},
		{"Bearer eyJhbGciOiJSUzI1NiJ9.e.sig", "eyJhbGciOiJSUzI1NiJ9.e.sig"},
		{"", ""},
		{"   ", ""},
		// Single word "Bearer" with no token — treated as an opaque token, not a prefix.
		{"Bearer", "Bearer"},
	}
	for _, tc := range cases {
		got := sanitizeToken(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeToken(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
