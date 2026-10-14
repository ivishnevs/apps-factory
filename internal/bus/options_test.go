package bus

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// pemPair is a self-signed certificate and its key in PEM files.
func pemPair(t *testing.T) (cert, key string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return write(t, "cert.pem", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))),
		write(t, "key.pem", string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})))
}

func userSeed(t *testing.T) (seedFile, pub string) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	pub, _ = kp.PublicKey()
	return write(t, "user.seed", string(seed)+"\n"), pub
}

// apply runs the built options on nats.go's defaults, as nats.Connect does.
func apply(t *testing.T, o Options) nats.Options {
	t.Helper()
	opts, err := o.NATSOptions()
	if err != nil {
		t.Fatal(err)
	}
	no := nats.GetDefaultOptions()
	for _, f := range opts {
		if err := f(&no); err != nil {
			t.Fatal(err)
		}
	}
	return no
}

func TestValidate(t *testing.T) {
	for _, c := range []struct {
		name, url string
		o         Options
		want      string // "" = valid
	}{
		{"plain", "nats://h:4222", Options{}, ""},
		{"list", "tls://a:4222, tls://b:4222", Options{TLS: TLS{CAFile: "ca"}}, ""},
		{"websocket", "ws://h:8080", Options{NkeySeedFile: "s"}, ""},
		{"secure websocket", "wss://h:443/nats", Options{CredsFile: "c", TLS: TLS{CAFile: "ca", ServerName: "n"}}, ""},
		{"options alone", "", Options{CredsFile: "c"}, ""},
		{"http", "http://h:4222", Options{}, "want nats://"},
		{"no host", "nats://", Options{}, "with a host"},
		{"bare host", "h:4222", Options{}, "want nats://"},
		{"both users", "", Options{CredsFile: "c", NkeySeedFile: "s"}, "exclusive"},
		{"cert without key", "", Options{TLS: TLS{CertFile: "c"}}, "go together"},
		{"user info and creds", "nats://u:p@h:4222", Options{CredsFile: "c"}, "exclusive"},
		{"tls over ws", "ws://h:8080", Options{TLS: TLS{CAFile: "ca"}}, "need wss://"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.o.Validate(c.url)
			if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("Validate(%q) = %v, want %q", c.url, err, c.want)
			}
			if err != nil && strings.Contains(err.Error(), ":p@") {
				t.Fatalf("password shown: %v", err)
			}
		})
	}
}

func TestRedact(t *testing.T) {
	got := Redact("nats://u:secret@a:4222,tls://token@b:4222,wss://c/nats")
	if strings.Contains(got, "secret") || strings.Contains(got, "token") || !strings.Contains(got, "wss://c/nats") {
		t.Fatal(got)
	}
	if got := Redact("nats://u:%zz@h"); strings.Contains(got, "zz") {
		t.Fatal(got)
	}
}

func TestNATSOptionsNkey(t *testing.T) {
	seed, pub := userSeed(t)
	no := apply(t, Options{NkeySeedFile: seed})
	if no.Nkey != pub || no.SignatureCB == nil || no.Secure || no.TLSConfig != nil {
		t.Fatalf("%+v", no)
	}
	if (Options{NkeySeedFile: seed}).Auth("nats://h") != "nkey" || (Options{}).Auth("nats://u:p@h") != "url" || (Options{}).Auth("ws://h") != "none" {
		t.Fatal("auth names")
	}
}

func TestNATSOptionsCreds(t *testing.T) {
	creds := write(t, "node.creds", "-----BEGIN NATS USER JWT-----\nnot-checked-until-connect\n------END NATS USER JWT------\n")
	no := apply(t, Options{CredsFile: creds})
	if no.UserJWT == nil || no.SignatureCB == nil || (Options{CredsFile: creds}).Auth("") != "creds" {
		t.Fatalf("%+v", no)
	}
	if _, err := (Options{CredsFile: filepath.Join(t.TempDir(), "missing.creds")}).NATSOptions(); err == nil || !strings.Contains(err.Error(), "creds_file") {
		t.Fatal(err)
	}
}

// TestNATSOptionsSecretsNotEchoed: a broken seed file fails without its contents in the error.
func TestNATSOptionsSecretsNotEchoed(t *testing.T) {
	bad := write(t, "bad.seed", "SUPERSECRETVALUE\n")
	_, err := (Options{NkeySeedFile: bad}).NATSOptions()
	if err == nil || strings.Contains(err.Error(), "SUPERSECRET") || !strings.Contains(err.Error(), bad) {
		t.Fatal(err)
	}
	_, err = (Options{NkeySeedFile: filepath.Join(t.TempDir(), "none")}).NATSOptions()
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatal(err)
	}
	cert, _ := pemPair(t)
	badKey := write(t, "bad.key", "-----BEGIN EC PRIVATE KEY-----\nSUPERSECRETVALUE\n-----END EC PRIVATE KEY-----\n")
	_, err = (Options{TLS: TLS{CertFile: cert, KeyFile: badKey}}).NATSOptions()
	if err == nil || strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatal(err)
	}
}

func TestNATSOptionsTLS(t *testing.T) {
	cert, key := pemPair(t)
	no := apply(t, Options{TLS: TLS{CAFile: cert, CertFile: cert, KeyFile: key, ServerName: "nats.example"}})
	c := no.TLSConfig
	if !no.Secure || c == nil || c.ServerName != "nats.example" || c.RootCAs == nil || len(c.Certificates) != 1 || c.MinVersion < 0x0303 {
		t.Fatalf("%+v", no)
	}
	if no := apply(t, Options{TLS: TLS{ServerName: "n"}}); no.TLSConfig.RootCAs != nil { // system roots
		t.Fatal("roots")
	}
	if _, err := (Options{TLS: TLS{CAFile: write(t, "ca.pem", "not pem")}}).NATSOptions(); err == nil || !strings.Contains(err.Error(), "no PEM") {
		t.Fatal(err)
	}
}

