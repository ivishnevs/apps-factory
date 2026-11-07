package node

import (
	"testing"
)

// TestLocalTrust (connector-trust 1.3, 2.1): a local pause the node honours, and the local activity log.
func TestLocalTrust(t *testing.T) {
	dir := t.TempDir()
	if LocallyPaused(dir) {
		t.Fatal("paused from the start")
	}
	if err := SetLocalPause(dir, true, "travelling"); err != nil || !LocallyPaused(dir) {
		t.Fatal(err)
	}
	if err := SetLocalPause(dir, false, ""); err != nil || LocallyPaused(dir) {
		t.Fatal(err)
	}
	for _, e := range []string{"run.offered", "run.ended", "run.offered"} {
		LogActivity(dir, Activity{Event: e, Run: "r1"})
	}
	got, err := ReadActivity(dir, 2)
	if err != nil || len(got) != 2 || got[1].Event != "run.offered" {
		t.Fatal(got, err)
	}
}
