package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/configfile"
	"appfactory/internal/node/janitor"
)

// ConfigEnv names node.yaml when no -config flag is given; without either the node runs on defaults.
const ConfigEnv = "FACTORY_NODE_CONFIG"

// Config is node.yaml (node-agent D1).
type Config struct {
	Name  string `yaml:"name"`
	Slots int    `yaml:"slots"`
	// Company is the company whose runs the node executes (its NATS subjects factory.<company>...); empty is
	// "default". A protocol v2 node refuses to run when its node token belongs to another company.
	Company string `yaml:"company"`
	// ProtocolVersion 1 (default) claims per run with per-run heartbeats; 2 registers the node and
	// speaks only runtime protocol v2 (docs/runtime-protocol-v2.md).
	ProtocolVersion int `yaml:"protocol_version"`
	// Resources is the node's capacity per class (e.g. simulator: 2), sent in register; Core enforces it at claim.
	Resources map[string]int `yaml:"resources"`
	Labels    []string       `yaml:"labels"`
	// StateDir keeps pidfiles of running runtimes and the v2 identity (node_id, epoch); it must not be shared by live nodes or workers.
	StateDir string `yaml:"state_dir"`
	// BinDir holds factory-mcp and factory-shim and is the base of the built Claude shim; empty is the executable's directory.
	BinDir string `yaml:"bin_dir"`
	// SkillsRoot is the factory checkout whose company catalog's skills.yaml (configs/skills.yaml, or
	// configs/<company>/skills.yaml) and skill directories are materialized for runs (pinned by hash); empty is
	// <bin_dir>/.., else the working directory.
	SkillsRoot string  `yaml:"skills_root"`
	Drivers    Drivers `yaml:"drivers"`
	// Probes: how often the node re-checks its drivers (runtime-drivers D4).
	Probes Probes `yaml:"probes"`
	Disk   Disk   `yaml:"disk"`
	// Worktrees serves runs with workspace_mode worktree (task-worktrees); off by default.
	Worktrees Worktrees `yaml:"worktrees"`
	// Hooks names the company catalog's NodeHooks document Core delivers (empty: default). Its hooks feed the
	// janitor and its stack checks the self-checks and doctor.
	Hooks string `yaml:"hooks"`
	// Janitor: run-stop and task-closed hooks, worktree removal after seal, prune (node-agent D4). A hook list
	// set here overrides the NodeHooks document's.
	Janitor janitor.Config `yaml:"janitor"`
	// NATS is the outbound connection (node-agent D1, task 1.5); without url, NATS_URL, else nats://127.0.0.1:4222.
	NATS NATS `yaml:"nats"`
	// TokenFile holds this machine's node token (`factory nodes token create`, node-agent 1.1); FACTORY_NODE_TOKEN
	// overrides it, and without either the node uses the shared FACTORY_TOKEN (transition).
	TokenFile string `yaml:"token_file"`
	// RuntimeEnv adds environment variable names (NAME_* for a prefix) to the allowlist of what agent runtimes
	// inherit from the node (worker.RuntimeEnv); the node's and the owner's credentials never pass.
	RuntimeEnv []string `yaml:"runtime_env"`
	// SecretsFile is a 0600 KEY=VALUE file of this node's secrets (ReadSecrets), read at each run: the values of
	// the ${secret:NAME} references in the headers of a run's external MCP servers. They never leave the node.
	SecretsFile string `yaml:"secrets_file"`
	// ProjectsRoot is where the node creates the repositories of new projects and clones existing ones given
	// by URL (project-creation D3); empty is <state_dir>/projects. A leading ~/ is the user's home.
	ProjectsRoot string `yaml:"projects_root"`
}

// DefaultCompany is the company of a node whose node.yaml names none.
const DefaultCompany = "default"

// TokenEnv carries the node token in the environment (a 0600 systemd EnvironmentFile or launchd plist).
const TokenEnv = "FACTORY_NODE_TOKEN"

// Token is the credential the node presents to Core: FACTORY_NODE_TOKEN, else the first line of token_file,
// else the shared FACTORY_TOKEN. source names where it came from ("" when none is set).
func (c Config) Token(getenv func(string) string) (token, source string, err error) {
	if v := strings.TrimSpace(getenv(TokenEnv)); v != "" {
		return v, TokenEnv, nil
	}
	if c.TokenFile != "" {
		b, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return "", "", fmt.Errorf("token_file: %w", err)
		}
		line, _, _ := strings.Cut(string(b), "\n")
		if line = strings.TrimSpace(line); line == "" {
			return "", "", fmt.Errorf("token_file %s is empty", c.TokenFile)
		}
		return line, "token_file", nil
	}
	if v := getenv("FACTORY_TOKEN"); v != "" {
		return v, "FACTORY_TOKEN", nil
	}
	return "", "", nil
}

// NATS: url is nats://, tls://, ws:// or wss:// (a comma-separated list is allowed) without user info: the
// per-node user comes from creds_file or nkey_seed_file, so node.yaml holds no secret. The NATS_URL fallback
// may still carry user:password, as for the local worker.
type NATS struct {
	URL         string `yaml:"url"`
	bus.Options `yaml:",inline"`
}

