package secrets

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func TestValuesBoundToTheirRow(t *testing.T) {
	key := NewCompanyKey()
	sealed, err := Encrypt(key, "acme", "API_TOKEN", 1, []byte("s3cret-value"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("s3cret-value")) {
		t.Fatal("plaintext in the ciphertext")
	}
	if v, err := Decrypt(key, "acme", "API_TOKEN", 1, sealed); err != nil || string(v) != "s3cret-value" {
		t.Fatal(string(v), err)
	}
	// Moved to another company, name or version, or opened with another key: nothing.
	for _, c := range []struct {
		key           []byte
		company, name string
		version       int64
	}{{key, "globex", "API_TOKEN", 1}, {key, "acme", "OTHER", 1}, {key, "acme", "API_TOKEN", 2}, {NewCompanyKey(), "acme", "API_TOKEN", 1}} {
		if _, err := Decrypt(c.key, c.company, c.name, c.version, sealed); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("%s/%s/%d opened: %v", c.company, c.name, c.version, err)
		}
	}
}

func TestMasterKeys(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if m, err := FromEnv(env(nil)); m != nil || err != nil {
		t.Fatal("no key", m, err)
	}
	if _, err := FromEnv(env(map[string]string{MasterKeyEnv: "short"})); err == nil {
		t.Fatal("a short key accepted")
	}
	old, next := NewCompanyKey(), NewCompanyKey()
	b64 := base64.StdEncoding.EncodeToString
	m, err := FromEnv(env(map[string]string{MasterKeyEnv: b64(old)}))
	if err != nil || m.Rotating() {
		t.Fatal(m, err)
	}
	company := NewCompanyKey()
	wrapped, err := m.Wrap(company)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := m.Unwrap(wrapped); err != nil || !bytes.Equal(k, company) {
		t.Fatal("unwrap", err)
	}
	if _, err = m.RewrapUnder(wrapped); err == nil {
		t.Fatal("rewrap without a new key")
	}
	// Another master key opens nothing.
	other, _ := NewMasterKeys(NewCompanyKey(), nil)
	if _, err = other.Unwrap(wrapped); !errors.Is(err, ErrDecrypt) {
		t.Fatal("another master key opened a company key", err)
	}
	// Rotation: re-wrapped under the new key, readable with both during the rotation, with the new alone after.
	rot, err := FromEnv(env(map[string]string{MasterKeyEnv: b64(old), NewMasterKeyEnv: b64(next)}))
	if err != nil || !rot.Rotating() {
		t.Fatal(rot, err)
	}
	rewrapped, err := rot.RewrapUnder(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := NewMasterKeys(next, nil)
	for _, w := range [][]byte{wrapped, rewrapped} {
		if k, err := rot.Unwrap(w); err != nil || !bytes.Equal(k, company) {
			t.Fatal("during rotation", err)
		}
	}
	if k, err := after.Unwrap(rewrapped); err != nil || !bytes.Equal(k, company) {
		t.Fatal("after rotation", err)
	}
	if _, err = after.Unwrap(wrapped); err == nil {
		t.Fatal("the old wrapping opens under the new key alone")
	}
	var none *MasterKeys
	if _, err = none.Wrap(company); !errors.Is(err, ErrNoMasterKey) {
		t.Fatal(err)
	}
}

// TestSealToNode (D4): a sealed value opens only with that node's key and that delivery's info.
func TestSealToNode(t *testing.T) {
	node, _ := ecdh.X25519().GenerateKey(rand.Reader)
	other, _ := ecdh.X25519().GenerateKey(rand.Reader)
	info := DeliveryInfo("acme", "run-1", "node-1", "API_TOKEN", 3)
	sealed, err := SealToNode(node.PublicKey().Bytes(), info, []byte("s3cret-value"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("s3cret")) {
		t.Fatal("plaintext in the sealed value")
	}
	if v, err := OpenOnNode(node, info, sealed); err != nil || string(v) != "s3cret-value" {
		t.Fatal(string(v), err)
	}
	for _, c := range []struct {
		key  *ecdh.PrivateKey
		info []byte
	}{{other, info}, {node, DeliveryInfo("acme", "run-2", "node-1", "API_TOKEN", 3)}, {node, DeliveryInfo("acme", "run-1", "node-2", "API_TOKEN", 3)},
		{node, DeliveryInfo("globex", "run-1", "node-1", "API_TOKEN", 3)}, {node, DeliveryInfo("acme", "run-1", "node-1", "API_TOKEN", 4)}} {
		if _, err := OpenOnNode(c.key, c.info, sealed); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("opened with %s", c.info)
		}
	}
}
