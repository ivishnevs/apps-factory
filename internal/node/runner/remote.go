package runner

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// Settings from the web, node side (node-packaging-updates design part B): the poll (and heartbeat) answer carries
// the version of the node's desired settings; a version the node has not applied is fetched from GET
// /nodes/{id}/config, layered over node.yaml (node.ApplyRemote), kept in remote-config.json and applied live: the
// pool resizes, a driver is turned on or off with only its own probe, the disk gate takes the new watermarks and the
// heartbeat carries the new capacity and the report.

// remoteConfig holds node.yaml's config, the settings from the web and what is in effect.
type remoteConfig struct {
	stateDir string
	local    node.Config
	// apply makes a new effective config take effect on the running node; nil before Run wires it.
	apply func(node.Config)

	mu     sync.Mutex
	file   node.RemoteFile
	eff    node.Config
	report protocol.NodeConfigReport

	fetching atomic.Bool
}

// newRemoteConfig reads remote-config.json and layers it over node.yaml's config local; a file it cannot read is
// ignored (node.yaml alone, version 0: Core's next answer sends the settings again).
func newRemoteConfig(local node.Config) *remoteConfig {
	r := &remoteConfig{stateDir: local.StateDir, local: local}
	f, err := node.LoadRemote(local.StateDir)
	if err != nil {
		slog.Error("settings from the web unreadable; running on node.yaml alone", "error", err)
		f = node.RemoteFile{}
	}
	r.file = f
	r.eff, r.report = node.ApplyRemote(local, f.Version, f.Config)
	if f.Version > 0 {
		slog.Info("settings from the web over node.yaml", "version", f.Version, "slots", r.eff.Slots, "conversation_slots", r.eff.ConversationSlots, "labels", r.eff.Labels)
	}
	return r
}

func (r *remoteConfig) version() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Version
}

func (r *remoteConfig) effective() node.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.eff
}

// reported is the heartbeat's config report.
func (r *remoteConfig) reported() *protocol.NodeConfigReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	rep := r.report
	return &rep
}

// set applies the settings of version over node.yaml, keeps them in remote-config.json and makes them take effect.
func (r *remoteConfig) set(version int64, s protocol.NodeSettings) {
	eff, rep := node.ApplyRemote(r.local, version, s)
	applied := protocol.NodeSettings{}
	all := node.LocalSettings(eff)
	for k, st := range rep.Settings {
		if st.State != protocol.SettingApplied {
			continue
		}
		switch k {
		case protocol.SettingSlots:
			applied.Slots = all.Slots
		case protocol.SettingConversationSlots:
			applied.ConversationSlots = all.ConversationSlots
		case protocol.SettingLabels:
			applied.Labels = all.Labels
		case protocol.SettingDisk:
			applied.Disk = all.Disk
		default: // drivers.<name>
			name := k[len(protocol.SettingDrivers)+1:]
			if applied.Drivers == nil {
				applied.Drivers = map[string]protocol.NodeDriverSettings{}
			}
			applied.Drivers[name] = all.Drivers[name]
		}
	}
	f := node.RemoteFile{Version: version, Config: s, Applied: applied, AppliedAt: time.Now().UTC()}
	if err := node.SaveRemote(r.stateDir, f); err != nil {
		slog.Error("keeping the settings from the web in remote-config.json", "error", err)
	}
	r.mu.Lock()
	r.file, r.eff, r.report = f, eff, rep
	r.mu.Unlock()
	var refused []string
	for _, k := range slices.Sorted(maps.Keys(rep.Settings)) {
		if st := rep.Settings[k]; st.State == protocol.SettingRefused {
			refused = append(refused, k+": "+st.Reason)
		}
	}
	slog.Info("settings from the web applied", "version", version, "slots", eff.Slots, "conversation_slots", eff.ConversationSlots, "labels", eff.Labels,
		"codex", eff.Drivers.Codex.On(), "claude", eff.Drivers.Claude.On(), "disk_low_gib", eff.Disk.LowGiB, "disk_critical_gib", eff.Disk.CriticalGiB, "refused", refused)
	if r.apply != nil {
		r.apply(eff)
	}
}

// fetchConfig fetches and applies the node's settings from the web when Core names a version the node has not
// applied (want > 0); one fetch at a time, a failed one is retried at the next answer.
func (s *session) fetchConfig(ctx context.Context, want int64) {
	if s.remote == nil || want <= 0 || want == s.remote.version() || !s.remote.fetching.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.remote.fetching.Store(false)
		var v protocol.NodeConfig
		if err := s.api.Do(ctx, "GET", "/nodes/"+s.inv.NodeID+"/config", nil, &v); err != nil {
			if ctx.Err() == nil {
				slog.Warn("fetching the settings from the web failed; retried at the next answer", "version", want, "error", err)
			}
			return
		}
		if v.Version <= 0 || v.Version == s.remote.version() {
			return
		}
		s.remote.set(v.Version, v.Config)
		s.driversChanged() // the heartbeat carries the new capacity and the report now
	}()
}

// capacity is what the node takes on now: its slots and labels, the owner's and the detected ones.
type capacity struct {
	slots, conversation int
	labels, detected    []string
}

func (s *session) capacity() capacity {
	s.capMu.Lock()
	defer s.capMu.Unlock()
	return s.cap
}

func (s *session) setCapacity(c capacity) {
	s.capMu.Lock()
	s.cap = c
	s.capMu.Unlock()
}

// union is a and the labels of b it lacks.
func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, l := range b {
		if !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}
