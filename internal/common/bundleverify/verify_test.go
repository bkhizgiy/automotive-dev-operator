package bundleverify

import (
	"context"
	"testing"
)

func TestVerifyBundle_InvalidPEM(t *testing.T) {
	err := VerifyBundle(context.Background(), "quay.io/test/img@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []byte("not-a-pem"))
	if err == nil {
		t.Fatal("expected error for invalid PEM key")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestVerifyBundle_InvalidRef(t *testing.T) {
	// Valid ECDSA P-256 test key (not a real signing key)
	testPEM := []byte(`-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEY1WtPBgOWxlBCpCIuR7SXPJG1sXD
VmOYGDB0PCBPeJQyaK1FGKs06iDQL4DP6jMzqpNL3D5LkF8bOJCGhIFjQ==
-----END PUBLIC KEY-----`)

	err := VerifyBundle(context.Background(), ":::invalid-ref", testPEM)
	if err == nil {
		t.Fatal("expected error for invalid reference")
	}
}
