package storage

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshDatabaseRecordsAllMigrations(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var n, max, version int
	if err = s.DB.QueryRow("SELECT count(*),max(version) FROM schema_migrations WHERE applied_at>0").Scan(&n, &max); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if n != len(ms) || max != len(ms) || version != len(ms) {
		t.Fatal(n, max, version)
	}
}

func TestLegacyDatabaseIsAdoptedWithoutReapplying(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("DROP TABLE schema_migrations"); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	// Re-applying any migration would fail with "table already exists".
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM schema_migrations WHERE applied_at=0").Scan(&n); err != nil || n != 7 {
		t.Fatal(n, err)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	for name, q := range map[string]string{
		"user_version":      "PRAGMA user_version=99",
		"schema_migrations": "INSERT INTO schema_migrations(version,name,applied_at) VALUES(99,'future',1)",
	} {
		path := filepath.Join(t.TempDir(), "db")
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if name == "user_version" {
			s.DB.Exec("DROP TABLE schema_migrations")
		}
		if _, err = s.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
		s.DB.Close()
		if _, err = Open(path); err == nil || !strings.Contains(err.Error(), "unsupported database schema 99") {
			t.Fatal(name, err)
		}
	}
}

func TestFailedMigrationLeavesNoRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// A conflicting table makes migration 0003 fail after 0001-0002 succeed.
	if _, err = db.Exec("CREATE TABLE tool_calls (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = Open(path); err == nil || !strings.Contains(err.Error(), "0003_tool_calls") {
		t.Fatal(err)
	}
	db, _ = sql.Open("sqlite", path)
	defer db.Close()
	var max, version int
	db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&max)
	db.QueryRow("PRAGMA user_version").Scan(&version)
	if max != 2 || version != 2 {
		t.Fatal(max, version)
	}
}
