package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadSecrets (external-mcp-servers): KEY=VALUE lines with comments, export and quotes; a file its group or
// others can read is refused, and errors never carry a value.
func TestReadSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.env")
	text := "# App Store Tracker\nexport ASTR_API_KEY='fake-value-1'\n\nPLAIN = fake-value-2\nEQ=a=b\n"
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSecrets(p)
	if err != nil || got["ASTR_API_KEY"] != "fake-value-1" || got["PLAIN"] != "fake-value-2" || got["EQ"] != "a=b" || len(got) != 3 {
		t.Fatal(got, err)
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSecrets(p); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Fatalf("mode %o accepted: %v", mode, err)
		}
	}
	bad := filepath.Join(t.TempDir(), "bad.env")
	if err := os.WriteFile(bad, []byte("OK=1\nfake-value-3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecrets(bad); err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "fake-value-3") {
		t.Fatal(err)
	}
	c, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	c.SecretsFile = "rel/secrets.env"
	if err := c.Absolute(); err != nil || !filepath.IsAbs(c.SecretsFile) {
		t.Fatal(c.SecretsFile, err)
	}
}
