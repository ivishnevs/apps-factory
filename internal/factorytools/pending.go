package factorytools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"appfactory/internal/delivery"
)

// PendingExit is an exit queued while Core was unreachable, written beside the exit file (PendingExitFile) so the
// node can deliver it after the runtime and factory-mcp are gone (exit-redelivery): otherwise the exit dies with the
// adapter's process and Core ends a finished run as lost.
type PendingExit struct {
	Body map[string]any `json:"body"`
	// AckSeq is the X-Factory-Ack-Seq the adapter sent with it; nil when the adapter tracked none.
	AckSeq *int64 `json:"ack_seq,omitempty"`
}

// PendingExitFile is where the pending exit of the run whose exit file is exitFile is kept.
func PendingExitFile(exitFile string) string { return exitFile + ".pending" }

// writePending keeps body for the node; the exit file's directory is the run's own (0700 temp dir).
func (a *Adapter) writePending(body map[string]any) {
	if a.ExitFile == "" {
		return
	}
	p := PendingExit{Body: body}
	a.mu.Lock()
	if a.tracked {
		seq := a.ack
		p.AckSeq = &seq
	}
	a.mu.Unlock()
	b, err := json.Marshal(p)
	if err != nil {
		return
	}
	path := PendingExitFile(a.ExitFile)
	if os.WriteFile(path+".tmp", b, 0o600) == nil {
		_ = os.Rename(path+".tmp", path)
	}
}

func (a *Adapter) clearPending() {
	if a.ExitFile != "" {
		_ = os.Remove(PendingExitFile(a.ExitFile))
	}
}

// ReadPendingExit reads the pending exit of exitFile; ok is false when there is none.
func ReadPendingExit(exitFile string) (PendingExit, bool) {
	var p PendingExit
	b, err := os.ReadFile(PendingExitFile(exitFile))
	if err != nil || json.Unmarshal(b, &p) != nil || p.Body == nil {
		return p, false
	}
	return p, true
}

// DeliverPendingExit posts p to Core as the run (token) once. It returns the settled state, accepted or refused (a
// final refusal by delivery.Classify: 409 new input, 410 the run ended or was released, a permanent auth refusal),
// or "" when a later attempt can pass (unreachable, 5xx, a temporary refusal).
func DeliverPendingExit(ctx context.Context, client *http.Client, coreURL, token string, p PendingExit) string {
	b, err := json.Marshal(p.Body)
	if err != nil {
		return "refused"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(coreURL, "/")+"/agent/exit", bytes.NewReader(b))
	if err != nil {
		return "refused"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if p.AckSeq != nil {
		req.Header.Set("X-Factory-Ack-Seq", strconv.FormatInt(*p.AckSeq, 10))
	}
	res, err := client.Do(req)
	if err != nil {
		return ""
	}
	b, _ = io.ReadAll(io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	switch {
	case res.StatusCode == 200:
		return "accepted"
	case delivery.Classify(res.StatusCode, delivery.Reason(string(b))) == delivery.Retry:
		return "" // unreachable, busy, or a temporary refusal: deliver again (node-wake-recovery)
	}
	return "refused"
}

// SettleExitFile records state in the exit file, keeping its kind and exit commit.
func SettleExitFile(exitFile, state string) {
	var s ExitState
	if b, err := os.ReadFile(exitFile); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	s.State, s.At = state, time.Now().UTC()
	b, _ := json.Marshal(s)
	if os.WriteFile(exitFile+".tmp", b, 0o600) == nil {
		_ = os.Rename(exitFile+".tmp", exitFile)
	}
}
