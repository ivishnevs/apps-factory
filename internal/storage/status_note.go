package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

// The status note (saas-release-readiness 2.4): one line the operator sets during an incident or maintenance;
// the landing, the sign-in and the app show it to everyone until it is cleared. It lives in core_meta, so every
// Core process serves the same note and it survives a restart.

// MaxStatusNote bounds the note (one line on a banner).
const MaxStatusNote = 300

// StatusNote is the current note, "" when none is set.
func (s *Store) StatusNote(ctx context.Context) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, "SELECT value FROM core_meta WHERE key='status_note'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetStatusNote sets the note; an empty note clears it.
func (s *Store) SetStatusNote(ctx context.Context, note string) (string, error) {
	note = strings.Join(strings.Fields(note), " ")
	if utf8.RuneCountInString(note) > MaxStatusNote {
		return "", inputError("the status note is one line of at most 300 characters")
	}
	var err error
	if note == "" {
		_, err = s.DB.ExecContext(ctx, "DELETE FROM core_meta WHERE key='status_note'")
	} else {
		_, err = s.DB.ExecContext(ctx, "INSERT INTO core_meta(key,value) VALUES('status_note',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", note)
	}
	return note, err
}
