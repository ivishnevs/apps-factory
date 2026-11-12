package skills

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type bodyTransport []byte

func (b bodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: http.Header{}}, nil
}

// TestFetchLimits (security-review-2026-11 F4): what a public source's server sends is cut at MaxFetchBytes, and the
// public client goes through that cut; an answer within the limit is read whole.
func TestFetchLimits(t *testing.T) {
	old := MaxFetchBytes
	MaxFetchBytes = 1 << 10
	t.Cleanup(func() { MaxFetchBytes = old })
	if _, ok := publicClient().Transport.(limitedTransport); !ok {
		t.Fatal("the public client is not limited")
	}
	get := func(n int) ([]byte, error) {
		req, _ := http.NewRequest("GET", "https://example.com/x", nil)
		resp, err := limitedTransport{next: bodyTransport(make([]byte, n))}.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	if b, err := get(1 << 10); err != nil || len(b) != 1<<10 {
		t.Fatal(len(b), err)
	}
	if _, err := get(1<<10 + 1); !errors.Is(err, ErrFetchTooLarge) {
		t.Fatal("a larger answer passed", err)
	}
}

// TestRemoveSymlinks (F9): a clone keeps no symlink, so indexing cannot follow one out of the source.
func TestRemoveSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "a", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"root", "skills/etc"} {
		if err := os.Symlink("/", filepath.Join(dir, l)); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeSymlinks(dir); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"root", "skills/etc"} {
		if _, err := os.Lstat(filepath.Join(dir, l)); !os.IsNotExist(err) {
			t.Fatal("symlink kept", l, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "a", "SKILL.md")); err != nil {
		t.Fatal("regular file removed", err)
	}
}
