package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"appfactory/internal/protocol"
)

// HooksFile is the copy of the last NodeHooks document Core delivered, in state_dir: `factory-node doctor`
// runs its stack checks without Core.
const HooksFile = "node-hooks.json"

// SaveHooks keeps the delivered NodeHooks document in stateDir.
func SaveHooks(stateDir string, d protocol.NodeHooksDelivery) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(stateDir, HooksFile+".tmp")
	if err = os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(stateDir, HooksFile))
}

// LoadHooks reads the copy SaveHooks wrote; ok is false when there is none.
func LoadHooks(stateDir string) (d protocol.NodeHooksDelivery, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(stateDir, HooksFile))
	if errors.Is(err, os.ErrNotExist) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	return d, true, json.Unmarshal(b, &d)
}

// StackChecks runs the NodeHooks stack checks that apply to a node with labels (a check applies when the node
// has every label of the check). A check passes when its command exits 0; it is reported as stack.<name>.
func StackChecks(ctx context.Context, h Host, labels []string, checks []protocol.NodeStackCheck) []CheckResult {
	var r []CheckResult
	for _, c := range checks {
		name := "stack." + c.Name
		if !slices.ContainsFunc(c.Labels, func(l string) bool { return !slices.Contains(labels, l) }) {
			if h.Run == nil {
				continue
			}
			out, errOut, err := h.Run(ctx, "/bin/sh", "-c", c.Command)
			if err != nil {
				detail := err.Error()
				if l := FirstLine(errOut + "\n" + out); l != "" {
					detail += ": " + l
				}
				r = append(r, CheckResult{Check: name, Status: Fail, Detail: detail, Hint: c.Hint})
				continue
			}
			r = append(r, CheckResult{Check: name, Status: Pass, Detail: FirstLine(out)})
			continue
		}
		r = append(r, CheckResult{Check: name, Status: Skip, Detail: "node lacks a label of " + jsonList(c.Labels)})
	}
	return r
}

func jsonList(v []string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// SelfChecks are the periodic self-checks of a running node (node-agent D7): git and forge credentials, process
// start times and the stack checks. Disk is reported as health.disk, runtimes by their driver probes, Core and
// NATS by the connection itself.
func (d Doctor) SelfChecks(ctx context.Context) []CheckResult {
	var r []CheckResult
	if d.Host.Run != nil && d.Host.LookPath != nil {
		r = append(d.git(ctx), d.ps(ctx))
	}
	return append(r, StackChecks(ctx, d.Host, d.Labels, d.Stack)...)
}