// DefaultNATSURL is the local NATS of `make up`.
const DefaultNATSURL = "nats://127.0.0.1:4222"

// ResolveURL is url, else NATS_URL from getenv, else DefaultNATSURL.
func (n NATS) ResolveURL(getenv func(string) string) string {
	if n.URL != "" {
		return n.URL
	}
	if v := getenv("NATS_URL"); v != "" {
		return v
	}
	return DefaultNATSURL
}

func (n NATS) validate() error {
	if n.URL != "" {
		us, err := bus.ParseURLs(n.URL)
		if err != nil {
			return fmt.Errorf("nats.url: %w", err)
		}
		for _, u := range us {
			if u.User != nil {
				return fmt.Errorf("nats.url %s: no user info in node.yaml; use creds_file or nkey_seed_file", bus.RedactURL(u))
			}
		}
	}
	if err := n.Options.Validate(n.URL); err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	return nil
}

// Worktrees: Root defaults to <state_dir>/worktrees; ScanCommand (pre-push secret scan) to gitleaks.
type Worktrees struct {
	Enabled     bool     `yaml:"enabled"`
	Root        string   `yaml:"root"`
	ScanCommand []string `yaml:"scan_command"`
}

type Drivers struct {
	Codex  CodexConfig  `yaml:"codex"`
	Claude ClaudeConfig `yaml:"claude"`
}

type CodexConfig struct {
	Enabled *bool `yaml:"enabled"` // default true
	// Binary defaults to CODEX_BIN, else codex.
	Binary string `yaml:"binary"`
}

// ClaudeConfig is the Go driver (internal/driver/claude), the only way Claude runs; off by default: enable it on
// nodes logged in to Claude.
type ClaudeConfig struct {
	Enabled *bool  `yaml:"enabled"`
	Node    string `yaml:"node"`
	// Shim defaults to <bin_dir>/../workers/claude/dist/src/shim.js, else the repository-relative default.
	Shim        string `yaml:"shim"`
	Executable  string `yaml:"executable"`
	AllowAPIKey bool   `yaml:"allow_api_key"`
}

// Probes: Interval runs each driver's Probe (binary, version range, auth; no model call), VersionInterval
// its version check. A failing check disables that driver on this node until a later check passes. A
// transient failure (CheckResult.Transient: killed by its timeout, no output, could not start under load) is
// retried up to Attempts times in all, RetryBackoff apart (doubling) with the command timeout doubled each
// time; only TransientFailures consecutive checks that end transient disable the driver, and never while a
// run on the driver succeeded or produced runtime activity since the last check.
type Probes struct {
	Interval          time.Duration `yaml:"interval"`           // default 10m
	VersionInterval   time.Duration `yaml:"version_interval"`   // default 1h
	Attempts          int           `yaml:"attempts"`           // default 3
	RetryBackoff      time.Duration `yaml:"retry_backoff"`      // default 5s
	TransientFailures int           `yaml:"transient_failures"` // default 3
}

// Disk watermarks in GiB on Path (default state_dir): below low warn, below critical stop claiming.
type Disk struct {
	Path        string  `yaml:"path"`
	LowGiB      float64 `yaml:"low_watermark_gib"`
	CriticalGiB float64 `yaml:"critical_watermark_gib"`
}

func (c CodexConfig) On() bool  { return c.Enabled == nil || *c.Enabled }
func (c ClaudeConfig) On() bool { return c.Enabled != nil && *c.Enabled }

// Defaults is the configuration without node.yaml.
func Defaults() Config {
	name, _ := os.Hostname()
	if !nodeName.MatchString(name) {
		name = "node"
	}
	return Config{Name: name, Slots: 2, ProtocolVersion: 1, StateDir: "data/factory-node",
		Probes: Probes{Interval: 10 * time.Minute, VersionInterval: time.Hour, Attempts: 3, RetryBackoff: 5 * time.Second, TransientFailures: 3}, Disk: Disk{LowGiB: 20, CriticalGiB: 5}}
}

// ConfigPath is the flag value, else $FACTORY_NODE_CONFIG; empty means defaults.
func ConfigPath(flag string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv(ConfigEnv)
}

// LoadConfig reads node.yaml strictly over the defaults; an empty path is the defaults alone.
func LoadConfig(path string) (Config, error) {
	c := Defaults()
	if path != "" {
		if err := configfile.Read(path, &c); err != nil {
			return c, err
		}
	}
	if err := c.Validate(); err != nil {
		if path != "" {
			err = fmt.Errorf("%s: %w", path, err)
		}
		return c, err
	}
	if err := c.Absolute(); err != nil {
		return c, err
	}
	return c, nil
}

