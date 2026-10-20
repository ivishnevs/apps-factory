package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
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

// SchemaKey identifies the embedded migrations (a hash of their names and contents): databases migrated by
// the same key have the same schema (test template databases are named after it).
func SchemaKey() string {
	ms, err := loadMigrations()
	if err != nil {
		return "invalid"
	}
	h := sha256.New()
	for _, m := range ms {
		fmt.Fprintf(h, "%d %s %d\n%s", m.Version, m.Name, len(m.SQL), m.SQL)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Migrate applies the pending embedded migrations to db, each in its own transaction, and records them in
// schema_migrations. It holds a session advisory lock meanwhile, so Core processes starting together apply
// each migration exactly once; a database with a version newer than this binary knows is refused.
func Migrate(ctx context.Context, db *sql.DB) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock($1, 0)", lockMigrate); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1, 0)", lockMigrate)
	if _, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, name text NOT NULL, applied_at bigint NOT NULL)"); err != nil {
		return err
	}
	var current int
	if err = conn.QueryRowContext(ctx, "SELECT coalesce(max(version),0) FROM schema_migrations").Scan(&current); err != nil {
		return err
	}
	if latest := ms[len(ms)-1].Version; current > latest {
		return fmt.Errorf("unsupported database schema %d (this Core knows up to %d)", current, latest)
	}
	for _, m := range ms[current:] {
		if err = apply(ctx, conn, m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
		}
	}
	return nil
}

func apply(ctx context.Context, conn *sql.Conn, m migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version,name,applied_at) VALUES($1,$2,$3)", m.Version, m.Name, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
