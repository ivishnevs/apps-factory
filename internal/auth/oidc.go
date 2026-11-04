// Package auth signs people in through one OIDC issuer (user-accounts D3): Dex next to Core federates Google,
// GitHub and other connectors. Core is the relying party: the authorization code flow with PKCE, the login
// state in a short-lived signed cookie, the ID token verified (signature by JWKS, issuer, audience, expiry,
// nonce). The provider's tokens are never kept.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// LoginCookie holds the state of one sign-in between the redirect to the issuer and the callback.
const LoginCookie = "factory_login"

// loginTTL bounds a sign-in from the button to the callback.
const loginTTL = 10 * time.Minute

// Config configures the relying party.
type Config struct {
	// Issuer is the OIDC issuer the browser is sent to (Dex: http://127.0.0.1:5556/dex).
	Issuer string
	// InternalURL, when set, replaces Issuer in Core's own requests to the issuer (discovery, JWKS, token):
	// Core in Docker reaches Dex as http://dex:5556/dex while browsers use the public address.
	InternalURL  string
	ClientID     string
	ClientSecret string
	// RedirectURL is the callback address the browser returns to (<public URL>/auth/callback).
	RedirectURL string
	// Providers are the connector ids offered as sign-in buttons (Dex connectors: google, github, mock).
	Providers []string
	// CookieKey signs the login cookie.
	CookieKey []byte
	// Secure marks cookies Secure (off only for plain-http development).
	Secure bool
	// HTTPClient is the client for issuer requests; nil uses a default with a 10 s timeout.
	HTTPClient *http.Client
	// Now is the clock (tests); nil means time.Now.
	Now func() time.Time
}

// Client is the relying party of Config.
type Client struct {
	cfg Config

	mu       sync.Mutex
	provider *oidc.Provider
}

// New returns a client; discovery happens on the first sign-in (the issuer may start after Core).
func New(cfg Config) (*Client, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("auth: issuer, client id and redirect URL required")
	}
	if len(cfg.CookieKey) < 32 {
		return nil, errors.New("auth: cookie key of at least 32 bytes required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.InternalURL != "" {
		base := cfg.HTTPClient.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		c := *cfg.HTTPClient
		c.Transport = rewrite{from: strings.TrimRight(cfg.Issuer, "/"), to: strings.TrimRight(cfg.InternalURL, "/"), next: base}
		cfg.HTTPClient = &c
	}
	return &Client{cfg: cfg}, nil
}

// Providers are the configured sign-in buttons.
func (c *Client) Providers() []string { return append([]string{}, c.cfg.Providers...) }

// Secure reports whether cookies are Secure.
func (c *Client) Secure() bool { return c.cfg.Secure }

func (c *Client) now() time.Time {
	if c.cfg.Now != nil {
		return c.cfg.Now()
	}
	return time.Now()
}

// rewrite sends requests for the public issuer address to its internal address.
type rewrite struct {
	from, to string
	next     http.RoundTripper
}

func (t rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	if u := r.URL.String(); strings.HasPrefix(u, t.from) {
		nu, err := url.Parse(t.to + strings.TrimPrefix(u, t.from))
		if err != nil {
			return nil, err
		}
		r = r.Clone(r.Context())
		r.URL, r.Host = nu, nu.Host
	}
	return t.next.RoundTrip(r)
}

func (c *Client) ctx(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, c.cfg.HTTPClient)
}

func (c *Client) discover(ctx context.Context) (*oidc.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return c.provider, nil
	}
	p, err := oidc.NewProvider(c.ctx(ctx), c.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: discovery of %s: %w", c.cfg.Issuer, err)
	}
	c.provider = p
	return p, nil
}

