package node_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/codex"
	. "appfactory/internal/node"
)

type reply struct {
	out, errOut string
	err         error
}

type fakeHost struct {
	env   map[string]string
	paths map[string]string
	files map[string]bool
	cmds  map[string]reply
	free  uint64
	ran   []string
}

type fileInfo struct{}

func (fileInfo) Name() string       { return "f" }
func (fileInfo) Size() int64        { return 1 }
func (fileInfo) Mode() fs.FileMode  { return 0o755 }
func (fileInfo) ModTime() time.Time { return time.Time{} }
func (fileInfo) IsDir() bool        { return false }
func (fileInfo) Sys() any           { return nil }

func (f *fakeHost) host(goos string) Host {
	return Host{GOOS: goos, Executable: "/opt/factory/bin/factory-node",
		Getenv: func(k string) string { return f.env[k] },
		LookPath: func(n string) (string, error) {
			if p, ok := f.paths[n]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Stat: func(p string) (fs.FileInfo, error) {
			if f.files[filepath.Clean(p)] {
				return fileInfo{}, nil
			}
			return nil, fs.ErrNotExist
		},
		FreeBytes: func(string) (uint64, error) { return f.free, nil },
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			line := strings.Join(append([]string{name}, args...), " ")
			f.ran = append(f.ran, line)
			r, ok := f.cmds[line]
			if !ok {
				return "", "", errors.New("exec: " + name + ": not found")
			}
			return r.out, r.errOut, r.err
		}}
}

const (
	claudeShim  = "/opt/factory/workers/claude/dist/src/shim.js"
	claudeProbe = "node " + claudeShim + " --probe"
)

// probeLine is what `shim.js --probe` prints for Claude Code version and accountInfo.
func probeLine(version, account string) string {
	return `{"event":"probe","claude_code_version":"` + version + `","sdk_version":"` + claude.SupportedSDK + `","account":` + account + "}\n"
}

// psLine is the doctor's process start time check of its own PID.
var psLine = strings.Join(PsCommand(os.Getpid()), " ")

// healthy is a macOS node with both drivers ready.
func healthy() *fakeHost {
	return &fakeHost{
		env: map[string]string{},
		// The Claude driver needs node and the built shim, not a claude CLI.
		paths: map[string]string{"gh": "/usr/bin/gh", "codex": "/usr/bin/codex", "node": "/usr/bin/node"},
		files: map[string]bool{"/opt/factory/bin/factory-mcp": true, "/opt/factory/bin/factory-shim": true,
			claudeShim: true, "/opt/factory/workers/claude/package.json": true},
		cmds: map[string]reply{
			"git --version":                          {out: "git version 2.50.1\n"},
			psLine:                                   {out: "Wed Oct 15 09:00:00 2026\n"},
			"git config --get user.name":             {out: "Factory\n"},
			"git config --get user.email":            {out: "f@example.com\n"},
			"gh auth status":                         {out: "github.com\n  ✓ Logged in to github.com account bot (keyring)\n"},
			"xcode-select -p":                        {out: "/Applications/Xcode.app/Contents/Developer\n"},
			"xcodebuild -version":                    {out: "Xcode 26.6\nBuild version 17F113\n"},
			"xcrun simctl list devices available -j": {out: `{"devices":{"ios26":[{"name":"iPhone"},{"name":"iPad"}],"ios25":[]}}`},
			"/usr/bin/codex --version":               {out: "codex-cli 0.162.1\n"},
			"/usr/bin/codex login status":            {errOut: "Logged in using ChatGPT\n"},
			"node --version":                         {out: "v22.4.0\n"},
			claudeProbe:                              {out: probeLine("2.1.284", `{"email":"secret@example.com","subscriptionType":"max","tokenSource":"claude.ai","apiKeySource":"none","apiProvider":"firstParty"}`)},
		},
		free: 100 * GiB,
	}
}

func doctor(f *fakeHost, goos string) Doctor {
	d := Doctor{Host: f.host(goos), DiskPath: "/work", Low: 20 * GiB, Critical: 5 * GiB}
	return withRuntimes(f, d)
}

// withRuntimes wires the probes as cmd/factory-node does.
func withRuntimes(f *fakeHost, d Doctor) Doctor {
	h := d.Host
	d.Runtimes = []Runtime{{Name: "codex", Probe: codex.Driver{Binary: f.env["CODEX_BIN"], Host: &h}}, {Name: "claude", Probe: claude.Driver{Host: &h, BinDir: d.BinDir()}}}
	return d
}

func byCheck(rs []CheckResult) map[string]CheckResult {
	m := map[string]CheckResult{}
	for _, r := range rs {
		m[r.Check] = r
	}
	return m
}

