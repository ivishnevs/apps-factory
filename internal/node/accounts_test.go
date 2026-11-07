package node

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRuntimeAccounts (node-runtime-accounts 3.1, 3.2, 3.4): the first account is recorded, the same one passes, a
// different one fails (the runtime is disabled) until accepted; Codex's account comes from its login file.
func TestRuntimeAccounts(t *testing.T) {
	dir := t.TempDir()
	personal := Account{Runtime: "claude", Plan: "Claude Max", Hash: AccountHash("me@example.com", "")}
	work := Account{Runtime: "claude", Plan: "Team", Org: "Acme", Hash: AccountHash("me@acme.com", "Acme")}
	if r := CheckAccount(dir, personal); r.Status != Pass || !strings.Contains(r.Detail, "recorded") {
		t.Fatal(r)
	}
	if r := CheckAccount(dir, personal); r.Status != Pass {
		t.Fatal(r)
	}
	r := CheckAccount(dir, work)
	if r.Status != Fail || !strings.Contains(r.Detail, "Claude Max") || !strings.Contains(r.Detail, "Team of Acme") || !strings.Contains(r.Hint, "accounts accept claude") {
		t.Fatal(r)
	}
	if strings.Contains(r.Detail, "@") {
		t.Fatal("the email leaks:", r.Detail)
	}
	if err := AcceptAccount(dir, "claude"); err != nil {
		t.Fatal(err)
	}
	if r := CheckAccount(dir, work); r.Status != Pass {
		t.Fatal(r)
	}
	// Codex: account id hashed, plan from the token's claims.
	home := t.TempDir()
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_plan_type":"pro"}}`))
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"id_token":"x.`+claims+`.y","account_id":"acc-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := CodexAccount(home)
	if a.Plan != "pro" || a.Hash == "" || a.Hash != AccountHash("acc-1") {
		t.Fatalf("%+v", a)
	}
	if r := CheckAccount(dir, Account{Runtime: "codex"}); r.Status != Skip {
		t.Fatal("an unidentified account checked:", r)
	}
}
