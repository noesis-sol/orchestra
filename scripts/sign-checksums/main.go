// Command sign-checksums signs a release's checksums.txt with the release key, for orchestra update to
// verify (internal/update): it writes the raw 64-byte ed25519 signature to checksums.txt.sig beside it. The
// key is a PKCS#8 PEM private key (openssl genpkey -algorithm ed25519) in $ORCHESTRA_RELEASE_KEY, the
// repository secret of that name in the release workflow. It refuses a key whose public half isn't among
// internal/update/release-keys, as orchestra update would refuse the release. docs/development.md
// (Releasing) has the whole process.
//
//	ORCHESTRA_RELEASE_KEY="$(cat release-key.pem)" go run ./scripts/sign-checksums dist/checksums.txt
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/noesis-sol/orchestra/internal/update"
)

// keyEnv is the environment variable holding the private key.
const keyEnv = "ORCHESTRA_RELEASE_KEY"

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stderr))
}

// run signs the file args names with the key getenv gives, and returns the exit code.
func run(args []string, getenv func(string) string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, "usage: %s=<PEM key> sign-checksums <checksums.txt>\n", keyEnv)
		return 2
	}
	keys, err := update.ReleaseKeys()
	if err == nil {
		err = signFile(args[0], []byte(getenv(keyEnv)), keys)
	}
	if err != nil {
		fmt.Fprintln(stderr, "sign-checksums:", err)
		return 1
	}
	return 0
}

// signFile signs the file at name with keyPEM, which must belong to one of keys, and writes the signature
// to name.sig.
func signFile(name string, keyPEM []byte, keys []ed25519.PublicKey) error {
	data, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	sig, err := sign(data, keyPEM, keys)
	if err != nil {
		return err
	}
	return os.WriteFile(name+".sig", sig, 0o644)
}

// sign returns data's signature with keyPEM, a PKCS#8 PEM ed25519 private key, once it verifies with keys as
// orchestra update verifies it.
func sign(data, keyPEM []byte, keys []ed25519.PublicKey) ([]byte, error) {
	if len(keyPEM) == 0 {
		return nil, fmt.Errorf("$%s is empty: it holds the PEM private key", keyEnv)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("$%s is not a PKCS#8 PEM private key (-----BEGIN PRIVATE KEY-----)", keyEnv)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("$%s: %w", keyEnv, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("$%s is a %T, not an ed25519 key", keyEnv, parsed)
	}
	sig := ed25519.Sign(key, data)
	if err := update.Verify(data, sig, keys); err != nil {
		if _, ok := errors.AsType[*update.SignatureError](err); ok {
			return nil, fmt.Errorf("$%s's public key is not in internal/update/release-keys, "+
				"so orchestra update would refuse the release: add it there first", keyEnv)
		}
		return nil, err
	}
	return sig, nil
}
