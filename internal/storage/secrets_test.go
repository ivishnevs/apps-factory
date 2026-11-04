package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"appfactory/internal/secrets"
)

func masterKeys(t *testing.T, next bool) *secrets.MasterKeys {
	t.Helper()
	var n []byte
	if next {
		n = bytes.Repeat([]byte{2}, secrets.KeySize)
	}
	k, err := secrets.NewMasterKeys(bytes.Repeat([]byte{1}, secrets.KeySize), n)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestSecretStore (core-secret-store 1.1): values round-trip encrypted (no plaintext row), versions grow, limits
// hold, a wrong master key is refused at start, a ciphertext moved to another company does not open, rotation
// keeps every value readable under the new key alone, and a deleted company's values are gone with its key.
func TestSecretStore(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.SetSecret(ctx, co, "API_TOKEN", SecretString, []byte("s3cret-value-1"), "owner"); !errors.Is(err, secrets.ErrNoMasterKey) {
		t.Fatal("set without a master key", err)
	}
	s.Keys = masterKeys(t, false)
	for _, bad := range []struct {
		name, kind string
		value      []byte
	}{{"API_TOKEN", SecretString, []byte("short")}, {"1bad", SecretString, []byte("long enough")}, {"API_TOKEN", "blob", []byte("long enough")},
		{"API_TOKEN", SecretFile, nil}, {"API_TOKEN", SecretFile, make([]byte, MaxSecretBytes+1)}, {"API_TOKEN", SecretString, []byte{0xff, 0xfe, 0, 1, 2, 3, 4, 5}}} {
		if _, err := s.SetSecret(ctx, co, bad.name, bad.kind, bad.value, "owner"); !errors.Is(err, ErrInput) {
			t.Errorf("%+v accepted: %v", bad, err)
		}
	}
	v1, err := s.SetSecret(ctx, co, "API_TOKEN", SecretString, []byte("s3cret-value-1"), "owner")
	if err != nil || v1.Version != 1 {
		t.Fatal(v1, err)
	}
	v2, err := s.SetSecret(ctx, co, "API_TOKEN", SecretString, []byte("s3cret-value-2"), "owner")
	if err != nil || v2.Version != 2 {
		t.Fatal(v2, err)
	}
	if _, err = s.SetSecret(ctx, co, "SIGNING_KEY", SecretFile, []byte("-----BEGIN KEY-----\nabc\n"), "owner"); err != nil {
		t.Fatal(err)
	}
	var plain int
	if err = s.DB.QueryRow("SELECT count(*) FROM company_secrets WHERE position('\\x7333637265'::bytea in ciphertext)>0").Scan(&plain); err != nil || plain != 0 {
		t.Fatal("plaintext in a row", plain, err)
	}
	vals, missing, err := s.SecretValues(ctx, co, []string{"API_TOKEN", "SIGNING_KEY", "NOT_SET"})
	if err != nil || string(vals["API_TOKEN"].Value) != "s3cret-value-2" || vals["SIGNING_KEY"].Kind != SecretFile || len(missing) != 1 || missing[0] != "NOT_SET" {
		t.Fatal(vals, missing, err)
	}
	list, _ := s.Secrets(ctx, co)
	if len(list) != 2 || list[0].Name != "API_TOKEN" || list[0].Version != 2 {
		t.Fatalf("%+v", list)
	}

	// A wrong master key is refused at start; none at all too, once a company has secrets.
	right := s.Keys
	s.Keys, _ = secrets.NewMasterKeys(bytes.Repeat([]byte{9}, secrets.KeySize), nil)
	if err = s.CheckMasterKey(ctx); err == nil {
		t.Fatal("a wrong master key passed the start check")
	}
	s.Keys = nil
	if err = s.CheckMasterKey(ctx); !errors.Is(err, secrets.ErrNoMasterKey) {
		t.Fatal(err)
	}
	s.Keys = right
	if err = s.CheckMasterKey(ctx); err != nil {
		t.Fatal(err)
	}

	// Another company's row moved here does not open.
	if err = s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetSecret(ctx, "acme", "API_TOKEN", SecretString, []byte("acme-secret-1"), "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE company_secrets SET ciphertext=(SELECT ciphertext FROM company_secrets WHERE company_id='acme' AND name='API_TOKEN'),version=1 WHERE company_id=? AND name='API_TOKEN'", co); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.SecretValues(ctx, co, []string{"API_TOKEN"}); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatal("a moved ciphertext opened:", err)
	}

	// Rotation: re-wrapped under the new key, readable with it alone.
	rot := masterKeys(t, true)
	s.Keys = rot
	if n, err := s.RotateMasterKey(ctx, rot); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	s.Keys, _ = secrets.NewMasterKeys(bytes.Repeat([]byte{2}, secrets.KeySize), nil)
	if vals, _, err = s.SecretValues(ctx, "acme", []string{"API_TOKEN"}); err != nil || string(vals["API_TOKEN"].Value) != "acme-secret-1" {
		t.Fatal("after rotation", err)
	}
	if err = s.CheckMasterKey(ctx); err != nil {
		t.Fatal(err)
	}

	// Deleting a secret; a deleted company takes its key and values with it.
	if err = s.DeleteSecret(ctx, co, "SIGNING_KEY", "owner"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSecret(ctx, co, "SIGNING_KEY", "owner"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("DELETE FROM feed_events WHERE company_id='acme'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("DELETE FROM companies WHERE id='acme'"); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = s.DB.QueryRow("SELECT (SELECT count(*) FROM company_keys WHERE company_id='acme')+(SELECT count(*) FROM company_secrets WHERE company_id='acme')").Scan(&left)
	if left != 0 {
		t.Fatal("a deleted company kept its key or secrets")
	}
	var feeds int
	_ = s.DB.QueryRow("SELECT count(*) FROM feed_events WHERE kind IN ('secret.set','secret.deleted') AND summary LIKE '%s3cret%'").Scan(&feeds)
	if feeds != 0 {
		t.Fatal("a value in the feed")
	}
}
