package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestPeopleRemindersAndBriefs (people-assignees 2.2, 3.1): people.remind_after defaults to 72h and is bounded; a
// person's task gets one reminder per episode (a change starts a new one); an agent's brief names the person its
// dependency waits for; snapshots carry the person's name.
func TestPeopleRemindersAndBriefs(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if d, err := s.PeopleRemindAfter(ctx, co); err != nil || d != 72*time.Hour {
		t.Fatal(d, err)
	}
	if err := s.SetPeopleRemindAfter(ctx, co, time.Minute); err == nil {
		t.Fatal("1 minute accepted")
	}
	if err := s.SetPeopleRemindAfter(ctx, co, 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.PeopleRemindAfter(ctx, co); d != 48*time.Hour {
		t.Fatal(d)
	}
	if err := s.CreateUser(ctx, DefaultAccount, "ilya", "ilya.v@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ctx, co, "ilya", RoleOwner); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateTask(ctx, co, tasks.Create{ID: "menu", ProjectID: "p", Title: "Menu", Assignee: "member:ilya", Ready: true, RequestID: "menu"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateTask(ctx, co, tasks.Create{ID: "art", ProjectID: "p", Title: "Art", Assignee: "demo", Ready: true, Dependencies: []string{"menu"}, RequestID: "art"})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.TaskSnapshot(ctx, co, "art")
	if err != nil || len(snap.Dependencies) != 1 || snap.Dependencies[0].Person != "ilya.v" {
		t.Fatalf("%+v %v", snap.Dependencies, err)
	}
	if snap, _ = s.TaskSnapshot(ctx, co, "menu"); snap.Person != "ilya.v" {
		t.Fatalf("%+v", snap.Person)
	}
	for i, want := range []bool{true, false} {
		sent, err := s.RemindPerson(ctx, co, p, []tasks.Task{a}, 50*time.Hour)
		if err != nil || sent != want {
			t.Fatal(i, sent, err)
		}
	}
	p.UpdatedAt++ // the task changed: a new episode
	if sent, err := s.RemindPerson(ctx, co, p, []tasks.Task{a}, 50*time.Hour); err != nil || !sent {
		t.Fatal(sent, err)
	}
	var n int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM messages WHERE company_id=? AND recipient='owner' AND body->>'body' LIKE 'Reminder for ilya.v: your task menu%art (\"Art\", demo)%'", co).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	// An agent's task is never reminded about.
	if sent, _ := s.RemindPerson(ctx, co, a, []tasks.Task{p}, 50*time.Hour); sent {
		t.Fatal("reminded an agent")
	}

	// The brief names the person the dependency waits for.
	tx, err := s.begin(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	brief, err := briefTx(ctx, tx, a, protocol.RunSpec{ID: "r", Agent: "demo", Project: "p"}, 0, 0)
	if err != nil || !strings.Contains(brief, "Dependency menu [READY]: waits for menu, with ilya.v (a person of the company") {
		t.Fatal(err, brief)
	}
}
