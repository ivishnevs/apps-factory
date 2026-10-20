package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestClaimCarriesManifest (core-owned-project-data 2.1): the claim answer lists the project's current
// OpenSpec tree by digest; the run's lease opens exactly the blobs of its project.
func TestClaimCarriesManifest(t *testing.T) {
	s := specStore(t)
	ctx := context.Background()
	spec := putSpec(t, s, "p", "specs/service/spec.md", "# Service\n")
	task := readyEngineTask(t, s, createEngineTask(t, s, "materialized"))
	run := dispatchEngineTask(t, s, task, "materialized-run")
	claim, err := s.ClaimRun(ctx, DefaultCompany, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := claim.RunSpec.Materialize
	if m == nil || m.OpenSpecDir != "openspec" || len(m.Files) != 2 || m.Files[1].Path != "openspec/specs/service/spec.md" || m.Files[1].SHA256 != spec.SHA256 || m.Files[1].Size != spec.Size {
		t.Fatal(m)
	}
	saved, _ := s.Get(ctx, DefaultCompany, run.ID)
	if saved.Spec.Materialize != nil {
		t.Fatal("manifest stored in the run's spec")
	}
	rc, b, err := s.RunBlob(ctx, DefaultCompany, run.ID, claim.Lease, spec.SHA256)
	if err != nil || b.Size != spec.Size {
		t.Fatal(b, err)
	}
	rc.Close()
	if _, _, err = s.RunBlob(ctx, DefaultCompany, run.ID, "wrong-lease", spec.SHA256); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	foreign, _ := s.PutBlob(ctx, DefaultCompany, strings.NewReader("not this project's"), MaxBlob)
	if _, _, err = s.RunBlob(ctx, DefaultCompany, run.ID, claim.Lease, foreign.SHA256); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.PutBlob(ctx, DefaultCompany, strings.NewReader("too large"), 4); !errors.Is(err, ErrInput) {
		t.Fatal("oversized blob", err)
	}
}
