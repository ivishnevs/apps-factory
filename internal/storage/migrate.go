package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	Version int
	Name    string
	SQL     string
}

// legacyVersion is the last schema applied through the PRAGMA user_version chain;
// such databases are adopted into schema_migrations without re-running anything.
const legacyVersion = 7

func migrations() ([]migration, error) {
	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	out := []migration{}
	for _, path := range entries {
		name := strings.TrimSuffix(strings.TrimPrefix(path, "migrations/"), ".sql")
		prefix, _, ok := strings.Cut(name, "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil || len(prefix) != 4 || version < 1 {
			return nil, fmt.Errorf("migration %s: name must be NNNN_name.sql", path)
		}
		b, err := migrationFiles.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{Version: version, Name: name, SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration %s: versions must be contiguous from 0001", m.Name)
		}
	}
	return out, nil
}

// migrate applies every unapplied migration in order, one transaction each. PRAGMA
// user_version mirrors the highest version so older Cores still refuse a newer schema.
func migrate(db *sql.DB) error {
	ms, err := migrations()
	if err != nil {
		return err
	}
	latest := ms[len(ms)-1].Version
	var tracked int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&tracked); err != nil {
		return err
	}
	if tracked == 0 {
		var version int
		if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version > legacyVersion {
			return fmt.Errorf("unsupported database schema %d", version)
		}
		if err = adoptLegacy(db, ms, version); err != nil {
			return err
		}
	}
	var current int
	if err = db.QueryRow("SELECT coalesce(max(version),0) FROM schema_migrations").Scan(&current); err != nil {
		return err
	}
	if current > latest {
		return fmt.Errorf("unsupported database schema %d (this Core knows up to %d)", current, latest)
	}
	for _, m := range ms[current:] {
		if err = apply(db, m); err != nil {
			return fmt.Errorf("migration %s: %w", m.Name, err)
		}
	}
	return nil
}

func adoptLegacy(db *sql.DB, ms []migration, version int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)"); err != nil {
		return err
	}
	for _, m := range ms[:version] {
		if _, err = tx.Exec("INSERT INTO schema_migrations VALUES(?,?,?)", m.Version, m.Name, time.Now().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func apply(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(m.SQL); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO schema_migrations VALUES(?,?,?)", m.Version, m.Name, time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", m.Version)); err != nil {
		return err
	}
	return tx.Commit()
}
