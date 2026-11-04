package nodesig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testNode = "0123456789abcdef0123456789abcdef"

// TestSignVerify (node-identity 2.1): a signed request verifies; any change to what is covered, a wrong key or a
// creation time outside the window does not.
func TestSignVerify(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"node_id":"n"}`)
	r := httptest.NewRequest("POST", "https://core.example/runs/r1/claim?a=1", nil)
	if err = Sign(r, testNode, key, body, now); err != nil {
		t.Fatal(err)
	}
	v, err := Verify(r, pub, body, now)
	if err != nil || v.Node != testNode || len(v.Nonce) != 32 || !v.Created.Equal(now) {
		t.Fatal(v, err)
	}
	if id, err := KeyID(r); err != nil || id != testNode {
		t.Fatal(id, err)
	}
	refused := func(name string, r2Target, method string, b []byte, k ed25519.PublicKey, at time.Time) {
		t.Helper()
		r2 := httptest.NewRequest(method, r2Target, nil)
		r2.Header = r.Header.Clone()
		if _, err := Verify(r2, k, b, at); !errors.Is(err, ErrSignature) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	const target = "https://core.example/runs/r1/claim?a=1"
	refused("other path", "https://core.example/runs/r2/claim?a=1", "POST", body, pub, now)
	refused("other query", "https://core.example/runs/r1/claim?a=2", "POST", body, pub, now)
	refused("other method", target, "PUT", body, pub, now)
	refused("other body", target, "POST", []byte(`{"node_id":"m"}`), pub, now)
	refused("other key", target, "POST", body, other, now)
	refused("too old", target, "POST", body, pub, now.Add(Window+time.Second))
	refused("too new", target, "POST", body, pub, now.Add(-Window-time.Second))
	if _, err := Verify(r, pub, body, now.Add(Window)); err != nil {
		t.Fatal("window edge refused", err)
	}

	// The digest header is covered: replacing body and digest together does not help.
	r3 := httptest.NewRequest("POST", target, nil)
	r3.Header = r.Header.Clone()
	r3.Header.Set("Content-Digest", digest([]byte("evil")))
	if _, err := Verify(r3, pub, []byte("evil"), now); !errors.Is(err, ErrSignature) {
		t.Fatal("swapped body accepted", err)
	}

	// Unsigned and other forms are refused.
	bare := httptest.NewRequest("POST", target, nil)
	if _, err := Verify(bare, pub, body, now); !errors.Is(err, ErrSignature) {
		t.Fatal("unsigned accepted", err)
	}
	if _, err := KeyID(bare); !errors.Is(err, ErrSignature) {
		t.Fatal(err)
	}
	r4 := httptest.NewRequest("POST", target, nil)
	r4.Header = r.Header.Clone()
	r4.Header.Set("Signature-Input", strings.Replace(r.Header.Get("Signature-Input"), `"@query" `, "", 1))
	if _, err := Verify(r4, pub, body, now); !errors.Is(err, ErrSignature) {
		t.Fatal("fewer components accepted", err)
	}

	// Each signature has its own nonce.
	r5 := httptest.NewRequest("POST", target, nil)
	if err = Sign(r5, testNode, key, body, now); err != nil {
		t.Fatal(err)
	}
	if r5.Header.Get("Signature-Input") == r.Header.Get("Signature-Input") {
		t.Fatal("nonce repeated")
	}
}

func TestDecodePublicKeys(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	box := make([]byte, 32)
	box[0] = 9
	sign := hex.EncodeToString(pub)
	if _, _, err := DecodePublicKeys(sign, hex.EncodeToString(box)); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"zz", hex.EncodeToString(box)}, {sign[:10], hex.EncodeToString(box)}, {sign, "zz"}, {sign, "00"}} {
		if _, _, err := DecodePublicKeys(c[0], c[1]); err == nil {
			t.Fatalf("accepted %v", c)
		}
	}
}

// TestFingerprint is stable, covers both public keys and comes in groups the owner can compare.
func TestFingerprint(t *testing.T) {
	a, b := make([]byte, 32), make([]byte, 32)
	got := Fingerprint(a, b)
	if len(got) != 39 || strings.Count(got, "-") != 7 || Fingerprint(a, b) != got {
		t.Fatal(got)
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