func TestLooseFiles(t *testing.T) {
	seed, _ := userSeed(t)
	creds := write(t, "c", "x")
	if err := os.Chmod(creds, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (Options{NkeySeedFile: seed}).LooseFiles(os.Stat); len(got) != 0 {
		t.Fatal(got)
	}
	if got := (Options{CredsFile: creds}).LooseFiles(os.Stat); len(got) != 1 || got[0] != creds {
		t.Fatal(got)
	}
}

// secureEnv is the server set up by scripts/nats-secure-test.sh; the test is skipped without it.
func secureEnv(t *testing.T, keys ...string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, k := range keys {
		if m[k] = os.Getenv(k); m[k] == "" {
			t.Skip("run scripts/nats-secure-test.sh up and export its NATS_TEST_* variables")
		}
	}
	return m
}

// TestSecureConnect: a node dials out over TLS and over secure WebSocket with its nkey user and works with
// JetStream under the node permission set (stream info, its pull consumer, acks); it cannot publish commands.
// A wrong CA, a missing server name and an unknown user are refused.
func TestSecureConnect(t *testing.T) {
	env := secureEnv(t, "NATS_TEST_SECURE_TLS_URL", "NATS_TEST_SECURE_WSS_URL", "NATS_TEST_SECURE_CA", "NATS_TEST_SECURE_SERVER_NAME",
		"NATS_TEST_ADMIN_SEED", "NATS_TEST_NODE_SEED")
	tlsOpts := TLS{CAFile: env["NATS_TEST_SECURE_CA"], ServerName: env["NATS_TEST_SECURE_SERVER_NAME"]}
	admin, err := ConnectWith(env["NATS_TEST_SECURE_TLS_URL"], Options{NkeySeedFile: env["NATS_TEST_ADMIN_SEED"], TLS: tlsOpts})
	if err != nil {
		t.Fatal(err) // creates the streams as Core does
	}
	defer admin.NC.Close()

	for _, u := range []string{env["NATS_TEST_SECURE_TLS_URL"], env["NATS_TEST_SECURE_WSS_URL"]} {
		t.Run(u[:strings.Index(u, ":")], func(t *testing.T) {
			b, err := ConnectWith(u, Options{NkeySeedFile: env["NATS_TEST_NODE_SEED"], TLS: tlsOpts})
			if err != nil {
				t.Fatal(err)
			}
			defer b.NC.Close()
			durable := "secure-" + u[:strings.Index(u, ":")] + time.Now().Format("150405")
			subject := "factory.commands.agent.run." + durable
			sub, err := b.Subscribe(subject, durable)
			if err != nil {
				t.Fatal(err)
			}
			id := durable + "-" + time.Now().Format("150405.000000000")
			if err = admin.Publish(t.Context(), subject, id, []byte(id)); err != nil {
				t.Fatal(err)
			}
			msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if string(msgs[0].Data) != id {
				t.Fatalf("got %q", msgs[0].Data)
			}
			if err = msgs[0].Ack(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second) // the violation is asynchronous: no ack comes
			defer cancel()
			if err = b.Publish(ctx, subject, id+"-forged", []byte("x")); err == nil {
				t.Fatal("node published a command")
			}
		})
	}

	other, _ := userSeed(t)
	for name, c := range map[string]struct {
		url string
		o   Options
	}{
		"unknown user":    {env["NATS_TEST_SECURE_WSS_URL"], Options{NkeySeedFile: other, TLS: tlsOpts}},
		"no user":         {env["NATS_TEST_SECURE_TLS_URL"], Options{TLS: tlsOpts}},
		"system roots":    {env["NATS_TEST_SECURE_TLS_URL"], Options{NkeySeedFile: env["NATS_TEST_NODE_SEED"], TLS: TLS{ServerName: tlsOpts.ServerName}}},
		"wrong name":      {env["NATS_TEST_SECURE_WSS_URL"], Options{NkeySeedFile: env["NATS_TEST_NODE_SEED"], TLS: TLS{CAFile: tlsOpts.CAFile, ServerName: "other.test"}}},
		"no tls over wss": {env["NATS_TEST_SECURE_WSS_URL"], Options{NkeySeedFile: env["NATS_TEST_NODE_SEED"]}},
	} {
		t.Run(name, func(t *testing.T) {
			nc, err := Dial(c.url, c.o, nats.Timeout(3*time.Second), nats.NoReconnect())
			if err == nil {
				nc.Close()
				t.Fatal("connected")
			}
		})
	}
}

// TestCredsConnect: a JWT user from a .creds file (operator mode) connects and uses JetStream.
func TestCredsConnect(t *testing.T) {
	env := secureEnv(t, "NATS_TEST_CREDS_URL", "NATS_TEST_CREDS_FILE")
	b, err := ConnectWith(env["NATS_TEST_CREDS_URL"], Options{CredsFile: env["NATS_TEST_CREDS_FILE"]})
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	if _, err = b.JS.AccountInfo(); err != nil {
		t.Fatal(err)
	}
	seed, _ := userSeed(t)
	if nc, err := Dial(env["NATS_TEST_CREDS_URL"], Options{NkeySeedFile: seed}, nats.NoReconnect()); err == nil {
		nc.Close()
		t.Fatal("unknown user connected")
	}
}
