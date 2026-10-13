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
var migrationFS embed.FS

type migration struct {
	Version int
	Name    string
	SQL     string
}

// loadMigrations returns the embedded migrations NNNN_name.sql ordered by version.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		num, name, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("bad migration file name %q", e.Name())
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{v, name, string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration %d missing or duplicated", i+1)
		}
	}
	return out, nil
}

// migrate applies pending migrations, each in its own transaction, and records them in
// schema_migrations. A database from before schema_migrations is adopted at its
// PRAGMA user_version. user_version is kept equal to the latest applied version so that
// older binaries, which check only user_version, refuse a newer schema.
func migrate(db *sql.DB) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	latest := ms[len(ms)-1].Version
	var legacy int
	if err = db.QueryRow("PRAGMA user_version").Scan(&legacy); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if legacy > latest {
			return fmt.Errorf("unsupported database schema %d", legacy)
		}
		if _, err = tx.Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)"); err != nil {
			return err
		}
		for _, m := range ms[:legacy] {
			if _, err = tx.Exec("INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,0)", m.Version, m.Name); err != nil {
				return err
			}
		}
	}
	var current int
	if err = tx.QueryRow("SELECT coalesce(max(version),0) FROM schema_migrations").Scan(&current); err != nil {
		return err
	}
	if current > latest {
		return fmt.Errorf("unsupported database schema %d", current)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	for _, m := range ms[current:] {
		if err = apply(db, m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
		}
	}
	return nil
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
	if _, err = tx.Exec("INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)", m.Version, m.Name, time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", m.Version)); err != nil {
		return err
	}
	return tx.Commit()
}
