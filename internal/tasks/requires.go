package tasks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Requirement is a tool a project's runs need on the node (node-readiness): a command on PATH and, when Version
// is set, at least that version as printed by `command VersionArgs` (default --version). Core offers a project's
// runs only to nodes whose probe found every requirement.
//
// In JSON a requirement is an object or the short form "godot 4.7.2" (command, then an optional minimum version).
type Requirement struct {
	Command     string   `json:"command"`
	Version     string   `json:"version,omitempty"`
	VersionArgs []string `json:"version_args,omitempty"`
}

// MaxRequirements bounds a project's requires list.
const MaxRequirements = 32

var (
	requirementCommand = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	requirementVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,3}$`)
	requirementArg     = regexp.MustCompile(`^[A-Za-z0-9=._+-]{1,32}$`)
)

func (r *Requirement) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		parsed, err := ParseRequirement(s)
		if err != nil {
			return err
		}
		*r = parsed
		return nil
	}
	type plain Requirement
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*r = Requirement(p)
	return nil
}

// ParseRequirement reads the short form "command [version]".
func ParseRequirement(s string) (Requirement, error) {
	f := strings.Fields(s)
	switch len(f) {
	case 1:
		return Requirement{Command: f[0]}, nil
	case 2:
		return Requirement{Command: f[0], Version: strings.TrimPrefix(f[1], "v")}, nil
	}
	return Requirement{}, fmt.Errorf("requirement %q: want \"command\" or \"command version\"", s)
}

// String is the short form.
func (r Requirement) String() string {
	if r.Version == "" {
		return r.Command
	}
	return r.Command + " " + r.Version
}

// Validate refuses what a node must not run: a command is a bare name looked up on PATH (no paths, no shell), a
// version is dotted numbers, version arguments are plain flags.
func (r Requirement) Validate() error {
	if !requirementCommand.MatchString(r.Command) {
		return fmt.Errorf("requirement command %q: a command name on PATH (letters, digits, . _ + -)", r.Command)
	}
	if r.Version != "" && !requirementVersion.MatchString(r.Version) {
		return fmt.Errorf("requirement %s: version %q must be dotted numbers such as 4.7.2", r.Command, r.Version)
	}
	if len(r.VersionArgs) > 4 {
		return fmt.Errorf("requirement %s: at most 4 version_args", r.Command)
	}
	for _, a := range r.VersionArgs {
		if !requirementArg.MatchString(a) {
			return fmt.Errorf("requirement %s: version argument %q: letters, digits, = . _ + -", r.Command, a)
		}
	}
	return nil
}

// ValidateRequirements checks a requires list: at most MaxRequirements, each valid, no command twice.
func ValidateRequirements(rs []Requirement) error {
	if len(rs) > MaxRequirements {
		return fmt.Errorf("requires: at most %d tools", MaxRequirements)
	}
	seen := map[string]bool{}
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			return err
		}
		if seen[r.Command] {
			return fmt.Errorf("requires: %s listed twice", r.Command)
		}
		seen[r.Command] = true
	}
	return nil
}
