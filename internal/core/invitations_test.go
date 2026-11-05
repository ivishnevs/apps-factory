package core

import (
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/auth/authtest"
	"appfactory/internal/storage"
)

// TestInvitations (team-invitations): an owner creates a link for a role; a person who signs up from it joins that
// company (no company of their own, no setup wizard); a signed-in person accepts a link on its page; a link is
// single-use, revocable and only owners manage links.
func TestInvitations(t *testing.T) {
	c, is := sessionCore(t)
	c.PublicURL = "https://app.example"
	h := c.Handler()
	ann := &browser{t: t, h: h, cookies: map[string]string{}}
	loc := ann.signIn(is, "google", authtest.Person{UserID: "g-ann", Email: "ann@example.com", EmailVerified: true, Name: "Ann Lee"})
	ann.company, _ = strings.CutSuffix(strings.TrimPrefix(loc, "/c/"), "/setup")

	type created struct {
		Invitation storage.Invitation `json:"invitation"`
		Token      string             `json:"token"`
		URL        string             `json:"url"`
	}
	invite := func(b *browser, role string) created {
		t.Helper()
		var out created
		w := b.do("POST", "/invitations", map[string]string{"role": role})
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Token == "" || out.URL != "https://app.example/invite/"+out.Token || out.Invitation.Role != role {
			t.Fatal(w.Code, w.Body.String())
		}
		return out
	}
	if w := ann.do("POST", "/invitations", map[string]string{"role": "owner"}); w.Code != 400 {
		t.Fatal("owner invitation", w.Code)
	}
	first := invite(ann, storage.RoleAdmin)
	var preview storage.InvitationPreview
	anon := &browser{t: t, h: h, cookies: map[string]string{}}
	if w := anon.do("GET", "/auth/invitations/"+first.Token, nil); json.Unmarshal(w.Body.Bytes(), &preview) != nil || !preview.Open ||
		preview.Company != ann.company || preview.CompanyName != "Ann Lee's company" || preview.Role != "admin" || preview.InvitedBy != "Ann Lee" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := anon.do("GET", "/auth/invitations/nope", nil); w.Code != 404 {
		t.Fatal("unknown link", w.Code)
	}

	// Bob signs up from the link: he joins Ann's company as admin and has no company of his own.
	bob := &browser{t: t, h: h, cookies: map[string]string{}}
	if loc = bob.signInFrom(is, "github", authtest.Person{UserID: "gh-bob", Email: "bob@example.com", EmailVerified: true, Name: "Bob"}, "/invite/"+first.Token); loc != "/c/"+ann.company {
		t.Fatal("redirect", loc)
	}
	var me Me
	if w := bob.do("GET", "/me", nil); json.Unmarshal(w.Body.Bytes(), &me) != nil || len(me.Companies) != 1 ||
		me.Companies[0] != (storage.Membership{Company: ann.company, Name: "Ann Lee's company", Role: storage.RoleAdmin}) || me.LastCompany != ann.company || me.User.WelcomedAt == 0 {
		t.Fatal(w.Body.String())
	}
	if w := anon.do("GET", "/auth/invitations/"+first.Token, nil); json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.Open {
		t.Fatal("used link still open", w.Body.String())
	}
	// Admins do not manage links.
	bob.company = ann.company
	if w := bob.do("POST", "/invitations", map[string]string{"role": "member"}); w.Code != 403 {
		t.Fatal("admin invites", w.Code)
	}
	if w := bob.do("GET", "/invitations", nil); w.Code != 403 {
		t.Fatal("admin lists links", w.Code)
	}
	var members []storage.Member
	if w := ann.do("GET", "/memberships", nil); json.Unmarshal(w.Body.Bytes(), &members) != nil || len(members) != 2 || members[1].Email != "bob@example.com" {
		t.Fatal(w.Body.String())
	}

	// Carl already has his own company; he accepts a member link on its page and works in both.
	second := invite(ann, storage.RoleMember)
	carl := &browser{t: t, h: h, cookies: map[string]string{}}
	loc = carl.signIn(is, "google", authtest.Person{UserID: "g-carl", Email: "carl@example.com", EmailVerified: true, Name: "Carl"})
	own, _ := strings.CutSuffix(strings.TrimPrefix(loc, "/c/"), "/setup")
	var joined storage.Membership
	if w := carl.do("POST", "/me/invitations/"+second.Token, nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &joined) != nil ||
		joined != (storage.Membership{Company: ann.company, Name: "Ann Lee's company", Role: storage.RoleMember}) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := carl.do("GET", "/me", nil); json.Unmarshal(w.Body.Bytes(), &me) != nil || len(me.Companies) != 2 || me.LastCompany != ann.company {
		t.Fatal(w.Body.String())
	}
	carl.company = own
	if w := carl.do("GET", "/tasks", nil); w.Code != 200 {
		t.Fatal("own company", w.Code)
	}
	// Accepting again changes nothing; someone else cannot use a used link.
	if w := carl.do("POST", "/me/invitations/"+second.Token, nil); w.Code != 200 {
		t.Fatal("again", w.Code, w.Body.String())
	}
	dave := &browser{t: t, h: h, cookies: map[string]string{}}
	loc = dave.signIn(is, "google", authtest.Person{UserID: "g-dave", Email: "dave@example.com", EmailVerified: true, Name: "Dave"})
	if w := dave.do("POST", "/me/invitations/"+second.Token, nil); w.Code != 409 {
		t.Fatal("used link", w.Code, w.Body.String())
	}

	// A revoked link is gone from the list and cannot be used; signing up from it gives the person their own company.
	third := invite(ann, storage.RoleMember)
	var open []storage.Invitation
	if w := ann.do("GET", "/invitations", nil); json.Unmarshal(w.Body.Bytes(), &open) != nil || len(open) != 1 || open[0].ID != third.Invitation.ID {
		t.Fatal(w.Body.String())
	}
	if w := ann.do("DELETE", "/invitations/"+third.Invitation.ID, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := ann.do("DELETE", "/invitations/"+third.Invitation.ID, nil); w.Code != 404 {
		t.Fatal("revoked twice", w.Code)
	}
	if w := ann.do("GET", "/invitations", nil); json.Unmarshal(w.Body.Bytes(), &open) != nil || len(open) != 0 {
		t.Fatal(w.Body.String())
	}
	if w := dave.do("POST", "/me/invitations/"+third.Token, nil); w.Code != 409 {
		t.Fatal("revoked link", w.Code)
	}
	eve := &browser{t: t, h: h, cookies: map[string]string{}}
	if loc = eve.signInFrom(is, "google", authtest.Person{UserID: "g-eve", Email: "eve@example.com", EmailVerified: true, Name: "Eve"}, "/invite/"+third.Token); !strings.HasPrefix(loc, "/c/eve-") || !strings.HasSuffix(loc, "/setup") {
		t.Fatal("revoked link at sign-up", loc)
	}
}
