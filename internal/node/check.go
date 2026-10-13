// Package node holds the node-agent self-checks (node-agent D7). Checks use an injectable Host so
// `factory-node doctor` and the future node process run the same code, and tests never touch the machine.
package node

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"
)

type Status string

const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip"
)

// CheckResult is one self-check outcome. Check is the stable name used in `node.check_failed{check}`.
type CheckResult struct {
	Check  string `json:"check"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// RuntimeProbe checks a runtime driver: binary, supported version, authentication; never a model call.
type RuntimeProbe interface {
	Probe(ctx context.Context) []CheckResult
}

// Host is the machine as seen by checks.
type Host struct {
	GOOS       string
	Executable string // path of the running binary, used to find sibling factory binaries
	Getenv     func(string) string
	LookPath   func(string) (string, error)
	Stat       func(string) (fs.FileInfo, error)
	FreeBytes  func(path string) (uint64, error)
	// Run executes a command and returns stdout and stderr.
	Run func(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}

const commandTimeout = 30 * time.Second

func LocalHost(goos string) Host {
	exe, _ := os.Executable()
	return Host{GOOS: goos, Executable: exe, Getenv: os.Getenv, LookPath: exec.LookPath, Stat: os.Stat, FreeBytes: freeBytes,
		Run: func(ctx context.Context, name string, args ...string) (string, string, error) {
			ctx, cancel := context.WithTimeout(ctx, commandTimeout)
			defer cancel()
			var out, errb bytes.Buffer
			c := exec.CommandContext(ctx, name, args...)
			c.Stdin, c.Stdout, c.Stderr = nil, &out, &errb
			err := c.Run()
			return out.String(), errb.String(), err
		}}
}

func (h Host) env(k, d string) string {
	if v := h.Getenv(k); v != "" {
		return v
	}
	return d
}
func (h Host) output(ctx context.Context, name string, args ...string) (string, error) {
	out, errOut, err := h.Run(ctx, name, args...)
	if err != nil && strings.TrimSpace(errOut) != "" {
		err = fmt.Errorf("%w: %s", err, firstLine(errOut))
	}
	return strings.TrimSpace(out), err
}
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Failed reports whether any result failed.
func Failed(results []CheckResult) bool {
	for _, r := range results {
		if r.Status == Fail {
			return true
		}
	}
	return false
}

// Render prints a STATUS/CHECK/DETAIL table, hints under failing and warning rows, and a summary.
func Render(w io.Writer, results []CheckResult) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tCHECK\tDETAIL")
	count := map[Status]int{}
	for _, r := range results {
		count[r.Status]++
		fmt.Fprintf(tw, "%s\t%s\t%s\n", strings.ToUpper(string(r.Status)), r.Check, r.Detail)
		if r.Hint != "" && (r.Status == Fail || r.Status == Warn) {
			fmt.Fprintf(tw, "\t\t-> %s\n", r.Hint)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%d passed, %d warnings, %d failed, %d skipped\n", count[Pass], count[Warn], count[Fail], count[Skip])
	return err
}
