package claude

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"

	"appfactory/internal/node"
)

// killedErr is a real "signal: killed" exit, as LocalHost's Run returns for a probe its timeout killed.
func killedErr(t *testing.T) error {
	t.Helper()
	err := exec.Command("sh", "-c", "kill -9 $$").Run()
	if err == nil {
		t.Fatal("sh survived kill -9")
	}
	return err
}

func probeHost(probe func() (string, string, error)) *node.Host {
	return &node.Host{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		Stat:     func(string) (fs.FileInfo, error) { return os.Stat("probe_test.go") },
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			if strings.HasSuffix(strings.Join(args, " "), "--version") {
				return "v22.4.0\n", "", nil
			}
			return probe()
		},
	}
}

// TestProbeClassifiesTransient: the live-run failure (`shim.js --probe` killed by its timeout: "unexpected
// end of JSON input (signal: killed)") and an empty answer are transient; not logged in and an unsupported
// version are definitive.
func TestProbeClassifiesTransient(t *testing.T) {
	killed := killedErr(t)
	ok := func(account string, sdk string) func() (string, string, error) {
		return func() (string, string, error) {
			return `{"event":"probe","claude_code_version":"` + SupportedClaudeCode + `","sdk_version":"` + sdk + `","account":` + account + `}` + "\n", "", nil
		}
	}
	for _, c := range []struct {
		name      string
		probe     func() (string, string, error)
		check     string
		transient bool
	}{
		{"killed", func() (string, string, error) { return "", "", killed }, "claude.version", true},
		{"killed with stderr", func() (string, string, error) { return "", "warming up\n", killed }, "claude.version", true},
		{"empty output", func() (string, string, error) { return "", "", nil }, "claude.version", true},
		{"startup timeout", func() (string, string, error) {
			return `{"event":"error","class":"startup_timeout","message":"no accountInfo"}` + "\n", "", errors.New("exit status 1")
		}, "claude.version", true},
		{"crash with message", func() (string, string, error) { return "", "Error: Cannot find module\n", errors.New("exit status 1") }, "claude.version", false},
		{"not logged in", ok(`{}`, SupportedSDK), "claude.auth", false},
		{"unsupported version", ok(`{"subscriptionType":"max"}`, "0.0.1"), "claude.version", false},
	} {
		d := Driver{Host: probeHost(c.probe), Node: "/opt/node/bin/node", Shim: "/opt/factory/shim.js"}
		var fail *node.CheckResult
		for _, r := range d.Probe(context.Background()) {
			if r.Status == node.Fail {
				fail = &r
				break
			}
		}
		if fail == nil || fail.Check != c.check || fail.Transient != c.transient {
			t.Errorf("%s: %+v", c.name, fail)
		}
	}
}
