// Package authtest is a fake OIDC issuer for tests: discovery, JWKS and a token endpoint that checks the PKCE
// verifier and answers an RS256 ID token for codes the test approved.
package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// Person is what the issuer says about the signed-in person.
type Person struct {
	Connector     string
	UserID        string
	Email         string
	EmailVerified bool
	Name          string
}

// Issuer is a running fake issuer.
type Issuer struct {
	*httptest.Server
	ClientID string
	key      *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]grant
	// Nonce, when set, replaces the nonce of the next ID token (forgery tests).
	Nonce string
}

type grant struct {
	person    Person
	nonce     string
	challenge string
}

// New starts an issuer for clientID; it stops with the test.
func New(t testing.TB, clientID string) *Issuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	is := &Issuer{ClientID: clientID, key: key, codes: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": is.URL, "authorization_endpoint": is.URL + "/auth", "token_endpoint": is.URL + "/token",
			"jwks_uri": is.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
		})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /token", is.token)
	is.Server = httptest.NewServer(mux)
	t.Cleanup(is.Close)
	return is
}

// Approve plays the person's visit to the authorization URL authURL (from Begin): it returns the callback
// query (code and state) the browser brings back.
func (is *Issuer) Approve(t testing.TB, authURL string, p Person) url.Values {
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" {
		t.Fatalf("authorization request without PKCE or nonce: %s", authURL)
	}
	if p.Connector == "" {
		p.Connector = q.Get("connector_id")
	}
	code := base64.RawURLEncoding.EncodeToString(sha256.New().Sum([]byte(time.Now().String() + p.UserID)))[:24]
	is.mu.Lock()
	is.codes[code] = grant{person: p, nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	is.mu.Unlock()
	return url.Values{"code": {code}, "state": {q.Get("state")}}
}

func (is *Issuer) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	is.mu.Lock()
	g, ok := is.codes[r.PostForm.Get("code")]
	delete(is.codes, r.PostForm.Get("code"))
	nonce := is.Nonce
	is.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	if nonce == "" {
		nonce = g.nonce
	}
	now := time.Now()
	claims := map[string]any{
		"iss": is.URL, "aud": is.ClientID, "sub": "dex-" + g.person.Connector + "-" + g.person.UserID,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": nonce,
		"email": g.person.Email, "email_verified": g.person.EmailVerified, "name": g.person.Name,
		"federated_claims": map[string]string{"connector_id": g.person.Connector, "user_id": g.person.UserID},
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: is.key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	b, _ := json.Marshal(claims)
	jws, err := signer.Sign(b)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	idToken, _ := jws.CompactSerialize()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idToken})
}
