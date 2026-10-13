package worker

import (
	"appfactory/internal/bus"
	"appfactory/internal/core"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func startGroup(t *testing.T) *exec.Cmd {
	t.Helper()
	// A shell with a child: the whole group, not only the leader, must die.
	cmd := exec.Command("sh", "-c", "sleep 60 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait() // reap the leader so it does not linger as a zombie
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd
}

func TestRestartKillsOrphanedRuntime(t *testing.T) {
	dir := t.TempDir()
	cmd := startGroup(t)
	if _, err := writePidfile(dir, "lost-run", cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := killOrphans(dir); err != nil {
		t.Fatal(err)
	}
	if groupAlive(cmd.Process.Pid) {
		t.Fatal("orphaned runtime group survived")
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Fatal("pidfile kept", files)
	}
}

func TestReusedPidIsNotKilled(t *testing.T) {
	dir := t.TempDir()
	cmd := startGroup(t)
	path, err := writePidfile(dir, "old-run", cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	// Same PID, different leader start time: the recorded runtime is gone.
	if err = os.WriteFile(path, []byte(`{"run":"old-run","group":`+strconv.Itoa(cmd.Process.Pid)+`,"started":"Thu Jan  1 00:00:00 1970"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = killOrphans(dir); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if !groupAlive(cmd.Process.Pid) {
		t.Fatal("unrelated process group killed")
	}
}

func TestInvalidPidfileIsRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pid.json")
	if err := os.WriteFile(path, []byte(`{"group":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := killOrphans(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid pidfile kept")
	}
}

func TestJetStreamRestartKillsRunningAppServer(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	t.Setenv("FACTORY_FAKE_SERVER", "1")
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	server := httptest.NewServer((&core.Core{Store: s, Bus: b, Token: "test-token"}).Handler())
	defer server.Close()
	spec := protocol.RunSpec{ID: "orphan-run", Project: "orphan-project", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "CANCEL_TEST", TimeoutSeconds: 30}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	if err = s.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin, _ := os.Executable()
	w := Worker{Bus: b, API: API{URL: server.URL, Token: "test-token"}, Binary: bin, Args: []string{"-test.run=^TestFakeAppServer$"}, StateDir: dir}
	done := make(chan error, 1)
	go func() { done <- w.execute(ctx, spec, lease, "orphan-command") }()
	var p pidfile
	for p.Group == 0 {
		select {
		case err = <-done:
			t.Fatal("run ended before pidfile", err)
		case <-time.After(20 * time.Millisecond):
		}
		if b, e := os.ReadFile(filepath.Join(dir, spec.ID+".pid.json")); e == nil {
			_ = json.Unmarshal(b, &p)
		}
	}
	// A restarted worker finds the group of the run that was in progress and kills it.
	if err = killOrphans(dir); err != nil {
		t.Fatal(err)
	}
	if groupAlive(p.Group) {
		t.Fatal("app-server survived")
	}
	if err = <-done; err == nil {
		t.Fatal("run without runtime reported success")
	}
}
