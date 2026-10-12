package update

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// A release whose checksums.txt.sig is missing, malformed, made with another key or of another
// checksums.txt isn't installed, the old binary kept, and its archive isn't downloaded.
func TestInstallVerifiesTheSignature(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(t *testing.T, r *fakeRelease)
		want   string
	}{
		{"missing", func(_ *testing.T, r *fakeRelease) { delete(r.files, Signature) },
			"release v0.6.0 has no checksums.txt.sig"},
		{"malformed", func(_ *testing.T, r *fakeRelease) { r.files[Signature] = r.files[Signature][:32] },
			"checksums.txt.sig doesn't verify checksums.txt: it has 32 bytes, not an ed25519 signature's 64"},
		{"empty", func(_ *testing.T, r *fakeRelease) { r.files[Signature] = nil },
			"checksums.txt.sig doesn't verify checksums.txt: it has 0 bytes, not an ed25519 signature's 64"},
		{"another key", func(t *testing.T, r *fakeRelease) {
			r.files[Signature] = ed25519.Sign(newKey(t), r.files[Checksums])
		}, "checksums.txt.sig doesn't verify checksums.txt: it isn't signed by a release key"},
		{"checksums changed", func(t *testing.T, r *fakeRelease) {
			// A tampered archive with its own sum, the signature left as it was.
			name := ArchiveName("v0.6.0", runtime.GOOS, runtime.GOARCH)
			r.files[name] = archive(t, "orchestra v0.6.0, tampered with\n")
			r.files[Checksums] = append(r.files[Checksums], "\n"...)
		}, "checksums.txt.sig doesn't verify checksums.txt: it isn't signed by a release key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
			c.change(t, r)
			u := r.serve(t)
			exe, _ := installed(t)

			_, err := u.Install(t.Context(), latest(t, u), exe)
			if err == nil || err.Error() != c.want {
				t.Errorf("err = %v, want %q", err, c.want)
			}
			_, missing := errors.AsType[*MissingAssetError](err)
			_, bad := errors.AsType[*SignatureError](err)
			if !missing && !bad {
				t.Errorf("err = %#v, want a *MissingAssetError or a *SignatureError", err)
			}
			assertUntouched(t, exe)
			archive := "/download/" + ArchiveName("v0.6.0", runtime.GOOS, runtime.GOARCH)
			if got := r.requested(); slices.Contains(got, archive) {
				t.Errorf("requested %q, want the archive left alone", got)
			}
		})
	}
}

// A release signed with any of the keys, an old one and a new one during a rotation, verifies.
func TestInstallTakesAnyReleaseKey(t *testing.T) {
	r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
	u := r.serve(t)
	u.Keys = append([]ed25519.PublicKey{newKey(t).Public().(ed25519.PublicKey)}, u.Keys...)
	exe, _ := installed(t)

	if _, err := u.Install(t.Context(), latest(t, u), exe); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(exe); err != nil || string(b) != "orchestra v0.6.0\n" {
		t.Errorf("the binary is %q (%v), want the release's", b, err)
	}
}

// The keys committed in release-keys/ parse, the maintainer's among them.
func TestReleaseKeys(t *testing.T) {
	keys, err := ReleaseKeys()
	if err != nil {
		t.Fatal(err)
	}
	b, err := releaseKeys.ReadFile("release-keys/2026-10.pem")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ParsePublicKey(b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(keys, func(k ed25519.PublicKey) bool { return k.Equal(key) }) {
		t.Errorf("ReleaseKeys() = %x, want 2026-10.pem's %x among them", keys, key)
	}
}

// ParsePublicKey takes only an ed25519 key in a PKIX PEM block.
func TestParsePublicKeyRefusesOtherKeys(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&ec.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, pem, want string
	}{
		{"ecdsa", string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), "not an ed25519 key"},
		{"private", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), "not a PEM public key"},
		{"not PEM", "MCowBQYDK2VwAyEAQtJ/wvLswsjQ/ZdWOmUEf2Bc14cFvf8lORE4uqdx0hk=\n", "not a PEM public key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParsePublicKey([]byte(c.pem)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}
