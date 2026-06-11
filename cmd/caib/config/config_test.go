package config

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive
)

// roundTripFunc adapts a function to http.RoundTripper for concise inline transports.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// writeJumpstarterConfig creates ~/.config/jumpstarter/{config.yaml,clients/mycluster.yaml} under homeDir.
func writeJumpstarterConfig(baseDir, endpoint string) {
	const alias = "mycluster"
	jmpDir := filepath.Join(baseDir, ".config", "jumpstarter")
	ExpectWithOffset(1, os.MkdirAll(filepath.Join(jmpDir, "clients"), 0700)).To(Succeed())

	configYAML := "config:\n  current-client: " + alias + "\n"
	ExpectWithOffset(1, os.WriteFile(filepath.Join(jmpDir, "config.yaml"), []byte(configYAML), 0600)).To(Succeed())

	clientYAML := "endpoint: " + endpoint + "\n"
	ExpectWithOffset(1, os.WriteFile(filepath.Join(jmpDir, "clients", alias+".yaml"), []byte(clientYAML), 0600)).To(Succeed())
}

var _ = Describe("DeriveServerFromJumpstarter", func() {
	var tempDir string
	var origHome, origXDG string

	var origBuildNS string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "caib-derive-test-*")
		Expect(err).NotTo(HaveOccurred())

		origHome = os.Getenv("HOME")
		origXDG = os.Getenv("XDG_CONFIG_HOME")
		origBuildNS = os.Getenv("CAIB_BUILD_API_NAMESPACE")
		Expect(os.Setenv("HOME", tempDir)).To(Succeed())
		Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
		Expect(os.Unsetenv("CAIB_BUILD_API_NAMESPACE")).To(Succeed())
	})

	AfterEach(func() {
		healthHTTPClient = nil
		_ = os.Setenv("HOME", origHome)
		if origXDG != "" {
			_ = os.Setenv("XDG_CONFIG_HOME", origXDG)
		} else {
			_ = os.Unsetenv("XDG_CONFIG_HOME")
		}
		if origBuildNS != "" {
			_ = os.Setenv("CAIB_BUILD_API_NAMESPACE", origBuildNS)
		} else {
			_ = os.Unsetenv("CAIB_BUILD_API_NAMESPACE")
		}
		_ = os.RemoveAll(tempDir)
	})

	It("derives correct URL from .apps. domain and saves config on health 200", func() {
		writeJumpstarterConfig(tempDir, "grpc.lab.apps.example.com:443")

		var requestedURL string
		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requestedURL = req.URL.String()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		result := DeriveServerFromJumpstarter()
		expected := "https://ado-build-api-automotive-dev-operator-system.apps.example.com"

		Expect(result).To(Equal(expected))
		Expect(requestedURL).To(Equal(expected + "/v1/healthz"))

		// Verify it was persisted
		cfg, err := Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.ServerURL).To(Equal(expected))
	})

	It("uses CAIB_BUILD_API_NAMESPACE when set", func() {
		writeJumpstarterConfig(tempDir, "grpc.lab.apps.example.com:443")
		Expect(os.Setenv("CAIB_BUILD_API_NAMESPACE", "custom-ns")).To(Succeed())

		var requestedURL string
		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requestedURL = req.URL.String()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		result := DeriveServerFromJumpstarter()
		expected := "https://ado-build-api-custom-ns.apps.example.com"

		Expect(result).To(Equal(expected))
		Expect(requestedURL).To(Equal(expected + "/v1/healthz"))
	})

	It("derives correct URL using fallback (non-.apps. domain)", func() {
		writeJumpstarterConfig(tempDir, "svc.namespace.cluster.local:443")

		var requestedURL string
		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requestedURL = req.URL.String()
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		result := DeriveServerFromJumpstarter()
		expected := "https://ado-build-api-automotive-dev-operator-system.cluster.local"

		Expect(result).To(Equal(expected))
		Expect(requestedURL).To(Equal(expected + "/v1/healthz"))
	})

	It("returns empty when health check returns non-200", func() {
		writeJumpstarterConfig(tempDir, "grpc.lab.apps.example.com:443")

		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		Expect(DeriveServerFromJumpstarter()).To(BeEmpty())
	})

	It("returns empty when no jumpstarter config exists", func() {
		called := false
		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				called = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		Expect(DeriveServerFromJumpstarter()).To(BeEmpty())
		Expect(called).To(BeFalse(), "health check should not be called when there is no jumpstarter config")
	})

	It("returns empty when health check returns a network error", func() {
		writeJumpstarterConfig(tempDir, "grpc.lab.apps.example.com:443")

		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("connection refused")
			}),
		}

		Expect(DeriveServerFromJumpstarter()).To(BeEmpty())
	})

	It("returns empty when endpoint has fewer than 3 domain labels", func() {
		writeJumpstarterConfig(tempDir, "localhost:443")

		called := false
		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				called = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		Expect(DeriveServerFromJumpstarter()).To(BeEmpty())
		Expect(called).To(BeFalse(), "health check should not be called when domain cannot be derived")
	})
})

