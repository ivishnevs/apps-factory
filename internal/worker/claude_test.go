package worker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/claude/claudetest"
)

// TestFakeClaudeShim is the fake shim process for the Claude driver: it replays a recorded
// stream and refuses a launch that carries the run token or lacks it in the environment.
func TestFakeClaudeShim(t *testing.T) {
	name := os.Getenv("FACTORY_FAKE_CLAUDE_SHIM")
	if name == "" {
		return
	}
	err := claudetest.Serve(claudetest.Dir(), name, os.Stdin, os.Stdout, func(line []byte) {
		token := os.Getenv("FACTORY_RUN_TOKEN")
		if token == "" || os.Getenv("FACTORY_URL") == "" || strings.Contains(string(line), token) || !bytes.Contains(line, []byte(`"mcp_command"`)) {
			fmt.Fprintln(os.Stderr, "run token must reach the shim only through the environment")
			os.Exit(1)
		}
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runClaude(t *testing.T, scenario string, cancel bool) (string, string, string, int, int) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	t.Setenv("FACTORY_FAKE_CLAUDE_SHIM", scenario)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(claudetest.Dir(), scenario+".events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	d := claude.Driver{Node: bin, Args: []string{"-test.run=^TestFakeClaudeShim$", "--"}}
	// MCPBinary only has to exist: the fake shim never starts it.
	r, events := runDriver(t, url, Worker{Driver: d, MCPBinary: bin}, cancel)
	return r.Status, r.Result, r.SessionID, len(events), strings.Count(string(b), "\n")
}

func TestJetStreamClaudeDriver(t *testing.T) {
	status, result, session, events, lines := runClaude(t, "start", false)
	if status != "COMPLETED" || result != "FOUNDATION_OK" || session != "6f1c7a52-3b0e-4d8f-9a61-2c4e8b7d0f13" {
		t.Fatal(status, result, session)
	}
	// session and result lines become started and completed; every other line is progress.
	if events != lines {
		t.Fatalf("%d events for %d shim lines", events, lines)
	}
}

func TestJetStreamClaudeDriverCancellation(t *testing.T) {
	if status, _, _, _, _ := runClaude(t, "interrupt", true); status != "CANCELLED" {
		t.Fatal(status)
	}
}
