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
// A transient failure (the probe killed by its timeout, no output, not started under load) is retried with a
// longer timeout and disables the driver only after probes.TransientFailures checks in a row, never while a
// run on the driver succeeded or was active since the last check (check, activity).
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
	// transients counts the consecutive checks that ended in a transient-only failure after their retries;
	// probes.TransientFailures of them disable the driver. evidence: a run on the driver succeeded or
	// produced runtime activity since the last check.
	transients int
	evidence   bool
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
	def := node.Defaults().Probes
	if p.Attempts <= 0 {
		p.Attempts, p.RetryBackoff = def.Attempts, def.RetryBackoff
	}
	if p.TransientFailures <= 0 {
		p.TransientFailures = def.TransientFailures
	}
	h := &driverHealth{drivers: drivers, probes: p, state: map[string]*driverState{}}
	for _, d := range drivers {
		h.state[string(d.Name())] = &driverState{}
	}
	return h
}

// probeResult is one attempt of a driver's checks.
type probeResult struct {
	version, versionErr string
	failed              []string
	transient           bool // failing, and every failure is transient (CheckResult.Transient, node.IsTransient)
}

func (r probeResult) failing() bool { return r.versionErr != "" || len(r.failed) > 0 }

// attempt runs the driver's checks once, with the per-command timeout under ctx.
func attempt(ctx context.Context, d driver.Driver, version bool) probeResult {
	var r probeResult
	transient := true
	if version {
		v, err := d.CheckVersion(ctx)
		r.version = v
		if err != nil {
			r.versionErr = err.Error()
			transient = node.IsTransient(err)
		}
	}
	for _, c := range d.Probe(ctx) {
		if c.Status == node.Fail {
			r.failed = append(r.failed, c.Check+": "+c.Detail)
			transient = transient && c.Transient
		}
	}
	r.transient = r.failing() && transient
	return r
}

// probe runs the driver's checks and retries a transient-only failure (runtime-drivers D4): up to
// probes.Attempts in all, probes.RetryBackoff apart (doubling), the command timeout doubled each time. A pass
// or a definitive failure ends it at once.
func (h *driverHealth) probe(ctx context.Context, d driver.Driver, version bool) probeResult {
	attempts := max(h.probes.Attempts, 1)
	var r probeResult
	for i := range attempts {
		if i > 0 {
			wait := h.probes.RetryBackoff << (i - 1)
			slog.Warn("driver probe failed transiently; retrying", "driver", d.Name(), "attempt", i+1, "of", attempts, "after", wait,
				"probe", strings.Join(r.failed, "; "), "version_error", r.versionErr)
			select {
			case <-ctx.Done():
				return r
			case <-time.After(wait):
			}
		}
		r = attempt(node.WithCommandTimeout(ctx, node.CommandTimeout<<i), d, version)
		if ctx.Err() != nil || !r.transient {
			return r
		}
	}
	return r
}

// check runs Probe on every driver, and the version check too when version is set; it reports whether any
// driver changed between usable and disabled, logging each change once. A check that still fails only
// transiently after its retries leaves the driver as it was, with a warning, unless it is the
// probes.TransientFailures-th such check in a row; a run on the driver that succeeded or produced runtime
// activity since the previous check is evidence of health, and such a failure then never disables it.
func (h *driverHealth) check(ctx context.Context, version bool) bool {
	flipped := false
	limit := max(h.probes.TransientFailures, 1)
	for _, d := range h.drivers {
		name := string(d.Name())
		r := h.probe(ctx, d, version)
		if ctx.Err() != nil {
			return flipped // a cancelled check says nothing about the driver
		}
		h.mu.Lock()
		s := h.state[name]
		was := s.ok()
		evidence := s.evidence
		s.evidence = false
		apply := true
		var warn string
		switch {
		case !r.transient:
			s.transients = 0
		case evidence:
			s.transients, apply = 0, false
			warn = "driver probe failed transiently; a run on it succeeded or was active since the last check, so it stays as it was"
		default:
			s.transients++
			if s.transients < limit {
				apply = false
				warn = "driver probe failed transiently; it stays as it was unless the failure repeats"
			}
		}
		if apply {
			if version {
				s.version, s.versionErr = r.version, r.versionErr
			}
			s.probeFail = strings.Join(r.failed, "; ")
		}
		transients := s.transients
		// The probe checks authentication: once the hold-off expired its result replaces a run's auth failure.
		holding := false
		if s.authFail != "" && apply {
			if t := h.clock(); !t.Before(s.authUntil) {
				s.authFail = ""
			} else if s.probeFail == "" && s.versionErr == "" && !s.held {
				s.held, holding = true, true
			}
		}
		now, verrNow, probeNow, vNow, until := s.ok(), s.versionErr, s.probeFail, s.version, s.authUntil
		h.mu.Unlock()
		if warn != "" {
			slog.Warn(warn, "driver", name, "consecutive", transients, "limit", limit, "probe", strings.Join(r.failed, "; "), "version_error", r.versionErr)
		}
		if holding {
			slog.Info("driver probe passes; it stays disabled until its auth hold-off ends", "driver", name, "until", until)
		}
		switch {
		case was && !now:
			slog.Error("driver disabled on this node", "driver", name, "version_error", verrNow, "probe", probeNow, "transient", r.transient)
		case !was && now:
			slog.Info("driver re-enabled on this node", "driver", name, "version", vNow)
		}
		flipped = flipped || was != now
	}
	return flipped
}

// activity records evidence that the named driver works: one of its runs produced a runtime event since
// the last check (runSucceeded counts too). The next check then does not disable it on a transient failure.
func (h *driverHealth) activity(name string) {
	h.mu.Lock()
	if s := h.state[name]; s != nil {
		s.evidence = true
	}
	h.mu.Unlock()
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
	if s != nil {
		s.evidence = true // health evidence for the next check
	}
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
