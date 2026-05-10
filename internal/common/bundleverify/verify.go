// Package bundleverify provides cosign signature verification for Tekton Bundles.
package bundleverify

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sigstore/cosign/v2/pkg/cosign"
	ociremote "github.com/sigstore/cosign/v2/pkg/oci/remote"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
)

// VerifyBundle verifies the cosign signature of an OCI image reference using the
// given cosign public key (PEM-encoded). Optional ociremote.Option values are
// forwarded to cosign for registry authentication.
func VerifyBundle(ctx context.Context, bundleRef string, cosignPubKeyPEM []byte, registryOpts ...ociremote.Option) error {
	pubKey, err := cryptoutils.UnmarshalPEMToPublicKey(cosignPubKeyPEM)
	if err != nil {
		return fmt.Errorf("parsing cosign public key: %w", err)
	}

	verifier, err := signature.LoadDefaultVerifier(pubKey)
	if err != nil {
		return fmt.Errorf("creating verifier: %w", err)
	}

	ref, err := name.ParseReference(bundleRef)
	if err != nil {
		return fmt.Errorf("parsing bundle reference %q: %w", bundleRef, err)
	}

	checkOpts := &cosign.CheckOpts{
		SigVerifier:        verifier,
		IgnoreTlog:         true, // key-based verification — no Rekor transparency log
		IgnoreSCT:          true, // key-based verification — no Fulcio SCT
		RegistryClientOpts: registryOpts,
	}

	_, _, err = cosign.VerifyImageSignatures(ctx, ref, checkOpts)
	if err != nil {
		return fmt.Errorf("cosign verification failed for %q: %w", bundleRef, err)
	}

	return nil
}
