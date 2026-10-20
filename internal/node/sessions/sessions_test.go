package sessions

import (
	"os"
	"slices"
	"testing"
)

func TestStore(t *testing.T) {
	s := Open(t.TempDir())
	if _, ok, err := s.Get("missing"); ok || err != nil {
		t.Fatalf("missing: %v %v", ok, err)
	}
	if gone, err := s.ForgetTask("t1"); err != nil || len(gone) != 0 {
		t.Fatalf("forget on empty store: %v %v", gone, err)
	}
	a := Record{SessionID: "thread/1", Task: "t1", Agent: "dev", Run: "r1"}
	if err := s.Put(a); err != nil {
		t.Fatal(err)
	}
	a.Started, a.Outcome, a.Run = true, "succeeded", "r2"
	if err := s.Put(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Record{SessionID: "other", Task: "t2", Agent: "dev", Run: "r3"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("thread/1")
	if err != nil || !ok || !got.Started || got.Outcome != "succeeded" || got.Run != "r2" || got.At.IsZero() {
		t.Fatalf("get = %+v %v %v", got, ok, err)
	}
	gone, err := s.ForgetTask("t1")
	if err != nil || !slices.Equal(gone, []string{"thread/1"}) {
		t.Fatalf("forget = %v %v", gone, err)
	}
	if _, ok, _ = s.Get("thread/1"); ok {
		t.Fatal("record kept")
	}
	if _, ok, _ = s.Get("other"); !ok {
		t.Fatal("other task's record forgotten")
	}
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) != 1 {
		t.Fatalf("store files %v", entries)
	}
	if err = s.Put(Record{}); err == nil {
		t.Fatal("record without session accepted")
	}
}