var _ = Describe("DefaultServerWithDerive", func() {
	var tempDir string
	var origHome, origXDG, origCAIBServer, origBuildNS string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "caib-default-test-*")
		Expect(err).NotTo(HaveOccurred())

		origHome = os.Getenv("HOME")
		origXDG = os.Getenv("XDG_CONFIG_HOME")
		origCAIBServer = os.Getenv("CAIB_SERVER")
		origBuildNS = os.Getenv("CAIB_BUILD_API_NAMESPACE")
		Expect(os.Setenv("HOME", tempDir)).To(Succeed())
		Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
		Expect(os.Unsetenv("CAIB_SERVER")).To(Succeed())
		Expect(os.Unsetenv("CAIB_BUILD_API_NAMESPACE")).To(Succeed())
	})

	AfterEach(func() {
		healthHTTPClient = nil
		_ = os.Setenv("HOME", origHome)
		if origXDG != "" {
			_ = os.Setenv("XDG_CONFIG_HOME", origXDG)
		} else {
			_ = os.Unsetenv("XDG_CONFIG_HOME")
		}
		if origBuildNS != "" {
			_ = os.Setenv("CAIB_BUILD_API_NAMESPACE", origBuildNS)
		} else {
			_ = os.Unsetenv("CAIB_BUILD_API_NAMESPACE")
		}
		if origCAIBServer != "" {
			_ = os.Setenv("CAIB_SERVER", origCAIBServer)
		} else {
			_ = os.Unsetenv("CAIB_SERVER")
		}
		_ = os.RemoveAll(tempDir)
	})

	It("returns CAIB_SERVER env when set, without calling derive", func() {
		Expect(os.Setenv("CAIB_SERVER", "https://from-env.example.com")).To(Succeed())
		Expect(SaveServerURL("https://from-config.example.com")).To(Succeed())
		writeJumpstarterConfig(tempDir, "grpc.lab.apps.example.com:443")

		called := false
		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				called = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		Expect(DefaultServerWithDerive()).To(Equal("https://from-env.example.com"))
		Expect(called).To(BeFalse(), "derivation should not be attempted when CAIB_SERVER is set")
	})

	It("returns saved config when CAIB_SERVER is empty, without calling derive", func() {
		Expect(SaveServerURL("https://from-config.example.com")).To(Succeed())
		writeJumpstarterConfig(tempDir, "grpc.lab.apps.example.com:443")

		called := false
		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				called = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		Expect(DefaultServerWithDerive()).To(Equal("https://from-config.example.com"))
		Expect(called).To(BeFalse(), "derivation should not be attempted when saved config exists")
	})

	It("falls through to Jumpstarter derivation when env and config are empty", func() {
		writeJumpstarterConfig(tempDir, "grpc.lab.apps.example.com:443")

		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		expected := "https://ado-build-api-automotive-dev-operator-system.apps.example.com"
		Expect(DefaultServerWithDerive()).To(Equal(expected))
	})

	It("returns empty when nothing is configured and no jumpstarter config exists", func() {
		called := false
		healthHTTPClient = &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				called = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}

		Expect(DefaultServerWithDerive()).To(BeEmpty())
		Expect(called).To(BeFalse(), "health check should not be called when there is no jumpstarter config")
	})
})

