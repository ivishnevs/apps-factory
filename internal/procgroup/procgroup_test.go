package procgroup

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// TestRecordOrLeader: with ps working the group is recorded; without ps the caller gets a zero Group (it
// kills only the leader) and the failure is logged once per process, not per runtime.
func TestRecordOrLeader(t *testing.T) {
	if g := RecordOrLeader(os.Getpid(), "test"); g.ID != os.Getpid() || g.Started == "" {
		t.Fatalf("recorded %+v", g)
	}
	var logs bytes.Buffer
	defer func(l *slog.Logger) { slog.SetDefault(l) }(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer func(p string) { psBinary = p }(psBinary)
	psBinary = "/nonexistent/ps"
	for range 3 {
		if g := RecordOrLeader(os.Getpid(), "test"); g != (Group{}) {
			t.Fatalf("recorded %+v without ps", g)
		}
	}
	if n := strings.Count(logs.String(), "runtime process group not recorded"); n != 1 || !strings.Contains(logs.String(), "procps") {
		t.Fatalf("%d log lines: %s", n, logs.String())
	}
}
