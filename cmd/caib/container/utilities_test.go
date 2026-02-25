package container

import (
	"encoding/json"
	"testing"
)

func TestSanitizeBuildName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple lowercase", input: "my-build", want: "my-build"},
		{name: "uppercase converted", input: "MyBuild", want: "mybuild"},
		{name: "special chars replaced", input: "my_build@v1", want: "my-build-v1"},
		{name: "leading/trailing dashes trimmed", input: "---build---", want: "build"},
		{name: "dots replaced", input: "my.build.name", want: "my-build-name"},
		{name: "long name truncated", input: "this-is-a-very-long-build-name-that-should-be-truncated-because-it-exceeds-the-limit", want: "this-is-a-very-long-build-name-that-should-be-trun"},
		{name: "empty string fallback", input: "", want: "build"},
		{name: "all special chars fallback", input: "!!!@@@###", want: "build"},
		{name: "spaces replaced", input: "my build name", want: "my-build-name"},
		{name: "mixed case and special", input: "My_App.V2", want: "my-app-v2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeBuildName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeBuildName(%q) = %q, want %q", tt.input, got, tt.want)
			}
			// Result must be a valid K8s name
			if got != "build" && !isValidKubernetesName(got) {
				t.Errorf("sanitizeBuildName(%q) = %q is not a valid Kubernetes name", tt.input, got)
			}
		})
	}
}

func TestIsValidKubernetesName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "valid simple", input: "my-build", want: true},
		{name: "valid single char", input: "a", want: true},
		{name: "valid numeric", input: "123", want: true},
		{name: "valid alphanumeric", input: "build-123-abc", want: true},
		{name: "invalid empty", input: "", want: false},
		{name: "invalid uppercase", input: "MyBuild", want: false},
		{name: "invalid leading dash", input: "-build", want: false},
		{name: "invalid trailing dash", input: "build-", want: false},
		{name: "invalid underscore", input: "my_build", want: false},
		{name: "invalid dot", input: "my.build", want: false},
		{name: "invalid spaces", input: "my build", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidKubernetesName(tt.input)
			if got != tt.want {
				t.Errorf("isValidKubernetesName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractRegistryCredentials(t *testing.T) {
	tests := []struct {
		name         string
		primaryRef   string
		secondaryRef string
		envUser      string
		envPass      string
		wantURL      string
		wantUser     string
		wantPass     string
	}{
		{
			name:       "quay.io reference",
			primaryRef: "quay.io/myorg/myimage:latest",
			envUser:    "user1",
			envPass:    "pass1",
			wantURL:    "quay.io",
			wantUser:   "user1",
			wantPass:   "pass1",
		},
		{
			name:       "docker hub implicit",
			primaryRef: "myimage:latest",
			wantURL:    "docker.io",
		},
		{
			name:       "registry with port",
			primaryRef: "localhost:5000/myimage:latest",
			wantURL:    "localhost:5000",
		},
		{
			name:         "falls back to secondary ref",
			primaryRef:   "",
			secondaryRef: "ghcr.io/org/image:v1",
			wantURL:      "ghcr.io",
		},
		{
			name:       "both refs empty",
			primaryRef: "",
			wantURL:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("REGISTRY_USERNAME", tt.envUser)
			t.Setenv("REGISTRY_PASSWORD", tt.envPass)

			gotURL, gotUser, gotPass := extractRegistryCredentials(tt.primaryRef, tt.secondaryRef)
			if gotURL != tt.wantURL {
				t.Errorf("URL = %q, want %q", gotURL, tt.wantURL)
			}
			if gotUser != tt.wantUser {
				t.Errorf("username = %q, want %q", gotUser, tt.wantUser)
			}
			if gotPass != tt.wantPass {
				t.Errorf("password = %q, want %q", gotPass, tt.wantPass)
			}
		})
	}
}

func TestValidateRegistryCredentials(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		user    string
		pass    string
		wantErr bool
	}{
		{name: "both set", url: "quay.io", user: "u", pass: "p", wantErr: false},
		{name: "neither set", url: "quay.io", user: "", pass: "", wantErr: false},
		{name: "no URL", url: "", user: "", pass: "", wantErr: false},
		{name: "username only", url: "quay.io", user: "u", pass: "", wantErr: true},
		{name: "password only", url: "quay.io", user: "", pass: "p", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRegistryCredentials(tt.url, tt.user, tt.pass)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRegistryCredentials() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildDockerConfigJSON(t *testing.T) {
	configJSON, err := buildDockerConfigJSON("quay.io", "myuser", "mypass")
	if err != nil {
		t.Fatalf("buildDockerConfigJSON() error = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(configJSON), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	auths, ok := parsed["auths"].(map[string]any)
	if !ok {
		t.Fatal("missing 'auths' key")
	}

	entry, ok := auths["quay.io"].(map[string]any)
	if !ok {
		t.Fatal("missing 'quay.io' entry in auths")
	}

	if entry["username"] != "myuser" {
		t.Errorf("username = %v, want myuser", entry["username"])
	}
	if entry["password"] != "mypass" {
		t.Errorf("password = %v, want mypass", entry["password"])
	}
	if entry["auth"] == nil || entry["auth"] == "" {
		t.Error("auth field should be non-empty base64")
	}
}
