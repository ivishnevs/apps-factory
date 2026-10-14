package node

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"
)

// Systemd is a systemd service running `factory-node run` with Restart=always (node-agent D7, the launchd
// KeepAlive equivalent on Linux). Secrets (FACTORY_TOKEN, NATS_URL with its password) are never written into the
// unit: they come from EnvFile, an owner-only (0600) KEY=value file. Logs go to journald.
type Systemd struct {
	Description, Executable, Config, WorkingDir string
	// EnvFile is the absolute path of the secrets file; required.
	EnvFile string
	// System renders a system unit run as User (multi-user.target); otherwise a user unit (default.target).
	System bool
	User   string
	// Env holds non-secret variables (PATH, FACTORY_URL, CODEX_BIN, ...) written as Environment= lines.
	Env map[string]string
}

// SystemdSecrets are the variables Systemd never inlines; they belong in EnvFile.
var SystemdSecrets = []string{"FACTORY_TOKEN", "NATS_URL", "NATS_PASSWORD"}

var unit = template.Must(template.New("unit").Parse(`# factory-node {{if .System}}system{{else}}user{{end}} unit (node-agent D7). Secrets come from EnvironmentFile (chmod 600), never from this file.
[Unit]
Description={{.Description}}
{{- if .System}}
Wants=network-online.target
After=network-online.target
{{- end}}
# Never stop restarting; Core counts the node's epochs and raises node.restart_loop.
StartLimitIntervalSec=0

[Service]
Type=simple
{{- if .System}}
User={{.User}}
{{- end}}
WorkingDirectory={{.WorkingDir}}
EnvironmentFile={{.EnvFile}}
{{- range .Env}}
Environment={{.}}
{{- end}}
ExecStart={{.ExecStart}}
Restart=always
RestartSec=10
# SIGTERM goes to factory-node only: it interrupts its runs and waits for them; leftovers are killed after 30 s.
KillMode=mixed
TimeoutStopSec=30
StandardOutput=journal
StandardError=journal
SyslogIdentifier=factory-node

[Install]
WantedBy={{if .System}}multi-user.target{{else}}default.target{{end}}
`))

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Render writes the unit. Values are escaped for systemd (specifiers %, ExecStart variables $, quotes and
// backslashes); values with control characters, relative paths and secret variables in Env are refused.
func (s Systemd) Render(w io.Writer) error {
	var errs []error
	for _, p := range []struct{ name, v string }{{"executable", s.Executable}, {"working directory", s.WorkingDir}, {"environment file", s.EnvFile}} {
		if !filepath.IsAbs(p.v) {
			errs = append(errs, fmt.Errorf("systemd: %s %q must be an absolute path", p.name, p.v))
		}
	}
	if s.Config != "" && !filepath.IsAbs(s.Config) {
		errs = append(errs, fmt.Errorf("systemd: config %q must be an absolute path", s.Config))
	}
	if s.System && s.User == "" {
		errs = append(errs, errors.New("systemd: a system unit needs a user"))
	}
	check := func(name, v string) {
		if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			errs = append(errs, fmt.Errorf("systemd: %s contains a control character", name))
		}
	}
	for _, f := range []struct{ name, v string }{{"description", s.Description}, {"executable", s.Executable}, {"config", s.Config},
		{"working directory", s.WorkingDir}, {"environment file", s.EnvFile}, {"user", s.User}} {
		check(f.name, f.v)
	}
	if strings.ContainsAny(s.User, " \"'\\%") {
		errs = append(errs, fmt.Errorf("systemd: invalid user %q", s.User))
	}
	keys := slices.Sorted(maps.Keys(s.Env))
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		if !envKey.MatchString(k) {
			errs = append(errs, fmt.Errorf("systemd: invalid variable name %q", k))
		}
		if slices.Contains(SystemdSecrets, k) {
			errs = append(errs, fmt.Errorf("systemd: %s is a secret; put it in the environment file", k))
		}
		check(k, s.Env[k])
		env = append(env, quote(k+"="+s.Env[k], false))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	exec := quote(s.Executable, true) + " run"
	if s.Config != "" {
		exec += " -config " + quote(s.Config, true)
	}
	desc := s.Description
	if desc == "" {
		desc = "App Factory node agent"
	}
	return unit.Execute(w, map[string]any{"System": s.System, "User": s.User, "Description": specifiers(desc),
		"WorkingDir": specifiers(s.WorkingDir), "EnvFile": specifiers(s.EnvFile), "Env": env, "ExecStart": exec})
}

// specifiers escapes systemd specifier expansion (%h, %u, ...).
func specifiers(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// quote makes one double-quoted systemd word: backslashes and quotes escaped, specifiers escaped, and in
// ExecStart also $ (variable substitution).
func quote(s string, exec bool) string {
	r := []string{`\`, `\\`, `"`, `\"`, "%", "%%"}
	if exec {
		r = append(r, "$", "$$")
	}
	return `"` + strings.NewReplacer(r...).Replace(s) + `"`
}
