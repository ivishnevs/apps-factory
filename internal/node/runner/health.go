package runner

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/node"
)

// driverHealth keeps the node's view of its drivers (runtime-drivers D4): the version check runs at start and
// every VersionInterval, Probe (binary, version range, auth; never a model call) at start and every Interval.
// A failing check disables the driver on this node: its slots stop claiming its subject until a check passes.
type driverHealth struct {
	drivers []driver.Driver
	probes  node.Probes
	// changed is called after a check flipped at least one driver; in v2 the session re-registers.
	changed func()

	mu    sync.Mutex
	state map[string]*driverState
}

type driverState struct {
	version    string
	versionErr string // last version check failure, empty when passing
	probeFail  string // failing probe checks, empty when passing
}

func (s *driverState) ok() bool { return s.versionErr == "" && s.probeFail == "" }

func newDriverHealth(drivers []driver.Driver, p node.Probes) *driverHealth {
	h := &driverHealth{drivers: drivers, probes: p, state: map[string]*driverState{}}
	for _, d := range drivers {
		h.state[string(d.Name())] = &driverState{}
	}
	return h
}

// check runs Probe on every driver, and the version check too when version is set; it reports whether any
// driver changed between usable and disabled, logging each change once.
func (h *driverHealth) check(ctx context.Context, version bool) bool {
	flipped := false
	for _, d := range h.drivers {
		name := string(d.Name())
		var v, verr string
		if version {
			var err error
			if v, err = d.CheckVersion(ctx); err != nil {
				verr = err.Error()
			}
		}
		var failed []string
		for _, r := range d.Probe(ctx) {
			if r.Status == node.Fail {
				failed = append(failed, r.Check+": "+r.Detail)
			}
		}
		if ctx.Err() != nil {
			return flipped // a cancelled check says nothing about the driver
		}
		h.mu.Lock()
		s := h.state[name]
		was := s.ok()
		if version {
			s.version, s.versionErr = v, verr
		}
		s.probeFail = strings.Join(failed, "; ")
		now, verrNow, probeNow, vNow := s.ok(), s.versionErr, s.probeFail, s.version
		h.mu.Unlock()
		switch {
		case was && !now:
			slog.Error("driver disabled on this node", "driver", name, "version_error", verrNow, "probe", probeNow)
		case !was && now:
			slog.Info("driver re-enabled on this node", "driver", name, "version", vNow)
		}
		flipped = flipped || was != now
	}
	return flipped
}

// usable reports whether the named driver passed its last checks.
func (h *driverHealth) usable(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state[name]
	return s != nil && s.ok()
}

// anyUsable is false when no driver is usable: the whole node pauses.
func (h *driverHealth) anyUsable() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.state {
		if s.ok() {
			return true
		}
	}
	return false
}

// infos is the register body's drivers: the last version and whether the driver is usable.
func (h *driverHealth) infos() []DriverInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := make([]DriverInfo, 0, len(h.drivers))
	for _, d := range h.drivers {
		s := h.state[string(d.Name())]
		r = append(r, DriverInfo{Name: string(d.Name()), Version: s.version, OK: s.ok()})
	}
	return r
}

// run repeats the checks on their intervals until ctx ends.
func (h *driverHealth) run(ctx context.Context) {
	probe := time.NewTicker(h.probes.Interval)
	defer probe.Stop()
	version := time.NewTicker(h.probes.VersionInterval)
	defer version.Stop()
	for {
		var flipped bool
		select {
		case <-ctx.Done():
			return
		case <-probe.C:
			flipped = h.check(ctx, false)
		case <-version.C:
			flipped = h.check(ctx, true)
		}
		if flipped && h.changed != nil {
			h.changed()
		}
	}
}
