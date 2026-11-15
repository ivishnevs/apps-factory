package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
)

// TestRunDataRetention (run-data-retention): a run ended longer ago than the retention loses its events, its log
// chunks (contents nothing else uses are deleted) and the large fields of its spec; its row and small spec fields
// stay. A run ended recently, a live run and content shared with an artifact are untouched; 0 keeps everything.
func TestRunDataRetention(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	ctx := context.Background()
	co := DefaultCompany
	createEngineTask(t, s, "t")
	now := fake.Now().Unix()
	big := strings.Repeat("prompt ", 400)
	run := func(id, status string, finished int64) {
		t.Helper()
		spec := `{"id":"` + id + `","task_id":"t","agent":"demo","project":"p","prompt":"` + big + `"}`
		if _, err := s.DB.Exec("INSERT INTO runs(id,project,agent,workspace,spec,status,created_at,finished_at,task_id,company_id) VALUES(?,'p','demo','/tmp/x',?,?,?,?,'t',?)",
			id, spec, status, finished-60, finished, co); err != nil {
			t.Fatal(err)
		}
		for i, body := range []string{`{"type":"text"}`, `{"type":"result"}`} {
			if _, err := s.DB.Exec("INSERT INTO events(id,run_id,body,company_id) VALUES(?,?,?,?)", id+"-e"+string(rune('a'+i)), id, body, co); err != nil {
				t.Fatal(err)
			}
		}
	}
	logOf := func(id string, seq int, content string) Blob {
		t.Helper()
		b, err := s.PutBlob(ctx, co, strings.NewReader(content), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.DB.Exec("INSERT INTO run_logs(company_id,run_id,seq,sha256,size,redacted,final,created_at) VALUES(?,?,?,?,?,0,1,?)", co, id, seq, b.SHA256, b.Size, now); err != nil {
			t.Fatal(err)
		}
		return b
	}
	run("old", "ENDED", now-20*86400)
	run("recent", "ENDED", now-2*86400)
	run("live", "RUNNING", 0)
	oldLog, recentLog := logOf("old", 1, "old transcript"), logOf("recent", 1, "recent transcript")
	// The old run's second chunk has the same content as a chunk of the recent run: it stays.
	shared := logOf("old", 2, "shared chunk")
	logOf("recent", 2, "shared chunk")

	if got, err := s.PruneRunData(ctx, co, 0); err != nil || got.Runs != 0 {
		t.Fatal("0 days pruned", got, err)
	}
	got, err := s.PruneRunData(ctx, co, 14)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runs != 1 || got.Events != 2 || got.Logs != 2 || got.Deleted != 1 || got.BytesFreed != oldLog.Size {
		t.Fatalf("%+v", got)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := s.DB.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("SELECT count(*) FROM events WHERE run_id='old'") != 0 || count("SELECT count(*) FROM events WHERE run_id='recent'") != 2 || count("SELECT count(*) FROM events WHERE run_id='live'") != 2 {
		t.Fatal("events of the wrong runs pruned")
	}
	if count("SELECT count(*) FROM runs WHERE id='old'") != 1 {
		t.Fatal("the run row was deleted")
	}
	var spec string
	if err = s.DB.QueryRow("SELECT spec::text FROM runs WHERE id='old'").Scan(&spec); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(spec, "prompt") || !strings.Contains(spec, `"task_id"`) || !strings.Contains(spec, `"agent"`) {
		t.Fatal(spec)
	}
	if ok, _ := s.Blobs.Exists(ctx, co, oldLog.SHA256); ok {
		t.Fatal("the old run's log content stayed")
	}
	for _, b := range []Blob{recentLog, shared} {
		if ok, _ := s.Blobs.Exists(ctx, co, b.SHA256); !ok {
			t.Fatal("content still in use was deleted", b.SHA256)
		}
	}
	// A second pass finds nothing left to do.
	if got, err = s.PruneRunData(ctx, co, 14); err != nil || got.Runs != 0 {
		t.Fatal(got, err)
	}
}
