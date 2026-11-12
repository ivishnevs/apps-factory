package protocol

import (
	"fmt"
	"regexp"
)

// Node updates (node-packaging-updates design C, tasks 2.2 and 3.4): Core offers a node the release it hands out in
// the poll and heartbeat answers; the node downloads and verifies it, runs its self-test, drains, switches and checks
// its health, rolling back on failure, and reports each step in its heartbeat.

// Node update policies of a company: auto updates a node that is behind at its next drain (the default, owner
// 2026-11-12); approve waits for an owner or admin to approve each node's update (Nodes page, Telegram).
const (
	NodeUpdateAuto    = "auto"
	NodeUpdateApprove = "approve"
)

// NodeUpdate is the update Core offers a node: the release's version, where its package for the node's platform is
// (a path on Core, or an absolute URL) and the package's sha256. Attempt names the approval it rides on (Core's time of
// the last approval or retry, 0 under auto): a node never tries a version and attempt again once it failed. Now: the
// owner chose "update now", so the drain does not wait for live runs (they are interrupted and queued again).
type NodeUpdate struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Attempt int64  `json:"attempt,omitempty"`
	Now     bool   `json:"now,omitempty"`
}

// States of a node's update as the node reports them.
const (
	UpdateDownloading = "downloading" // the package and the runtimes its locks name
	UpdateTesting     = "testing"     // the new version's self-test
	UpdateDraining    = "draining"    // no new claims; waiting for the live runs to end
	UpdateSwitching   = "switching"   // current points at the new version; the service restarts it
	UpdateFailed      = "failed"      // the update did not happen or was rolled back; Reason says why
)

// NodeUpdateStatus is the node's report of its update (register and heartbeat): the state, the version it updates to,
// the version it runs, why it failed, how many live runs a drain waits for, and the attempt. An empty State: no
// update in progress and none failed.
type NodeUpdateStatus struct {
	State   string `json:"state,omitempty"`
	Version string `json:"version,omitempty"`
	From    string `json:"from,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Runs    int    `json:"runs,omitempty"`
	Attempt int64  `json:"attempt,omitempty"`
	// At is when the state began (Unix seconds; Core's clock once Core recorded it).
	At int64 `json:"at,omitempty"`
}

var nodeVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)

// ValidNodeVersion reports whether v can name a node release (and its directory versions/<v> on the node).
func ValidNodeVersion(v string) bool { return nodeVersion.MatchString(v) && v != "." && v != ".." }

// Validate checks a reported status.
func (s NodeUpdateStatus) Validate() error {
	switch s.State {
	case "", UpdateDownloading, UpdateTesting, UpdateDraining, UpdateSwitching, UpdateFailed:
	default:
		return fmt.Errorf("update: unknown state %q", s.State)
	}
	if s.State != "" && !ValidNodeVersion(s.Version) {
		return fmt.Errorf("update: invalid version %q", s.Version)
	}
	if len(s.From) > 64 || len(s.Reason) > 2000 || s.Runs < 0 {
		return fmt.Errorf("update: from at most 64 characters, reason at most 2000, runs not negative")
	}
	return nil
}
