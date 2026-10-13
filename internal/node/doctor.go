package node

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const GiB = 1 << 30

// Doctor runs every self-check that does not need Core: git/forge, disk, stack (Xcode), factory binaries
// and runtime probes. Core/NATS reachability belongs to the node process.
type Doctor struct {
	Host     Host
	DiskPath string
	Low      uint64 // disk.low_watermark, bytes
	Critical uint64 // disk.critical_watermark, bytes
	Repo     string // optional repository for `git ls-remote`
	// Drivers are required by name; empty probes every runtime whose binary is installed.
	Drivers  []string
	Runtimes []Runtime
}

// Runtime is a driver probe as the doctor runs it; the driver owns the probe (runtime-drivers D4).
type Runtime struct {
	Name  string
	Probe DriverProbe
}

// DriverProbe is a RuntimeProbe that can tell whether its runtime is installed at all.
type DriverProbe interface {
	RuntimeProbe
	Installed() bool
}

func (d Doctor) Run(ctx context.Context) []CheckResult {
	r := d.git(ctx)
	r = append(r, d.disk())
	r = append(r, d.xcode(ctx)...)
	r = append(r, binary("factory-mcp", d.BinDir(), d.Host))
	usable := []string{}
	for _, rt := range d.Runtimes {
		explicit := slices.Contains(d.Drivers, rt.Name)
		if len(d.Drivers) > 0 && !explicit {
			continue
		}
		if !explicit && !rt.Probe.Installed() {
			r = append(r, CheckResult{Check: rt.Name, Status: Skip, Detail: "not installed; driver disabled"})
			continue
		}
		pr := rt.Probe.Probe(ctx)
		r = append(r, pr...)
		if !Failed(pr) {
			usable = append(usable, rt.Name)
		}
	}
	if len(usable) == 0 {
		r = append(r, CheckResult{Check: "drivers", Status: Fail, Detail: "no usable runtime driver",
			Hint: "fix the codex or claude checks above; a node without a usable driver pauses"})
	} else {
		r = append(r, CheckResult{Check: "drivers", Status: Pass, Detail: "usable: " + strings.Join(usable, ", ")})
	}
	return r
}

func (d Doctor) git(ctx context.Context) []CheckResult {
	v, err := d.Host.Output(ctx, "git", "--version")
	if err != nil {
		return []CheckResult{{Check: "git", Status: Fail, Detail: err.Error(), Hint: "install git (macOS: xcode-select --install)"}}
	}
	r := []CheckResult{{Check: "git", Status: Pass, Detail: v}}
	name, _ := d.Host.Output(ctx, "git", "config", "--get", "user.name")
	email, _ := d.Host.Output(ctx, "git", "config", "--get", "user.email")
	if name == "" || email == "" {
		r = append(r, CheckResult{Check: "git.user", Status: Fail, Detail: "user.name or user.email not set",
			Hint: `git config --global user.name "NAME" && git config --global user.email EMAIL`})
	} else {
		r = append(r, CheckResult{Check: "git.user", Status: Pass, Detail: name + " <" + email + ">"})
	}
	if _, err = d.Host.LookPath("gh"); err != nil {
		r = append(r, CheckResult{Check: "forge.auth", Status: Warn, Detail: "gh not found", Hint: "install GitHub CLI and run gh auth login"})
	} else if out, errOut, err := d.Host.Run(ctx, "gh", "auth", "status"); err != nil {
		r = append(r, CheckResult{Check: "forge.auth", Status: Warn, Detail: "gh not authenticated: " + FirstLine(errOut+out), Hint: "gh auth login"})
	} else {
		r = append(r, CheckResult{Check: "forge.auth", Status: Pass, Detail: ghAccount(out + errOut)})
	}
	if d.Repo != "" {
		if _, err = d.Host.Output(ctx, "git", "ls-remote", "--exit-code", d.Repo, "HEAD"); err != nil {
			r = append(r, CheckResult{Check: "forge.repo", Status: Fail, Detail: err.Error(), Hint: "check git credentials (credential helper, SSH key or gh auth setup-git) for " + d.Repo})
		} else {
			r = append(r, CheckResult{Check: "forge.repo", Status: Pass, Detail: d.Repo})
		}
	}
	return r
}

func ghAccount(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "✓✔ ")); strings.HasPrefix(l, "Logged in to") {
			return l
		}
	}
	return "authenticated"
}

