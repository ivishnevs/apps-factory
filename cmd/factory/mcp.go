package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/ownermcp"
	"appfactory/internal/support"
	"appfactory/internal/worker"
)

// mcpCommand serves the owner MCP server on stdio (setup-assistant 1): `factory mcp` with FACTORY_URL and the owner's
// FACTORY_TOKEN, for the owner's own coding agent. FACTORY_COMPANY, when set, names the company (X-Factory-Company),
// as an operator token needs it for a company other than Core's own.
func mcpCommand(ctx context.Context, api worker.API, company string) error {
	api.Client = companyClient(company)
	s := &ownermcp.Server{Core: api, Local: ownermcp.Local{NodeDoctor: nodeDoctor, NodeLog: nodeLog, ReadFile: os.ReadFile}}
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

// companyClient is an HTTP client that names company on every request ("" names none).
func companyClient(company string) *http.Client {
	c := &http.Client{Timeout: 60 * time.Second}
	if company != "" {
		c.Transport = companyHeader{company, http.DefaultTransport}
	}
	return c
}

type companyHeader struct {
	company string
	next    http.RoundTripper
}

func (h companyHeader) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(core.CompanyHeader, h.company)
	return h.next.RoundTrip(r)
}

// nodeBinary is factory-node: FACTORY_NODE_BIN, else next to this executable, else on PATH.
func nodeBinary() string {
	if v := os.Getenv("FACTORY_NODE_BIN"); v != "" {
		return v
	}
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), "factory-node"); fileExists(p) {
			return p
		}
	}
	return "factory-node"
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// nodeDoctor runs `factory-node doctor` on this machine; a failing check is a result, not an error.
func nodeDoctor(ctx context.Context) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, nodeBinary(), "doctor").CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return string(out), false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("factory-node doctor: %w: %s (set FACTORY_NODE_BIN if factory-node is elsewhere)", err, strings.TrimSpace(string(out)))
	}
	return string(out), true, nil
}

// nodeLog is the tail of the node's log: FACTORY_NODE_LOG, else the launchd log on macOS, else the systemd journal.
func nodeLog(ctx context.Context, lines int) (string, error) {
	path := os.Getenv("FACTORY_NODE_LOG")
	if path == "" && runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, "Library", "Logs", "factory-node.log")
	}
	if path != "" {
		return tailFile(path, lines)
	}
	out, err := exec.CommandContext(ctx, "journalctl", "--user", "-u", "factory-node", "-n", strconv.Itoa(lines), "--no-pager").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("journalctl: %w: %s (set FACTORY_NODE_LOG to the node's log file)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// tailFile returns the last n lines of path.
func tailFile(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > 8<<20 {
		if _, err = f.Seek(info.Size()-8<<20, 0); err != nil {
			return "", err
		}
	}
	ring := make([]string, 0, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, sc.Text())
	}
	return strings.Join(ring, "\n"), sc.Err()
}

// companyDoctor is `factory doctor --company [--since 24h] [--json]` (setup-assistant 3.1).
func companyDoctor(ctx context.Context, api worker.API, args []string) error {
	usage := errors.New("usage: factory doctor --company [--since 24h] [--json]")
	if args[0] != "--company" {
		return usage
	}
	path, asJSON := "/doctor", false
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			asJSON = true
		case args[i] == "--since" && i+1 < len(args):
			path += "?since=" + url.QueryEscape(args[i+1])
			i++
		default:
			return usage
		}
	}
	var d support.CompanyDoctor
	if err := api.Do(ctx, "GET", path, nil, &d); err != nil {
		return err
	}
	if asJSON {
		return printJSON(d)
	}
	fmt.Print(d.Text())
	if !d.OK {
		return errors.New("problems found")
	}
	return nil
}
