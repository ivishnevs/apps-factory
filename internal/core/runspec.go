package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// RunSnapshot is the agent configuration captured on a run (RunSpec.config_snapshot) and its hash. It pins
// the content of the agent's offered skills (skill_hashes, skills.Hash over Core's registry, runtime-drivers
// 2.3): the node materializes each skill from its own registry and refuses the run before the runtime starts
// when a copy differs. A skill Core cannot hash is left unpinned (logged); the node then materializes it as is.
func (c *Core) RunSnapshot(a config.Agent) (json.RawMessage, string, error) {
	if c.Skills != nil && len(a.AvailableSkills) > 0 {
		a.SkillHashes = map[string]string{}
		for _, name := range a.AvailableSkills {
			pin, err := c.Skills.Pins([]string{name})
			if err != nil {
				slog.Warn("skill hash not pinned", "agent", a.Metadata.Name, "skill", name, "error", err)
				continue
			}
			a.SkillHashes[name] = pin[name]
		}
	}
	snapshot, err := json.Marshal(a)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(snapshot)
	return snapshot, hex.EncodeToString(hash[:]), nil
}

// modelOverride checks a task update that sets the task's model (runtime-drivers 2.7): only an authority over
// the task (the owner, the assignee's manager chain, the delegating task's assignee) may set it, and a model
// must fit the provider of the task's assignee after the update; "" clears it. It answers the refusal itself.
func (c *Core) modelOverride(w http.ResponseWriter, r *http.Request, p tasks.Update) bool {
	t, err := c.Store.Task(r.Context(), c.companyFrom(r), r.PathValue("id"))
	if err != nil {
		reply(w, nil, err)
		return false
	}
	if !c.hasAuthority(r.Context(), c.companyFrom(r), p.Actor, t) {
		http.Error(w, "only an authority over the task (the owner or the assignee's manager chain) may set its model", 403)
		return false
	}
	model := strings.TrimSpace(*p.Model)
	if model == "" {
		return true
	}
	assignee := t.Assignee
	if p.Assignee != nil {
		assignee = *p.Assignee
	}
	a, ok := c.Agents[assignee]
	if !ok {
		http.Error(w, "a model override needs a configured assignee", 400)
		return false
	}
	if provider := protocol.RuntimeProvider(a.Spec.Runtime.Provider); !config.ModelFits(provider, model) {
		http.Error(w, fmt.Sprintf("model %q does not fit provider %s of assignee %s", model, provider, assignee), 400)
		return false
	}
	return true
}
