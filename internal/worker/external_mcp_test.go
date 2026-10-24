package worker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/redact"
	"appfactory/internal/storage"
)

// fakeSecret is a made-up key; no real credential is ever used in tests.
const fakeSecret = "fake-astr-key-0123456789"

const mcpSnapshot = `{"mcp_servers":{"appstoretracker":{"transport":"http","url":"https://mcp.example.test/api/mcp","headers":{"Authorization":"Bearer ${secret:ASTR_API_KEY}"},"deny_tools":["add_to_watchlist"]}}}`

func secretsFile(t *testing.T, text string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(p, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestExternalMCPResolve (external-mcp-servers): the node resolves ${secret:NAME} from its secrets file into a
// runtime environment variable the header references; a missing secret, a missing secrets file, an unsafe
// file mode or a driver without ExternalMCP fail with a reason naming the secret, never a value.
func TestExternalMCPResolve(t *testing.T) {
	spec := protocol.RunSpec{Provider: protocol.RuntimeClaude, ConfigSnapshot: []byte(mcpSnapshot)}
	caps := driver.Capabilities{ExternalMCP: true}
	ok := secretsFile(t, "# node secrets\nexport ASTR_API_KEY=\""+fakeSecret+"\"\nOTHER=x\n", 0o600)
	r, err := externalMCP(spec, ok, caps)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Servers) != 1 || r.Servers[0].Name != "appstoretracker" || r.Servers[0].Headers["Authorization"] != "Bearer ${FACTORY_MCP_SECRET_ASTR_API_KEY}" || r.Servers[0].DenyTools[0] != "add_to_watchlist" {
		t.Fatalf("%+v", r.Servers)
	}
	if !slices.Equal(r.Env, []string{"FACTORY_MCP_SECRET_ASTR_API_KEY=" + fakeSecret}) || !slices.Equal(r.Values, []string{fakeSecret}) {
		t.Fatalf("env %v values %v", r.Env, r.Values)
	}
	if r, err := externalMCP(protocol.RunSpec{ConfigSnapshot: []byte(`{"rules":[]}`)}, "", caps); err != nil || len(r.Servers) != 0 {
		t.Fatal("an agent without external MCP needs nothing:", r, err)
	}
	for name, tc := range map[string]struct {
		file string
		caps driver.Capabilities
		want string
	}{
		"missing secret":   {secretsFile(t, "OTHER="+fakeSecret+"\n", 0o600), caps, "needs node secret ASTR_API_KEY, which the node's secrets_file does not define"},
		"no secrets file":  {"", caps, "needs node secret ASTR_API_KEY, but this node has no secrets_file"},
		"group readable":   {secretsFile(t, "ASTR_API_KEY="+fakeSecret+"\n", 0o640), caps, "chmod 600"},
		"bad line":         {secretsFile(t, "just-a-value\n", 0o600), caps, "line 1: expected KEY=VALUE"},
		"codex runtime":    {ok, driver.Capabilities{}, "not supported by the claude runtime"},
		"absent file":      {filepath.Join(t.TempDir(), "none"), caps, "secrets_file"},
		"empty secret val": {secretsFile(t, "ASTR_API_KEY=\n", 0o600), caps, "does not define"},
	} {
		_, err := externalMCP(spec, tc.file, tc.caps)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), fakeSecret) {
			t.Errorf("%s: want %q, got %v", name, tc.want, err)
		}
	}
}

// TestJetStreamExternalMCPRun: the driver gets the server with the secret as an environment reference and the
// value in its environment only; a command that prints the secret has it redacted from the run's log, events
// and result. A node without the secret fails the run's setup naming the secret.
func TestJetStreamExternalMCPRun(t *testing.T) {
	nats := os.Getenv("NATS_TEST_URL")
	if nats == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	withMCP := stored(func(s *protocol.RunSpec) { s.ConfigSnapshot = []byte(mcpSnapshot) })
	var s *storage.Store
	d := &fake.Driver{ExternalMCP: true, Script: fake.Script{Status: "COMPLETED", Text: "done", Activity: []fake.Activity{
		{Exec: []string{"sh", "-c", "echo key is $FACTORY_MCP_SECRET_ASTR_API_KEY"}},
	}}}
	w := Worker{Driver: d, SecretsFile: secretsFile(t, "ASTR_API_KEY="+fakeSecret+"\n", 0o600)}
	r, events := runDriver(t, nats, w, false, withMCP, withStore(func(store *storage.Store) { s = store }))
	if r.Outcome != "succeeded" {
		t.Fatalf("%s %q", r.Outcome, r.Result)
	}
	l := d.Launches()[0]
	if len(l.ExternalMCP) != 1 || strings.Contains(l.ExternalMCP[0].Headers["Authorization"], fakeSecret) || !slices.Contains(l.Env, "FACTORY_MCP_SECRET_ASTR_API_KEY="+fakeSecret) {
		t.Fatalf("launch %+v", l.ExternalMCP)
	}
	for _, e := range events {
		if strings.Contains(string(e.Payload), fakeSecret) {
			t.Fatal("secret in a published event:", string(e.Payload))
		}
	}
	ctx := context.Background()
	chunks, err := s.RunLogs(ctx, storage.DefaultCompany, "fake-run")
	if err != nil || len(chunks) == 0 {
		t.Fatal(chunks, err)
	}
	var log strings.Builder
	for _, c := range chunks {
		rc, _, err := s.RunLogChunk(ctx, storage.DefaultCompany, "fake-run", c.Seq)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		log.Write(b)
	}
	if strings.Contains(log.String(), fakeSecret) || !strings.Contains(log.String(), "key is "+redact.Marker) {
		t.Fatal(log.String())
	}

	d2 := &fake.Driver{ExternalMCP: true, Script: fake.Script{Status: "COMPLETED"}}
	r, _ = runDriver(t, nats, Worker{Driver: d2, SecretsFile: secretsFile(t, "OTHER=x\n", 0o600)}, false, withMCP)
	if r.Outcome != "setup_failed" || !strings.Contains(r.Result, "needs node secret ASTR_API_KEY") || len(d2.Launches()) != 0 {
		t.Fatalf("%s %q launches %d", r.Outcome, r.Result, len(d2.Launches()))
	}
}
