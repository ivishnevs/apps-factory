package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"appfactory/internal/procgroup"
)

func TestRPCProcess(t *testing.T) {
	if os.Getenv("FACTORY_RPC_HELPER") != "1" {
		return
	}
	s := bufio.NewScanner(os.Stdin)
	e := json.NewEncoder(os.Stdout)
	for s.Scan() {
		var m Message
		_ = json.Unmarshal(s.Bytes(), &m)
		switch m.Method {
		case "echo":
			_ = e.Encode(map[string]any{"id": m.ID, "result": map[string]string{"text": "ok"}})
		case "request":
			_ = e.Encode(map[string]any{"id": "server-1", "method": "approval", "params": map[string]any{}})
			_ = e.Encode(map[string]any{"id": m.ID, "result": map[string]any{}})
		case "fail":
			_ = e.Encode(map[string]any{"id": m.ID, "error": map[string]any{"code": -1, "message": "failure"}})
		}
	}
	os.Exit(0)
}

// TestCloseSparesReusedGroup: Start records the runtime's group, and Close does not kill a group
// whose leader has another start time (the runtime was reaped and its PID reused).
func TestCloseSparesReusedGroup(t *testing.T) {
	t.Setenv("FACTORY_RPC_HELPER", "1")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c, err := Start(context.Background(), bin, "-test.run=^TestRPCProcess$")
	if err != nil {
		t.Fatal(err)
	}
	if c.group.ID != c.Pid() || c.group.Started == "" {
		t.Fatalf("group not recorded: %+v", c.group)
	}
	foreign := exec.Command("sleep", "30")
	foreign.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = foreign.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = foreign.Wait(); close(done) }()
	defer func() { _ = syscall.Kill(-foreign.Process.Pid, syscall.SIGKILL); <-done }()
	// The helper exits when stdin closes; Close must then leave the foreign group alone.
	c.group = procgroup.Group{ID: foreign.Process.Pid, Started: "Thu Jan  1 00:00:00 1970"}
	c.Close()
	select {
	case <-done:
		t.Fatal("Close killed a group that reused the runtime's PID")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestRPCBidirectionalAndCancellation(t *testing.T) {
	t.Setenv("FACTORY_RPC_HELPER", "1")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c, err := Start(context.Background(), bin, "-test.run=^TestRPCProcess$")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var res struct{ Text string }
	if err = c.Call(ctx, "echo", map[string]any{}, &res); err != nil || res.Text != "ok" {
		t.Fatal(res, err)
	}
	if err = c.Call(ctx, "request", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-c.Events:
		if string(m.ID) != "\"server-1\"" {
			t.Fatal(string(m.ID))
		}
		if err = c.Respond(m.ID, map[string]string{"decision": "decline"}, nil); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("missing server request")
	}
	if err = c.Call(ctx, "fail", nil, nil); err == nil {
		t.Fatal("RPC error lost")
	}
	short, stop := context.WithTimeout(ctx, 10*time.Millisecond)
	defer stop()
	if err = c.Call(short, "hang", nil, nil); err == nil {
		t.Fatal("timeout lost")
	}
}
