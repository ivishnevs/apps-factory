package node

import (
	"log/slog"
	"sync"
)

// DiskGate decides whether slots may claim from free space on Path (node-agent D4, local part):
// below Critical claiming pauses, below Low the node warns; each is logged once per crossing and
// cleared when space returns. Pruning by the janitor and Core events come later.
type DiskGate struct {
	Path          string
	Low, Critical uint64 // bytes
	FreeBytes     func(path string) (uint64, error)
	Log           *slog.Logger // nil is slog.Default()

	mu                sync.Mutex
	paused, low, fail bool
}

func (d *DiskGate) log() *slog.Logger { return logger(d.Log) }

func logger(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}

func NewDiskGate(h Host, c Disk) *DiskGate {
	return &DiskGate{Path: c.Path, Low: uint64(c.LowGiB * GiB), Critical: uint64(c.CriticalGiB * GiB), FreeBytes: h.FreeBytes}
}

// Claimable reports whether a slot may claim now. An unreadable disk keeps the previous decision.
func (d *DiskGate) Claimable() bool {
	free, err := d.FreeBytes(d.Path)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		if !d.fail {
			d.log().Error("disk free space unreadable", "path", d.Path, "error", err)
		}
		d.fail = true
		return !d.paused
	}
	d.fail = false
	gib := float64(free) / GiB
	switch critical := free < d.Critical; {
	case critical && !d.paused:
		d.log().Error("disk below critical watermark; claiming paused", "path", d.Path, "free_gib", gib, "critical_gib", float64(d.Critical)/GiB)
	case !critical && d.paused:
		d.log().Info("disk above critical watermark; claiming resumed", "path", d.Path, "free_gib", gib)
	}
	d.paused = free < d.Critical
	switch low := free < d.Low; {
	case low && !d.low:
		d.log().Warn("disk below low watermark", "path", d.Path, "free_gib", gib, "low_gib", float64(d.Low)/GiB)
	case !low && d.low:
		d.log().Info("disk above low watermark", "path", d.Path, "free_gib", gib)
	}
	d.low = free < d.Low
	return !d.paused
}

// Level classifies free bytes by the watermarks: ok, low or critical (the inventory heartbeat's health.disk).
func (d *DiskGate) Level(free uint64) string {
	switch {
	case free < d.Critical:
		return "critical"
	case free < d.Low:
		return "low"
	}
	return "ok"
}

// Paused reports the last decision.
func (d *DiskGate) Paused() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.paused
}
