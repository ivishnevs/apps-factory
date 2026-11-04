package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"strings"
	"testing"

	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/redact"
	"appfactory/internal/secrets"
	"appfactory/internal/storage"
)

const (
	plantedToken = "planted-token-0123456789"
	plantedFile  = "-----BEGIN PLANTED KEY-----\nplanted-file-body-9876\n-----END PLANTED KEY-----\n"
)

// leaks scans every stored record (all tables as text, every run log chunk) for value and its base64; the
// ciphertext table is skipped, it holds no plaintext.
func leaks(t *testing.T, s *storage.Store, run string, values ...string) []string {
	t.Helper()
	ctx := context.Background()
	var found []string
	tables, err := s.DB.QueryContext(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename NOT IN ('company_secrets','company_keys')")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var n string
		_ = tables.Scan(&n)
		names = append(names, n)
	}
	tables.Close()
	var needles []string
	for _, v := range values {
		needles = append(needles, v, base64.StdEncoding.EncodeToString([]byte(v)))
	}
	for _, table := range names {
		rows, err := s.DB.QueryContext(ctx, `SELECT t::text FROM "`+table+`" t`)
		if err != nil {
			t.Fatal(table, err)
		}
		for rows.Next() {
			var row string
			_ = rows.Scan(&row)
			for _, n := range needles {
				if strings.Contains(row, n) {
					found = append(found, table)
				}
			}
		}
		rows.Close()
	}
	chunks, _ := s.RunLogs(ctx, storage.DefaultCompany, run)
	for _, c := range chunks {
		rc, _, err := s.RunLogChunk(ctx, storage.DefaultCompany, run, c.Seq)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		for _, n := range needles {
			if bytes.Contains(b, []byte(n)) {
				found = append(found, "run log")
			}
		}
	}
	return found
}

// TestAgentSecretsOnTheNode (core-secret-store 3.2, 3.3): spec.secrets reach the runtime as an env variable and a
// 0600 file whose path is in its variable; the file is gone after the run; a run that prints both has them
// redacted, and no stored record or log holds a value or its base64 (the scan finds one planted on purpose).
func TestAgentSecretsOnTheNode(t *testing.T) {
	stateDir := t.TempDir()
	var s *storage.Store
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "done", Activity: []fake.Activity{
		{Exec: []string{"sh", "-c", `echo "token $API_TOKEN"; echo "path $KEY_PATH"; cat "$KEY_PATH"; ls -l "$KEY_PATH"`}},
	}}}
	withSecrets := stored(func(spec *protocol.RunSpec) {
		spec.ConfigSnapshot = []byte(`{"spec":{"secrets":[{"name":"API_TOKEN","env":"API_TOKEN"},{"name":"SIGNING_KEY","env":"KEY_PATH","file":true}]}}`)
	})
	r, _ := runDriver(t, Worker{Driver: d, StateDir: stateDir}, false, withSecrets, withStore(func(store *storage.Store) {
		s = store
		store.Keys, _ = secrets.NewMasterKeys(bytes.Repeat([]byte{4}, secrets.KeySize), nil)
		ctx := context.Background()
		if _, err := store.SetSecret(ctx, storage.DefaultCompany, "API_TOKEN", storage.SecretString, []byte(plantedToken), "owner"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetSecret(ctx, storage.DefaultCompany, "SIGNING_KEY", storage.SecretFile, []byte(plantedFile), "owner"); err != nil {
			t.Fatal(err)
		}
	}))
	if r.Outcome != "succeeded" {
		t.Fatalf("%s %q", r.Outcome, r.Result)
	}
	l := d.Launches()[0]
	var keyPath string
	for _, kv := range l.Env {
		if v, ok := strings.CutPrefix(kv, "KEY_PATH="); ok {
			keyPath = v
		}
	}
	if !strings.HasPrefix(keyPath, SecretsDir(stateDir, "fake-run")) {
		t.Fatalf("key path %q", keyPath)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("the file secret outlived its run", err)
	}
	var log strings.Builder
	chunks, _ := s.RunLogs(context.Background(), storage.DefaultCompany, "fake-run")
	for _, c := range chunks {
		rc, _, _ := s.RunLogChunk(context.Background(), storage.DefaultCompany, "fake-run", c.Seq)
		b, _ := io.ReadAll(rc)
		rc.Close()
		log.Write(b)
	}
	if !strings.Contains(log.String(), "token "+redact.Marker) || !strings.Contains(log.String(), "-rw-------") {
		t.Fatal(log.String())
	}
	if found := leaks(t, s, "fake-run", plantedToken, "planted-file-body-9876"); len(found) > 0 {
		t.Fatal("secret values stored in", found)
	}
	// The scan finds a value that is stored on purpose.
	if _, err := s.DB.Exec("UPDATE runs SET result=$1 WHERE id='fake-run'", "oops "+plantedToken); err != nil {
		t.Fatal(err)
	}
	if found := leaks(t, s, "fake-run", plantedToken); len(found) == 0 {
		t.Fatal("the leak scan missed a planted value")
	}
}
