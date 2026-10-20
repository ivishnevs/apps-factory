package node

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestTransientRun: a probe command killed by its timeout, exiting without output or failing to start for
// lack of resources is transient; a missing binary or an exit with a message is definitive.
func TestTransientRun(t *testing.T) {
	h := LocalHost("darwin")
	ctx := WithCommandTimeout(context.Background(), 50*time.Millisecond)
	out, errOut, err := h.Run(ctx, "sleep", "5")
	if err == nil || !TransientRun(out, errOut, err) {
		t.Fatalf("killed by the timeout: %v", err)
	}
	if got := CommandTimeoutOf(context.Background()); got != CommandTimeout {
		t.Fatalf("default timeout %v", got)
	}
	out, errOut, err = h.Run(context.Background(), "sh", "-c", "exit 3")
	if !TransientRun(out, errOut, err) {
		t.Fatalf("exit without output: %v", err)
	}
	out, errOut, err = h.Run(context.Background(), "sh", "-c", "echo not logged in >&2; exit 1")
	if TransientRun(out, errOut, err) {
		t.Fatal("exit with a message is definitive")
	}
	out, errOut, err = h.Run(context.Background(), "/nonexistent/factory-probe")
	if TransientRun(out, errOut, err) {
		t.Fatalf("missing binary is definitive: %v", err)
	}
	if !TransientRun("", "", &exec.Error{Name: "node", Err: syscall.EAGAIN}) || TransientRun("", "", nil) {
		t.Fatal("start failure under load")
	}
	if !IsTransient(Transient(errors.New("x"))) || IsTransient(errors.New("x")) || Transient(nil) != nil {
		t.Fatal("Transient wrapper")
	}
}
