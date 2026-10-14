package claude

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"appfactory/internal/node"
)

// TestProbeChecksWhatLaunchRuns: Installed and Probe look at the Node binary and the shim the driver
// launches (not a claude CLI), and the version and login
// come from `shim.js --probe` run with the pinned Executable.
func TestProbeChecksWhatLaunchRuns(t *testing.T) {
	files := map[string]bool{"/opt/node/bin/node": true, "/opt/factory/shim.js": true}
	var ran []string
	h := node.Host{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		Stat: func(p string) (fs.FileInfo, error) {
			if files[p] {
				return os.Stat("probe_test.go") // any regular file
			}
			return nil, fs.ErrNotExist
		},
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			line := strings.Join(append([]string{name}, args...), " ")
			ran = append(ran, line)
			switch line {
			case "/opt/node/bin/node --version":
				return "v22.4.0\n", "", nil
			case "env CLAUDE_CODE_EXECUTABLE=/x/claude /opt/node/bin/node /opt/factory/shim.js --probe":
				return `{"event":"probe","claude_code_version":"` + SupportedClaudeCode + `","sdk_version":"` + SupportedSDK + `","account":{"subscriptionType":"max","email":"secret@example.com","apiKeySource":"none"}}` + "\n", "", nil
			}
			return "", "", errors.New("unexpected command " + line)
		},
	}
	d := Driver{Host: &h, Node: "/opt/node/bin/node", Shim: "/opt/factory/shim.js", Executable: "/x/claude"}
	if !d.Installed() {
		t.Fatal("node and shim present, driver not installed")
	}
	got := map[string]node.CheckResult{}
	for _, r := range d.Probe(context.Background()) {
		got[r.Check] = r
		if r.Status != node.Pass {
			t.Errorf("%s = %+v", r.Check, r)
		}
	}
	for _, c := range []string{"claude.node", "claude.shim", "claude.version", "claude.auth"} {
		if _, ok := got[c]; !ok {
			t.Errorf("missing %s in %v", c, got)
		}
	}
	if strings.Contains(got["claude.auth"].Detail, "secret") {
		t.Fatal("account email reported", got["claude.auth"])
	}
	if len(ran) != 2 {
		t.Fatalf("ran %q", ran)
	}
	delete(files, "/opt/factory/shim.js")
	if d.Installed() {
		t.Fatal("installed without the shim")
	}
	rs := d.Probe(context.Background())
	if last := rs[len(rs)-1]; last.Check != "claude.shim" || last.Status != node.Fail {
		t.Fatalf("%+v", rs)
	}
}
