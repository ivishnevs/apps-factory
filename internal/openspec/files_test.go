package openspec

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

const text = "# Requirement\nПривет мир 漢字\n"

func fixture() []Doc {
	var docs []Doc
	for _, p := range []string{"changes/feature/proposal.md", "changes/feature/specs/service/spec.md", "specs/service/spec.md"} {
		docs = append(docs, Doc{Path: p, SHA256: Digest([]byte(text)), Size: int64(len(text))})
	}
	return docs
}

// TestDigestPagination: pages keep UTF-8 boundaries, continuation needs the digest and a changed document
// conflicts (now read from Core storage, without a filesystem).
func TestDigestPagination(t *testing.T) {
	name := "changes/feature/proposal.md"
	check, e := CheckChange("feature", []byte(text), true)
	if e != nil || !check.Valid || check.Scope != "reference" || check.ProposalSHA256 != Digest([]byte(text)) {
		t.Fatal(check, e)
	}
	d, e := Read(name, []byte(text), 0, 17, "")
	if e != nil || !d.HasMore || !utf8.ValidString(d.Content) {
		t.Fatal(d, e)
	}
	contents := d.Content
	for d.HasMore {
		d, e = Read(name, []byte(text), d.NextOffset, 17, d.SHA256)
		if e != nil || !utf8.ValidString(d.Content) {
			t.Fatal(d, e)
		}
		contents += d.Content
	}
	if contents != text {
		t.Fatal(contents)
	}
	first, _ := Read(name, []byte(text), 0, 8, "")
	if _, e = Read(name, []byte(text), first.NextOffset, 8, ""); e == nil {
		t.Fatal("unversioned continuation")
	}
	if _, e = Read(name, []byte("# Updated\n"), first.NextOffset, 8, first.SHA256); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = CheckChange("feature", []byte("  \n"), true); e == nil {
		t.Fatal("empty proposal")
	}
	if _, e = CheckChange("feature", nil, false); e == nil {
		t.Fatal("missing proposal")
	}
	for _, name := range []string{"../feature", "archive", "feature/other", "../../secret", "Feature"} {
		if _, e = CheckChange(name, []byte(text), true); e == nil {
			t.Fatal("invalid change", name)
		}
	}
}

// TestPathRules: traversal, absolute and foreign paths stay refused.
func TestPathRules(t *testing.T) {
	for _, name := range []string{"../secret.md", "/etc/passwd", "changes/feature/../../../../secret.md", "config.yaml/../secret.md", "changes\\feature\\proposal.md", "notes.md", "changes/feature/script.sh", "changes//x.md"} {
		if ValidPath(name) {
			t.Fatal("accepted", name)
		}
		if _, e := Read(name, []byte(text), 0, 8192, ""); e == nil {
			t.Fatal("read", name)
		}
	}
	for _, name := range []string{"config.yaml", "specs/a/spec.md", "changes/archive/2025-01-01-x/proposal.md", "changes/x/.openspec.yaml"} {
		if !ValidPath(name) {
			t.Fatal("refused", name)
		}
	}
	for _, dir := range []string{"openspec", "docs/openspec"} {
		if !ValidTreeDir(dir) {
			t.Fatal(dir)
		}
	}
	for _, dir := range []string{"", ".", "..", "/abs", ".factory", ".git/x", "a/../b"} {
		if ValidTreeDir(dir) {
			t.Fatal(dir)
		}
	}
}

func TestListAndDocumentBounds(t *testing.T) {
	docs := fixture()
	for _, name := range []string{"alpha", "omega"} {
		docs = append(docs, Doc{Path: "changes/" + name + "/proposal.md", Size: 1})
	}
	root, e := List(docs, ".", "", 10)
	if e != nil || len(root.Items) != 2 || root.Items[0].Name != "changes" || !root.Items[0].Directory {
		t.Fatal(root, e)
	}
	first, e := List(docs, "changes", "", 2)
	if e != nil || len(first.Items) != 2 || !first.HasMore || first.Items[0].Name != "alpha" {
		t.Fatal(first, e)
	}
	second, e := List(docs, "changes", first.NextAfter, 100)
	if e != nil || second.HasMore || len(second.Items) != 2 {
		t.Fatal(second, e)
	}
	for _, entry := range second.Items {
		if entry.Name <= first.NextAfter {
			t.Fatal(entry)
		}
	}
	// The fixed archive directory is listed even when empty.
	if !first.Items[1].Directory || first.Items[1].Name != "archive" || second.Items[0].Name != "feature" {
		t.Fatal(first, second)
	}
	files, e := List(docs, "changes/feature", "", 10)
	if e != nil || len(files.Items) != 2 || files.Items[0].Name != "proposal.md" || files.Items[0].Bytes != int64(len(text)) || !files.Items[1].Directory {
		t.Fatal(files, e)
	}
	for _, dir := range []string{"../", "/tmp", "changes/../../..", "changes\\feature"} {
		if _, e = List(docs, dir, "", 10); e == nil {
			t.Fatal(dir)
		}
	}
	if _, e = Read("specs/service/spec.md", []byte(strings.Repeat("a", MaxDocument+1)), 0, 8192, ""); e == nil {
		t.Fatal("oversize document")
	}
	if _, e = Read("specs/service/spec.md", []byte{0xff, 0xfe}, 0, 8192, ""); e == nil {
		t.Fatal("non UTF8 document")
	}
	if got := Changes(docs); strings.Join(got, ",") != "alpha,feature,omega" {
		t.Fatal(got)
	}
}

func TestArchivedProposals(t *testing.T) {
	paths := []string{"changes/feature/proposal.md", "changes/archive/2026-01-02-feature/proposal.md", "changes/archive/2026-03-04-feature/proposal.md",
		"changes/archive/2026-03-04-feature-two/proposal.md", "changes/archive/2026-03-04-feature/tasks.md", "changes/archive/feature/proposal.md"}
	got := ArchivedProposals("feature", paths)
	if len(got) != 2 || got[0] != "changes/archive/2026-03-04-feature/proposal.md" {
		t.Fatal(got)
	}
	c, e := CheckArchived("feature", got[0], []byte("# Feature\n"))
	if e != nil || !c.Valid || !c.Archived || c.Path != "openspec/changes/archive/2026-03-04-feature/proposal.md" {
		t.Fatal(c, e)
	}
}