func (d Doctor) disk() CheckResult {
	free, err := d.Host.FreeBytes(d.DiskPath)
	if err != nil {
		return CheckResult{Check: "disk", Status: Fail, Detail: err.Error(), Hint: "pass an existing workspace root with -disk-path"}
	}
	detail := fmt.Sprintf("%.1f GiB free at %s (low %s, critical %s)", float64(free)/GiB, d.DiskPath, gib(d.Low), gib(d.Critical))
	hint := "free space: remove closed worktrees, ~/Library/Developer/Xcode/DerivedData, `xcrun simctl delete unavailable`"
	switch {
	case free < d.Critical:
		return CheckResult{Check: "disk", Status: Fail, Detail: detail, Hint: hint + "; below critical the node pauses"}
	case free < d.Low:
		return CheckResult{Check: "disk", Status: Warn, Detail: detail, Hint: hint + "; below low the janitor prunes aggressively"}
	}
	return CheckResult{Check: "disk", Status: Pass, Detail: detail}
}

func gib(b uint64) string { return fmt.Sprintf("%g GiB", float64(b)/GiB) }

func (d Doctor) xcode(ctx context.Context) []CheckResult {
	if d.Host.GOOS != "darwin" {
		return []CheckResult{{Check: "xcode", Status: Skip, Detail: "not macOS"}}
	}
	dir, err := d.Host.Output(ctx, "xcode-select", "-p")
	if err != nil {
		return []CheckResult{{Check: "xcode", Status: Fail, Detail: err.Error(), Hint: "install Xcode, then sudo xcode-select -s /Applications/Xcode.app"}}
	}
	if strings.Contains(dir, "CommandLineTools") {
		return []CheckResult{{Check: "xcode", Status: Fail, Detail: "only Command Line Tools selected: " + dir, Hint: "sudo xcode-select -s /Applications/Xcode.app"}}
	}
	v, err := d.Host.Output(ctx, "xcodebuild", "-version")
	if err != nil {
		return []CheckResult{{Check: "xcode", Status: Fail, Detail: err.Error(), Hint: "sudo xcodebuild -license accept && sudo xcodebuild -runFirstLaunch"}}
	}
	r := []CheckResult{{Check: "xcode", Status: Pass, Detail: strings.ReplaceAll(v, "\n", ", ")}}
	out, err := d.Host.Output(ctx, "xcrun", "simctl", "list", "devices", "available", "-j")
	if err != nil {
		return append(r, CheckResult{Check: "xcode.simctl", Status: Fail, Detail: err.Error(), Hint: "sudo xcodebuild -runFirstLaunch; check `xcrun simctl list`"})
	}
	var list struct {
		Devices map[string][]json.RawMessage `json:"devices"`
	}
	if err = json.Unmarshal([]byte(out), &list); err != nil {
		return append(r, CheckResult{Check: "xcode.simctl", Status: Fail, Detail: "unreadable simctl output: " + err.Error()})
	}
	n, runtimes := 0, 0
	for _, devices := range list.Devices {
		if len(devices) > 0 {
			n += len(devices)
			runtimes++
		}
	}
	if n == 0 {
		return append(r, CheckResult{Check: "xcode.simctl", Status: Warn, Detail: "no available simulators", Hint: "xcodebuild -downloadPlatform iOS"})
	}
	return append(r, CheckResult{Check: "xcode.simctl", Status: Pass, Detail: fmt.Sprintf("%d available simulators in %d runtimes", n, runtimes)})
}

// BinDir is FACTORY_BIN_DIR, else the directory of this executable if it holds factory-mcp, else ./bin.
func (d Doctor) BinDir() string {
	if v := d.Host.Getenv("FACTORY_BIN_DIR"); v != "" {
		return v
	}
	if d.Host.Executable != "" {
		dir := filepath.Dir(d.Host.Executable)
		if exists(d.Host, filepath.Join(dir, "factory-mcp")) {
			return dir
		}
	}
	return "bin"
}

func exists(h Host, path string) bool {
	info, err := h.Stat(path)
	return err == nil && !info.IsDir()
}

func binary(name, dir string, h Host) CheckResult {
	path := filepath.Join(dir, name)
	if !exists(h, path) {
		return CheckResult{Check: name, Status: Fail, Detail: path + " missing", Hint: "make build (or set FACTORY_BIN_DIR)"}
	}
	return CheckResult{Check: name, Status: Pass, Detail: path}
}
