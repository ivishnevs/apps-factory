// Package selfupdate is the node's side of version updates on disk (node-packaging-updates design C, tasks 2.2 and
// 3.4): the versioned layout install.sh creates, unpacking a verified package into it, switching `current`, and the
// health check after the switch with its rollback.
//
// Layout of an installed node (FACTORY_NODE_HOME, default ~/.app-factory):
//
//	versions/<v>/{VERSION,bin/,claude-code/,codex/}   one directory per installed version (package and its runtimes)
//	current -> versions/<v>                           the version the service runs
//	bin -> current/bin, claude-code -> current/claude-code, codex -> current/codex
//	update.json                                       the update in progress, done or rolled back
//
// The service (launchd, systemd) runs <home>/bin/factory-node, so a restart runs whatever current points at.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"appfactory/internal/protocol"
)

const (
	VersionsDir = "versions"
	CurrentLink = "current"
	StateFile   = "update.json"
)

// HealthWindow is how long a new version has to register and pass its checks before it is rolled back; MaxBoots is
// how often it may start within that window (a crash loop rolls back sooner).
var (
	HealthWindow = 2 * time.Minute
	MaxBoots     = 3
)

// Phases of update.json.
const (
	PhaseSwitched = "switched" // current points at To; its health check is pending
	PhaseDone     = "done"     // To passed its health check
	PhaseFailed   = "failed"   // To was rolled back (or never switched to); Reason says why
)

// State is update.json.
type State struct {
	Phase      string `json:"phase"`
	From       string `json:"from,omitempty"`
	To         string `json:"to"`
	Attempt    int64  `json:"attempt,omitempty"`
	SwitchedAt int64  `json:"switched_at,omitempty"`
	Boots      int    `json:"boots,omitempty"`
	Reason     string `json:"reason,omitempty"`
	FailedAt   int64  `json:"failed_at,omitempty"`
}

// Locate finds the package home of the executable exe: home and version when exe resolves to
// <home>/versions/<v>/bin/<name>; ok is false for any other place (a build from the checkout, a package installed
// before the versioned layout), where the node cannot switch versions.
func Locate(exe string) (home, version string, ok bool) {
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", "", false
	}
	bin := filepath.Dir(real)
	vdir := filepath.Dir(bin)
	versions := filepath.Dir(vdir)
	if filepath.Base(bin) != "bin" || filepath.Base(versions) != VersionsDir || !protocol.ValidNodeVersion(filepath.Base(vdir)) {
		return "", "", false
	}
	return filepath.Dir(versions), filepath.Base(vdir), true
}

// Dir is the directory of version v under home.
func Dir(home, v string) string { return filepath.Join(home, VersionsDir, v) }

// Current is the version current points at ("" when there is no current link).
func Current(home string) string {
	t, err := os.Readlink(filepath.Join(home, CurrentLink))
	if err != nil {
		return ""
	}
	return filepath.Base(t)
}

// Switch points current at versions/<v> atomically (a new link renamed over the old one).
func Switch(home, v string) error {
	if !protocol.ValidNodeVersion(v) {
		return fmt.Errorf("invalid version %q", v)
	}
	if info, err := os.Stat(filepath.Join(Dir(home, v), "bin", "factory-node")); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("version %s is not installed in %s", v, Dir(home, v))
	}
	tmp := filepath.Join(home, CurrentLink+".next")
	_ = os.Remove(tmp)
	if err := os.Symlink(filepath.Join(VersionsDir, v), tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(home, CurrentLink))
}

