package node

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUnit(t *testing.T) {
	var b strings.Builder
	s := Systemd{Executable: "/opt/fac tory/bin/factory-node", Config: `/etc/node "$HOME" 100%.yaml`, WorkingDir: "/srv/work%dir",
		EnvFile: "/home/u/.config/factory-node/env", Env: map[string]string{"PATH": `/usr/bin:/a b\c`, "FACTORY_URL": "http://h:8080/%h"}}
	if err := s.Render(&b); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"Description=App Factory node agent\n",
		"StartLimitIntervalSec=0\n",
		"WorkingDirectory=/srv/work%%dir\n",
		"EnvironmentFile=/home/u/.config/factory-node/env\n",
		// Sorted, quoted, specifiers and backslashes escaped; $ is literal in Environment=.
		"Environment=\"FACTORY_URL=http://h:8080/%%h\"\nEnvironment=\"PATH=/usr/bin:/a b\\\\c\"\n",
		`ExecStart="/opt/fac tory/bin/factory-node" run -config "/etc/node \"$$HOME\" 100%%.yaml"` + "\n",
		"Restart=always\n", "RestartSec=10\n", "KillMode=mixed\n", "TimeoutStopSec=30\n",
		"StandardOutput=journal\n", "StandardError=journal\n",
		"WantedBy=default.target\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit lacks %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"User=", "network-online", "FACTORY_TOKEN"} {
		if strings.Contains(got, bad) {
			t.Errorf("user unit contains %q:\n%s", bad, got)
		}
	}

	b.Reset()
	s.System, s.User, s.Config, s.Description = true, "factory", "", "node 1"
	if err := s.Render(&b); err != nil {
		t.Fatal(err)
	}
	got = b.String()
	for _, want := range []string{"Description=node 1\n", "User=factory\n", "After=network-online.target\n", "WantedBy=multi-user.target\n",
		`ExecStart="/opt/fac tory/bin/factory-node" run` + "\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("system unit lacks %q:\n%s", want, got)
		}
	}
	verify(t, got)
}

func TestSystemdRefuses(t *testing.T) {
	ok := Systemd{Executable: "/bin/factory-node", WorkingDir: "/srv", EnvFile: "/etc/factory-node/env"}
	for name, mut := range map[string]func(*Systemd){
		"secret inlined":      func(s *Systemd) { s.Env = map[string]string{"FACTORY_TOKEN": "t"} },
		"nats url inlined":    func(s *Systemd) { s.Env = map[string]string{"NATS_URL": "nats://u:p@h"} },
		"newline in value":    func(s *Systemd) { s.Env = map[string]string{"PATH": "/bin\nExecStartPre=/bin/evil"} },
		"newline in path":     func(s *Systemd) { s.WorkingDir = "/srv\nUser=root" },
		"bad variable name":   func(s *Systemd) { s.Env = map[string]string{"A B": "x"} },
		"relative env file":   func(s *Systemd) { s.EnvFile = "env" },
		"relative config":     func(s *Systemd) { s.Config = "node.yaml" },
		"no env file":         func(s *Systemd) { s.EnvFile = "" },
		"system without user": func(s *Systemd) { s.System = true },
		"bad user":            func(s *Systemd) { s.System, s.User = true, "a b" },
	} {
		s := ok
		mut(&s)
		if err := s.Render(&strings.Builder{}); err == nil {
			t.Errorf("%s: rendered", name)
		}
	}
	if err := ok.Render(&strings.Builder{}); err != nil {
		t.Fatal(err)
	}
}

// verify runs systemd-analyze verify when it is installed (Linux); elsewhere the rendering tests stand alone.
func verify(t *testing.T, unit string) {
	sa, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Log("systemd-analyze not installed; skipping verify")
		return
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "fac tory", "factory-node")
	if err = os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	unit = strings.ReplaceAll(unit, "/opt/fac tory/bin/factory-node", exe)
	path := filepath.Join(dir, "factory-node.service")
	if err = os.WriteFile(path, []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(sa, "verify", path).CombinedOutput(); err != nil {
		t.Fatalf("systemd-analyze verify: %v\n%s\n%s", err, out, unit)
	}
}