func TestHealthyNode(t *testing.T) {
	rs := doctor(healthy(), "darwin").Run(context.Background())
	for _, r := range rs {
		if r.Status != Pass && !(r.Status == Skip && (r.Check == "core" || r.Check == "nats")) { // no FACTORY_URL, NATS_URL
			t.Errorf("%s = %s %s", r.Check, r.Status, r.Detail)
		}
	}
	m := byCheck(rs)
	if m["drivers"].Detail != "usable: codex, claude" || m["xcode.simctl"].Detail != "2 available simulators in 1 runtimes" {
		t.Fatal(m["drivers"], m["xcode.simctl"])
	}
	if strings.Contains(m["claude.auth"].Detail, "secret") {
		t.Fatal("account email leaked", m["claude.auth"])
	}
}

func TestChecks(t *testing.T) {
	cases := []struct {
		name   string
		goos   string
		change func(*fakeHost, *Doctor)
		check  string
		status Status
		want   string
	}{
		{"git missing", "linux", func(f *fakeHost, _ *Doctor) { delete(f.cmds, "git --version") }, "git", Fail, "install git"},
		{"git user unset", "linux", func(f *fakeHost, _ *Doctor) { delete(f.cmds, "git config --get user.email") }, "git.user", Fail, "user.email"},
		{"gh missing", "linux", func(f *fakeHost, _ *Doctor) { delete(f.paths, "gh") }, "forge.auth", Warn, "gh auth login"},
		{"gh logged out", "linux", func(f *fakeHost, _ *Doctor) {
			f.cmds["gh auth status"] = reply{errOut: "You are not logged into any GitHub hosts.", err: errors.New("exit 1")}
		}, "forge.auth", Warn, "gh auth login"},
		{"repo unreachable", "linux", func(f *fakeHost, d *Doctor) { d.Repo = "git@github.com:x/y.git" }, "forge.repo", Fail, "credentials"},
		{"repo ok", "linux", func(f *fakeHost, d *Doctor) {
			d.Repo = "r"
			f.cmds["git ls-remote --exit-code r HEAD"] = reply{out: "abc\tHEAD"}
		}, "forge.repo", Pass, "r"},
		{"disk low", "linux", func(f *fakeHost, _ *Doctor) { f.free = 10 * GiB }, "disk", Warn, "janitor"},
		{"disk critical", "linux", func(f *fakeHost, _ *Doctor) { f.free = 4 * GiB }, "disk", Fail, "pauses"},
		{"ps missing", "linux", func(f *fakeHost, _ *Doctor) { delete(f.cmds, psLine) }, "ps", Fail, "install procps"},
		{"ps empty", "linux", func(f *fakeHost, _ *Doctor) { f.cmds[psLine] = reply{} }, "ps", Fail, "install procps"},
		{"xcode skipped off macOS", "linux", nil, "xcode", Skip, "not macOS"},
		{"command line tools only", "darwin", func(f *fakeHost, _ *Doctor) {
			f.cmds["xcode-select -p"] = reply{out: "/Library/Developer/CommandLineTools"}
		}, "xcode", Fail, "xcode-select -s"},
		{"no simulators", "darwin", func(f *fakeHost, _ *Doctor) {
			f.cmds["xcrun simctl list devices available -j"] = reply{out: `{"devices":{}}`}
		}, "xcode.simctl", Warn, "downloadPlatform"},
		{"factory-mcp missing", "linux", func(f *fakeHost, _ *Doctor) { delete(f.files, "/opt/factory/bin/factory-mcp") }, "factory-mcp", Fail, "bin/factory-mcp missing"},
		{"FACTORY_BIN_DIR", "linux", func(f *fakeHost, _ *Doctor) {
			f.env["FACTORY_BIN_DIR"] = "/b"
			f.files["/b/factory-mcp"] = true
		}, "factory-mcp", Pass, "/b/factory-mcp"},
		{"codex outdated", "linux", func(f *fakeHost, _ *Doctor) {
			f.cmds["/usr/bin/codex --version"] = reply{out: "codex-cli 0.150.0"}
		}, "codex.version", Fail, "supported: exactly codex-cli 0.162.1"},
		{"codex logged out", "linux", func(f *fakeHost, _ *Doctor) {
			f.cmds["/usr/bin/codex login status"] = reply{errOut: "Not logged in", err: errors.New("exit 1")}
		}, "codex.auth", Fail, "codex login"},
		{"CODEX_BIN", "linux", func(f *fakeHost, _ *Doctor) {
			f.env["CODEX_BIN"] = "codex-0.162.1"
			f.paths["codex-0.162.1"] = "/usr/bin/codex"
			delete(f.paths, "codex")
		}, "codex.version", Pass, "0.162.1"},
		{"old node", "linux", func(f *fakeHost, _ *Doctor) { f.cmds["node --version"] = reply{out: "v20.1.0"} }, "claude.node", Fail, "Node.js 22"},
		{"claude shim not built", "linux", func(f *fakeHost, _ *Doctor) { delete(f.files, claudeShim) }, "claude.shim", Fail, "make build"},
		{"claude logged out", "linux", func(f *fakeHost, _ *Doctor) {
			f.cmds[claudeProbe] = reply{out: probeLine("2.1.284", `{"apiKeySource":"none"}`)}
		}, "claude.auth", Fail, "/login"},
		{"claude api key refused", "linux", func(f *fakeHost, _ *Doctor) {
			f.cmds[claudeProbe] = reply{out: probeLine("2.1.284", `{"apiKeySource":"apiKeyHelper"}`)}
		}, "claude.auth", Fail, "CLAUDE_ALLOW_API_KEY"},
		{"claude api key allowed", "linux", func(f *fakeHost, _ *Doctor) {
			f.env["CLAUDE_ALLOW_API_KEY"] = "1"
			f.cmds[claudeProbe] = reply{out: probeLine("2.1.284", `{"apiKeySource":"apiKeyHelper"}`)}
		}, "claude.auth", Pass, "apiKeyHelper"},
		{"claude env api key removed by runs", "linux", func(f *fakeHost, _ *Doctor) {
			f.cmds[claudeProbe] = reply{out: probeLine("2.1.284", `{"apiKeySource":"ANTHROPIC_API_KEY"}`)}
		}, "claude.auth", Warn, "runs remove it"},
		{"claude probe error", "linux", func(f *fakeHost, _ *Doctor) {
			f.cmds[claudeProbe] = reply{out: `{"event":"error","class":"executable","message":"ENOENT"}` + "\n", err: errors.New("exit 1")}
		}, "claude.version", Fail, "ENOENT"},
		{"claude off version", "linux", func(f *fakeHost, _ *Doctor) {
			f.cmds[claudeProbe] = reply{out: probeLine("2.2.0", `{"subscriptionType":"max"}`)}
		}, "claude.version", Fail, "expected SDK"},
		{"codex not installed is skipped", "linux", func(f *fakeHost, _ *Doctor) { delete(f.paths, "codex") }, "codex", Skip, "driver disabled"},
		{"required codex missing", "linux", func(f *fakeHost, d *Doctor) {
			delete(f.paths, "codex")
			d.Drivers = []string{"codex"}
		}, "codex.binary", Fail, "@openai/codex@0.162.1"},
		{"one driver down", "linux", func(f *fakeHost, _ *Doctor) {
			f.cmds["/usr/bin/codex login status"] = reply{err: errors.New("exit 1")}
		}, "drivers", Pass, "usable: claude"},
		{"no driver usable", "linux", func(f *fakeHost, _ *Doctor) {
			delete(f.paths, "codex")
			delete(f.files, claudeShim)
		}, "drivers", Fail, "no usable runtime driver"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := healthy()
			d := doctor(f, c.goos)
			if c.change != nil {
				c.change(f, &d)
			}
			d = withRuntimes(f, d)
			r, ok := byCheck(d.Run(context.Background()))[c.check]
			if !ok || r.Status != c.status || !strings.Contains(r.Detail+" "+r.Hint, c.want) {
				t.Fatalf("%s = %+v, want %s containing %q", c.check, r, c.status, c.want)
			}
		})
	}
}

