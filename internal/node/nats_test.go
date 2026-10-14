package node

import (
	"strings"
	"testing"
)

func TestConfigNATS(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, `
nats:
  url: wss://nats.example.com:443/nats
  creds_file: /etc/factory/node.creds
  tls: {ca_file: /etc/factory/ca.pem, server_name: nats.example.com}
`))
	if err != nil {
		t.Fatal(err)
	}
	n := c.NATS
	if n.URL != "wss://nats.example.com:443/nats" || n.CredsFile != "/etc/factory/node.creds" || n.TLS.CAFile != "/etc/factory/ca.pem" || n.TLS.ServerName != "nats.example.com" {
		t.Fatalf("%+v", n)
	}
	if got := n.ResolveURL(func(string) string { return "nats://env:4222" }); got != n.URL {
		t.Fatal(got)
	}
	if _, err = LoadConfig(writeConfig(t, "nats: {tls: {client_key: x}}")); err == nil {
		t.Fatal("unknown nats key accepted")
	}
}

func TestNATSResolveURL(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if got := (NATS{}).ResolveURL(get); got != DefaultNATSURL {
		t.Fatal(got)
	}
	env["NATS_URL"] = "nats://factory:pw@core:4222"
	if got := (NATS{}).ResolveURL(get); got != env["NATS_URL"] {
		t.Fatal(got)
	}
}

func TestConfigNATSValidation(t *testing.T) {
	for body, want := range map[string]string{
		"nats: {url: 'http://h:4222'}":                                 "want nats://",
		"nats: {url: 'nats://factory:secret@h:4222'}":                  "no user info in node.yaml",
		"nats: {url: 'tls://token@h:4222'}":                            "no user info",
		"nats: {creds_file: a, nkey_seed_file: b}":                     "exclusive",
		"nats: {tls: {cert_file: c}}":                                  "go together",
		"nats: {url: 'ws://h:8080', tls: {ca_file: ca}}":               "need wss://",
		"nats: {url: 'tls://a:4222,nats://u:p@b:4222', creds_file: c}": "no user info",
	} {
		_, err := LoadConfig(writeConfig(t, body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", body, err, want)
		}
		if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token@") || strings.Contains(err.Error(), ":p@")) {
			t.Errorf("%s: secret shown: %v", body, err)
		}
	}
	for _, body := range []string{"nats: {url: 'nats://h:4222'}", "nats: {url: 'tls://h:4222', nkey_seed_file: s, tls: {ca_file: ca}}", "nats: {nkey_seed_file: s}"} {
		if _, err := LoadConfig(writeConfig(t, body)); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
}
