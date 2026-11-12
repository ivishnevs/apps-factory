// Package runtimes installs the agent runtimes a node package pins (node-packaging-updates A2–A3): Claude Code from
// claude-code/claude.lock (sha512, the SDK's npm platform package) and Codex from codex/codex.lock (sha256, the
// official standalone release binary). Each runtime is a file of the package directory, checked against its lock:
// a package update brings new locks and the node fetches the matching runtimes before it switches; a runtime the
// previous version already has at the same pin is reused, not downloaded again.
package runtimes

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Where a package keeps its runtimes and their locks.
const (
	ClaudeDir  = "claude-code"
	ClaudeLock = "claude.lock"
	ClaudeBin  = "claude"
	CodexDir   = "codex"
	CodexLock  = "codex.lock"
	CodexBin   = "codex"
	// pinFile records the lock line a runtime file was installed from (<dir>/.<binary>.pin).
	pinSuffix = ".pin"
)

// ErrNoPin: the package's lock has no build for this machine (or the package has no lock): the runtime stays off.
var ErrNoPin = errors.New("no pinned build for this machine")

// Platform is this machine as codex.lock names it: darwin-arm64, linux-amd64, ...
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Libc is this machine as claude.lock names it: glibc or musl on Linux, any elsewhere.
func Libc() string {
	if runtime.GOOS != "linux" {
		return "any"
	}
	if m, _ := filepath.Glob("/lib/ld-musl-*"); len(m) > 0 {
		return "musl"
	}
	return "glibc"
}

// Pin is one lock line: the download, its checksum (hex) and, for an archive with more than the binary, the entry.
type Pin struct {
	Key, URL, Sum, Entry string
}

// line is the pin as the .pin file records it.
func (p Pin) line() string { return strings.TrimSpace(p.URL + " " + p.Sum + " " + p.Entry) }

// ReadLock finds the line of key in a lock file: "key url checksum [entry]".
func ReadLock(path, key string) (Pin, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Pin{}, fmt.Errorf("%w: the package has no %s", ErrNoPin, filepath.Base(path))
	}
	if err != nil {
		return Pin{}, err
	}
	defer f.Close()
	for sc := bufio.NewScanner(f); sc.Scan(); {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Fields(line)
		if len(p) >= 3 && p[0] == key {
			pin := Pin{Key: p[0], URL: p[1], Sum: strings.ToLower(p[2])}
			if len(p) > 3 {
				pin.Entry = p[3]
			}
			return pin, nil
		}
	}
	return Pin{}, fmt.Errorf("%w: %s has no build for %s", ErrNoPin, filepath.Base(path), key)
}

// Installer fetches pinned runtimes; Client nil uses a client with a 10-minute timeout.
type Installer struct {
	Client *http.Client
}

func (in Installer) client() *http.Client {
	if in.Client != nil {
		return in.Client
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

// Claude installs Claude Code pinned for libc into home/claude-code/claude (sha512), reusing prev's when it has the
// same pin. It returns the installed version line.
func (in Installer) Claude(ctx context.Context, home, prev, libc string) (string, error) {
	dir := filepath.Join(home, ClaudeDir)
	pin, err := ReadLock(filepath.Join(dir, ClaudeLock), libc)
	if err != nil {
		return "", err
	}
	if pin.Entry == "" {
		pin.Entry = "package/claude"
	}
	if err = in.install(ctx, dir, ClaudeBin, pin, sha512.New, prevDir(prev, ClaudeDir)); err != nil {
		return "", err
	}
	return "Claude Code " + version(ctx, filepath.Join(dir, ClaudeBin)) + " in " + dir, nil
}

// Codex installs the standalone Codex pinned for platform into home/codex/codex (sha256), reusing prev's when it has
// the same pin.
func (in Installer) Codex(ctx context.Context, home, prev, platform string) (string, error) {
	dir := filepath.Join(home, CodexDir)
	pin, err := ReadLock(filepath.Join(dir, CodexLock), platform)
	if err != nil {
		return "", err
	}
	if err = in.install(ctx, dir, CodexBin, pin, sha256.New, prevDir(prev, CodexDir)); err != nil {
		return "", err
	}
	return version(ctx, filepath.Join(dir, CodexBin)) + " in " + dir, nil
}

func prevDir(prev, sub string) string {
	if prev == "" {
		return ""
	}
	return filepath.Join(prev, sub)
}

// Installed reports whether dir/binary was installed from the pin dir's lock names now.
func installed(dir, binary string, pin Pin) bool {
	b, err := os.ReadFile(filepath.Join(dir, "."+binary+pinSuffix))
	if err != nil || strings.TrimSpace(string(b)) != pin.line() {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, binary))
	return err == nil && info.Mode().IsRegular()
}

// install puts the pinned binary at dir/binary: kept when it is installed from the same pin, linked or copied from
// prev when prev has it, else downloaded, checked against the pin's checksum and extracted. The file is replaced
// atomically (running agents keep the old one).
func (in Installer) install(ctx context.Context, dir, binary string, pin Pin, sum func() hash.Hash, prev string) error {
	if installed(dir, binary, pin) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	next := filepath.Join(dir, binary+".next")
	defer os.Remove(next)
	if prev != "" && installed(prev, binary, pin) {
		_ = os.Remove(next)
		if err := os.Link(filepath.Join(prev, binary), next); err != nil {
			if err = copyFile(filepath.Join(prev, binary), next); err != nil {
				return err
			}
		}
	} else if err := in.download(ctx, dir, pin, sum, next); err != nil {
		return err
	}
	if err := os.Rename(next, filepath.Join(dir, binary)); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "."+binary+pinSuffix), []byte(pin.line()+"\n"), 0o644)
}

func (in Installer) download(ctx context.Context, dir string, pin Pin, sum func() hash.Hash, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pin.URL, nil)
	if err != nil {
		return err
	}
	resp, err := in.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", pin.URL, resp.Status)
	}
	tmp, err := os.CreateTemp(dir, "download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sum()
	if _, err = io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != pin.Sum {
		return fmt.Errorf("the download of %s does not match its checksum in the lock; nothing changed", pin.URL)
	}
	return ExtractEntry(tmp.Name(), pin.Entry, dest)
}

// ExtractEntry writes the regular file entry name of a tar.gz archive to dest (mode 0755); an empty name takes the
// archive's only regular file.
func ExtractEntry(archive, name, dest string) error {
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
			if name == "" {
				name = "a file"
			}
			return fmt.Errorf("%s not in the download", name)
		}
		if err != nil {
			return err
		}
		if hd.Typeflag != tar.TypeReg || name != "" && strings.TrimPrefix(hd.Name, "./") != name {
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

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// version is the binary's --version line ("" when it does not answer within 30 s).
func version(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "--version").Output()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line
}

// Binary is where the package at home keeps runtime binary name (ClaudeBin or CodexBin), "" when it has none.
func Binary(home, name string) string {
	dir := CodexDir
	if name == ClaudeBin {
		dir = ClaudeDir
	}
	p := filepath.Join(home, dir, name)
	if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
		return p
	}
	return ""
}
