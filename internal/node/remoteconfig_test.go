package node

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/protocol"
)

func remoteCfg(t *testing.T, yaml string) Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(p, []byte("state_dir: "+t.TempDir()+"\n"+yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestRemoteYAML: remote: off, a map of pins, unknown keys refused; slots_max bounded.
func TestRemoteYAML(t *testing.T) {
	if c := remoteCfg(t, "remote: off\n"); !c.Remote.Off || !c.Remote.Pinned("slots") {
		t.Fatalf("%+v", c.Remote)
	}
	c := remoteCfg(t, "remote:\n  labels: false\n  slots: true\nslots_max: 4\n")
	if c.Remote.Off || !c.Remote.Pinned("labels") || c.Remote.Pinned("slots") || c.Remote.Pinned("disk") || c.SlotsMax != 4 {
		t.Fatalf("%+v", c)
	}
	if !slices.Equal(c.Remote.PinnedKeys(), []string{"labels"}) {
		t.Fatal(c.Remote.PinnedKeys())
	}
	for _, bad := range []string{"remote:\n  sandbox: false\n", "remote: maybe\n", "slots_max: 99\n"} {
		p := filepath.Join(t.TempDir(), "node.yaml")
		_ = os.WriteFile(p, []byte("state_dir: /tmp/x\n"+bad), 0o600)
		if _, err := LoadConfig(p); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// TestApplyRemote (node-packaging-updates B): settings from the web layer over node.yaml; pins, slots_max,
// policy.runtimes and a change that leaves no driver are refused or capped and reported; node.yaml's values are
// reported next to them.
func TestApplyRemote(t *testing.T) {
	local := remoteCfg(t, "slots: 2\nlabels: [team-a]\nslots_max: 3\nremote:\n  labels: false\npolicy:\n  runtimes: [codex]\n")
	slots, conv, low, crit := 6, 3, 40.0, 8.0
	labels := []string{"ios"}
	on, off := true, false
	eff, rep := ApplyRemote(local, 7, protocol.NodeSettings{Slots: &slots, ConversationSlots: &conv, Labels: &labels,
		Drivers: map[string]protocol.NodeDriverSettings{"claude": {Enabled: &on}}, Disk: &protocol.NodeDiskSettings{LowGiB: &low, CriticalGiB: &crit}})
	if eff.Slots != 3 || eff.ConversationSlots != 3 || !slices.Equal(eff.Labels, []string{"team-a"}) || eff.Drivers.Claude.On() || eff.Disk.LowGiB != 40 || eff.Disk.CriticalGiB != 8 || eff.Disk.ConversationFloorGiB != 1 {
		t.Fatalf("%+v", eff)
	}
	want := map[string]string{"slots": "applied", "conversation_slots": "applied", "labels": "refused", "drivers.claude": "refused", "disk": "applied"}
	for k, st := range want {
		if rep.Settings[k].State != st {
			t.Errorf("%s: %+v", k, rep.Settings[k])
		}
	}
	if rep.Version != 7 || rep.Settings["slots"].Reason != "capped at slots_max 3" || !strings.Contains(rep.Settings["drivers.claude"].Reason, "policy.runtimes") ||
		*rep.Local.Slots != 2 || !slices.Equal(*rep.Local.Labels, []string{"team-a"}) || !slices.Equal(rep.Pinned, []string{"labels"}) || rep.SlotsMax != 3 {
		t.Fatalf("%+v", rep)
	}
	if local.Slots != 2 || local.Disk.LowGiB != 20 {
		t.Fatal("node.yaml's config changed", local)
	}
	// The last driver cannot be turned off; an invalid disk keeps node.yaml's.
	open := remoteCfg(t, "")
	eff, rep = ApplyRemote(open, 1, protocol.NodeSettings{Drivers: map[string]protocol.NodeDriverSettings{"codex": {Enabled: &off}}, Disk: &protocol.NodeDiskSettings{CriticalGiB: &low}})
	if !eff.Drivers.Codex.On() || rep.Settings["drivers.codex"].State != "refused" || rep.Settings["disk"].State != "refused" || eff.Disk.CriticalGiB != 5 {
		t.Fatalf("%+v %+v", eff.Drivers, rep)
	}
	// Turning Claude on and Codex off together is fine.
	eff, rep = ApplyRemote(open, 2, protocol.NodeSettings{Drivers: map[string]protocol.NodeDriverSettings{"codex": {Enabled: &off}, "claude": {Enabled: &on}}})
	if eff.Drivers.Codex.On() || !eff.Drivers.Claude.On() || rep.Settings["drivers.codex"].State != "applied" {
		t.Fatalf("%+v %+v", eff.Drivers, rep)
	}
	// remote: off refuses everything.
	eff, rep = ApplyRemote(remoteCfg(t, "remote: off\n"), 3, protocol.NodeSettings{Slots: &slots})
	if eff.Slots != 2 || rep.Settings["slots"].State != "refused" || !strings.Contains(rep.Settings["slots"].Reason, "remote: off") {
		t.Fatalf("%+v", rep)
	}
}

func TestRemoteFile(t *testing.T) {
	dir := t.TempDir()
	if f, err := LoadRemote(dir); err != nil || f.Version != 0 {
		t.Fatal(f, err)
	}
	slots := 3
	if err := SaveRemote(dir, RemoteFile{Version: 4, Config: protocol.NodeSettings{Slots: &slots}}); err != nil {
		t.Fatal(err)
	}
	f, err := LoadRemote(dir)
	if err != nil || f.Version != 4 || *f.Config.Slots != 3 {
		t.Fatal(f, err)
	}
	if info, _ := os.Stat(RemoteConfigPath(dir)); info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode())
	}
	_ = os.WriteFile(RemoteConfigPath(dir), []byte("{"), 0o600)
	if _, err = LoadRemote(dir); err == nil {
		t.Fatal("broken file read")
	}
}

// TestDetectLabels (node-packaging-updates 3.6): a Mac with Xcode and an iOS simulator runtime, a Linux server
// without a display with a render node, a Mac with only the command line tools.
func TestDetectLabels(t *testing.T) {
	host := func(goos string, cmds map[string]string, files ...string) Host {
		return Host{GOOS: goos, Getenv: func(string) string { return "" },
			LookPath: func(name string) (string, error) {
				if _, ok := cmds[name]; ok {
					return "/usr/bin/" + name, nil
				}
				return "", errors.New("not found")
			},
			Stat: func(p string) (fs.FileInfo, error) {
				if slices.Contains(files, p) {
					return nil, nil
				}
				return nil, fs.ErrNotExist
			},
			Run: func(_ context.Context, name string, args ...string) (string, string, error) {
				out, ok := cmds[name]
				if !ok {
					return "", "xcode-select: error: tool 'xcodebuild' requires Xcode", errors.New("exit 1")
				}
				return out, "", nil
			}}
	}
	ctx := context.Background()
	mac := DetectLabels(ctx, host("darwin", map[string]string{"xcodebuild": "Xcode 26.1", "xcrun": "== Runtimes ==\niOS 26.1 (26.1 - 23B80)"}), "arm64")
	if !slices.Equal(mac, []string{"macos", "arm64", "xcode", "ios-sim", "gpu"}) {
		t.Fatal(mac)
	}
	if clt := DetectLabels(ctx, host("darwin", map[string]string{}), "amd64"); !slices.Equal(clt, []string{"macos", "amd64", "gpu"}) {
		t.Fatal(clt)
	}
	linux := DetectLabels(ctx, host("linux", map[string]string{}, "/dev/dri/renderD128"), "amd64")
	if !slices.Equal(linux, []string{"linux", "amd64", "headless", "gpu"}) {
		t.Fatal(linux)
	}
	desktop := host("linux", map[string]string{"nvidia-smi": ""})
	desktop.Getenv = func(k string) string { return map[string]string{"DISPLAY": ":0"}[k] }
	if l := DetectLabels(ctx, desktop, "arm64"); !slices.Equal(l, []string{"linux", "arm64"}) {
		t.Fatal(l)
	}
}

func TestPackageVersion(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o700)
	if v := PackageVersion(bin); v != "dev" {
		t.Fatal(v)
	}
	_ = os.WriteFile(filepath.Join(dir, "VERSION"), []byte("2026.11.12.1530\n"), 0o600)
	if v := PackageVersion(bin); v != "2026.11.12.1530" {
		t.Fatal(v)
	}
	old := Version
	Version = "2026.11.13.0800"
	defer func() { Version = old }()
	if v := PackageVersion(bin); v != "2026.11.13.0800" {
		t.Fatal(v)
	}
}
