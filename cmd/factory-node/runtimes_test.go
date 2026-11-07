package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateClaudeCode (node-packaging-updates 2.4): the pinned build is downloaded, checked against its sha512 and
// replaces claude-code/claude; a mismatch changes nothing.
func TestUpdateClaudeCode(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	script := []byte("#!/bin/sh\necho '9.9.9 (Claude Code)'\n")
	_ = tw.WriteHeader(&tar.Header{Name: "package/claude", Mode: 0o755, Size: int64(len(script))})
	_, _ = tw.Write(script)
	_ = tw.Close()
	_ = gz.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive.Bytes()) }))
	defer srv.Close()
	sum := sha512.Sum512(archive.Bytes())

	home := t.TempDir()
	dir := filepath.Join(home, "claude-code")
	_ = os.MkdirAll(dir, 0o755)
	lock := func(hexsum string) {
		_ = os.WriteFile(filepath.Join(dir, "claude.lock"), []byte("glibc "+srv.URL+"/c.tgz "+hexsum+"\nmusl "+srv.URL+"/m.tgz x\n"), 0o644)
	}
	lock(strings.Repeat("0", 128))
	if _, err := updateClaudeCode(home, "glibc", srv.Client()); err == nil || !strings.Contains(err.Error(), "sha512") {
		t.Fatal("mismatching download accepted", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude")); err == nil {
		t.Fatal("a mismatching download was installed")
	}
	lock(hex.EncodeToString(sum[:]))
	msg, err := updateClaudeCode(home, "glibc", srv.Client())
	if err != nil || !strings.Contains(msg, "9.9.9") {
		t.Fatal(msg, err)
	}
	if _, err = updateClaudeCode(home, "any", srv.Client()); err == nil {
		t.Fatal("a libc without a pinned build accepted")
	}
}
