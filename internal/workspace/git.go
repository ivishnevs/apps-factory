package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

var lockErr = regexp.MustCompile(`(index|config|packed-refs|HEAD|shallow)\.lock|\.lock': File exists|cannot lock ref|could not lock config file`)

type gitError struct {
	args   []string
	err    error
	stderr string
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, strings.TrimSpace(e.stderr))
}
func (e *gitError) Unwrap() error { return e.err }

func git(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	delay := 50 * time.Millisecond
	for attempt := 0; ; attempt++ {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
		cmd.Env = append(cmd.Env, env...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		if err == nil {
			return strings.TrimSpace(out.String()), nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if attempt >= 8 || !lockErr.MatchString(errb.String()) {
			return "", &gitError{args, err, errb.String()}
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

var repoMu sync.Map

// lockRepo serializes git mutations of one repository within the process (mutex) and across processes (flock).
func lockRepo(ctx context.Context, lockPath string) (func(), error) {
	v, _ := repoMu.LoadOrStore(lockPath, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	got := make(chan struct{})
	go func() { mu.Lock(); close(got) }()
	select {
	case <-got:
	case <-ctx.Done():
		go func() { <-got; mu.Unlock() }()
		return nil, ctx.Err()
	}
	unlock, err := flockWait(ctx, lockPath)
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return func() { unlock(); mu.Unlock() }, nil
}

func flockWait(ctx context.Context, path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		}
	}
}
