package node

import (
	"context"
	"log/slog"
	"time"
)

// Slot is one executor slot as the watchdog sees it. Beat is the last iteration of an idle slot's
// claim loop; a busy slot runs Run and is bounded by the run's own timeout instead.
type Slot struct {
	Index int
	Busy  bool
	Run   string
	Beat  time.Time
}

// Slots is the slot pool under watch.
type Slots interface {
	Status() []Slot
	// Restart replaces the loop of an idle slot.
	Restart(index int)
	// Cancel cancels a busy slot's run; the executor interrupts the runtime and reports the outcome.
	Cancel(run string)
}

// TrackedGroup is a runtime process group recorded for a run (the executor's StateDir pidfiles).
type TrackedGroup struct {
	Run   string
	Group int
}

// Groups is the process view of the node's runtimes.
type Groups interface {
	Tracked() ([]TrackedGroup, error)
	Alive(group int) bool
	// Reap kills the group if it is still the recorded one and forgets it.
	Reap(TrackedGroup) error
}

// Watchdog is the node's internal consistency check (node-agent D7, local part): every busy slot's
// runtime group is alive (else its run is cancelled), no tracked group lacks a busy slot (else it is
// reaped), and idle slot loops keep beating (else the slot is restarted).
type Watchdog struct {
	Slots    Slots
	Groups   Groups // nil skips process checks
	Stale    time.Duration
	Interval time.Duration
	Now      func() time.Time
	Log      *slog.Logger

	cancelled map[string]bool
}

const (
	DefaultWatchdogInterval = 30 * time.Second
	DefaultSlotStale        = 2 * time.Minute
)

func (w *Watchdog) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = DefaultWatchdogInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Check()
		}
	}
}

// Check runs one pass.
func (w *Watchdog) Check() {
	log := logger(w.Log)
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	stale := w.Stale
	if stale <= 0 {
		stale = DefaultSlotStale
	}
	// Groups first: a group recorded before the slot snapshot belongs to a run that was busy then or has ended.
	var tracked []TrackedGroup
	if w.Groups != nil {
		var err error
		if tracked, err = w.Groups.Tracked(); err != nil {
			log.Error("watchdog: tracked runtimes unreadable", "error", err)
		}
	}
	slots := w.Slots.Status()
	busy := map[string]bool{}
	for _, s := range slots {
		if s.Busy {
			busy[s.Run] = true
		} else if age := now().Sub(s.Beat); age > stale {
			log.Error("watchdog: slot loop stalled; restarting slot", "slot", s.Index, "since", age.Round(time.Second).String())
			w.Slots.Restart(s.Index)
		}
	}
	if w.cancelled == nil {
		w.cancelled = map[string]bool{}
	}
	for run := range w.cancelled {
		if !busy[run] {
			delete(w.cancelled, run)
		}
	}
	for _, g := range tracked {
		switch {
		case !busy[g.Run]:
			log.Error("watchdog: runtime without a slot; reaping", "run", g.Run, "group", g.Group)
			if err := w.Groups.Reap(g); err != nil {
				log.Error("watchdog: reap failed", "run", g.Run, "group", g.Group, "error", err)
			}
		case !w.cancelled[g.Run] && !w.Groups.Alive(g.Group):
			log.Error("watchdog: runtime process group gone; cancelling run", "run", g.Run, "group", g.Group)
			w.cancelled[g.Run] = true
			w.Slots.Cancel(g.Run)
		}
	}
}
