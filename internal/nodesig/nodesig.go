// Package nodesig signs and verifies node requests to Core (node-identity D4): a fixed profile of HTTP Message
// Signatures (RFC 9421) with Ed25519. Exactly one form is accepted, so nothing is negotiated.
package nodesig

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Window is how far a request's creation time may be from the verifier's clock.
const Window = 120 * time.Second

const components = `"@method" "@path" "@query" "content-digest"`

// ErrSignature: the request is unsigned or its signature does not hold. Reasons are in the wrapped text.
var ErrSignature = errors.New("invalid node signature")

var signatureInput = regexp.MustCompile(`^sig1=\(` + regexp.QuoteMeta(components) + `\);created=([0-9]{1,19});nonce="([0-9a-f]{32})";keyid="([0-9a-f]{32})";alg="ed25519"$`)

func digest(body []byte) string {
	h := sha256.Sum256(body)
	return "sha-256=:" + base64.StdEncoding.EncodeToString(h[:]) + ":"
}

// base is the RFC 9421 signature base of the fixed profile; params is the value after "sig1=".
func base(r *http.Request, contentDigest, params string) []byte {
	return []byte(`"@method": ` + r.Method + "\n" +
		`"@path": ` + r.URL.EscapedPath() + "\n" +
		`"@query": ?` + r.URL.RawQuery + "\n" +
		`"content-digest": ` + contentDigest + "\n" +
		`"@signature-params": ` + params)
}

// Sign adds the signature headers for body, which must be the request's whole body. Each attempt of a retry signs
// anew, because Core accepts a nonce once.
func Sign(r *http.Request, node string, key ed25519.PrivateKey, body []byte, now time.Time) error {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	params := fmt.Sprintf(`(%s);created=%d;nonce="%s";keyid="%s";alg="ed25519"`, components, now.Unix(), hex.EncodeToString(nonce), node)
	d := digest(body)
	r.Header.Set("Content-Digest", d)
	r.Header.Set("Signature-Input", "sig1="+params)
	r.Header.Set("Signature", "sig1=:"+base64.StdEncoding.EncodeToString(ed25519.Sign(key, base(r, d, params)))+":")
	return nil
}

// Verified is what a valid signature says.
type Verified struct {
	Node    string
	Nonce   string
	Created time.Time
}

// KeyID returns the node a request claims to be, for looking up its key before Verify.
func KeyID(r *http.Request) (string, error) {
	m := signatureInput.FindStringSubmatch(r.Header.Get("Signature-Input"))
	if m == nil {
		return "", fmt.Errorf("%w: missing or unsupported Signature-Input", ErrSignature)
	}
	return m[3], nil
}

// Verify checks the signature of r against key, the digest against body and the creation time against now. It does
// not check replay: the caller remembers Nonce for the window.
func Verify(r *http.Request, key ed25519.PublicKey, body []byte, now time.Time) (Verified, error) {
	var v Verified
	input := r.Header.Get("Signature-Input")
	m := signatureInput.FindStringSubmatch(input)
	if m == nil {
		return v, fmt.Errorf("%w: missing or unsupported Signature-Input", ErrSignature)
	}
	created, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return v, fmt.Errorf("%w: created", ErrSignature)
	}
	sig := r.Header.Get("Signature")
	if !strings.HasPrefix(sig, "sig1=:") || !strings.HasSuffix(sig, ":") || len(sig) < len("sig1=::") {
		return v, fmt.Errorf("%w: missing or unsupported Signature", ErrSignature)
	}
	raw, err := base64.StdEncoding.DecodeString(sig[len("sig1=:") : len(sig)-1])
	if err != nil || len(raw) != ed25519.SignatureSize || len(key) != ed25519.PublicKeySize {
		return v, fmt.Errorf("%w: malformed", ErrSignature)
	}
	d := r.Header.Get("Content-Digest")
	if d != digest(body) {
		return v, fmt.Errorf("%w: body digest", ErrSignature)
	}
	if !ed25519.Verify(key, base(r, d, strings.TrimPrefix(input, "sig1=")), raw) {
		return v, fmt.Errorf("%w: bad signature", ErrSignature)
	}
	at := time.Unix(created, 0)
	if at.Before(now.Add(-Window)) || at.After(now.Add(Window)) {
		return v, fmt.Errorf("%w: created outside the allowed window: check the node's clock", ErrSignature)
	}
	return Verified{Node: m[3], Nonce: m[2], Created: at}, nil
}

// EnrollMessage is what a node signs with its new key to prove it holds it when it enrolls.
func EnrollMessage(code, node, signPub, boxPub string) []byte {
	return []byte("factory-node-enroll\x00" + code + "\x00" + node + "\x00" + signPub + "\x00" + boxPub)
}

// DecodePublicKeys validates the hex public keys of a node.
func DecodePublicKeys(signPub, boxPub string) (sign, box []byte, err error) {
	if sign, err = hex.DecodeString(signPub); err != nil || len(sign) != ed25519.PublicKeySize {
		return nil, nil, errors.New("sign_pub must be a hex Ed25519 public key")
	}
	if box, err = hex.DecodeString(boxPub); err != nil {
		return nil, nil, errors.New("box_pub must be a hex X25519 public key")
	}
	if _, err = ecdh.X25519().NewPublicKey(box); err != nil {
		return nil, nil, errors.New("box_pub must be a hex X25519 public key")
	}
	return sign, box, nil
}

// Fingerprint is the first 16 bytes of SHA-256 over both public keys, in hex groups of four characters.
func Fingerprint(signPub, boxPub []byte) string {
	h := sha256.New()
	h.Write(signPub)
	h.Write(boxPub)
	s := hex.EncodeToString(h.Sum(nil)[:16])
	groups := make([]string, 0, 8)
	for i := 0; i < len(s); i += 4 {
		groups = append(groups, s[i:i+4])
	}
	return strings.Join(groups, "-")
}
