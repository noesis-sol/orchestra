package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/update"
)

// newKey is a new ed25519 key pair, the private key as openssl genpkey writes it.
func newKey(t *testing.T) (ed25519.PublicKey, []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return public, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// The signature written beside checksums.txt is the raw 64 bytes orchestra update verifies.
func TestSignWritesWhatUpdateVerifies(t *testing.T) {
	public, private := newKey(t)
	name := filepath.Join(t.TempDir(), update.Checksums)
	sums := []byte("0123  orchestra_0.6.0_darwin_arm64.tar.gz\n")
	if err := os.WriteFile(name, sums, 0o644); err != nil {
		t.Fatal(err)
	}
	other, _ := newKey(t)

	if err := signFile(name, private, []ed25519.PublicKey{other, public}); err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(filepath.Join(filepath.Dir(name), update.Signature))
	if err != nil {
		t.Fatal(err)
	}
	if err := update.Verify(sums, sig, []ed25519.PublicKey{public}); err != nil {
		t.Error(err)
	}
}

// A key that isn't a release key, or isn't an ed25519 PKCS#8 PEM key, signs nothing.
func TestSignRefusesABadKey(t *testing.T) {
	public, _ := newKey(t)
	_, unknown := newKey(t)
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, key, want string
	}{
		{"not a release key", string(unknown), "public key is not in internal/update/release-keys"},
		{"empty", "", "$ORCHESTRA_RELEASE_KEY is empty"},
		{"not PEM", "secret", "is not a PKCS#8 PEM private key"},
		{"ecdsa", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "not an ed25519 key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), update.Checksums)
			if err := os.WriteFile(name, []byte("sums\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			err := signFile(name, []byte(c.key), []ed25519.PublicKey{public})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
			if _, err := os.Stat(name + ".sig"); !os.IsNotExist(err) {
				t.Errorf("%s.sig written: %v", name, err)
			}
		})
	}
}

// run takes the key from $ORCHESTRA_RELEASE_KEY and checks it against the embedded release keys, which a
// test key isn't among.
func TestRunChecksTheReleaseKeys(t *testing.T) {
	_, private := newKey(t)
	name := filepath.Join(t.TempDir(), update.Checksums)
	if err := os.WriteFile(name, []byte("sums\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	getenv := func(k string) string {
		if k == keyEnv {
			return string(private)
		}
		return ""
	}
	if code := run([]string{name}, getenv, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "not in internal/update/release-keys") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := run(nil, getenv, &stderr); code != 2 || !strings.HasPrefix(stderr.String(), "usage:") {
		t.Errorf("no arguments: exit %d, stderr %q", code, stderr.String())
	}
}