// Unpack extracts a package archive (already verified) into versions/<v>: regular files, directories and links to a
// file in the same directory (bin/factory-shim -> factory-node); anything else, a path leaving the directory, or a
// VERSION other than v is refused. A version directory left by an earlier attempt is replaced.
func Unpack(archive, home, v string) (string, error) {
	if !protocol.ValidNodeVersion(v) {
		return "", fmt.Errorf("invalid version %q", v)
	}
	if err := os.MkdirAll(filepath.Join(home, VersionsDir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Join(home, VersionsDir), "."+v+".partial-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err = untar(archive, tmp); err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(tmp, "VERSION"))
	if err != nil {
		return "", errors.New("the package has no VERSION")
	}
	if got := strings.TrimSpace(string(b)); got != v {
		return "", fmt.Errorf("the package is version %s, not %s", got, v)
	}
	if info, err := os.Stat(filepath.Join(tmp, "bin", "factory-node")); err != nil || !info.Mode().IsRegular() {
		return "", errors.New("the package has no bin/factory-node")
	}
	dir := Dir(home, v)
	if Current(home) == v {
		return "", fmt.Errorf("version %s is the one running", v)
	}
	if err = os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err = os.Rename(tmp, dir); err != nil {
		return "", err
	}
	return dir, nil
}

func untar(archive, dest string) error {
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
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(strings.TrimPrefix(hd.Name, "./"))
		if name == "." {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("the package has an entry outside its directory: %s", hd.Name)
		}
		path := filepath.Join(dest, name)
		switch hd.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(hd.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			if _, err = io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err = out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if strings.ContainsRune(hd.Linkname, filepath.Separator) || hd.Linkname == ".." || hd.Linkname == "." {
				return fmt.Errorf("the package links %s outside its directory (%s)", hd.Name, hd.Linkname)
			}
			if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err = os.Symlink(hd.Linkname, path); err != nil {
				return err
			}
		default:
			return fmt.Errorf("the package has an entry of an unexpected type: %s", hd.Name)
		}
	}
}

// Load reads update.json (a zero State when there is none).
func Load(home string) (State, error) {
	var s State
	b, err := os.ReadFile(filepath.Join(home, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// Save writes update.json atomically.
func Save(home string, s State) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := filepath.Join(home, StateFile+".next")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(home, StateFile))
}

// Begin switches current from version from to version to and records the pending health check; the caller then
// exits for the service to start the new version.
func Begin(home, from, to string, attempt int64, now time.Time) error {
	if err := Save(home, State{Phase: PhaseSwitched, From: from, To: to, Attempt: attempt, SwitchedAt: now.Unix()}); err != nil {
		return err
	}
	if err := Switch(home, to); err != nil {
		_ = Save(home, State{Phase: PhaseFailed, From: from, To: to, Attempt: attempt, Reason: "switching: " + err.Error(), FailedAt: now.Unix()})
		return err
	}
	return nil
}

// Boot is what Check found at a node's start.
type Boot struct {
	// Pending: this start runs a new version whose health check is pending until Deadline.
	Pending  bool
	Deadline time.Time
	// RolledBack: the new version failed its health check; current points at the previous version again and the
	// process must exit for the service to start it.
	RolledBack bool
	Reason     string
}

// Check is run first at every start of version v: a version switched to and not yet healthy counts the start; past
// MaxBoots starts or the HealthWindow it is rolled back.
func Check(home, v string, now time.Time) (Boot, error) {
	s, err := Load(home)
	if err != nil || s.Phase != PhaseSwitched {
		return Boot{}, err
	}
	if s.To != v {
		// Another version runs (installed by hand meanwhile): the switch is over without a verdict on To.
		s.Phase, s.Reason, s.FailedAt = PhaseFailed, "the node was started as version "+v+" instead", now.Unix()
		return Boot{}, Save(home, s)
	}
	s.Boots++
	deadline := time.Unix(s.SwitchedAt, 0).Add(HealthWindow)
	switch {
	case s.Boots > MaxBoots:
		return rollback(home, s, fmt.Sprintf("version %s stopped %d times before it was healthy", v, s.Boots-1), now)
	case !now.Before(deadline):
		return rollback(home, s, fmt.Sprintf("version %s did not register and pass its checks within %s", v, HealthWindow), now)
	}
	return Boot{Pending: true, Deadline: deadline}, Save(home, s)
}

// Rollback rolls the pending version back for reason (the health check failed in the running process).
func Rollback(home, reason string, now time.Time) (Boot, error) {
	s, err := Load(home)
	if err != nil {
		return Boot{}, err
	}
	if s.Phase != PhaseSwitched {
		return Boot{}, fmt.Errorf("no update to roll back (%s)", cmpOr(s.Phase, "none"))
	}
	return rollback(home, s, reason, now)
}

func rollback(home string, s State, reason string, now time.Time) (Boot, error) {
	if s.From == "" {
		return Boot{}, fmt.Errorf("%s, and there is no previous version to roll back to", reason)
	}
	if err := Switch(home, s.From); err != nil {
		return Boot{}, fmt.Errorf("%s; rolling back to %s failed: %w", reason, s.From, err)
	}
	s.Phase, s.Reason, s.FailedAt = PhaseFailed, reason+"; rolled back to "+s.From, now.Unix()
	if err := Save(home, s); err != nil {
		return Boot{}, err
	}
	_ = os.RemoveAll(Dir(home, s.To)) // the failed version; it is downloaded again if it is retried
	return Boot{RolledBack: true, Reason: s.Reason}, nil
}

// Confirm records that version v passed its health check and keeps only it and the previous version.
func Confirm(home, v string) error {
	s, err := Load(home)
	if err != nil || s.Phase != PhaseSwitched || s.To != v {
		return err
	}
	s.Phase, s.Reason = PhaseDone, ""
	if err = Save(home, s); err != nil {
		return err
	}
	return Prune(home, s.To, s.From)
}

// Prune removes the version directories other than keep (and partial unpacks).
func Prune(home string, keep ...string) error {
	entries, err := os.ReadDir(filepath.Join(home, VersionsDir))
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name == "" || containsString(keep, name) {
			continue
		}
		errs = append(errs, os.RemoveAll(filepath.Join(home, VersionsDir, name)))
	}
	return errors.Join(errs...)
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Status is the update report a node sends from update.json alone (no update in progress in this process): a
// rolled-back or never-switched version, else nothing.
func (s State) Status() protocol.NodeUpdateStatus {
	if s.Phase != PhaseFailed {
		return protocol.NodeUpdateStatus{}
	}
	return protocol.NodeUpdateStatus{State: protocol.UpdateFailed, Version: s.To, From: s.From, Reason: s.Reason, Attempt: s.Attempt, At: s.FailedAt}
}
