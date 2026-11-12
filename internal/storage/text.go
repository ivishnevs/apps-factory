package storage

import "unicode/utf8"

// cutUTF8 returns at most n bytes of s, ending on a rune boundary: a byte cut through a multi-byte character (a
// Cyrillic letter is two bytes) is invalid UTF-8, which Postgres refuses (SQLSTATE 22021), so a run's last events
// were never accepted and its task stayed held by a lost run.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
