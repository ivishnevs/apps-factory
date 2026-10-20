package node

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type timeoutKey struct{}

// WithCommandTimeout makes LocalHost's Run allow d per command under ctx instead of CommandTimeout; the
// node's driver health doubles it on each retry of a transient probe failure.
func WithCommandTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, timeoutKey{}, d)
}

// CommandTimeoutOf is the per-command timeout of Run under ctx (WithCommandTimeout, else CommandTimeout).
func CommandTimeoutOf(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(timeoutKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return CommandTimeout
}

// TransientRun reports whether a check command's failure is transient rather than a finding about the
// runtime: it timed out or was killed by a signal, exited without any output, or could not start for lack
// of resources (EAGAIN, ENOMEM, EMFILE, ENFILE, ETXTBSY). A missing or non-executable binary and an exit
// with a message are definitive.
func TransientRun(stdout, stderr string, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return false
	}
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return true
		}
		return strings.TrimSpace(stdout) == "" && strings.TrimSpace(stderr) == ""
	}
	for _, e := range []error{syscall.EAGAIN, syscall.ENOMEM, syscall.EMFILE, syscall.ENFILE, syscall.ETXTBSY} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// TransientError marks an error as transient (TransientRun); IsTransient finds it.
type TransientError struct{ Err error }

func (e *TransientError) Error() string { return e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// Transient wraps err as a TransientError; nil stays nil.
func Transient(err error) error {
	if err == nil {
		return nil
	}
	return &TransientError{Err: err}
}

// IsTransient reports whether err is, or wraps, a TransientError.
func IsTransient(err error) bool {
	var t *TransientError
	return errors.As(err, &t)
}
