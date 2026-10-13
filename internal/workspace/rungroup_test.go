package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"appfactory/internal/procgroup"
)

// startGroup starts sh -c script as the leader of a new process group and reaps it in the background.
func startGroup(t *testing.T, script string) (int, chan struct{}) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid, exited := cmd.Process.Pid, make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); <-exited })
	// Wait for the background sleep so the group has a non-leader member.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if out, _ := exec.Command("pgrep", "-g", strconv.Itoa(pid)).Output(); len(strings.Fields(string(out))) >= 2 {
			return pid, exited
		}
		if time.Now().After(deadline) {
			t.Fatalf("group %d did not start its child", pid)
		}
	}
}

func groupGone(pid int) bool { return errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) }

func TestReleaseKillsRunGroup(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wt, err := f.w.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := startGroup(t, "sleep 60 & wait")
	if err = wt.Track(pid); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(pidPath(wt.Path)); err != nil {
		t.Fatal(err)
	}
	next := make(chan error, 1)
	go func() {
		wt2, err := f.w.Acquire(ctx, f.req)
		if err == nil && !groupGone(pid) {
			err = errors.New("next run acquired while the old group was alive")
		}
		if err == nil {
			err = wt2.Release()
		}
		next <- err
	}()
	select {
	case err := <-next:
		t.Fatalf("next run did not wait: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	start := time.Now()
	if err = wt.Release(); err != nil {
		t.Fatal(err)
	}
	if !groupGone(pid) {
		t.Fatal("group alive after release")
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("release took %s", el)
	}
	if err = <-next; err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(pidPath(wt.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pidfile left: %v", err)
	}
}

func TestStopEscalatesToSIGKILL(t *testing.T) {
	defer func(g time.Duration) { stopGrace = g }(stopGrace)
	stopGrace = 300 * time.Millisecond
	f := setup(t)
	wt, err := f.w.Ensure(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := startGroup(t, `trap "" TERM; sleep 60 & wait`)
	if err = wt.Track(pid); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = wt.Stop(); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < stopGrace || !groupGone(pid) {
		t.Fatalf("stop after %s, gone %v", el, groupGone(pid))
	}
}

func TestReleaseReportsSurvivingGroup(t *testing.T) {
	defer func(g, l time.Duration) { stopGrace, stopLimit = g, l }(stopGrace, stopLimit)
	stopGrace, stopLimit = 0, 300*time.Millisecond
	f := setup(t)
	wt, err := f.w.Acquire(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	// An unreaped leader stays a zombie, so its group outlives SIGKILL.
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	if err = wt.Track(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err = wt.Release(); err == nil || !strings.Contains(err.Error(), "still alive") {
		t.Fatalf("release = %v", err)
	}
	if _, err = os.Stat(pidPath(wt.Path)); err != nil {
		t.Fatalf("pidfile of a surviving group removed: %v", err)
	}
}

func TestAcquireKillsStaleRunGroup(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	// A holder that crashed: pidfile written, flock released by the kernel.
	wt, err := f.w.Ensure(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := startGroup(t, "sleep 60 & wait")
	if err = wt.Track(pid); err != nil {
		t.Fatal(err)
	}
	got, err := f.w.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Release()
	if !groupGone(pid) {
		t.Fatal("stale group alive after acquire")
	}
	if _, err = os.Stat(pidPath(wt.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale pidfile left: %v", err)
	}
}

func TestStaleRunPIDReused(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wt, err := f.w.Ensure(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	// The recorded leader is gone and its PID now belongs to an unrelated group leader.
	pid, exited := startGroup(t, "sleep 60 & wait")
	b, _ := json.Marshal(procgroup.Group{ID: pid, Started: "Mon Jan  1 00:00:00 2001"})
	write(t, pidPath(wt.Path), string(b))
	got, err := f.w.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	if err = got.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
		t.Fatal("killed an unrelated process group with a reused PID")
	case <-time.After(200 * time.Millisecond):
	}
	if groupGone(pid) {
		t.Fatal("unrelated group killed")
	}
	if _, err = os.Stat(pidPath(wt.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale pidfile left: %v", err)
	}
}

func TestTrackRejectsDeadLeader(t *testing.T) {
	f := setup(t)
	wt, err := f.w.Ensure(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("true")
	if err = cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err = wt.Track(cmd.Process.Pid); err == nil {
		t.Fatal("tracked a dead leader")
	}
	if err = wt.Track(1); err == nil {
		t.Fatal("tracked group 1")
	}
}
