package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type entry struct {
	name, body, link string
	dir              bool
}

// Package writes a node package archive of entries to a file and returns its path.
func pkg(t *testing.T, entries ...entry) string {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o755}
		switch {
		case e.dir:
			h.Typeflag = tar.TypeDir
		case e.link != "":
			h.Typeflag, h.Linkname = tar.TypeSymlink, e.link
		default:
			h.Typeflag, h.Size = tar.TypeReg, int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	p := filepath.Join(t.TempDir(), "pkg.tar.gz")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func version(v string) []entry {
	return []entry{{name: "VERSION", body: v + "\n"}, {name: "bin/", dir: true}, {name: "bin/factory-node", body: "#!/bin/sh\n"},
		{name: "bin/factory-shim", link: "factory-node"}, {name: "bin/factory-mcp", link: "factory-node"}, {name: "codex/codex.lock", body: "x"}}
}

// TestUnpackSwitchLocate (node-packaging-updates 3.4): a package unpacks into versions/<v> with its links, current
// switches atomically, and the executable behind <home>/bin locates its home and version; a package that is not
// version v, escapes its directory or links outside it is refused.
func TestUnpackSwitchLocate(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"2026.11.12.1500", "2026.11.13.0900+abc"} {
		dir, err := Unpack(pkg(t, version(v)...), home, v)
		if err != nil {
			t.Fatal(err)
		}
		if l, err := os.Readlink(filepath.Join(dir, "bin", "factory-shim")); err != nil || l != "factory-node" {
			t.Fatal("link", l, err)
		}
	}
	if err := Switch(home, "2026.11.12.1500"); err != nil {
		t.Fatal(err)
	}
	// install.sh's links: bin -> current/bin.
	if err := os.Symlink(filepath.Join(CurrentLink, "bin"), filepath.Join(home, "bin")); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "bin", "factory-node")
	if h, v, ok := Locate(exe); !ok || v != "2026.11.12.1500" || !sameDir(t, h, home) {
		t.Fatal(h, v, ok)
	}
	if err := Switch(home, "2026.11.13.0900+abc"); err != nil || Current(home) != "2026.11.13.0900+abc" {
		t.Fatal(err, Current(home))
	}
	if _, v, ok := Locate(exe); !ok || v != "2026.11.13.0900+abc" {
		t.Fatal("bin does not follow current", v, ok)
	}
	if _, _, ok := Locate(filepath.Join(t.TempDir(), "factory-node")); ok {
		t.Fatal("a checkout build located as a package")
	}
	if _, err := Unpack(pkg(t, version("2026.11.14")...), home, "2026.11.15"); err == nil || !strings.Contains(err.Error(), "not 2026.11.15") {
		t.Fatal("a package of another version accepted:", err)
	}
	bad := map[string][]entry{
		"outside":   append(version("1.0.1"), entry{name: "../evil", body: "x"}),
		"link":      append(version("1.0.1"), entry{name: "bin/evil", link: "../../evil"}),
		"no binary": {{name: "VERSION", body: "1.0.1"}},
	}
	for what, e := range bad {
		if _, err := Unpack(pkg(t, e...), home, "1.0.1"); err == nil {
			t.Error(what, "accepted")
		}
	}
	if _, err := os.Stat(Dir(home, "1.0.1")); err == nil {
		t.Fatal("a refused package left its directory")
	}
	if err := Switch(home, "9.9.9"); err == nil {
		t.Fatal("switched to a version that is not installed")
	}
	if _, err := Unpack(pkg(t, version("2026.11.13.0900+abc")...), home, "2026.11.13.0900+abc"); err == nil {
		t.Fatal("unpacked over the running version")
	}
}

func sameDir(t *testing.T, a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

// TestHealthCheckRollback (node-packaging-updates 3.4): after a switch each start of the new version counts; it is
// confirmed once healthy (older versions pruned, the previous kept), or rolled back to the previous version past
// MaxBoots starts or the HealthWindow, which update.json reports as failed.
func TestHealthCheckRollback(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	setup := func() string {
		home := t.TempDir()
		for _, v := range []string{"1.0.0", "1.0.1", "1.0.2"} {
			if _, err := Unpack(pkg(t, version(v)...), home, v); err != nil {
				t.Fatal(err)
			}
		}
		if err := Switch(home, "1.0.1"); err != nil {
			t.Fatal(err)
		}
		if err := Begin(home, "1.0.1", "1.0.2", 7, now); err != nil {
			t.Fatal(err)
		}
		if Current(home) != "1.0.2" {
			t.Fatal("not switched")
		}
		return home
	}
	// Healthy: confirmed, 1.0.0 pruned, 1.0.1 kept for the next rollback.
	home := setup()
	b, err := Check(home, "1.0.2", now.Add(10*time.Second))
	if err != nil || !b.Pending || !b.Deadline.Equal(now.Add(HealthWindow)) {
		t.Fatal(b, err)
	}
	if err = Confirm(home, "1.0.2"); err != nil {
		t.Fatal(err)
	}
	if s, _ := Load(home); s.Phase != PhaseDone {
		t.Fatal(s)
	}
	if _, err = os.Stat(Dir(home, "1.0.0")); err == nil {
		t.Fatal("old version kept")
	}
	if _, err = os.Stat(Dir(home, "1.0.1")); err != nil {
		t.Fatal("previous version pruned")
	}
	if b, _ = Check(home, "1.0.2", now.Add(time.Hour)); b.Pending || b.RolledBack {
		t.Fatal("a confirmed version checked again", b)
	}

	// A crash loop: the fourth start rolls back.
	home = setup()
	for i := 0; i < MaxBoots; i++ {
		if b, err = Check(home, "1.0.2", now.Add(time.Duration(i)*time.Second)); err != nil || !b.Pending {
			t.Fatal(i, b, err)
		}
	}
	if b, err = Check(home, "1.0.2", now.Add(5*time.Second)); err != nil || !b.RolledBack || !strings.Contains(b.Reason, "stopped 3 times") {
		t.Fatal(b, err)
	}
	if Current(home) != "1.0.1" {
		t.Fatal("not rolled back", Current(home))
	}
	s, _ := Load(home)
	if st := s.Status(); st.State != "failed" || st.Version != "1.0.2" || st.From != "1.0.1" || st.Attempt != 7 || !strings.Contains(st.Reason, "rolled back to 1.0.1") {
		t.Fatalf("%+v", st)
	}
	if _, err = os.Stat(Dir(home, "1.0.2")); err == nil {
		t.Fatal("the failed version kept")
	}
	// The previous version starting after the rollback: nothing pending.
	if b, _ = Check(home, "1.0.1", now.Add(time.Minute)); b.Pending || b.RolledBack {
		t.Fatal(b)
	}

	// Past the window: rolled back at the next start, or by the running process (Rollback).
	home = setup()
	if b, _ = Check(home, "1.0.2", now.Add(HealthWindow)); !b.RolledBack || !strings.Contains(b.Reason, "within") {
		t.Fatal(b)
	}
	home = setup()
	if _, err = Check(home, "1.0.2", now); err != nil {
		t.Fatal(err)
	}
	if b, err = Rollback(home, "unhealthy", now.Add(HealthWindow)); err != nil || !b.RolledBack || Current(home) != "1.0.1" {
		t.Fatal(b, err)
	}
}
