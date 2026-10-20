package storage

import (
	"context"
	"errors"
	"testing"

	"appfactory/internal/tasks"
)

// TestForeignKeysCarryTheCompany (postgres-tenancy 2.1): every foreign key includes company_id, so the
// database refuses a row of company B that references a row of company A, and nothing of the write stays.
func TestForeignKeysCarryTheCompany(t *testing.T) {
	ctx := context.Background()
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/fk-p"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: "t", ProjectID: "p", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"task of acme in default's project":    "INSERT INTO tasks(id,project_id,status,assignee,priority,version,body,created_at,company_id) VALUES('x','p','BACKLOG','',0,1,'{}',0,'acme')",
		"dependency on default's task":         "INSERT INTO task_dependencies(task_id,depends_on,company_id) VALUES('t','t2','acme')",
		"run of acme for default's task":       "INSERT INTO runs(id,project,agent,workspace,spec,status,created_at,task_id,company_id) VALUES('r','p','demo','/tmp/x','{}','QUEUED',0,'t','acme')",
		"comment of acme on default's task":    "INSERT INTO task_comments(id,task_id,author,text,created_at,company_id) VALUES('c','t','owner','x',0,'acme')",
		"conversation in default's project":    "INSERT INTO conversations(id,project_id,body,company_id) VALUES('v','p','{}','acme')",
		"row of a company that does not exist": "INSERT INTO pauses(company_id,scope,target,created_at) VALUES('nobody','company','',0)",
	} {
		_, err := s.DB.Exec(q)
		if code := pgCode(err); code != "23503" { // foreign_key_violation
			t.Errorf("%s: %v (SQLSTATE %s)", name, err, code)
		}
		if !errors.Is(constraint(err), ErrConflict) {
			t.Errorf("%s: not a conflict: %v", name, err)
		}
	}
	// Through the Store: acme cannot see, so cannot reference, default's project; the failed mutation leaves
	// no task and no feed row.
	if _, err = s.CreateTask(ctx, "acme", tasks.Create{ID: "y", ProjectID: "p", Title: "y"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var n int
	if err = s.DB.QueryRow("SELECT (SELECT count(*) FROM tasks WHERE company_id='acme')+(SELECT count(*) FROM feed_events WHERE company_id='acme')").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

// TestUsersBelongToCompaniesOfTheirAccount (postgres-tenancy 2.1, D2): an account holds companies and users;
// a user is a member of several companies of its own account, never of another account's.
func TestUsersBelongToCompaniesOfTheirAccount(t *testing.T) {
	ctx := context.Background()
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.CreateAccount(ctx, "other", "Other"); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{DefaultAccount, "acme"}, {"other", "globex"}} {
		if err = s.CreateAccountCompany(ctx, c[0], c[1], c[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.CreateUser(ctx, DefaultAccount, "ann", "Ann@example.com"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateUser(ctx, "other", "ann2", "ann@example.com"); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate email", err)
	}
	if err = s.AddMember(ctx, DefaultCompany, "ann", RoleOwner); err != nil {
		t.Fatal(err)
	}
	if err = s.AddMember(ctx, "acme", "ann", RoleMember); err != nil {
		t.Fatal(err)
	}
	if err = s.AddMember(ctx, "globex", "ann", RoleMember); !errors.Is(err, ErrConflict) {
		t.Fatal("member of another account's company", err)
	}
	if err = s.AddMember(ctx, "acme", "nobody", RoleMember); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.AddMember(ctx, "acme", "ann", "admin"); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	ms, err := s.Memberships(ctx, "ann")
	if err != nil || len(ms) != 2 || ms[0] != (Membership{"acme", RoleMember}) || ms[1] != (Membership{DefaultCompany, RoleOwner}) {
		t.Fatal(ms, err)
	}
}
