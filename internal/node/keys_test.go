package node

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNodeKeys (node-identity 1.1): keys are created once in owner-only files, reload unchanged and are refused
// when others may read them, and errors never carry key material.
func TestNodeKeys(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadKeys(dir); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("no keys: %v", err)
	}
	k, err := GenerateKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{SignKeyFile, BoxKeyFile} {
		st, err := os.Stat(filepath.Join(dir, KeysDir, name))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, st, err)
		}
	}
	if st, err := os.Stat(filepath.Join(dir, KeysDir)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("keys dir: %v %v", st, err)
	}
	got, err := LoadKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Sign.Equal(k.Sign) || !got.Box.Equal(k.Box) || got.Fingerprint() != k.Fingerprint() {
		t.Fatal("reloaded keys differ")
	}
	sig := ed25519.Sign(got.Sign, []byte("message"))
	if !ed25519.Verify(k.SignPublic(), []byte("message"), sig) {
		t.Fatal("reloaded signing key does not match")
	}
	if _, err = GenerateKeys(dir); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("keys replaced: %v", err)
	}

	sign := filepath.Join(dir, KeysDir, SignKeyFile)
	secret, _ := os.ReadFile(sign)
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644} {
		if err = os.Chmod(sign, mode); err != nil {
			t.Fatal(err)
		}
		_, err = LoadKeys(dir)
		if err == nil || !strings.Contains(err.Error(), "chmod 600") || strings.Contains(err.Error(), strings.TrimSpace(string(secret))) {
			t.Fatalf("mode %o: %v", mode, err)
		}
	}
	if c := keysCheck(dir); c.Status != Fail || !strings.Contains(c.Detail, "chmod 600") {
		t.Fatalf("doctor on a loose key: %+v", c)
	}
	if err = os.Chmod(sign, 0o600); err != nil {
		t.Fatal(err)
	}
	if c := keysCheck(dir); c.Status != Pass || !strings.Contains(c.Detail, k.Fingerprint()) {
		t.Fatalf("doctor: %+v", c)
	}

	// One key file gone or damaged is an error, not a node without keys that could enroll over its identity.
	if err = os.WriteFile(sign, []byte("not hex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadKeys(dir); err == nil || errors.Is(err, ErrNoKeys) || !strings.Contains(err.Error(), "invalid key") {
		t.Fatalf("damaged key: %v", err)
	}
	if err = os.Remove(sign); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadKeys(dir); err == nil || errors.Is(err, ErrNoKeys) {
		t.Fatalf("half a key pair: %v", err)
	}
	if _, err = GenerateKeys(dir); err == nil {
		t.Fatal("half a key pair replaced")
	}
}

// TestFingerprint is stable, covers both public keys and comes in groups the owner can compare.
func TestFingerprint(t *testing.T) {
	a, b := make([]byte, 32), make([]byte, 32)
	got := Fingerprint(a, b)
	if len(got) != 39 || strings.Count(got, "-") != 7 {
		t.Fatal(got)
	}
	if Fingerprint(a, b) != got {
		t.Fatal("not stable")
	}
	b[0] = 1
	if Fingerprint(a, b) == got {
		t.Fatal("box key not covered")
	}
	b[0], a[0] = 0, 1
	if Fingerprint(a, b) == got {
		t.Fatal("sign key not covered")
	}
}

// TestKeysCheckMissing: no state_dir is skipped and missing keys only warn.
func TestKeysCheckMissing(t *testing.T) {
	if c := keysCheck(""); c.Status != Skip {
		t.Fatalf("%+v", c)
	}
	dir := t.TempDir()
	if c := keysCheck(dir); c.Status != Warn || !strings.Contains(c.Hint, "enroll") {
		t.Fatalf("%+v", c)
	}
}
