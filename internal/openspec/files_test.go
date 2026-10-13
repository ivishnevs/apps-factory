package openspec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func fixture(t *testing.T) (Access, string) {
	t.Helper()
	workspace := t.TempDir()
	a := Access{}
	if _, e := a.Init(workspace); e != nil {
		t.Fatal(e)
	}
	for _, dir := range []string{"openspec/changes/feature/specs/service", "openspec/specs/service"} {
		if e := os.MkdirAll(filepath.Join(workspace, dir), 0755); e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range []string{"openspec/changes/feature/proposal.md", "openspec/changes/feature/specs/service/spec.md", "openspec/specs/service/spec.md"} {
		if e := os.WriteFile(filepath.Join(workspace, p), []byte("# Requirement\nПривет мир 漢字\n"), 0644); e != nil {
			t.Fatal(e)
		}
	}
	return a, workspace
}
func TestFilesCurrentReadsAndDigestPagination(t *testing.T) {
	a, w := fixture(t)
	name := "changes/feature/proposal.md"
	check, e := a.CheckChange(w, "feature")
	if e != nil || !check.Valid || check.Scope != "reference" {
		t.Fatal(check, e)
	}
	d, e := a.Read(w, name, 0, 17, "")
	if e != nil || !d.HasMore || !utf8.ValidString(d.Content) {
		t.Fatal(d, e)
	}
	contents := d.Content
	for d.HasMore {
		d, e = a.Read(w, name, d.NextOffset, 17, d.SHA256)
		if e != nil || !utf8.ValidString(d.Content) {
			t.Fatal(d, e)
		}
		contents += d.Content
	}
	expected, _ := os.ReadFile(filepath.Join(w, "openspec", name))
	if contents != string(expected) {
		t.Fatal(contents)
	}
	first, _ := a.Read(w, name, 0, 8, "")
	if _, e = a.Read(w, name, first.NextOffset, 8, ""); e == nil {
		t.Fatal("unversioned continuation")
	}
	os.WriteFile(filepath.Join(w, "openspec", name), []byte("# Updated\n"), 0644)
	if _, e = a.Read(w, name, first.NextOffset, 8, first.SHA256); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	updated, e := a.Read(w, name, 0, 8192, "")
	if e != nil || updated.Content != "# Updated\n" {
		t.Fatal(updated, e)
	}
	if _, e = a.Init(w); e != nil {
		t.Fatal(e)
	}
	again, _ := a.Read(w, name, 0, 8192, "")
	if again.Content != updated.Content {
		t.Fatal("overwrote existing document")
	}
	os.WriteFile(filepath.Join(w, "openspec", name), []byte("  \n"), 0644)
	if _, e = a.CheckChange(w, "feature"); e == nil {
		t.Fatal("empty proposal")
	}
	for _, name := range []string{"../feature", "archive", "feature/other", "../../secret", "Feature"} {
		if _, e = a.CheckChange(w, name); e == nil {
			t.Fatal("invalid change", name)
		}
	}
}
func TestConfinedFilesAndMountedWorkspace(t *testing.T) {
	a, w := fixture(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0644)
	os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(w, "openspec/changes/feature/link.md"))
	os.Symlink(outside, filepath.Join(w, "openspec/changes/escape"))
	for _, name := range []string{"../secret.md", "/etc/passwd", "changes/feature/link.md", "changes/escape/secret.md", "changes/feature/../../../../secret.md", "config.yaml/../secret.md", "changes\\feature\\proposal.md"} {
		if _, e := a.Read(w, name, 0, 8192, ""); e == nil {
			t.Fatal("escaped", name)
		}
	}
	if _, e := a.List(w, "changes/escape", "", 10); e == nil {
		t.Fatal("symlink directory")
	}
	mapped := Access{HostRoot: "/native/projects", LocalRoot: filepath.Dir(w)}
	hostWorkspace := filepath.Join("/native/projects", filepath.Base(w))
	if _, e := mapped.CheckChange(hostWorkspace, "feature"); e != nil {
		t.Fatal(e)
	}
	if _, e := mapped.Read("/native/projects-other/foreign", "changes/feature/proposal.md", 0, 8192, ""); e == nil {
		t.Fatal("prefix mapping escape")
	}
	if _, e := (Access{HostRoot: "/native/projects"}).Init(hostWorkspace); e == nil {
		t.Fatal("partial mapping")
	}
	root := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "openspec"))
	if _, e := a.Init(root); e == nil {
		t.Fatal("symlink initialization")
	}
}
func TestListAndDocumentBounds(t *testing.T) {
	a, w := fixture(t)
	for _, name := range []string{"alpha", "omega"} {
		os.Mkdir(filepath.Join(w, "openspec/changes", name), 0755)
	}
	first, e := a.List(w, "changes", "", 2)
	if e != nil || len(first.Items) != 2 || !first.HasMore {
		t.Fatal(first, e)
	}
	second, e := a.List(w, "changes", first.NextAfter, 100)
	if e != nil || second.HasMore {
		t.Fatal(second, e)
	}
	for _, entry := range second.Items {
		if entry.Name <= first.NextAfter {
			t.Fatal(entry)
		}
	}
	for _, dir := range []string{"../", "/tmp", "changes/../../..", "changes\\feature"} {
		if _, e = a.List(w, dir, "", 10); e == nil {
			t.Fatal(dir)
		}
	}
	name := filepath.Join(w, "openspec/specs/service/spec.md")
	os.WriteFile(name, []byte(strings.Repeat("a", MaxDocument+1)), 0644)
	if _, e = a.Read(w, "specs/service/spec.md", 0, 8192, ""); e == nil {
		t.Fatal("oversize document")
	}
	os.WriteFile(name, []byte{0xff, 0xfe}, 0644)
	if _, e = a.Read(w, "specs/service/spec.md", 0, 8192, ""); e == nil {
		t.Fatal("non UTF8 document")
	}
}
