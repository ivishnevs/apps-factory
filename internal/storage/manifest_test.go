package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestClaimCarriesManifest (core-owned-project-data 2.1, openspec-in-git): the claim answer carries a
// manifest without OpenSpec documents (they are in the checkout); the run's lease opens only its project's
// blobs.
func TestClaimCarriesManifest(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "materialized"))
	run := dispatchEngineTask(t, s, task, "materialized-run")
	claim, err := s.ClaimRun(ctx, DefaultCompany, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m := claim.RunSpec.Materialize; m == nil || len(m.Files) != 0 {
		t.Fatal(m)
	}
	saved, _ := s.Get(ctx, DefaultCompany, run.ID)
	if saved.Spec.Materialize != nil {
		t.Fatal("manifest stored in the run's spec")
	}
	foreign, _ := s.PutBlob(ctx, DefaultCompany, strings.NewReader("not this project's"), MaxBlob)
	if _, _, err = s.RunBlob(ctx, DefaultCompany, run.ID, claim.Lease, foreign.SHA256); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, _, err = s.RunBlob(ctx, DefaultCompany, run.ID, "wrong-lease", foreign.SHA256); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = s.PutBlob(ctx, DefaultCompany, strings.NewReader("too large"), 4); !errors.Is(err, ErrInput) {
		t.Fatal("oversized blob", err)
	}
}
