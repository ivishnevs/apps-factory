package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Version is the node package's VERSION (node-packaging-updates C), set at build time by scripts/package-node.sh
// (-ldflags "-X appfactory/internal/node.Version=..."); "dev" for a build from the checkout.
var Version = "dev"

// PackageVersion is the version a node reports: the build's, else the VERSION file of the installed package next to
// bin_dir (install.sh unpacks VERSION and bin/ into one directory), else "dev".
func PackageVersion(binDir string) string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if binDir == "" {
		if exe, err := os.Executable(); err == nil {
			binDir = filepath.Dir(exe)
		}
	}
	if binDir != "" {
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(binDir), "VERSION")); err == nil {
			if v := strings.TrimSpace(string(b)); v != "" && len(v) <= 64 {
				return v
			}
		}
	}
	return "dev"
}

// DetectLabels finds what this machine can do (node-packaging-updates 3.6), reported next to the labels its owner
// sets so agents select on them without anyone setting them: the OS (macos, linux), the architecture (arm64,
// amd64), xcode (xcodebuild works: Xcode, not only its command line tools), ios-sim (an iOS simulator runtime is
// installed), headless (Linux without a display: tests run with a virtual one or none) and gpu (every Mac; on
// Linux an NVIDIA GPU or a DRI render node). The plain names match the labels agents already ask for
// (labels: [macos, xcode]).
func DetectLabels(ctx context.Context, h Host, goarch string) []string {
	ctx = WithCommandTimeout(ctx, 15*time.Second)
	var out []string
	ok := func(name string, args ...string) (string, bool) {
		if h.Run == nil {
			return "", false
		}
		s, err := h.Output(ctx, name, args...)
		return s, err == nil
	}
	found := func(name string) bool {
		if h.LookPath == nil {
			return false
		}
		_, err := h.LookPath(name)
		return err == nil
	}
	exists := func(p string) bool {
		if h.Stat == nil {
			return false
		}
		_, err := h.Stat(p)
		return err == nil
	}
	env := func(k string) string {
		if h.Getenv == nil {
			return ""
		}
		return h.Getenv(k)
	}
	switch h.GOOS {
	case "darwin":
		out = append(out, "macos")
	case "linux", "windows":
		out = append(out, h.GOOS)
	}
	if goarch == "arm64" || goarch == "amd64" {
		out = append(out, goarch)
	}
	switch h.GOOS {
	case "darwin":
		if _, xcode := ok("xcodebuild", "-version"); xcode {
			out = append(out, "xcode")
			if s, sims := ok("xcrun", "simctl", "list", "runtimes"); sims && strings.Contains(s, "iOS") {
				out = append(out, "ios-sim")
			}
		}
		out = append(out, "gpu") // Metal on every Mac
	case "linux":
		if env("DISPLAY") == "" && env("WAYLAND_DISPLAY") == "" {
			out = append(out, "headless")
		}
		gpu := exists("/dev/dri/renderD128")
		if !gpu && found("nvidia-smi") {
			s, ran := ok("nvidia-smi", "-L")
			gpu = ran && strings.TrimSpace(s) != ""
		}
		if gpu {
			out = append(out, "gpu")
		}
	}
	return out
}
