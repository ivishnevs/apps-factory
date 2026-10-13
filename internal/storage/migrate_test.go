package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/protocol"
)

// legacySchema builds a database the way the PRAGMA user_version chain left it.
func legacySchema(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms[:version] {
		if _, err = db.Exec(m.SQL); err != nil {
			t.Fatal(m.Name, err)
		}
	}
	if _, err = db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyV7DatabaseIsAdopted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacySchema(t, db, legacyVersion)
	b, _ := json.Marshal(protocol.RunSpec{ID: "old", Project: "p", Agent: "demo"})
	if _, err = db.Exec("INSERT INTO runs(id,project,agent,workspace,spec,status,created_at,result) VALUES('old','p','demo','/tmp/p',?,'COMPLETED',1,'kept')", b); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := s.Get(context.Background(), DefaultCompany, "old"); err != nil || r.Result != "kept" {
		t.Fatal(r, err)
	}
	var adopted int
	if err = s.DB.QueryRow("SELECT count(*) FROM schema_migrations WHERE version<=?", legacyVersion).Scan(&adopted); err != nil || adopted != legacyVersion {
		t.Fatal(adopted, err)
	}
	s.DB.Close()
	if s, err = Open(path); err != nil {
		t.Fatal("reopen", err)
	}
	s.DB.Close()
}

func TestNewerSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO schema_migrations VALUES(9999,'9999_future',0)"); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	if _, err = Open(path); err == nil || !strings.Contains(err.Error(), "unsupported database schema 9999") {
		t.Fatal("newer schema accepted", err)
	}

	untracked := filepath.Join(t.TempDir(), "db")
	db, err := sql.Open("sqlite", untracked)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=8"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = Open(untracked); err == nil {
		t.Fatal("untracked newer schema accepted")
	}
}
