package janitor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"appfactory/internal/protocol"
)

// TestCatalogHooks: the catalog's NodeHooks (delivered by Core) remove the fake simulator of the closed task
// only; node.yaml's janitor overrides a hook list it sets, the others come from the catalog.
func TestCatalogHooks(t *testing.T) {
	ctx := context.Background()
	sims, logs := t.TempDir(), t.TempDir()
	for _, task := range []string{"t1", "t2"} {
		write(t, filepath.Join(sims, "sim-"+task, "device.plist"), task)
	}
	j := &Janitor{Config: Config{OnRunStop: []string{`echo local >> ` + filepath.Join(logs, "stop.log")}}}
	j.SetCatalog(protocol.NodeHooks{
		OnRunStop:    []string{`echo catalog >> ` + filepath.Join(logs, "stop.log")},
		OnTaskClosed: []string{`rm -rf "` + sims + `/sim-$FACTORY_TASK_ID"`},
		Prune:        []protocol.NodePrune{{Command: `echo "$FACTORY_OLDER_THAN_MINUTES" >> ` + filepath.Join(logs, "prune.log"), OlderThan: "72h"}},
	})
	if done := j.Cleanup(ctx, []string{"t1"}); !slices.Equal(done, []string{"t1"}) {
		t.Fatal(done)
	}
	if _, err := os.Stat(filepath.Join(sims, "sim-t1")); !os.IsNotExist(err) {
		t.Fatal("closed task's simulator kept", err)
	}
	if _, err := os.Stat(filepath.Join(sims, "sim-t2")); err != nil {
		t.Fatal("open task's simulator removed", err)
	}
	j.RunStopped(ctx, "r1", "t2", "")
	j.Prune(ctx, false)
	j.Prune(ctx, true)
	if b, _ := os.ReadFile(filepath.Join(logs, "stop.log")); string(b) != "local\n" {
		t.Fatalf("on_run_stop: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(logs, "prune.log")); string(b) != "4320\n0\n" {
		t.Fatalf("prune: %q", b)
	}
}
