package update

import (
	"crypto/ed25519"
	"crypto/x509"
	"embed"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"path"
)

// Signature is the file of a release's ed25519 signature of checksums.txt: the raw 64 bytes, made with the
// release key (scripts/sign-checksums) by the release workflow.
const Signature = Checksums + ".sig"

// releaseKeys are the public keys a release's checksums.txt may be signed with, one PKIX PEM file each
// (-----BEGIN PUBLIC KEY-----). A new key is added a release before the old one is retired, so a binary of
// either release verifies the next: docs/development.md (Releasing) has the rotation.
//
//go:embed release-keys/*.pem
var releaseKeys embed.FS

// ReleaseKeys returns the public keys embedded from release-keys/.
func ReleaseKeys() ([]ed25519.PublicKey, error) {
	names, err := fs.Glob(releaseKeys, "release-keys/*.pem")
	if err != nil {
		return nil, err
	}
	var keys []ed25519.PublicKey
	for _, name := range names {
		b, err := releaseKeys.ReadFile(name)
		if err != nil {
			return nil, err
		}
		key, err := ParsePublicKey(b)
		if err != nil {
			return nil, fmt.Errorf("release key %s: %w", path.Base(name), err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("no release keys embedded")
	}
	return keys, nil
}

// ParsePublicKey reads an ed25519 public key from a PKIX PEM block, as openssl pkey -pubout writes it.
func ParsePublicKey(b []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("not a PEM public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ed, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("a %T, not an ed25519 key", key)
	}
	return ed, nil
}

// SignatureError is a checksums.txt.sig that isn't a signature of checksums.txt by one of the release keys:
// the release wasn't made by the release workflow, or was changed since.
type SignatureError struct {
	Reason string
}

func (e *SignatureError) Error() string {
	return fmt.Sprintf("%s doesn't verify %s: %s", Signature, Checksums, e.Reason)
}

// Verify checks that sig is an ed25519 signature of sums by one of keys, and returns a *SignatureError if it
// isn't.
func Verify(sums, sig []byte, keys []ed25519.PublicKey) error {
	if len(sig) != ed25519.SignatureSize {
		return &SignatureError{Reason: fmt.Sprintf("it has %d bytes, not an ed25519 signature's %d",
			len(sig), ed25519.SignatureSize)}
	}
	for _, key := range keys {
		if ed25519.Verify(key, sums, sig) {
			return nil
		}
	}
	return &SignatureError{Reason: "it isn't signed by a release key"}
}
