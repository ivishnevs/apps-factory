// Package storagetest opens migrated Postgres stores for tests of the packages that use storage.
package storagetest

import (
	"context"
	"database/sql"
	"testing"

	"appfactory/internal/storage"
	"appfactory/internal/storage/pgtest"
)

// URL returns the URL of a fresh migrated database for t, dropped when t ends; without
// FACTORY_TEST_DATABASE_URL the test is skipped.
func URL(t testing.TB) string {
	t.Helper()
	return pgtest.FromTemplate(t, storage.SchemaKey(), Prepare)
}

// Prepare migrates the database at url (a test template).
func Prepare(url string) error {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return err
	}
	defer db.Close()
	return storage.Migrate(context.Background(), db)
}

// Open opens a store on a fresh migrated database; the store is closed when t ends.
func Open(t testing.TB) *storage.Store {
	t.Helper()
	s, err := storage.Open(URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}
