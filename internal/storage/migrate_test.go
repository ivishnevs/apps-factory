package storage

import (
	"database/sql"
	"fmt"
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
	ms, _ := loadMigrations()
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM schema_migrations WHERE applied_at=0").Scan(&n); err != nil || n != len(ms) {
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

// downs undo migrations from 0009 on, so backfill tests can build a database as an older Core left it.
var downs = map[int][]string{
	9:  {"DROP TABLE feed_events"},
	10: {"DROP TABLE task_triggers", "DROP TABLE task_parks", "ALTER TABLE tasks DROP COLUMN wake_seq", "ALTER TABLE tasks DROP COLUMN handled_seq", "ALTER TABLE runs DROP COLUMN brief_from", "ALTER TABLE runs DROP COLUMN brief_to"},
	23: {"ALTER TABLE workspaces DROP COLUMN reset_pending"},
	24: {"ALTER TABLE companies DROP COLUMN rules"},
	25: {"DROP TABLE node_tokens", "ALTER TABLE runs DROP COLUMN node_held"},
	26: {"ALTER TABLE nodes DROP COLUMN disk_alerts", "ALTER TABLE nodes DROP COLUMN core_paused", "ALTER TABLE nodes DROP COLUMN failed_checks",
		"ALTER TABLE nodes DROP COLUMN starts", "ALTER TABLE nodes DROP COLUMN restart_alerted", "ALTER TABLE nodes DROP COLUMN offline_alerted"},
	27: {"DROP INDEX escalation_open_scope"}, // task_id stays nullable (a superset)
	28: {"ALTER TABLE task_parks DROP COLUMN run_id", "ALTER TABLE task_triggers DROP COLUMN addressee"},
	22: {"ALTER TABLE workspaces DROP COLUMN abandon_branch"},
	21: {"ALTER TABLE outbox DROP COLUMN attempts", "ALTER TABLE outbox DROP COLUMN next_at", "ALTER TABLE outbox DROP COLUMN last_error"},
	20: {"ALTER TABLE workspaces DROP COLUMN unpushed", "ALTER TABLE nodes DROP COLUMN worktrees",
		"CREATE UNIQUE INDEX project_writer ON runs(project) WHERE status IN ('QUEUED','RUNNING','CANCELLING') OR (status='UNKNOWN' AND released=0)"},
	19: {"DROP TABLE pr_approvals", "DROP TABLE run_exits"}, // the widened task_triggers CHECK stays (a superset)
	18: {"DROP TABLE task_budget", "DROP INDEX project_writer", "DROP INDEX workspace_writer", "DROP INDEX session_turn",
		"CREATE UNIQUE INDEX project_writer ON runs(project) WHERE status IN ('QUEUED','RUNNING','CANCELLING','UNKNOWN')",
		"CREATE UNIQUE INDEX workspace_writer ON runs(workspace) WHERE workspace != '' AND status IN ('QUEUED','RUNNING','CANCELLING','UNKNOWN')",
		"CREATE UNIQUE INDEX session_turn ON runs(session_id) WHERE session_id != '' AND status IN ('QUEUED','RUNNING','CANCELLING','UNKNOWN')",
		"DROP INDEX active_task_run", "CREATE UNIQUE INDEX active_task_run ON runs(task_id) WHERE task_id IS NOT NULL AND status IN ('QUEUED','RUNNING','CANCELLING','UNKNOWN')",
		"ALTER TABLE runs DROP COLUMN released"},
	17: {"DROP TABLE escalations"},
	16: {"DROP TRIGGER run_outcome", "ALTER TABLE runs DROP COLUMN outcome", "ALTER TABLE runs DROP COLUMN last_activity_at", "ALTER TABLE runs DROP COLUMN stop_reason"},
	15: {"DROP TABLE core_meta", "DROP TABLE nodes", "DROP INDEX runs_node", "ALTER TABLE runs DROP COLUMN protocol", "ALTER TABLE runs DROP COLUMN node_id", "ALTER TABLE runs DROP COLUMN epoch", "ALTER TABLE runs DROP COLUMN claim_id", "ALTER TABLE runs DROP COLUMN slot", "ALTER TABLE runs DROP COLUMN start_by", "ALTER TABLE runs DROP COLUMN run_started", "ALTER TABLE runs DROP COLUMN end_applied", "ALTER TABLE workspaces DROP COLUMN cleanup_listed"},
	14: {"ALTER TABLE runs DROP COLUMN usage", "DROP TABLE workspaces", "ALTER TABLE pauses DROP COLUMN until"},
	13: {"DROP TABLE pauses", "ALTER TABLE runs DROP COLUMN revoked"},
	12: {"DROP TABLE task_alerts"},
	11: {"DROP INDEX queued_runs", "ALTER TABLE runs DROP COLUMN command_count", "ALTER TABLE runs DROP COLUMN command_at", "ALTER TABLE outbox DROP COLUMN sent_at"},
}

func rollbackTo(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for v := len(ms); v > version; v-- {
		qs, ok := downs[v]
		if !ok {
			t.Fatalf("no down for migration %d", v)
		}
		for _, q := range qs {
			if _, err = db.Exec(q); err != nil {
				t.Fatal(q, err)
			}
		}
	}
	if _, err = db.Exec(fmt.Sprintf("DELETE FROM schema_migrations WHERE version>%d; PRAGMA user_version=%d", version, version)); err != nil {
		t.Fatal(err)
	}
}
