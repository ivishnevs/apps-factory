package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/secrets"
)

// Company secrets on the node (core-secret-store D5): the claim answer carries them sealed to this node; they are
// opened at claim, held in memory for the run, given to the runtime only (env variables, 0600 files under
// <state_dir>/runs/<run>/secrets) and redacted from everything the run reports.

// openSecrets opens the sealed secrets of a claim for run and keeps them until forgetSecrets.
func (w *Worker) openSecrets(company, run, node string, sealed []protocol.SealedSecret) error {
	if len(sealed) == 0 {
		return nil
	}
	if w.Box == nil {
		return errors.New("this node has no encryption key to open its run's secrets")
	}
	out := map[string]string{}
	for _, x := range sealed {
		v, err := secrets.OpenOnNode(w.Box, secrets.DeliveryInfo(company, run, node, x.Name, x.Version), x.Sealed)
		if err != nil {
			return fmt.Errorf("secret %s does not open on this node: %w", x.Name, err)
		}
		out[x.Name] = string(v)
	}
	delivered.Store(run, out)
	return nil
}

func (w *Worker) deliveredSecrets(run string) map[string]string {
	if v, ok := delivered.Load(run); ok {
		return v.(map[string]string)
	}
	return map[string]string{}
}

func (w *Worker) forgetSecrets(run string) { delivered.Delete(run) }

// delivered holds the opened secrets of the node's claimed runs (run ID → name → value) until each run ends; run
// IDs are unique, so the executors of one node share it.
var delivered sync.Map

// SecretsDir is where a run's file secrets live (removed when the run ends and at node start).
func SecretsDir(stateDir, run string) string { return filepath.Join(stateDir, "runs", run, "secrets") }

// agentSecrets turns the agent's spec.secrets into runtime environment entries: a string's value, or the path of
// a 0600 file holding a file secret. dir is the run's secrets directory to remove when the run ends ("" when none).
func (w *Worker) agentSecrets(spec protocol.RunSpec, delivered map[string]string) (env []string, dir string, err error) {
	var a config.Agent
	if len(spec.ConfigSnapshot) > 0 {
		if err := json.Unmarshal(spec.ConfigSnapshot, &a); err != nil {
			return nil, "", fmt.Errorf("invalid agent snapshot: %w", err)
		}
	}
	for _, u := range a.Spec.Secrets {
		v, ok := delivered[u.Name]
		if !ok {
			return env, dir, fmt.Errorf("secret %s was not delivered with the claim (set it on the Secrets page)", u.Name)
		}
		if !u.File {
			env = append(env, u.Env+"="+v)
			continue
		}
		if dir == "" {
			base := w.StateDir
			if base == "" {
				base = os.TempDir()
			}
			dir = SecretsDir(base, spec.ID)
			if err = os.MkdirAll(dir, 0o700); err != nil {
				return env, dir, err
			}
		}
		path := filepath.Join(dir, u.Name)
		if err = os.WriteFile(path, []byte(v), 0o600); err != nil {
			return env, dir, err
		}
		env = append(env, u.Env+"="+path)
	}
	return env, dir, nil
}

// RemoveSecretLeftovers removes the file secrets and redaction files a previous node process left in stateDir's
// run directories (D5); the event spools there stay.
func RemoveSecretLeftovers(stateDir string) error {
	dirs, err := filepath.Glob(filepath.Join(stateDir, "runs", "*", "secrets"))
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if err = os.RemoveAll(d); err != nil {
			return err
		}
	}
	return nil
}
