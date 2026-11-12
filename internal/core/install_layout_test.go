package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeNodeBinary stands in for factory-node in a package: `runtimes update -home DIR` installs the two runtimes into
// DIR and logs its arguments; anything else just logs.
const fakeNodeBinary = `#!/bin/sh
echo "$*" >> "$FAKE_NODE_LOG"
if [ "$1" = runtimes ]; then
  while [ $# -gt 0 ]; do [ "$1" = -home ] && home=$2; shift; done
  mkdir -p "$home/claude-code" "$home/codex"
  printf '#!/bin/sh\necho 1\n' > "$home/claude-code/claude"
  printf '#!/bin/sh\necho 1\n' > "$home/codex/codex"
  chmod 755 "$home/claude-code/claude" "$home/codex/codex"
  echo "claude: ok"
fi
`

func fakePackage(t *testing.T, v string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	add(&tar.Header{Name: "VERSION", Mode: 0o644, Typeflag: tar.TypeReg}, v+"\n")
	add(&tar.Header{Name: "bin/factory-node", Mode: 0o755, Typeflag: tar.TypeReg}, fakeNodeBinary)
	add(&tar.Header{Name: "bin/factory-shim", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "factory-node"}, "")
	add(&tar.Header{Name: "bin/factory-mcp", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "factory-node"}, "")
	add(&tar.Header{Name: "codex/codex.lock", Mode: 0o644, Typeflag: tar.TypeReg}, "pins\n")
	add(&tar.Header{Name: "claude-code/claude.lock", Mode: 0o644, Typeflag: tar.TypeReg}, "pins\n")
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

// TestInstallScriptVersionedLayout (node-packaging-updates 3.3, 3.4) runs install.sh against a Core serving packages:
// with nothing but curl, tar and git it unpacks each release into versions/<v>, has the node fetch the runtimes its
// package pins (reusing the previous version's), points current, bin, claude-code and codex at it, and keeps only the
// previous version. An install from before the layout loses its own bin, claude-code and npm Codex, and its node.yaml
// drops the npm Codex binary for the pinned one.
func TestInstallScriptVersionedLayout(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("install.sh runs on macOS and Linux")
	}
	for _, tool := range []string{"sh", "curl", "tar", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	if os.Getuid() == 0 {
		t.Skip("install.sh refuses root on Linux")
	}
	var release, sum string
	var pkg []byte
	name := "factory-node-" + runtime.GOOS + "-" + map[string]string{"arm64": "arm64", "amd64": "amd64"}[runtime.GOARCH] + ".tar.gz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/install.sh":
			_ = installTemplate.Execute(w, map[string]string{"CoreURL": "http://" + r.Host})
		case "/downloads/node/" + name:
			_, _ = w.Write(pkg)
		case "/downloads/node/SHA256SUMS":
			_, _ = w.Write([]byte(sum + "  " + name + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	publish := func(v string) {
		release, pkg = v, fakePackage(t, v)
		h := sha256.Sum256(pkg)
		sum = hex.EncodeToString(h[:])
	}
	home := t.TempDir()
	nodeHome := filepath.Join(home, ".app-factory")
	log := filepath.Join(home, "node.log")
	// An install from before the versioned layout.
	for _, p := range []string{"bin/factory-node", "claude-code/claude", "codex/node_modules/.bin/codex", "state/company"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(nodeHome, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nodeHome, p), []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	npmCodex := filepath.Join(nodeHome, "codex", "node_modules", ".bin", "codex")
	yaml := "name: \"mac\"\ndrivers:\n  codex: {enabled: true, binary: \"" + npmCodex + "\"}\n  claude: {enabled: true}\n"
	if err := os.WriteFile(filepath.Join(nodeHome, "node.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	install := func() string {
		t.Helper()
		script, err := exec.Command("curl", "-fsSL", srv.URL+"/install.sh").Output()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-s", "--", "--no-service", "--no-browser")
		cmd.Stdin = bytes.NewReader(script)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "FACTORY_NODE_HOME=" + nodeHome, "FAKE_NODE_LOG=" + log}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install.sh: %v\n%s", err, out)
		}
		return string(out)
	}
	link := func(p string) string {
		l, _ := os.Readlink(filepath.Join(nodeHome, p))
		return l
	}

	publish("2026.11.12.1500")
	out := install()
	if strings.Contains(out, "npm") || link("current") != "versions/2026.11.12.1500" || link("bin") != "current/bin" || link("claude-code") != "current/claude-code" || link("codex") != "current/codex" {
		t.Fatalf("%s\ncurrent=%s bin=%s", out, link("current"), link("bin"))
	}
	if b, err := os.ReadFile(filepath.Join(nodeHome, "codex", "codex")); err != nil || !strings.Contains(string(b), "echo 1") {
		t.Fatal("the pinned Codex is not behind codex/codex", err)
	}
	if b, _ := os.ReadFile(filepath.Join(nodeHome, "node.yaml")); strings.Contains(string(b), "node_modules") || !strings.Contains(string(b), "codex: {enabled: true}") {
		t.Fatalf("node.yaml keeps the npm Codex:\n%s", b)
	}
	if l, _ := os.Readlink(filepath.Join(nodeHome, "versions", release, "bin", "factory-shim")); l != "factory-node" {
		t.Fatal("factory-shim link", l)
	}

	publish("2026.11.13.0900")
	install()
	publish("2026.11.14.0900")
	install()
	entries, _ := os.ReadDir(filepath.Join(nodeHome, "versions"))
	var kept []string
	for _, e := range entries {
		kept = append(kept, e.Name())
	}
	if strings.Join(kept, ",") != "2026.11.13.0900,2026.11.14.0900" || link("current") != "versions/2026.11.14.0900" {
		t.Fatal("versions kept:", kept, link("current"))
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "runtimes update -home "+filepath.Join(nodeHome, "versions", "2026.11.14.0900")+" -prev "+filepath.Join(nodeHome, "versions", "2026.11.13.0900")) {
		t.Fatalf("runtimes not fetched with the previous version to reuse:\n%s", calls)
	}
	// Reinstalling the same version keeps it current.
	install()
	if link("current") != "versions/2026.11.14.0900" {
		t.Fatal(link("current"))
	}
	if _, err := os.Stat(filepath.Join(nodeHome, "versions", "2026.11.14.0900", "codex", "codex")); err != nil {
		t.Fatal("reinstall lost the runtimes", err)
	}
}