var _ = Describe("SaveToken and LoadSavedToken", func() {
	var tempDir string
	var origHome, origXDG string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "caib-savetoken-test-*")
		Expect(err).NotTo(HaveOccurred())

		origHome = os.Getenv("HOME")
		origXDG = os.Getenv("XDG_CONFIG_HOME")
		Expect(os.Setenv("HOME", tempDir)).To(Succeed())
		Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
	})

	AfterEach(func() {
		_ = os.Setenv("HOME", origHome)
		if origXDG != "" {
			_ = os.Setenv("XDG_CONFIG_HOME", origXDG)
		} else {
			_ = os.Unsetenv("XDG_CONFIG_HOME")
		}
		_ = os.RemoveAll(tempDir)
	})

	It("saves and loads an opaque token (e.g. oc whoami -t)", func() {
		opaqueToken := "sha256~someRandomOpaqueToken"
		Expect(SaveToken(opaqueToken)).To(Succeed())
		Expect(LoadSavedToken()).To(Equal(opaqueToken))
	})

	It("saves and loads a JWT token", func() {
		fakeJWT := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.sig"
		Expect(SaveToken(fakeJWT)).To(Succeed())
		Expect(LoadSavedToken()).To(Equal(fakeJWT))
	})

	It("normalizes bearer-prefixed tokens before saving", func() {
		Expect(SaveToken("Bearer sha256~someRandomOpaqueToken")).To(Succeed())
		Expect(LoadSavedToken()).To(Equal("sha256~someRandomOpaqueToken"))
	})

	It("normalizes bearer-prefixed tokens with extra spaces", func() {
		Expect(SaveToken("  Bearer   eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.sig  ")).To(Succeed())
		Expect(LoadSavedToken()).To(Equal("eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.sig"))
	})

	It("returns empty string when no token is saved", func() {
		Expect(LoadSavedToken()).To(Equal(""))
	})

	It("clears a saved token when empty string is passed", func() {
		Expect(SaveToken("some-token")).To(Succeed())
		Expect(SaveToken("")).To(Succeed())
		Expect(LoadSavedToken()).To(Equal(""))
	})

	It("SaveServerURL preserves the saved token", func() {
		Expect(SaveToken("my-token")).To(Succeed())
		Expect(SaveServerURL("https://build-api.example.com")).To(Succeed())

		Expect(LoadSavedToken()).To(Equal("my-token"))
		cfg, err := Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ServerURL).To(Equal("https://build-api.example.com"))
	})

	It("SaveToken preserves the server URL", func() {
		Expect(SaveServerURL("https://build-api.example.com")).To(Succeed())
		Expect(SaveToken("my-token")).To(Succeed())

		cfg, err := Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ServerURL).To(Equal("https://build-api.example.com"))
		Expect(cfg.SavedToken).To(Equal("my-token"))
	})

	It("stores token in cli.json with 0600 permissions", func() {
		Expect(SaveToken("secret-token")).To(Succeed())

		configPath := filepath.Join(tempDir, ".config", "caib", "cli.json")
		info, err := os.Stat(configPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
	})
})

var _ = Describe("Read with XDG config override", func() {
	var tempDir string
	var origHome, origXDG string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "caib-xdg-config-test-*")
		Expect(err).NotTo(HaveOccurred())

		origHome = os.Getenv("HOME")
		origXDG = os.Getenv("XDG_CONFIG_HOME")
		Expect(os.Setenv("HOME", filepath.Join(tempDir, "home"))).To(Succeed())
		Expect(os.Setenv("XDG_CONFIG_HOME", filepath.Join(tempDir, "custom-config"))).To(Succeed())
	})

	AfterEach(func() {
		_ = os.Setenv("HOME", origHome)
		if origXDG != "" {
			_ = os.Setenv("XDG_CONFIG_HOME", origXDG)
		} else {
			_ = os.Unsetenv("XDG_CONFIG_HOME")
		}
		_ = os.RemoveAll(tempDir)
	})

	It("reads cli.json from XDG_CONFIG_HOME when set", func() {
		configDir := filepath.Join(tempDir, "custom-config", "caib")
		Expect(os.MkdirAll(configDir, 0700)).To(Succeed())
		Expect(os.WriteFile(
			filepath.Join(configDir, "cli.json"),
			[]byte("{\"server_url\":\"https://from-xdg.example.com\"}"),
			0600,
		)).To(Succeed())

		cfg, err := Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.ServerURL).To(Equal("https://from-xdg.example.com"))
	})
})
