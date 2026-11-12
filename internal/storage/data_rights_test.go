package storage

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// seedCompany gives company a project, a task with a comment, a conversation, a run, a blob, a secret, an
// invitation, an owner token, a node enrollment and node keys, and a skill source: rows in many company tables.
func seedCompany(t *testing.T, s *Store, company, by string) (blob string, tokens []string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CreateProject(ctx, company, tasks.ProjectCreate{ID: company + "-p", Name: "P", Workspace: "/tmp/" + company}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, company, tasks.Create{ID: company + "-t", ProjectID: company + "-p", Title: "Build it"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommentTask(ctx, company, company+"-t", tasks.Comment{Text: "looks good", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateConversation(ctx, company, messaging.ConversationCreate{ProjectID: company + "-p", Agent: "lead", Title: "Plan", RequestID: company + "-conv"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO runs(id,project,agent,workspace,spec,status,created_at,task_id,company_id) VALUES(?,?,'dev','/tmp/x','{}','QUEUED',0,?,?)",
		company+"-r", company+"-p", company+"-t", company); err != nil {
		t.Fatal(err)
	}
	b, err := s.PutBlob(ctx, company, strings.NewReader("artifact of "+company), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetSecret(ctx, company, "API_TOKEN", SecretString, []byte("s3cret-of-"+company), "owner"); err != nil {
		t.Fatal(err)
	}
	_, invitation, err := s.CreateInvitation(ctx, company, RoleMember, by)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateOwnerToken(ctx, company, "cli", "owner")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := s.CreateNodeEnrollment(ctx, company, "mini")
	if err != nil {
		t.Fatal(err)
	}
	nodeKey(t, s, company, company+"-node", "active", s.Now().Unix())
	if _, err = s.DB.Exec("INSERT INTO skill_sources(scope,id,kind,created_at) VALUES(?,'mine','upload',0)", company); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO skill_index(scope,source_id,name,hash,indexed_at) VALUES(?,'mine','review','h1',0)", company); err != nil {
		t.Fatal(err)
	}
	return b.SHA256, []string{invitation, owner.Token, enrollment.Code}
}

// companyRows counts every company table's rows of company.
func companyRows(t *testing.T, s *Store, company string) map[string]int {
	t.Helper()
	ctx := context.Background()
	tables, err := companyTables(ctx, s.DB)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, tb := range tables {
		var n int
		if err = s.DB.QueryRow("SELECT count(*) FROM "+quoteIdent(tb)+" WHERE company_id=?", company).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			out[tb] = n
		}
	}
	for _, q := range []string{"SELECT count(*) FROM companies WHERE id=?", "SELECT count(*) FROM skill_sources WHERE scope=?", "SELECT count(*) FROM skill_index WHERE scope=?"} {
		var n int
		if err = s.DB.QueryRow(q, company).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			out[q] = n
		}
	}
	return out
}

func unzip(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		v, _ := io.ReadAll(r)
		r.Close()
		out[f.Name] = string(v)
	}
	return out
}

// TestCompanyExportAndDeletion (saas-release-readiness 4.2): an export holds every company table, the skills and
// the blobs, but no secret value, key, token or code; deleting the company leaves no row in any company table and
// no blob, and another company keeps all of its data; the default company is never deleted.
func TestCompanyExportAndDeletion(t *testing.T) {
	s, _ := clockStore(t)
	s.Keys = masterKeys(t, false)
	ctx := context.Background()
	for _, co := range []string{"acme", "keep"} {
		if err := s.CreateCompany(ctx, co, strings.ToUpper(co)); err != nil {
			t.Fatal(err)
		}
	}
	blob, secrets := seedCompany(t, s, "acme", "owner")
	keepBlob, _ := seedCompany(t, s, "keep", "owner")
	keep := companyRows(t, s, "keep")
	if len(keep) < 15 {
		t.Fatal("seed reached too few tables", keep)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := s.ExportCompany(ctx, "acme", zw, ""); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	files := unzip(t, buf.Bytes())
	var m ExportManifest
	if err := json.Unmarshal([]byte(files["manifest.json"]), &m); err != nil || m.Company != "acme" || m.Name != "ACME" || m.Blobs != 1 || m.Tables["tasks"] != 1 || m.Tables["company_secrets"] != 1 {
		t.Fatal(files["manifest.json"], err)
	}
	if files["blobs/"+blob] != "artifact of acme" {
		t.Fatal("blob not exported", len(files))
	}
	if !strings.Contains(files["tables/company_secrets.json"], `"API_TOKEN"`) || !strings.Contains(files["tables/tasks.json"], "Build it") || !strings.Contains(files["skills.json"], `"review"`) {
		t.Fatal(files["tables/company_secrets.json"], files["skills.json"])
	}
	for _, tb := range []string{"tasks", "runs", "invitations", "owner_tokens", "company_keys", "node_keys"} {
		if _, ok := files["tables/"+tb+".json"]; !ok {
			t.Fatal("table missing from the export", tb)
		}
	}
	for name, body := range files {
		for _, leak := range append([]string{"s3cret", `"ciphertext"`, `"wrapped"`, `"hash"`, `"token_hash"`, `"sealed"`, "keep"}, secrets...) {
			// A skill's hash is the hash of its content, not of a secret.
			if strings.Contains(body, leak) && (leak != `"hash"` || name != "skills.json") {
				t.Fatalf("%s holds %q", name, leak)
			}
		}
	}

	if err := s.DeleteCompany(ctx, DefaultCompany); !errors.Is(err, ErrInput) {
		t.Fatal("default company", err)
	}
	if err := s.DeleteCompany(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown company", err)
	}
	if err := s.DeleteCompany(ctx, "acme"); err != nil {
		t.Fatal(err)
	}
	if left := companyRows(t, s, "acme"); len(left) != 0 {
		t.Fatal("rows left", left)
	}
	if ok, _ := s.Blobs.Exists(ctx, "acme", blob); ok {
		t.Fatal("blob left")
	}
	if after := companyRows(t, s, "keep"); len(after) != len(keep) {
		t.Fatal("another company changed", keep, after)
	}
	if ok, _ := s.Blobs.Exists(ctx, "keep", keepBlob); !ok {
		t.Fatal("another company's blob deleted")
	}
}

// TestAccountExportAndDeletion (saas-release-readiness 4.2): a person's export holds their user, identities,
// memberships, subscription and the companies of their account; deleting the account is refused while a paid
// subscription renews, then removes the account's companies, the person's memberships elsewhere, identities,
// sessions, subscription and billing events; another person keeps their company and loses only the membership in
// the deleted one.
func TestAccountExportAndDeletion(t *testing.T) {
	s, _ := clockStore(t)
	s.Keys = masterKeys(t, false)
	ctx := context.Background()
	ann, err := s.SignIn(ctx, Identity{Provider: "github", Subject: "ann", Email: "ann@example.com", EmailVerified: true, Name: "Ann"}, NewCompany{ID: "ann-co", Name: "Ann Co"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.SignIn(ctx, Identity{Provider: "github", Subject: "bob", Email: "bob@example.com", EmailVerified: true, Name: "Bob"}, NewCompany{ID: "bob-co", Name: "Bob Co"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CreateUserCompany(ctx, ann.User.ID, NewCompany{ID: "ann-two", Name: "Ann Two"}); err != nil {
		t.Fatal(err)
	}
	// Ann is a member of Bob's company; Bob of Ann's.
	for _, m := range [][3]string{{bob.User.ID, "bob-co", ann.User.ID}, {ann.User.ID, "ann-co", bob.User.ID}} {
		if _, err = s.DB.Exec("INSERT INTO memberships(account_id,company_id,user_id,role,created_at) VALUES(?,?,?,'member',0)", m[0], m[1], m[2]); err != nil {
			t.Fatal(err)
		}
	}
	annBlob, _ := seedCompany(t, s, "ann-co", ann.User.ID)
	seedCompany(t, s, "bob-co", bob.User.ID)
	bobRows := companyRows(t, s, "bob-co")
	if _, _, err = s.CreateSession(ctx, ann.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.StartTrial(ctx, ann.User.Account); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE account_subscriptions SET status='active', provider='paddle', subscription_id='sub_1', customer_id='ctm_1' WHERE account_id=?", ann.User.Account); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO billing_events(provider,event_id,type,account_id,occurred_at,received_at,outcome) VALUES('paddle','evt_1','subscription.created',?,0,0,'applied')", ann.User.Account); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err = s.ExportAccount(ctx, ann.User.ID, zw); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	files := unzip(t, buf.Bytes())
	var acc AccountExport
	if err = json.Unmarshal([]byte(files["account.json"]), &acc); err != nil || acc.User.Email != "ann@example.com" || len(acc.Identities) != 1 ||
		len(acc.Memberships) != 3 || acc.Subscription == nil || acc.Subscription.SubscriptionID != "sub_1" || strings.Join(acc.Companies, ",") != "ann-co,ann-two" {
		t.Fatal(files["account.json"], err)
	}
	if files["companies/ann-co/blobs/"+annBlob] != "artifact of ann-co" || files["companies/ann-two/manifest.json"] == "" || files["companies/bob-co/manifest.json"] != "" {
		t.Fatal("companies in the export", len(files))
	}

	if _, err = s.DeleteAccount(ctx, ann.User.ID); !errors.Is(err, ErrSubscriptionActive) {
		t.Fatal("renewing subscription", err)
	}
	if _, err = s.DB.Exec("UPDATE account_subscriptions SET cancel_at=1 WHERE account_id=?", ann.User.Account); err != nil {
		t.Fatal(err)
	}
	out, err := s.DeleteAccount(ctx, ann.User.ID)
	if err != nil || strings.Join(out.Companies, ",") != "ann-co,ann-two" {
		t.Fatal(out, err)
	}
	for _, co := range []string{"ann-co", "ann-two"} {
		if left := companyRows(t, s, co); len(left) != 0 {
			t.Fatal(co, "rows left", left)
		}
	}
	for q, want := range map[string]int{
		"SELECT count(*) FROM users WHERE id=?":                         0,
		"SELECT count(*) FROM identities WHERE user_id=?":               0,
		"SELECT count(*) FROM sessions WHERE user_id=?":                 0,
		"SELECT count(*) FROM memberships WHERE user_id=?":              0,
		"SELECT count(*) FROM accounts WHERE id=?":                      0,
		"SELECT count(*) FROM account_subscriptions WHERE account_id=?": 0,
		"SELECT count(*) FROM billing_events WHERE account_id=?":        0,
	} {
		var n int
		if err = s.DB.QueryRow(q, ann.User.ID).Scan(&n); err != nil || n != want {
			t.Fatal(q, n, err)
		}
	}
	if ok, _ := s.Blobs.Exists(ctx, "ann-co", annBlob); ok {
		t.Fatal("blob left")
	}
	// Bob keeps his company whole and loses the membership in Ann's.
	if after := companyRows(t, s, "bob-co"); len(after) != len(bobRows) || after["memberships"] != 1 {
		t.Fatal(bobRows, after)
	}
	ms, err := s.Memberships(ctx, bob.User.ID)
	if err != nil || len(ms) != 1 || ms[0].Company != "bob-co" {
		t.Fatal(ms, err)
	}
	if _, err = s.DeleteAccount(ctx, ann.User.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted twice", err)
	}
}
