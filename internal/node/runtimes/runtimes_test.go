package runtimes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func tgz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

// release is a fake release server: path -> archive, counting downloads.
func release(t *testing.T, files map[string][]byte) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		n.Add(1)
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCodexFromLock (node-packaging-updates 3.3): the standalone Codex of codex.lock is downloaded from the release,
// checked against its sha256 and installed as codex/codex; a mismatch installs nothing; a second package with the same
// pin reuses the first one's file without a download; a platform without a pin is ErrNoPin.
func TestCodexFromLock(t *testing.T) {
	archive := tgz(t, "codex-aarch64-apple-darwin", []byte("#!/bin/sh\necho 'codex-cli 0.162.1'\n"))
	srv, downloads := release(t, map[string][]byte{"/rust-v0.162.1/codex-aarch64-apple-darwin.tar.gz": archive})
	sum := sha256.Sum256(archive)
	lock := func(home, hexsum string) {
		write(t, filepath.Join(home, CodexDir, CodexLock), "# pinned\ndarwin-arm64 "+srv.URL+"/rust-v0.162.1/codex-aarch64-apple-darwin.tar.gz "+hexsum+" codex-aarch64-apple-darwin\n")
	}
	ctx := context.Background()
	in := Installer{Client: srv.Client()}

	v1 := filepath.Join(t.TempDir(), "versions", "1")
	lock(v1, strings.Repeat("0", 64))
	if _, err := in.Codex(ctx, v1, "", "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal("a mismatching download was accepted:", err)
	}
	if Binary(v1, CodexBin) != "" {
		t.Fatal("a mismatching download was installed")
	}
	lock(v1, hex.EncodeToString(sum[:]))
	msg, err := in.Codex(ctx, v1, "", "darwin-arm64")
	if err != nil || !strings.Contains(msg, "codex-cli 0.162.1") {
		t.Fatal(msg, err)
	}
	if Binary(v1, CodexBin) == "" || downloads.Load() != 2 {
		t.Fatalf("installed %q after %d downloads", Binary(v1, CodexBin), downloads.Load())
	}
	// Again: already installed from this pin.
	if _, err = in.Codex(ctx, v1, "", "darwin-arm64"); err != nil || downloads.Load() != 2 {
		t.Fatal("an installed pin was downloaded again", err, downloads.Load())
	}
	// The next version pins the same build: reused from the previous version.
	v2 := filepath.Join(filepath.Dir(v1), "2")
	lock(v2, hex.EncodeToString(sum[:]))
	if _, err = in.Codex(ctx, v2, v1, "darwin-arm64"); err != nil || downloads.Load() != 2 || Binary(v2, CodexBin) == "" {
		t.Fatal("the previous version's runtime was not reused", err, downloads.Load())
	}
	if _, err = in.Codex(ctx, v2, v1, "linux-arm64"); err == nil || !strings.Contains(err.Error(), ErrNoPin.Error()) {
		t.Fatal("a platform without a pin:", err)
	}
	if _, err = in.Codex(ctx, t.TempDir(), "", "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "no codex.lock") {
		t.Fatal("a package without codex.lock:", err)
	}
}

// TestClaudeFromLock (node-packaging-updates 2.4, 3.3): Claude Code from claude.lock (sha512, package/claude in the npm
// tarball) replaces claude-code/claude; a mismatch changes nothing.
func TestClaudeFromLock(t *testing.T) {
	archive := tgz(t, "package/claude", []byte("#!/bin/sh\necho '9.9.9 (Claude Code)'\n"))
	srv, _ := release(t, map[string][]byte{"/c.tgz": archive})
	sum := sha512.Sum512(archive)
	home := t.TempDir()
	lock := func(hexsum string) {
		write(t, filepath.Join(home, ClaudeDir, ClaudeLock), "glibc "+srv.URL+"/c.tgz "+hexsum+"\nmusl "+srv.URL+"/m.tgz x\n")
	}
	in := Installer{Client: srv.Client()}
	lock(strings.Repeat("0", 128))
	if _, err := in.Claude(context.Background(), home, "", "glibc"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal("mismatching download accepted", err)
	}
	if Binary(home, ClaudeBin) != "" {
		t.Fatal("a mismatching download was installed")
	}
	lock(hex.EncodeToString(sum[:]))
	msg, err := in.Claude(context.Background(), home, "", "glibc")
	if err != nil || !strings.Contains(msg, "9.9.9") {
		t.Fatal(msg, err)
	}
	if _, err = in.Claude(context.Background(), home, "", "any"); err == nil {
		t.Fatal("a libc without a pinned build accepted")
	}
}
