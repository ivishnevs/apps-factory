package codex

import (
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/node/runtimes"
)

// TestCodexLock (node-packaging-updates 3.3): codex.lock pins the supported Codex for every platform a node package is
// built for, from the official release of exactly SupportedVersion, with a sha256 and the archive's entry.
func TestCodexLock(t *testing.T) {
	version := strings.TrimPrefix(SupportedVersion, "codex-cli ")
	for _, p := range []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"} {
		pin, err := runtimes.ReadLock(filepath.Join(".", "codex.lock"), p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(pin.URL, "https://github.com/openai/codex/releases/download/rust-v"+version+"/") {
			t.Errorf("%s: %s is not the official release of %s", p, pin.URL, version)
		}
		if len(pin.Sum) != 64 || strings.Trim(pin.Sum, "0123456789abcdef") != "" {
			t.Errorf("%s: sha256 %q", p, pin.Sum)
		}
		if pin.Entry == "" || !strings.Contains(pin.URL, pin.Entry+".tar.gz") {
			t.Errorf("%s: entry %q does not match %s", p, pin.Entry, pin.URL)
		}
	}
}
