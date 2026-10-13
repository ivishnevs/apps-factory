package node

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigDefaults(t *testing.T) {
	c, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Slots != 2 || !c.Drivers.Codex.On() || c.Drivers.Claude.On() || c.Disk.LowGiB != 20 || c.Disk.CriticalGiB != 5 || c.Disk.Path != c.StateDir || c.Name == "" {
		t.Fatalf("%+v", c)
	}
}

func TestConfigFile(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, `
name: mini-1
slots: 3
state_dir: /tmp/node-state
bin_dir: /opt/factory/bin
drivers:
  codex: {enabled: false}
  claude: {enabled: true, executable: /usr/local/bin/claude}
disk: {path: /work, low_watermark_gib: 50, critical_watermark_gib: 10}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "mini-1" || c.Slots != 3 || c.Drivers.Codex.On() || !c.Drivers.Claude.On() || c.Drivers.Claude.Executable != "/usr/local/bin/claude" || c.Disk.Path != "/work" || c.Disk.LowGiB != 50 || c.BinDir != "/opt/factory/bin" {
		t.Fatalf("%+v", c)
	}
}

func TestConfigValidation(t *testing.T) {
	for body, want := range map[string]string{
		"slots: 0":                           "slots 0",
		"slots: 65":                          "slots 65",
		"name: 'a b'":                        `name "a b"`,
		"drivers: {codex: {enabled: false}}": "enable codex or claude",
		"disk: {low_watermark_gib: 5, critical_watermark_gib: 10}": "must not exceed",
		"disk: {critical_watermark_gib: -1}":                       "negative",
		"state_dir: ''":                                            "state_dir is required",
		"slot: 2":                                                  "field slot not found",
		"drivers: {codex: {enabled: true, binary: codex, model: gpt}}": "field model not found",
		"worktrees: {enabled: true, scan_command: ['']}":               "empty executable",
	} {
		_, err := LoadConfig(writeConfig(t, body))
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "node.yaml") {
			t.Errorf("%s: %v, want %q", body, err, want)
		}
	}
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestConfigWorktrees(t *testing.T) {
	c, err := LoadConfig("")
	if err != nil || c.Worktrees.Enabled || c.Worktrees.Root != "" {
		t.Fatalf("worktrees must be off by default: %+v %v", c.Worktrees, err)
	}
	c, err = LoadConfig(writeConfig(t, "state_dir: /s\nworktrees: {enabled: true}\n"))
	if err != nil || !c.Worktrees.Enabled || c.Worktrees.Root != "/s/worktrees" {
		t.Fatalf("%+v %v", c.Worktrees, err)
	}
	c, err = LoadConfig(writeConfig(t, "worktrees: {enabled: true, root: /w, scan_command: [true]}\n"))
	if err != nil || c.Worktrees.Root != "/w" || len(c.Worktrees.ScanCommand) != 1 {
		t.Fatalf("%+v %v", c.Worktrees, err)
	}
}

func TestConfigPath(t *testing.T) {
	t.Setenv(ConfigEnv, "/env/node.yaml")
	if ConfigPath("") != "/env/node.yaml" || ConfigPath("/flag.yaml") != "/flag.yaml" {
		t.Fatal("flag must win over env")
	}
}

func testLog() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	return slog.New(slog.NewTextHandler(&b, nil)), &b
}

func TestDiskGatePausesOnceAndResumes(t *testing.T) {
	free := uint64(30 * GiB)
	var readErr error
	log, out := testLog()
	d := &DiskGate{Path: "/work", Low: 20 * GiB, Critical: 5 * GiB, Log: log, FreeBytes: func(p string) (uint64, error) {
		if p != "/work" {
			t.Fatal(p)
		}
		return free, readErr
	}}
	steps := []struct {
		free      uint64
		err       error
		claimable bool
	}{
		{30 * GiB, nil, true}, {15 * GiB, nil, true}, {14 * GiB, nil, true},
		{4 * GiB, nil, false}, {3 * GiB, nil, false},
		{0, errors.New("statfs"), false}, {0, errors.New("statfs"), false},
		{6 * GiB, nil, true}, {25 * GiB, nil, true}, {4 * GiB, nil, false},
	}
	for i, s := range steps {
		free, readErr = s.free, s.err
		if got := d.Claimable(); got != s.claimable || d.Paused() == got {
			t.Fatalf("step %d: claimable %v", i, got)
		}
	}
	log2 := out.String()
	for msg, n := range map[string]int{"below low watermark": 2, "below critical watermark; claiming paused": 2, "claiming resumed": 1, "above low watermark": 1, "unreadable": 1} {
		if c := strings.Count(log2, msg); c != n {
			t.Errorf("%q logged %d times, want %d\n%s", msg, c, n, log2)
		}
	}
}

type fakeSlots struct {
	slots     []Slot
	restarted []int
	cancelled []string
}

func (f *fakeSlots) Status() []Slot { return append([]Slot{}, f.slots...) }
func (f *fakeSlots) Restart(i int) {
	f.restarted = append(f.restarted, i)
	f.slots[i].Beat = time.Unix(1000, 0)
}
func (f *fakeSlots) Cancel(run string) { f.cancelled = append(f.cancelled, run) }

type fakeGroups struct {
	tracked []TrackedGroup
	alive   map[int]bool
	reaped  []string
}

func (f *fakeGroups) Tracked() ([]TrackedGroup, error) { return f.tracked, nil }
func (f *fakeGroups) Alive(g int) bool                 { return f.alive[g] }
func (f *fakeGroups) Reap(t TrackedGroup) error        { f.reaped = append(f.reaped, t.Run); return nil }

func TestWatchdog(t *testing.T) {
	now := time.Unix(1000, 0)
	slots := &fakeSlots{slots: []Slot{
		{Index: 0, Busy: true, Run: "live", Beat: now.Add(-time.Hour)}, // busy slots are not judged by beat
		{Index: 1, Busy: true, Run: "dead"},
		{Index: 2, Beat: now.Add(-3 * time.Minute)},
		{Index: 3, Beat: now.Add(-time.Second)},
	}}
	groups := &fakeGroups{tracked: []TrackedGroup{{"live", 10}, {"dead", 11}, {"stray", 12}}, alive: map[int]bool{10: true, 12: true}}
	log, _ := testLog()
	w := Watchdog{Slots: slots, Groups: groups, Stale: 2 * time.Minute, Now: func() time.Time { return now }, Log: log}
	w.Check()
	if len(slots.restarted) != 1 || slots.restarted[0] != 2 || len(slots.cancelled) != 1 || slots.cancelled[0] != "dead" || len(groups.reaped) != 1 || groups.reaped[0] != "stray" {
		t.Fatalf("restarted %v cancelled %v reaped %v", slots.restarted, slots.cancelled, groups.reaped)
	}
	// The dead run is cancelled once; once its slot is free its group is reaped if still recorded.
	groups.tracked = groups.tracked[:2]
	w.Check()
	if len(slots.cancelled) != 1 || len(slots.restarted) != 1 {
		t.Fatalf("repeated actions: %v %v", slots.cancelled, slots.restarted)
	}
	slots.slots[1] = Slot{Index: 1, Beat: now}
	w.Check()
	if len(groups.reaped) != 2 || groups.reaped[1] != "dead" {
		t.Fatal(groups.reaped)
	}
}

func TestLaunchdPlist(t *testing.T) {
	var b bytes.Buffer
	l := Launchd{Label: "com.appfactory.node", Executable: "/opt/factory/bin/factory-node", Config: "/etc/node & co.yaml", WorkingDir: "/opt/factory", Log: "/tmp/node.log", Env: map[string]string{"FACTORY_TOKEN": "a<b"}}
	if err := l.Render(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"<key>KeepAlive</key><true/>", "<string>run</string>", "<string>/etc/node &amp; co.yaml</string>", "<string>a&lt;b</string>", "<key>WorkingDirectory</key><string>/opt/factory</string>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q\n%s", want, out)
		}
	}
}
