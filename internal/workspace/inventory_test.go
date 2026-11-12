package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestInventoryAndRemoveStale: the inventory lists every task worktree with its local work and lock state; a stale
// removal keeps recently used worktrees and those holding work no remote has (worktree-inventory-cleanup D5).
func TestInventoryAndRemoveStale(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ensure := func(id string) Worktree {
		t.Helper()
		wt, err := f.w.Ensure(ctx, f.task(id))
		if err != nil {
			t.Fatal(err)
		}
		return wt
	}
	seal := func(id string) {
		t.Helper()
		if _, err := f.w.Seal(ctx, f.task(id)); err != nil {
			t.Fatal(err)
		}
	}
	clean := ensure("clean")
	seal("clean")
	committed := ensure("committed")
	write(t, filepath.Join(committed.Path, "c.txt"), "c\n")
	run(t, committed.Path, "add", "c.txt")
	run(t, committed.Path, "commit", "-qm", "local only")
	seal("committed")
	dirty := ensure("dirty")
	write(t, filepath.Join(dirty.Path, "d.txt"), "d\n")
	seal("dirty")
	ensure("held")
	held, err := f.w.Acquire(ctx, f.task("held"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	inv, err := f.w.Inventory(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range inv {
		if e.Project != "app" || e.LastUsed == 0 || e.Bytes == 0 {
			t.Fatalf("entry %+v", e)
		}
		got[e.Task] = true
		want := map[string][2]bool{"clean": {false, false}, "committed": {true, false}, "dirty": {true, false}, "held": {false, true}}[e.Task]
		if e.Unpushed != want[0] || e.Busy != want[1] {
			t.Fatalf("%s: unpushed %v busy %v", e.Task, e.Unpushed, e.Busy)
		}
	}
	if len(got) != 4 {
		t.Fatal(inv)
	}

	if _, err = f.w.RemoveStale(ctx, f.task("clean"), 0, 1<<62); !errors.Is(err, ErrRecentlyUsed) {
		t.Fatal("recently used removed:", err)
	}
	for _, id := range []string{"committed", "dirty"} {
		if _, err = f.w.RemoveStale(ctx, f.task(id), 0, 0); !errors.Is(err, ErrLocalWork) {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, err = f.w.RemoveStale(ctx, f.task("held"), 0, 0); !errors.Is(err, ErrBusy) {
		t.Fatal("held:", err)
	}
	if _, err = f.w.RemoveStale(ctx, f.task("clean"), 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(clean.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("clean worktree left", err)
	}
	if inv, err = f.w.Inventory(ctx, "acme"); err != nil || len(inv) != 3 {
		t.Fatal(inv, err)
	}
	if _, err = f.w.Inventory(ctx, "../x"); err == nil {
		t.Fatal("unsafe company accepted")
	}
}
