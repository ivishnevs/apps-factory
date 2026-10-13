package node

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"appfactory/internal/configfile"
)

// ConfigEnv names node.yaml when no -config flag is given; without either the node runs on defaults.
const ConfigEnv = "FACTORY_NODE_CONFIG"

// Config is node.yaml (node-agent D1). Registration, resources and labels arrive with Core's node API.
type Config struct {
	Name  string `yaml:"name"`
	Slots int    `yaml:"slots"`
	// StateDir keeps pidfiles of running runtimes; it must not be shared by live nodes or workers.
	StateDir string `yaml:"state_dir"`
	// BinDir holds factory-mcp and is the base of the built Claude shim; empty is the executable's directory.
	BinDir  string  `yaml:"bin_dir"`
	Drivers Drivers `yaml:"drivers"`
	Disk    Disk    `yaml:"disk"`
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

// ClaudeConfig is the Go driver (internal/driver/claude); off by default while workers/claude serves Claude.
type ClaudeConfig struct {
	Enabled *bool  `yaml:"enabled"`
	Node    string `yaml:"node"`
	// Shim defaults to <bin_dir>/../workers/claude/dist/src/shim.js, else the repository-relative default.
	Shim        string `yaml:"shim"`
	Executable  string `yaml:"executable"`
	AllowAPIKey bool   `yaml:"allow_api_key"`
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
	return Config{Name: name, Slots: 2, StateDir: "data/factory-node", Disk: Disk{LowGiB: 20, CriticalGiB: 5}}
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
	return c, nil
}

var nodeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

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
	if c.StateDir == "" {
		errs = append(errs, errors.New("state_dir is required"))
	}
	if !c.Drivers.Codex.On() && !c.Drivers.Claude.On() {
		errs = append(errs, errors.New("drivers: enable codex or claude"))
	}
	if c.Disk.Path == "" {
		c.Disk.Path = c.StateDir
	}
	switch {
	case c.Disk.CriticalGiB < 0 || c.Disk.LowGiB < 0:
		errs = append(errs, errors.New("disk watermarks must not be negative"))
	case c.Disk.CriticalGiB > c.Disk.LowGiB:
		errs = append(errs, fmt.Errorf("disk.critical_watermark_gib %g must not exceed disk.low_watermark_gib %g", c.Disk.CriticalGiB, c.Disk.LowGiB))
	}
	return errors.Join(errs...)
}
