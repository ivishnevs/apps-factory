package ownermcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"appfactory/internal/factorytools"
)

// fakeCore answers GETs from a table and records every request.
type fakeCore struct {
	get   map[string]string // path → JSON
	calls []string
	puts  []string // bodies of PUT/POST
	fail  error
}

func (f *fakeCore) Do(_ context.Context, method, path string, body, result any) error {
	f.calls = append(f.calls, method+" "+path)
	if method != "GET" {
		b, _ := json.Marshal(body)
		f.puts = append(f.puts, string(b))
		if f.fail != nil {
			return f.fail
		}
		if result != nil {
			return json.Unmarshal([]byte(`{"ok":true,"name":"GITHUB_TOKEN","kind":"string","version":2}`), result)
		}
		return nil
	}
	v, ok := f.get[path]
	if !ok {
		return errors.New("404 " + path)
	}
	return json.Unmarshal([]byte(v), result)
}

func (f *fakeCore) Get(_ context.Context, path string) (io.ReadCloser, error) {
	f.calls = append(f.calls, "GET "+path)
	if strings.HasSuffix(path, "/references/dns-and-proxy.md") {
		return io.NopCloser(strings.NewReader("# DNS and proxy faults\n...\n## Proof\nfactory-node doctor passes net.dns and net.api")), nil
	}
	return nil, errors.New("404 " + path)
}

func (f *fakeCore) mutations() []string {
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") {
			out = append(out, c)
		}
	}
	return out
}

func call(t *testing.T, s *Server, tool string, args map[string]any) (string, error) {
	t.Helper()
	v, err := s.Call(context.Background(), factorytools.Call{Name: tool, Arguments: args})
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(v)
	return string(b), nil
}

// TestSecretNeverShown (setup-assistant 1.2): setting a secret previews without the value, needs the owner's code,
// sends the value to Core once and never answers with it, also when Core refuses it.
func TestSecretNeverShown(t *testing.T) {
	const secret = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	f := &fakeCore{get: map[string]string{"/secrets": `[{"name":"GITHUB_TOKEN","used_by":["dev"]}]`}}
	s := &Server{Core: f, Local: Local{ReadFile: func(p string) ([]byte, error) {
		if p == "/home/o/token" {
			return []byte(secret + "\n"), nil
		}
		return nil, errors.New("no such file")
	}}}
	for _, args := range []map[string]any{{"name": "GITHUB_TOKEN", "file": "/home/o/token"}, {"name": "GITHUB_TOKEN", "value": secret}} {
		f.calls, f.puts = nil, nil
		out, err := call(t, s, "factory_secret_set", args)
		if err != nil || strings.Contains(out, secret) || !strings.Contains(out, "Replaces company secret GITHUB_TOKEN") || !strings.Contains(out, "used by dev") {
			t.Fatalf("preview %s %v", out, err)
		}
		if len(f.mutations()) != 0 {
			t.Fatalf("preview changed something: %v", f.calls)
		}
		var p struct{ Confirm string }
		_ = json.Unmarshal([]byte(out), &p)
		args["confirm"] = p.Confirm
		if out, err = call(t, s, "factory_secret_set", args); err != nil || strings.Contains(out, secret) {
			t.Fatalf("set %s %v", out, err)
		}
		if len(f.puts) != 1 || !strings.Contains(f.puts[0], `"value":"`+secret+`"`) {
			t.Fatalf("Core got %v", f.puts)
		}
	}
	// Core refuses and quotes the value: the error does not carry it.
	f.fail = errors.New("400: bad value " + secret)
	out, _ := call(t, s, "factory_secret_set", map[string]any{"name": "X", "value": secret})
	var p struct{ Confirm string }
	_ = json.Unmarshal([]byte(out), &p)
	if _, err := call(t, s, "factory_secret_set", map[string]any{"name": "X", "value": secret, "confirm": p.Confirm}); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error %v", err)
	}
	if _, err := call(t, s, "factory_secret_set", map[string]any{"name": "X", "file": "/nope"}); err == nil {
		t.Fatal("a missing file previewed")
	}
}

// TestConfirmationExpires: a code is valid for ConfirmTTL only.
func TestConfirmationExpires(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &fakeCore{get: map[string]string{}}
	s := &Server{Core: f, Now: func() time.Time { return now }}
	out, err := call(t, s, "factory_node_enrollment", map[string]any{"name": "mac2"})
	if err != nil || !strings.Contains(out, "one-time enrollment code: mac2") {
		t.Fatalf("%s %v", out, err)
	}
	var p struct{ Confirm string }
	_ = json.Unmarshal([]byte(out), &p)
	now = now.Add(11 * time.Minute)
	if _, err = call(t, s, "factory_node_enrollment", map[string]any{"name": "mac2", "confirm": p.Confirm}); err == nil {
		t.Fatal("expired code confirmed")
	}
	if len(f.mutations()) != 0 {
		t.Fatalf("changed: %v", f.calls)
	}
}

