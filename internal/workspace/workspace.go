// Package workspace isolates allocation so Git worktrees can replace the MVP writer lock.
package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Allocation struct {
	Path    string
	Release func()
}
type Allocator interface {
	Acquire(context.Context, string) (Allocation, error)
}
type Local struct{}

// writerLock: the project workspace's writer lock file (Local).
const writerLock = ".factory-writer.lock"

func (Local) Acquire(ctx context.Context, path string) (Allocation, error) {
	if err := ctx.Err(); err != nil {
		return Allocation{}, err
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Allocation{}, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return Allocation{}, fmt.Errorf("workspace is not a directory")
	}
	f, err := os.OpenFile(filepath.Join(path, writerLock), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Allocation{}, err
	}
	for {
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		}
		// The node moving the workspace into the project clone holds the lock for a while: wait for it.
		if !pathExists(path + adoptingSuffix) {
			f.Close()
			return Allocation{}, fmt.Errorf("workspace has an active writer: %w", err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return Allocation{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return Allocation{Path: path, Release: func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }}, nil
}