func TestNoModelOrNetworkCommands(t *testing.T) {
	f := healthy()
	doctor(f, "darwin").Run(context.Background())
	allowed := map[string]bool{}
	for k := range f.cmds {
		allowed[k] = true
	}
	for _, line := range f.ran {
		if !allowed[line] {
			t.Errorf("unexpected command %q", line)
		}
	}
}

func TestRender(t *testing.T) {
	var b bytes.Buffer
	rs := []CheckResult{
		{Check: "git", Status: Pass, Detail: "git version 2.50.1", Hint: "unused"},
		{Check: "codex.version", Status: Fail, Detail: "codex-cli 0.150.0", Hint: "npm install -g @openai/codex@0.162.1"},
		{Check: "disk", Status: Warn, Detail: "10.0 GiB free", Hint: "prune"},
		{Check: "xcode", Status: Skip, Detail: "not macOS"},
	}
	if err := Render(&b, rs); err != nil {
		t.Fatal(err)
	}
	want := `STATUS  CHECK          DETAIL
PASS    git            git version 2.50.1
FAIL    codex.version  codex-cli 0.150.0
                       -> npm install -g @openai/codex@0.162.1
WARN    disk           10.0 GiB free
                       -> prune
SKIP    xcode          not macOS

1 passed, 1 warnings, 1 failed, 1 skipped
`
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
	if !Failed(rs) || Failed(rs[:1]) {
		t.Fatal("Failed")
	}
}

var _ DriverProbe = claude.Driver{}