// TestApproveChecksTheFingerprint: approval previews only when the fingerprint the machine shows matches Core's.
func TestApproveChecksTheFingerprint(t *testing.T) {
	f := &fakeCore{get: map[string]string{"/node-keys": `[{"node_id":"mac2","name":"mac2","fingerprint":"AB:CD:EF","status":"pending"}]`}}
	s := &Server{Core: f}
	if _, err := call(t, s, "factory_node_approve", map[string]any{"node": "mac2", "fingerprint": "AB:CD:00"}); err == nil || !strings.Contains(err.Error(), "do not approve") {
		t.Fatalf("mismatch: %v", err)
	}
	out, err := call(t, s, "factory_node_approve", map[string]any{"node": "mac2", "fingerprint": "abcdef"})
	if err != nil || !strings.Contains(out, "Approves node mac2") {
		t.Fatalf("%s %v", out, err)
	}
}

// TestReadTools: the catalog's errors come with fixes, runbooks come from Core's factory-setup skill, and the local
// node tools are read-only and redact credentials.
func TestReadTools(t *testing.T) {
	f := &fakeCore{get: map[string]string{"/catalog": `{"version":7}`}, fail: errors.New(`400: agents/ceo.yaml: yaml: unmarshal errors: line 4: field company not found in type config.Agent`)}
	s := &Server{Core: f, Local: Local{
		NodeDoctor: func(context.Context) (string, bool, error) {
			return "net.api  fail  cannot resolve api.anthropic.com\n", false, nil
		},
		NodeLog: func(_ context.Context, n int) (string, error) {
			return "token ghp_abcdefghijklmnopqrstuvwxyz0123456789 seen", nil
		},
	}}
	out, err := call(t, s, "factory_catalog_validate", map[string]any{"documents": map[string]any{"agents/ceo.yaml": "company: x"}})
	if err != nil || !strings.Contains(out, `"valid":false`) || !strings.Contains(out, "remove or rename the key") {
		t.Fatalf("%s %v", out, err)
	}
	if !strings.Contains(strings.Join(f.puts, ""), `"dry_run":true`) || !strings.Contains(strings.Join(f.puts, ""), `"base":7`) {
		t.Fatalf("not a dry run on the current version: %v", f.puts)
	}
	if out, err = call(t, s, "factory_runbook", map[string]any{"name": "dns-and-proxy"}); err != nil || !strings.Contains(out, "net.dns") {
		t.Fatalf("%s %v", out, err)
	}
	if _, err = call(t, s, "factory_runbook", map[string]any{"name": "nope"}); err == nil {
		t.Fatal("unknown runbook")
	}
	if out, err = call(t, s, "factory_node_doctor", nil); err != nil || !strings.Contains(out, `"passed":false`) || !strings.Contains(out, "dns-and-proxy") {
		t.Fatalf("%s %v", out, err)
	}
	if out, err = call(t, s, "factory_node_log", map[string]any{"lines": 50}); err != nil || strings.Contains(out, "ghp_abcdef") {
		t.Fatalf("log not redacted: %s %v", out, err)
	}
	if _, err = (&Server{Core: f}).Call(context.Background(), factorytools.Call{Name: "factory_node_log"}); err == nil {
		t.Fatal("node log without a local node")
	}
}

// TestServeListsTools: the stdio server speaks MCP and lists the read and change tools.
func TestServeListsTools(t *testing.T) {
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	s := &Server{Core: &fakeCore{}, Local: Local{NodeDoctor: func(context.Context) (string, bool, error) { return "", true, nil }}}
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"factory_status", "factory_why", "factory_doctor", "factory_resume", "factory_secret_set", "factory_node_doctor"} {
		if !strings.Contains(out.String(), `"name":"`+name+`"`) {
			t.Fatalf("%s not listed: %s", name, out.String())
		}
	}
	if strings.Contains(out.String(), "factory_node_log") {
		t.Fatal("node log listed without a local log")
	}
}

func TestCatalogFix(t *testing.T) {
	for msg, want := range map[string]string{
		"yaml: line 3: mapping values are not allowed in this context": "YAML syntax",
		"agent ceo: prompt file prompts/ceo.md not found":              "prompt document",
		"agent dev: unknown profile fast":                              "define the entity",
	} {
		if got := CatalogFix(msg); !strings.Contains(got, want) {
			t.Errorf("%q: %q", msg, got)
		}
	}
}
