package worker

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/redact"
	"appfactory/internal/secrets"
	"appfactory/internal/storage"
)

// fakeSecret is a made-up key; no real credential is ever used in tests.
const fakeSecret = "fake-astr-key-0123456789"

const mcpSnapshot = `{"mcp_servers":{"appstoretracker":{"transport":"http","url":"https://mcp.example.test/api/mcp","headers":{"Authorization":"Bearer ${secret:ASTR_API_KEY}"},"deny_tools":["add_to_watchlist"]}}}`

// TestExternalMCPResolve (external-mcp-servers, core-secret-store D5): ${secret:NAME} resolves from the secrets
// Core delivered with the claim into a runtime environment variable the header references; a secret that was not
// delivered or a driver without ExternalMCP fails with a reason naming the secret, never a value.
func TestExternalMCPResolve(t *testing.T) {
	spec := protocol.RunSpec{Provider: protocol.RuntimeClaude, ConfigSnapshot: []byte(mcpSnapshot)}
	caps := driver.Capabilities{ExternalMCP: true}
	ok := map[string]string{"ASTR_API_KEY": fakeSecret, "OTHER": "x"}
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
	if r, err := externalMCP(protocol.RunSpec{ConfigSnapshot: []byte(`{"rules":[]}`)}, nil, caps); err != nil || len(r.Servers) != 0 {
		t.Fatal("an agent without external MCP needs nothing:", r, err)
	}
	for name, tc := range map[string]struct {
		secrets map[string]string
		caps    driver.Capabilities
		want    string
	}{
		"not delivered": {map[string]string{"OTHER": fakeSecret}, caps, "needs company secret ASTR_API_KEY, which Core did not deliver"},
		"none":          {nil, caps, "needs company secret ASTR_API_KEY"},
		"codex runtime": {ok, driver.Capabilities{}, "not supported by the claude runtime"},
		"empty value":   {map[string]string{"ASTR_API_KEY": ""}, caps, "did not deliver"},
	} {
		_, err := externalMCP(spec, tc.secrets, tc.caps)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), fakeSecret) {
			t.Errorf("%s: want %q, got %v", name, tc.want, err)
		}
	}
}

// TestExternalMCPRun: the driver gets the server with the secret as an environment reference and the
// value in its environment only, delivered sealed with the claim from Core's secret store; a command that prints
// the secret has it redacted from the run's log, events and result.
func TestExternalMCPRun(t *testing.T) {
	withMCP := stored(func(s *protocol.RunSpec) { s.ConfigSnapshot = []byte(mcpSnapshot) })
	var s *storage.Store
	d := &fake.Driver{ExternalMCP: true, Script: fake.Script{Status: "COMPLETED", Text: "done", Activity: []fake.Activity{
		{Exec: []string{"sh", "-c", "echo key is $FACTORY_MCP_SECRET_ASTR_API_KEY"}},
	}}}
	w := Worker{Driver: d}
	r, events := runDriver(t, w, false, withMCP, withStore(func(store *storage.Store) {
		s = store
		store.Keys, _ = secrets.NewMasterKeys(bytes.Repeat([]byte{3}, secrets.KeySize), nil)
		if _, err := store.SetSecret(context.Background(), storage.DefaultCompany, "ASTR_API_KEY", storage.SecretString, []byte(fakeSecret), "owner"); err != nil {
			t.Fatal(err)
		}
	}))
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
}
