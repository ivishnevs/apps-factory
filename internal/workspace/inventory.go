package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// MaxInventory bounds an inventory (protocol.MaxWorktreeEntries): the most recently used worktrees are listed.
const MaxInventory = 500

// Entry is one task worktree of the inventory (protocol.WorktreeEntry without the removal error).
type Entry struct {
	Task, Project string
	SealedHead    string
	// Unpushed: the last seal left work no remote has.
	Unpushed bool
	// LastUsed: Unix time of the last seal, or the directory's modification time without one.
	LastUsed int64
	Bytes    int64
	// Busy: the worktree lock is held now.
	Busy bool
}

// sizeTTL: how long a measured worktree size is reused before it is measured again.
const sizeTTL = time.Hour

type sizeEntry struct {
	bytes int64
	at    time.Time
}

var (
	sizesMu sync.Mutex
	sizes   = map[string]sizeEntry{}
)

// Inventory lists the task worktrees of company on this node (worktree-inventory-cleanup D2), the most recently
// used first, at most MaxInventory. Sizes are measured at most once an hour per worktree.
func (w *Worktrees) Inventory(ctx context.Context, company string) ([]Entry, error) {
	if !safeID.MatchString(company) {
		return nil, errors.New("workspace: invalid company id")
	}
	paths, err := filepath.Glob(filepath.Join(w.root, company, "*", "tasks", "*"))
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	live := map[string]bool{}
	for _, p := range paths {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		task, project := filepath.Base(p), filepath.Base(filepath.Dir(filepath.Dir(p)))
		if !safeID.MatchString(task) || !safeID.MatchString(project) {
			continue
		}
		live[p] = true
		e := Entry{Task: task, Project: project, LastUsed: info.ModTime().Unix(), Busy: locked(p)}
		if st, err := readState(p); err == nil {
			e.SealedHead = st.SealedHead
			e.Unpushed = localWork(st.SealResult, st.SavedWIP)
			if !st.SealedAt.IsZero() {
				e.LastUsed = st.SealedAt.Unix()
			}
		}
		e.Bytes = measured(p)
		out = append(out, e)
	}
	sizesMu.Lock()
	for p := range sizes {
		if !live[p] {
			delete(sizes, p)
		}
	}
	sizesMu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastUsed > out[j].LastUsed })
	if len(out) > MaxInventory {
		out = out[:MaxInventory]
	}
	return out, nil
}

// localWork: the seal left work no remote has: commits on no origin branch, or a WIP snapshot not saved to origin.
func localWork(res SealResult, savedWIP string) bool {
	return res.Unpushed || res.WIPSHA != "" && res.WIPSHA != savedWIP
}

// locked: a run (or a seal, reset or removal) holds the worktree lock now. A missing lock file is not created.
func locked(path string) bool {
	f, err := os.OpenFile(filepath.Join(path, ".factory", "lock"), os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// measured is the worktree's size on disk, measured again after sizeTTL.
func measured(path string) int64 {
	sizesMu.Lock()
	e, ok := sizes[path]
	sizesMu.Unlock()
	if ok && time.Since(e.at) < sizeTTL {
		return e.bytes
	}
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	sizesMu.Lock()
	sizes[path] = sizeEntry{bytes: total, at: time.Now()}
	sizesMu.Unlock()
	return total
}

// ErrLocalWork: the worktree holds work no remote has; a stale copy or an unknown task's worktree is kept.
var ErrLocalWork = errors.New("workspace: the worktree holds work no remote has")

// ErrRecentlyUsed: the worktree was used more recently than a stale removal allows (a run came back to it).
var ErrRecentlyUsed = errors.New("workspace: the worktree was used recently")

// RemoveStale removes the worktree of a task that is not closed (stale_copy, unknown_task): like Remove, but under
// the lock it is kept when it was used within minIdle or its seal finds work no remote has, and the task's saved
// WIP on origin stays (another node may continue from it).
func (w *Worktrees) RemoveStale(ctx context.Context, r Request, wait, minIdle time.Duration) (SealResult, error) {
	return w.remove(ctx, r, wait, nil, func(path string, before worktreeState, res SealResult) error {
		used := before.SealedAt
		if used.IsZero() {
			if info, err := os.Stat(path); err == nil {
				used = info.ModTime()
			}
		}
		if time.Since(used) < minIdle {
			return ErrRecentlyUsed
		}
		if localWork(res, before.SavedWIP) {
			return ErrLocalWork
		}
		return nil
	})
}
