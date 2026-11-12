package storage

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCutUTF8 (2026-11-12): a run's error text in Russian cut at 512 bytes left half a Cyrillic letter before "…";
// Postgres refused the event (invalid byte sequence 0xd1 0xe2) and the lost run held its task for good.
func TestCutUTF8(t *testing.T) {
	s := strings.Repeat("я", 300) // 600 bytes
	for _, n := range []int{0, 1, 511, 512, 513, 600, 700} {
		got := cutUTF8(s, n)
		if !utf8.ValidString(got) || len(got) > n {
			t.Fatalf("cut %d: %d bytes, valid %t", n, len(got), utf8.ValidString(got))
		}
	}
	if e := excerpt512(strings.Repeat("a", 1) + strings.Repeat("я", 300)); !utf8.ValidString(e) {
		t.Fatal("excerpt512 produced invalid UTF-8")
	}
	if b := briefExcerpt("x"+strings.Repeat("ж", 100), 50); !utf8.ValidString(b) {
		t.Fatal("briefExcerpt produced invalid UTF-8")
	}
}
