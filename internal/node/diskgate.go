package node

import (
	"log/slog"
	"sync"
)

// DiskGate decides whether slots may claim from free space on Path (node-agent D4, local part):
// below Critical work and review claims pause while conversation claims go on (disk-pause-conversations: an
// answer takes no disk), below Floor every claim pauses, below Low the node warns; each is logged once per
// crossing and cleared when space returns.
type DiskGate struct {
	Path          string
	Low, Critical uint64 // bytes
	// Floor (disk.conversation_floor_gib) is the free space conversations need; at most Critical.
	Floor     uint64
	FreeBytes func(path string) (uint64, error)
	Log       *slog.Logger // nil is slog.Default()

	mu                      sync.Mutex
	paused, full, low, fail bool
}

func (d *DiskGate) log() *slog.Logger { return logger(d.Log) }

func logger(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}

func NewDiskGate(h Host, c Disk) *DiskGate {
	return &DiskGate{Path: c.Path, Low: uint64(c.LowGiB * GiB), Critical: uint64(c.CriticalGiB * GiB), Floor: uint64(c.ConversationFloorGiB * GiB), FreeBytes: h.FreeBytes}
}

// floor is the conversation floor in effect: never above the critical watermark.
func (d *DiskGate) floor() uint64 { return min(d.Floor, d.Critical) }

// FloorBytes is the conversation floor in effect (health.disk_conversation_floor_bytes).
func (d *DiskGate) FloorBytes() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.floor()
}

// Set replaces the watermarks (settings from the web, node-packaging-updates B); the next check judges by them.
func (d *DiskGate) Set(c Disk) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Low, d.Critical, d.Floor = uint64(c.LowGiB*GiB), uint64(c.CriticalGiB*GiB), uint64(c.ConversationFloorGiB*GiB)
}

// Watermarks are the low and critical watermarks in bytes.
func (d *DiskGate) Watermarks() (low, critical uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Low, d.Critical
}

// Claimable reports whether a slot may claim a work or review run now. An unreadable disk keeps the previous
// decision.
func (d *DiskGate) Claimable() bool {
	d.check()
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.paused
}

// ConversationClaimable reports whether a slot may claim a conversation run now: free space is above the
// conversation floor (disk-pause-conversations). An unreadable disk keeps the previous decision.
func (d *DiskGate) ConversationClaimable() bool {
	d.check()
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.full
}

// check reads free space and updates the decisions, logging each crossing once.
func (d *DiskGate) check() {
	free, err := d.FreeBytes(d.Path)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		if !d.fail {
			d.log().Error("disk free space unreadable", "path", d.Path, "error", err)
		}
		d.fail = true
		return
	}
	d.fail = false
	gib := float64(free) / GiB
	switch critical := free < d.Critical; {
	case critical && !d.paused:
		d.log().Error("disk below critical watermark; work claims paused, conversations continue", "path", d.Path, "free_gib", gib, "critical_gib", float64(d.Critical)/GiB)
	case !critical && d.paused:
		d.log().Info("disk above critical watermark; claiming resumed", "path", d.Path, "free_gib", gib)
	}
	d.paused = free < d.Critical
	switch full := free < d.floor(); {
	case full && !d.full:
		d.log().Error("disk below the conversation floor; every claim paused", "path", d.Path, "free_gib", gib, "floor_gib", float64(d.floor())/GiB)
	case !full && d.full:
		d.log().Info("disk above the conversation floor; conversation claims resumed", "path", d.Path, "free_gib", gib)
	}
	d.full = free < d.floor()
	switch low := free < d.Low; {
	case low && !d.low:
		d.log().Warn("disk below low watermark", "path", d.Path, "free_gib", gib, "low_gib", float64(d.Low)/GiB)
	case !low && d.low:
		d.log().Info("disk above low watermark", "path", d.Path, "free_gib", gib)
	}
	d.low = free < d.Low
}

// Level classifies free bytes by the watermarks: ok, low or critical (the inventory heartbeat's health.disk).
func (d *DiskGate) Level(free uint64) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case free < d.Critical:
		return "critical"
	case free < d.Low:
		return "low"
	}
	return "ok"
}

// Paused reports the last decision for work and review runs.
func (d *DiskGate) Paused() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.paused
}
