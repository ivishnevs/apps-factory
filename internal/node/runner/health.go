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
// A run ended by the runtime's authentication failure disables it the same way (runtime-drivers 3.2); a
// passing probe re-enables it only once the auth hold-off expired (authHoldOffs: 10 min, then 30 min, then
// 2 h for each further failure), so a probe that passes while runs keep failing auth does not flap it. The
// hold-off steps back to the first after a run on the driver succeeds, or after the driver ran the cap
// period (2 h) since its last hold-off without an auth failure.
type driverHealth struct {
	drivers []driver.Driver
	probes  node.Probes
	// changed is called after a check flipped at least one driver; in v2 the next heartbeat declares it.
	changed func()
	// now is the clock of the auth hold-off; nil is time.Now.
	now func() time.Time

	mu    sync.Mutex
	state map[string]*driverState
}

type driverState struct {
	version    string
	versionErr string // last version check failure, empty when passing
	probeFail  string // failing probe checks, empty when passing
	authFail   string // a run's authentication failure, empty when none or its hold-off expired at a passing probe
	// strikes counts the auth failures of the current escalation (authHoldOffs); authUntil is the end of the
	// current hold-off: a passing probe before it keeps the driver disabled.
	strikes   int
	authUntil time.Time
	held      bool // the "probe passes, hold-off continues" line was logged for this hold-off
}

// authHoldOffs are the successive hold-offs after a run's authentication failure; the last is the cap and
// also the failure-free period after which the escalation starts over.
var authHoldOffs = []time.Duration{10 * time.Minute, 30 * time.Minute, 2 * time.Hour}

func (h *driverHealth) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func (s *driverState) ok() bool { return s.versionErr == "" && s.probeFail == "" && s.authFail == "" }

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
		// The probe checks authentication: once the hold-off expired its result replaces a run's auth failure.
		holding := false
		if s.authFail != "" {
			if t := h.clock(); !t.Before(s.authUntil) {
				s.authFail = ""
			} else if s.probeFail == "" && s.versionErr == "" && !s.held {
				s.held, holding = true, true
			}
		}
		now, verrNow, probeNow, vNow, until := s.ok(), s.versionErr, s.probeFail, s.version, s.authUntil
		h.mu.Unlock()
		if holding {
			slog.Info("driver probe passes; it stays disabled until its auth hold-off ends", "driver", name, "until", until)
		}
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

// authFailed disables the named driver after a run ended on the runtime's authentication failure, like a
// failing probe, for the next hold-off of authHoldOffs; the first probe that passes after it re-enables the
// driver. Failures while the driver is already disabled by one (runs that were in flight) do not escalate. It
// calls changed when the driver was usable.
func (h *driverHealth) authFailed(name string, err error) {
	h.mu.Lock()
	s := h.state[name]
	if s == nil {
		h.mu.Unlock()
		return
	}
	was := s.ok()
	escalated := s.authFail == ""
	var hold time.Duration
	if escalated {
		t := h.clock()
		last := authHoldOffs[len(authHoldOffs)-1]
		if s.strikes > 0 && t.Sub(s.authUntil) >= last {
			s.strikes = 0 // the cap period passed since the last hold-off without an auth failure
		}
		hold = authHoldOffs[min(s.strikes, len(authHoldOffs)-1)]
		s.strikes++
		s.authUntil, s.held = t.Add(hold), false
	}
	s.authFail = err.Error()
	until := s.authUntil
	h.mu.Unlock()
	if escalated {
		slog.Error("driver disabled on this node after a run's auth failure", "driver", name, "auth_error", err.Error(), "hold_off", hold, "until", until)
	}
	if was && h.changed != nil {
		h.changed()
	}
}

// runSucceeded resets the named driver's auth hold-off escalation after one of its runs completed, unless a
// run's auth failure currently disables it.
func (h *driverHealth) runSucceeded(name string) {
	h.mu.Lock()
	s := h.state[name]
	reset := s != nil && s.strikes > 0 && s.authFail == ""
	if reset {
		s.strikes = 0
	}
	h.mu.Unlock()
	if reset {
		slog.Info("driver auth hold-off reset after a successful run", "driver", name)
	}
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

// infos is the drivers of register and heartbeat: the last version, whether the driver is usable and, when
// not, the failing check (version, probe or auth) with its detail.
func (h *driverHealth) infos() []DriverInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := make([]DriverInfo, 0, len(h.drivers))
	for _, d := range h.drivers {
		s := h.state[string(d.Name())]
		i := DriverInfo{Name: string(d.Name()), Version: s.version, OK: s.ok()}
		switch {
		case s.versionErr != "":
			i.Check, i.Detail = "version", s.versionErr
		case s.probeFail != "":
			i.Check, i.Detail = "probe", s.probeFail
		case s.authFail != "":
			i.Check, i.Detail = "auth", s.authFail
		}
		r = append(r, i)
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
