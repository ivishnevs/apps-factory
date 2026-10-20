package storage

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"

	"appfactory/internal/storage/pgtest"
)

// testURL is a fresh migrated database for t (see pgtest); storagetest.URL for other packages.
func testURL(t testing.TB) string {
	t.Helper()
	return pgtest.FromTemplate(t, SchemaKey(), func(url string) error {
		db, err := sql.Open("pgx", url)
		if err != nil {
			return err
		}
		defer db.Close()
		return Migrate(context.Background(), db)
	})
}

func TestFreshDatabaseRecordsAllMigrations(t *testing.T) {
	s, err := Open(pgtest.Empty(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var n, max int
	if err = s.DB.QueryRow("SELECT count(*),max(version) FROM schema_migrations WHERE applied_at>0").Scan(&n, &max); err != nil {
		t.Fatal(err)
	}
	if n != len(ms) || max != len(ms) {
		t.Fatal(n, max)
	}
	var company, account string
	if err = s.DB.QueryRow("SELECT id,account_id FROM companies").Scan(&company, &account); err != nil || company != DefaultCompany || account != DefaultAccount {
		t.Fatal(company, account, err)
	}
}

// TestConcurrentStartAppliesMigrationsOnce (1.3): Core processes starting together against an empty database
// apply each migration exactly once (the startup advisory lock) and share one incarnation.
func TestConcurrentStartAppliesMigrationsOnce(t *testing.T) {
	url := pgtest.Empty(t)
	const cores = 4
	stores := make([]*Store, cores)
	errs := make([]error, cores)
	var wg sync.WaitGroup
	for i := range cores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stores[i], errs[i] = Open(url)
		}()
	}
	wg.Wait()
	incarnations := map[string]bool{}
	for i := range cores {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		inc, err := stores[i].Incarnation(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		incarnations[inc] = true
		defer stores[i].DB.Close()
	}
	ms, _ := loadMigrations()
	var rows, versions int
	if err := stores[0].DB.QueryRow("SELECT count(*),count(DISTINCT version) FROM schema_migrations").Scan(&rows, &versions); err != nil || rows != len(ms) || versions != len(ms) {
		t.Fatal(rows, versions, err)
	}
	if len(incarnations) != 1 {
		t.Fatal("incarnations", incarnations)
	}
}

// TestNewerSchemaIsRefused (1.3): a database migrated by a newer Core makes this one refuse to start.
func TestNewerSchemaIsRefused(t *testing.T) {
	url := testURL(t)
	s, err := Open(url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO schema_migrations(version,name,applied_at) VALUES(99,'future',1)"); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	if _, err = Open(url); err == nil || !strings.Contains(err.Error(), "unsupported database schema 99") {
		t.Fatal(err)
	}
}

// TestFailedMigrationLeavesNoRecord: a migration that fails is rolled back with its schema_migrations row.
func TestFailedMigrationLeavesNoRecord(t *testing.T) {
	url := pgtest.Empty(t)
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A conflicting table makes the baseline fail.
	if _, err = db.Exec("CREATE TABLE tool_calls (id text)"); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(url); err == nil || !strings.Contains(err.Error(), "0001_baseline") {
		t.Fatal(err)
	}
	var n, companies int
	if err = db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err = db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_name='companies'").Scan(&companies); err != nil || companies != 0 {
		t.Fatal("partial baseline", companies, err)
	}
}
