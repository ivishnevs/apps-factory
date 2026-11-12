package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"appfactory/internal/protocol"
)

// Settings from the web (node-packaging-updates design part B), node side. Core keeps the desired settings; the node
// applies what node.yaml lets it over node.yaml (which stays what the person wrote) and keeps them in
// <state_dir>/remote-config.json, read over node.yaml at the next start. node.yaml decides: `remote: off` keeps every
// setting local, `remote: {slots: false}` one of them, slots_max caps the slots Core may set, and policy.runtimes
// limits the drivers it may enable. Trust and machine settings (sandbox, policy, env, paths, logins) are never remote.

// Remote is node.yaml's remote: off (or false) for no setting from the web, or a map of setting → false to keep
// those local (slots, conversation_slots, labels, drivers, disk). Absent: every remote setting may come from Core.
type Remote struct {
	Off  bool
	Pins map[string]bool
}

func (r *Remote) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		switch n.Value {
		case "off", "false":
			r.Off = true
			return nil
		case "on", "true":
			return nil
		}
		return fmt.Errorf("remote: off, on or a map of settings to false, not %q", n.Value)
	}
	var m map[string]bool
	if err := n.Decode(&m); err != nil {
		return fmt.Errorf("remote: %w", err)
	}
	for k := range m {
		if !slices.Contains(protocol.SettingKeys, k) {
			return fmt.Errorf("remote.%s: not a remote setting (%v)", k, protocol.SettingKeys)
		}
	}
	r.Pins = m
	return nil
}

// Pinned reports whether node.yaml keeps setting key (slots, conversation_slots, labels, drivers, disk) local.
func (r Remote) Pinned(key string) bool {
	if r.Off {
		return true
	}
	allowed, set := r.Pins[key]
	return set && !allowed
}

// PinnedKeys are the settings node.yaml keeps local.
func (r Remote) PinnedKeys() []string {
	var out []string
	for _, k := range protocol.SettingKeys {
		if r.Pinned(k) {
			out = append(out, k)
		}
	}
	return out
}

// LocalSettings are node.yaml's own values of the remote settings (reported next to the desired ones).
func LocalSettings(c Config) protocol.NodeSettings {
	slots, conv := c.Slots, c.ConversationSlots
	labels := append([]string{}, c.Labels...)
	codex, claude := c.Drivers.Codex.On(), c.Drivers.Claude.On()
	low, critical, floor := c.Disk.LowGiB, c.Disk.CriticalGiB, c.Disk.ConversationFloorGiB
	return protocol.NodeSettings{Slots: &slots, ConversationSlots: &conv, Labels: &labels,
		Drivers: map[string]protocol.NodeDriverSettings{"codex": {Enabled: &codex}, "claude": {Enabled: &claude}},
		Disk:    &protocol.NodeDiskSettings{LowGiB: &low, CriticalGiB: &critical, ConversationFloorGiB: &floor}}
}