// Absolute resolves node.yaml's relative paths against the working directory the node starts in (as the
// docs' `state_dir: data/...` examples read when the node starts from the checkout), so nothing downstream
// sees a relative path (a conversation run's directory once its runtime runs elsewhere, filepath.Rel against
// a repository top level). A driver binary or executable is resolved only when it names a path (holds a
// separator); a bare name stays a PATH lookup.
func (c *Config) Absolute() error {
	abs := func(p *string) error {
		if *p == "" || filepath.IsAbs(*p) {
			return nil
		}
		a, err := filepath.Abs(*p)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", *p, err)
		}
		*p = a
		return nil
	}
	cmd := func(p *string) error {
		if !strings.ContainsRune(*p, filepath.Separator) {
			return nil
		}
		return abs(p)
	}
	if rest, ok := strings.CutPrefix(c.ProjectsRoot, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("projects_root: %w", err)
		}
		c.ProjectsRoot = filepath.Join(home, rest)
	}
	return errors.Join(abs(&c.StateDir), abs(&c.ProjectsRoot), abs(&c.BinDir), abs(&c.SkillsRoot), abs(&c.Disk.Path), abs(&c.Worktrees.Root),
		abs(&c.TokenFile), abs(&c.SecretsFile), abs(&c.Drivers.Claude.Shim), cmd(&c.Drivers.Claude.Executable), cmd(&c.Drivers.Codex.Binary),
		abs(&c.NATS.CredsFile), abs(&c.NATS.NkeySeedFile), abs(&c.NATS.TLS.CAFile), abs(&c.NATS.TLS.CertFile), abs(&c.NATS.TLS.KeyFile))
}

var (
	nodeName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	companyName  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$`) // as Core's company IDs
	resourceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)           // as agent spec.resources
)

const maxSlots = 64

// Validate fills derived defaults and rejects unusable values.
func (c *Config) Validate() error {
	var errs []error
	if !nodeName.MatchString(c.Name) {
		errs = append(errs, fmt.Errorf("name %q: letters, digits, '.', '_' or '-', at most 63", c.Name))
	}
	if c.Slots < 1 || c.Slots > maxSlots {
		errs = append(errs, fmt.Errorf("slots %d: must be between 1 and %d", c.Slots, maxSlots))
	}
	if c.Company == "" {
		c.Company = DefaultCompany
	} else if !companyName.MatchString(c.Company) {
		errs = append(errs, fmt.Errorf("company %q: letters, digits, '_' or '-', at most 96", c.Company))
	}
	if c.StateDir == "" {
		errs = append(errs, errors.New("state_dir is required"))
	}
	if c.ProtocolVersion != 1 && c.ProtocolVersion != 2 {
		errs = append(errs, fmt.Errorf("protocol_version %d: must be 1 or 2", c.ProtocolVersion))
	}
	for k, n := range c.Resources {
		if !resourceName.MatchString(k) || n < 0 {
			errs = append(errs, fmt.Errorf("resources.%s: lowercase name and a count of at least 0", k))
		}
	}
	for _, l := range c.Labels {
		if !nodeName.MatchString(l) {
			errs = append(errs, fmt.Errorf("label %q: letters, digits, '.', '_' or '-', at most 63", l))
		}
	}
	if c.Hooks != "" && !resourceName.MatchString(c.Hooks) {
		errs = append(errs, fmt.Errorf("hooks %q: lowercase NodeHooks document name", c.Hooks))
	}
	if !c.Drivers.Codex.On() && !c.Drivers.Claude.On() {
		errs = append(errs, errors.New("drivers: enable codex or claude"))
	}
	if c.Probes.Interval < time.Minute || c.Probes.VersionInterval < time.Minute {
		errs = append(errs, errors.New("probes.interval and probes.version_interval must be at least 1m"))
	}
	if c.Probes.Attempts < 1 || c.Probes.Attempts > 10 || c.Probes.TransientFailures < 1 || c.Probes.RetryBackoff < 0 {
		errs = append(errs, errors.New("probes.attempts must be 1-10, probes.transient_failures at least 1, probes.retry_backoff not negative"))
	}
	if c.Disk.Path == "" {
		c.Disk.Path = c.StateDir
	}
	if c.Worktrees.Enabled && c.Worktrees.Root == "" && c.StateDir != "" {
		c.Worktrees.Root = filepath.Join(c.StateDir, "worktrees")
	}
	if c.ProjectsRoot == "" && c.StateDir != "" {
		c.ProjectsRoot = filepath.Join(c.StateDir, "projects")
	}
	if len(c.Worktrees.ScanCommand) > 0 && c.Worktrees.ScanCommand[0] == "" {
		errs = append(errs, errors.New("worktrees.scan_command: empty executable"))
	}
	switch {
	case c.Disk.CriticalGiB < 0 || c.Disk.LowGiB < 0:
		errs = append(errs, errors.New("disk watermarks must not be negative"))
	case c.Disk.CriticalGiB > c.Disk.LowGiB:
		errs = append(errs, fmt.Errorf("disk.critical_watermark_gib %g must not exceed disk.low_watermark_gib %g", c.Disk.CriticalGiB, c.Disk.LowGiB))
	}
	errs = append(errs, c.Janitor.Validate(), c.NATS.validate())
	return errors.Join(errs...)
}
