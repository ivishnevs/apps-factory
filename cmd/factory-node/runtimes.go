package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"appfactory/internal/driver/codex"
	"appfactory/internal/node"
)

// `factory-node runtimes update [claude|codex]` (node-packaging-updates 2.4) installs or updates the runtimes to the
// versions this node supports, on request only: Claude Code from the package's claude-code/claude.lock (sha512
// checked), Codex with npm into the node's codex directory. Logins are untouched.
func runtimes(args []string) (int, error) {
	const use = "usage: factory-node runtimes update [claude|codex] [-config node.yaml]"
	if len(args) == 0 || args[0] != "update" {
		return 2, errors.New(use)
	}
	args = args[1:]
	which := []string{"claude", "codex"}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		if args[0] != "claude" && args[0] != "codex" {
			return 2, errors.New(use)
		}
		which, args = args[:1], args[1:]
	}
	fs := flag.NewFlagSet("runtimes update", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+")")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return 2, errors.New(use)
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	home := packageHome(c)
	failed := false
	for _, rt := range which {
		var msg string
		switch rt {
		case "claude":
			msg, err = updateClaudeCode(home, linuxLibc(), &http.Client{})
		case "codex":
			msg, err = updateCodex(home, c.Drivers.Codex.Binary)
		}
		if err != nil {
			failed = true
			fmt.Printf("%s: %v\n", rt, err)
			continue
		}
		fmt.Printf("%s: %s\n", rt, msg)
	}
	if failed {
		return 1, nil
	}
	fmt.Println("Restart the node to use them: they are checked again at start (factory-node doctor).")
	return 0, nil
}

// packageHome is the installed package's directory (the parent of bin).
func packageHome(c node.Config) string {
	binDir := c.BinDir
	if binDir == "" {
		if exe, err := os.Executable(); err == nil {
			binDir = filepath.Dir(exe)
		}
	}
	return filepath.Dir(binDir)
}

func linuxLibc() string {
	if runtime.GOOS != "linux" {
		return "any"
	}
	if m, _ := filepath.Glob("/lib/ld-musl-*"); len(m) > 0 {
		return "musl"
	}
	return "glibc"
}

// updateClaudeCode downloads the Claude Code build pinned for this platform and libc in home/claude-code/claude.lock,
// checks its sha512 and replaces home/claude-code/claude atomically (running agents keep the old file).
func updateClaudeCode(home, libc string, client *http.Client) (string, error) {
	dir := filepath.Join(home, "claude-code")
	f, err := os.Open(filepath.Join(dir, "claude.lock"))
	if err != nil {
		return "", fmt.Errorf("this node's package has no claude-code/claude.lock: reinstall the node with install.sh")
	}
	defer f.Close()
	var url, want string
	for sc := bufio.NewScanner(f); sc.Scan(); {
		if p := strings.Fields(sc.Text()); len(p) == 3 && p[0] == libc {
			url, want = p[1], p[2]
		}
	}
	if url == "" {
		return "", fmt.Errorf("claude.lock has no build for %s", libc)
	}
	client.Timeout = 10 * time.Minute
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(dir, "claude-*.tgz")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha512.New()
	if _, err = io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", errors.New("the download does not match its sha512 in claude.lock; nothing changed")
	}
	next := filepath.Join(dir, "claude.next")
	if err = extractEntry(tmp.Name(), "package/claude", next); err != nil {
		return "", err
	}
	if err = os.Rename(next, filepath.Join(dir, "claude")); err != nil {
		return "", err
	}
	out, _ := exec.Command(filepath.Join(dir, "claude"), "--version").Output()
	return "Claude Code " + strings.TrimSpace(string(out)) + " in " + dir, nil
}

// extractEntry writes the tar.gz entry name of archive to dest (mode 0755).
func extractEntry(archive, name, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s not in the download", name)
		}
		if err != nil {
			return err
		}
		if hd.Name != name {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err = io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}

// updateCodex installs the supported Codex next to the node with npm; a Codex installed elsewhere is the person's
// own and only gets the command to run.
func updateCodex(home, binary string) (string, error) {
	version := strings.TrimPrefix(codex.SupportedVersion, "codex-cli ")
	prefix := filepath.Join(home, "codex")
	if binary != "" && !strings.HasPrefix(node.ExpandHome(binary), prefix+string(filepath.Separator)) {
		return "", fmt.Errorf("Codex is your own install (%s): run npm install -g @openai/codex@%s", binary, version)
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return "", fmt.Errorf("npm is required for Codex: install Node.js, or install codex-cli %s yourself", version)
	}
	cmd := exec.Command("npm", "install", "--prefix", prefix, "--no-audit", "--no-fund", "--no-update-notifier", "--loglevel=error", "@openai/codex@"+version)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return "codex-cli " + version + " in " + prefix, nil
}