// ApplyRemote layers the desired settings of version over node.yaml's config local: each setting is applied or
// refused with the reason (pinned in node.yaml, out of range, not allowed by policy.runtimes, leaving no driver);
// slots above slots_max are capped. A refused setting keeps node.yaml's value.
func ApplyRemote(local Config, version int64, s protocol.NodeSettings) (Config, protocol.NodeConfigReport) {
	eff := local
	eff.Labels = append([]string(nil), local.Labels...)
	rep := protocol.NodeConfigReport{Version: version, Settings: map[string]protocol.NodeSettingState{}, Local: LocalSettings(local),
		Pinned: local.Remote.PinnedKeys(), SlotsMax: local.SlotsMax}
	applied := func(k, note string) {
		rep.Settings[k] = protocol.NodeSettingState{State: protocol.SettingApplied, Reason: note}
	}
	refuse := func(k, why string) {
		rep.Settings[k] = protocol.NodeSettingState{State: protocol.SettingRefused, Reason: why}
	}
	pinned := func(k, pin string) bool {
		if !local.Remote.Pinned(pin) {
			return false
		}
		if local.Remote.Off {
			refuse(k, "node.yaml keeps every setting local (remote: off)")
		} else {
			refuse(k, "node.yaml keeps it local (remote: {"+pin+": false})")
		}
		return true
	}
	if v := s.Slots; v != nil && !pinned(protocol.SettingSlots, protocol.SettingSlots) {
		switch {
		case *v < 1 || *v > protocol.MaxNodeSlots:
			refuse(protocol.SettingSlots, fmt.Sprintf("slots %d: must be between 1 and %d", *v, protocol.MaxNodeSlots))
		case local.SlotsMax > 0 && *v > local.SlotsMax:
			eff.Slots = local.SlotsMax
			applied(protocol.SettingSlots, fmt.Sprintf("capped at slots_max %d", local.SlotsMax))
		default:
			eff.Slots = *v
			applied(protocol.SettingSlots, "")
		}
	}
	if v := s.ConversationSlots; v != nil && !pinned(protocol.SettingConversationSlots, protocol.SettingConversationSlots) {
		if *v < 0 || *v > protocol.MaxNodeSlots {
			refuse(protocol.SettingConversationSlots, fmt.Sprintf("conversation_slots %d: must be between 0 and %d", *v, protocol.MaxNodeSlots))
		} else {
			eff.ConversationSlots = *v
			applied(protocol.SettingConversationSlots, "")
		}
	}
	if v := s.Labels; v != nil && !pinned(protocol.SettingLabels, protocol.SettingLabels) {
		if i := slices.IndexFunc(*v, func(l string) bool { return !protocol.ValidLabel(l) }); i >= 0 {
			refuse(protocol.SettingLabels, fmt.Sprintf("label %q: letters, digits, '.', '_' or '-', at most 63", (*v)[i]))
		} else {
			eff.Labels = append([]string{}, *v...)
			applied(protocol.SettingLabels, "")
		}
	}
	var driverKeys []string
	for _, name := range protocol.RemoteDrivers {
		d, ok := s.Drivers[name]
		if !ok || d.Enabled == nil {
			continue
		}
		k := protocol.SettingDrivers + "." + name
		if pinned(k, protocol.SettingDrivers) {
			continue
		}
		on := *d.Enabled
		if on && len(local.Policy.Runtimes) > 0 && !slices.Contains(local.Policy.Runtimes, name) {
			refuse(k, "not allowed on this machine (policy.runtimes)")
			continue
		}
		if name == "codex" {
			eff.Drivers.Codex.Enabled = &on
		} else {
			eff.Drivers.Claude.Enabled = &on
		}
		driverKeys = append(driverKeys, k)
		applied(k, "")
	}
	usable := func(name string, on bool) bool {
		return on && (len(local.Policy.Runtimes) == 0 || slices.Contains(local.Policy.Runtimes, name))
	}
	if len(driverKeys) > 0 && !usable("codex", eff.Drivers.Codex.On()) && !usable("claude", eff.Drivers.Claude.On()) {
		eff.Drivers = local.Drivers
		for _, k := range driverKeys {
			refuse(k, "would leave this node without a driver")
		}
	}
	if d := s.Disk; d != nil && (d.LowGiB != nil || d.CriticalGiB != nil || d.ConversationFloorGiB != nil) && !pinned(protocol.SettingDisk, protocol.SettingDisk) {
		disk := local.Disk
		for _, w := range []struct {
			from *float64
			to   *float64
		}{{d.LowGiB, &disk.LowGiB}, {d.CriticalGiB, &disk.CriticalGiB}, {d.ConversationFloorGiB, &disk.ConversationFloorGiB}} {
			if w.from != nil {
				*w.to = *w.from
			}
		}
		switch {
		case disk.LowGiB < 0 || disk.CriticalGiB < 0 || disk.ConversationFloorGiB < 0:
			refuse(protocol.SettingDisk, "disk watermarks must not be negative")
		case disk.CriticalGiB > disk.LowGiB:
			refuse(protocol.SettingDisk, fmt.Sprintf("critical %g GiB above low %g GiB", disk.CriticalGiB, disk.LowGiB))
		default:
			eff.Disk = disk
			applied(protocol.SettingDisk, "")
		}
	}
	return eff, rep
}

// RemoteFile is <state_dir>/remote-config.json: the desired settings of version as Core sent them and the values
// in effect after node.yaml (applied); the node applies config again over node.yaml at its next start.
type RemoteFile struct {
	Version   int64                 `json:"version"`
	Config    protocol.NodeSettings `json:"config"`
	Applied   protocol.NodeSettings `json:"applied"`
	AppliedAt time.Time             `json:"applied_at"`
}

// RemoteConfigPath is the layered settings file of a state directory.
func RemoteConfigPath(stateDir string) string { return filepath.Join(stateDir, "remote-config.json") }

// LoadRemote reads remote-config.json (version 0 and no settings when there is none).
func LoadRemote(stateDir string) (RemoteFile, error) {
	var f RemoteFile
	b, err := os.ReadFile(RemoteConfigPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err = json.Unmarshal(b, &f); err != nil {
		return RemoteFile{}, fmt.Errorf("%s: %w", RemoteConfigPath(stateDir), err)
	}
	return f, nil
}

// SaveRemote writes remote-config.json atomically.
func SaveRemote(stateDir string, f RemoteFile) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := RemoteConfigPath(stateDir) + ".tmp"
	if err = os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, RemoteConfigPath(stateDir))
}
