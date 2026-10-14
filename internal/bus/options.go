package bus

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// Options are the client settings beyond the server URL (node-agent D1, task 1.5): a per-node user from a
// credentials (JWT) file or an nkey seed file, and TLS with a private CA, a client certificate and a server
// name. Secrets are only ever read from files; only paths are kept and shown, never file contents.
type Options struct {
	// CredsFile is a NATS .creds file (user JWT and nkey seed), read on every (re)connect.
	CredsFile string `yaml:"creds_file"`
	// NkeySeedFile holds an nkey user seed (SU...); the public key is the user name in the server config.
	NkeySeedFile string `yaml:"nkey_seed_file"`
	TLS          TLS    `yaml:"tls"`
}

// TLS of the client connection; any field set makes TLS required (for nats://, tls://, wss://).
type TLS struct {
	CAFile     string `yaml:"ca_file"`     // PEM roots; empty uses the system roots
	CertFile   string `yaml:"cert_file"`   // client certificate (mutual TLS), with KeyFile
	KeyFile    string `yaml:"key_file"`    // client key
	ServerName string `yaml:"server_name"` // expected name in the server certificate; empty is the URL host
}

func (t TLS) Enabled() bool { return t != TLS{} }

// Auth names the configured authentication for logs: creds, nkey, url (user info in the URL) or none.
func (o Options) Auth(rawURL string) string {
	switch {
	case o.CredsFile != "":
		return "creds"
	case o.NkeySeedFile != "":
		return "nkey"
	case strings.Contains(rawURL, "@"):
		return "url"
	}
	return "none"
}

// Schemes nats.go dials: nats and tls over TCP, ws and wss over WebSocket.
var Schemes = []string{"nats", "tls", "ws", "wss"}

// ParseURLs splits a comma-separated server list and checks every entry has a known scheme and a host.
func ParseURLs(raw string) ([]*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("empty NATS URL")
	}
	var r []*url.URL
	for _, s := range strings.Split(raw, ",") {
		u, err := url.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("NATS URL: %w", errors.Unwrap(err)) // the url.Error would echo a password
		}
		ok := false
		for _, s := range Schemes {
			ok = ok || u.Scheme == s
		}
		if !ok || u.Host == "" {
			return nil, fmt.Errorf("NATS URL %s: want nats://, tls://, ws:// or wss:// with a host", RedactURL(u))
		}
		r = append(r, u)
	}
	return r, nil
}

// Redact hides passwords in a (possibly comma-separated) NATS URL for logs and doctor output.
func Redact(raw string) string {
	parts := strings.Split(raw, ",")
	for i, s := range parts {
		if u, err := url.Parse(strings.TrimSpace(s)); err == nil && u.Host != "" {
			parts[i] = RedactURL(u)
		} else {
			parts[i] = "(invalid URL)"
		}
	}
	return strings.Join(parts, ",")
}

// RedactURL is u without secrets: a password, or a bare token in the user part, is replaced by xxxxx.
func RedactURL(u *url.URL) string {
	if u.User == nil {
		return u.String()
	}
	c := *u
	if _, has := u.User.Password(); has {
		return c.Redacted()
	}
	c.User = url.User("xxxxx")
	return c.String()
}

// Validate checks o against the URL it will be used with; an empty URL checks o alone.
func (o Options) Validate(rawURL string) error {
	var errs []error
	if o.CredsFile != "" && o.NkeySeedFile != "" {
		errs = append(errs, errors.New("creds_file and nkey_seed_file are exclusive"))
	}
	if (o.TLS.CertFile == "") != (o.TLS.KeyFile == "") {
		errs = append(errs, errors.New("tls.cert_file and tls.key_file go together"))
	}
	if rawURL != "" {
		us, err := ParseURLs(rawURL)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, u := range us {
			if u.User != nil && (o.CredsFile != "" || o.NkeySeedFile != "") {
				errs = append(errs, fmt.Errorf("NATS URL %s: user info and a credentials file are exclusive", RedactURL(u)))
			}
			if u.Scheme == "ws" && o.TLS.Enabled() {
				errs = append(errs, fmt.Errorf("NATS URL %s: tls settings need wss://", RedactURL(u)))
			}
		}
	}
	return errors.Join(errs...)
}

// NATSOptions builds the nats.go options of o; files are read here (the creds file again on every
// reconnect), and errors name files, never their contents.
func (o Options) NATSOptions() ([]nats.Option, error) {
	if err := o.Validate(""); err != nil {
		return nil, err
	}
	var r []nats.Option
	switch {
	case o.CredsFile != "":
		if err := readable(o.CredsFile); err != nil {
			return nil, fmt.Errorf("creds_file: %w", err)
		}
		r = append(r, nats.UserCredentials(o.CredsFile))
	case o.NkeySeedFile != "":
		opt, err := nats.NkeyOptionFromSeed(o.NkeySeedFile)
		if err != nil {
			return nil, fmt.Errorf("nkey_seed_file %s: %s", o.NkeySeedFile, seedError(err))
		}
		r = append(r, opt)
	}
	if o.TLS.Enabled() {
		cfg, err := o.TLS.Config()
		if err != nil {
			return nil, err
		}
		r = append(r, nats.Secure(cfg))
	}
	return r, nil
}

// Config is the client TLS configuration: TLS 1.2 or later, roots from CAFile, optional client certificate.
func (t TLS) Config() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: t.ServerName}
	if t.CAFile != "" {
		pem, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, fmt.Errorf("tls.ca_file: %w", err)
		}
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls.ca_file %s: no PEM certificate", t.CAFile)
		}
	}
	if t.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("tls.cert_file/key_file %s, %s: unusable key pair", t.CertFile, t.KeyFile)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// LooseFiles lists the secret files (creds, nkey seed, client key) readable by group or others.
func (o Options) LooseFiles(stat func(string) (os.FileInfo, error)) []string {
	var r []string
	for _, p := range []string{o.CredsFile, o.NkeySeedFile, o.TLS.KeyFile} {
		if p == "" {
			continue
		}
		if fi, err := stat(p); err == nil && fi.Mode().Perm()&0o077 != 0 {
			r = append(r, p)
		}
	}
	return r
}

// Dial connects with o and the extra options (name, timeouts, reconnect policy).
func Dial(rawURL string, o Options, extra ...nats.Option) (*nats.Conn, error) {
	if err := o.Validate(rawURL); err != nil {
		return nil, err
	}
	opts, err := o.NATSOptions()
	if err != nil {
		return nil, err
	}
	return nats.Connect(rawURL, append(opts, extra...)...)
}

// ConnectWith is Connect with client options: the node's outbound connection (TLS or WebSocket, per-node user).
func ConnectWith(rawURL string, o Options) (*Bus, error) {
	nc, err := Dial(rawURL, o, nats.Name("appfactory"), nats.Timeout(5*time.Second), nats.MaxReconnects(-1))
	if err != nil {
		return nil, err
	}
	return setup(nc)
}

func readable(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	return f.Close()
}

// seedError keeps the cause of a seed failure without echoing anything read from the file.
func seedError(err error) string {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return err.Error()
	}
	return "no usable nkey user seed"
}