func (c *Client) oauth(p *oidc.Provider) oauth2.Config {
	return oauth2.Config{ClientID: c.cfg.ClientID, ClientSecret: c.cfg.ClientSecret, RedirectURL: c.cfg.RedirectURL,
		Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile", "federated:id"}}
}

// login is the signed login cookie's content.
type login struct {
	State     string `json:"s"`
	Nonce     string `json:"n"`
	Verifier  string `json:"v"`
	Provider  string `json:"p"`
	Return    string `json:"r,omitempty"`
	ExpiresAt int64  `json:"e"`
}

// ErrUnknownProvider is a sign-in button that is not configured.
var ErrUnknownProvider = errors.New("auth: unknown provider")

// Begin starts a sign-in with provider (a connector id): it sets the login cookie and returns the issuer URL to
// redirect to. returnTo is a local path the callback returns to.
func (c *Client) Begin(ctx context.Context, w http.ResponseWriter, provider, returnTo string) (string, error) {
	known := false
	for _, p := range c.cfg.Providers {
		known = known || p == provider
	}
	if !known {
		return "", ErrUnknownProvider
	}
	p, err := c.discover(ctx)
	if err != nil {
		return "", err
	}
	l := login{State: random(), Nonce: random(), Verifier: oauth2.GenerateVerifier(), Provider: provider, Return: LocalPath(returnTo), ExpiresAt: c.now().Add(loginTTL).Unix()}
	value, err := c.seal(l)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{Name: LoginCookie, Value: value, Path: "/auth/", MaxAge: int(loginTTL / time.Second), HttpOnly: true, Secure: c.cfg.Secure, SameSite: http.SameSiteLaxMode})
	cfg := c.oauth(p)
	return cfg.AuthCodeURL(l.State, oidc.Nonce(l.Nonce), oauth2.S256ChallengeOption(l.Verifier), oauth2.SetAuthURLParam("connector_id", provider)), nil
}

// Claims is what Core keeps of a verified ID token.
type Claims struct {
	// Provider is the Dex connector (federated_claims.connector_id), else the issuer.
	Provider string
	// Subject is the person's id at the provider (federated_claims.user_id), else the token's sub.
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	// Return is the local path the sign-in started from.
	Return string
}

// ErrState is a callback that does not belong to this browser's sign-in (forged, replayed or expired).
var ErrState = errors.New("auth: sign-in state mismatch")

// Finish completes the sign-in of r (the callback): the state must match the login cookie, the code is
// exchanged with the PKCE verifier and the ID token verified, nonce included. The login cookie is cleared.
func (c *Client) Finish(ctx context.Context, w http.ResponseWriter, r *http.Request) (Claims, error) {
	var out Claims
	http.SetCookie(w, &http.Cookie{Name: LoginCookie, Value: "", Path: "/auth/", MaxAge: -1, HttpOnly: true, Secure: c.cfg.Secure, SameSite: http.SameSiteLaxMode})
	ck, err := r.Cookie(LoginCookie)
	if err != nil {
		return out, ErrState
	}
	l, err := c.open(ck.Value)
	if err != nil {
		return out, ErrState
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		return out, fmt.Errorf("auth: the provider refused the sign-in: %s", e)
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(l.State)) != 1 || q.Get("code") == "" {
		return out, ErrState
	}
	p, err := c.discover(ctx)
	if err != nil {
		return out, err
	}
	cfg := c.oauth(p)
	tok, err := cfg.Exchange(c.ctx(ctx), q.Get("code"), oauth2.VerifierOption(l.Verifier))
	if err != nil {
		return out, fmt.Errorf("auth: code exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return out, errors.New("auth: no ID token in the answer")
	}
	vcfg := &oidc.Config{ClientID: c.cfg.ClientID}
	if c.cfg.Now != nil {
		vcfg.Now = c.cfg.Now
	}
	idt, err := p.Verifier(vcfg).Verify(c.ctx(ctx), raw)
	if err != nil {
		return out, fmt.Errorf("auth: ID token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(l.Nonce)) != 1 {
		return out, errors.New("auth: ID token nonce mismatch")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Federated     struct {
			ConnectorID string `json:"connector_id"`
			UserID      string `json:"user_id"`
		} `json:"federated_claims"`
	}
	if err = idt.Claims(&claims); err != nil {
		return out, fmt.Errorf("auth: ID token claims: %w", err)
	}
	out = Claims{Provider: idt.Issuer, Subject: idt.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified, Name: claims.Name, Return: l.Return}
	if claims.Federated.ConnectorID != "" && claims.Federated.UserID != "" {
		out.Provider, out.Subject = claims.Federated.ConnectorID, claims.Federated.UserID
	}
	return out, nil
}

func random() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (c *Client) seal(l login) (string, error) {
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + c.mac(payload), nil
}

func (c *Client) mac(payload string) string {
	m := hmac.New(sha256.New, c.cfg.CookieKey)
	m.Write([]byte("factory-login\x00" + payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (c *Client) open(value string) (login, error) {
	var l login
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(c.mac(payload))) {
		return l, ErrState
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(b, &l) != nil || l.State == "" || c.now().Unix() > l.ExpiresAt {
		return l, ErrState
	}
	return l, nil
}

// LocalPath keeps p only when it is a path of this site ("/c/acme/tasks"), so a sign-in cannot be used to
// redirect elsewhere; anything else is "".
func LocalPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || strings.ContainsAny(p, "\r\n") {
		return ""
	}
	return p
}
