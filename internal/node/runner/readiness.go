package runner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/readiness"
)

// projectChecks runs the project readiness checks Core asks for in the heartbeat answer (node-readiness): clone
// access to each project's repository and its required tools. A check runs again when its generation or contents
// change; the results ride every heartbeat (readiness), and a finished pass asks for a heartbeat at once.
type projectChecks struct {
	run  readiness.Run
	done func() // a pass finished: send the results
	kick chan struct{}

	mu      sync.Mutex
	want    map[string]protocol.ProjectCheck
	keys    map[string]string // project → key of the check its result answers
	results map[string]protocol.ProjectReadiness
}

func newProjectChecks(run readiness.Run, done func()) *projectChecks {
	return &projectChecks{run: run, done: done, kick: make(chan struct{}, 1), want: map[string]protocol.ProjectCheck{}, keys: map[string]string{}, results: map[string]protocol.ProjectReadiness{}}
}

// set replaces the wanted checks (a heartbeat answer) and starts a pass when one changed.
func (p *projectChecks) set(checks []protocol.ProjectCheck) {
	p.mu.Lock()
	want := make(map[string]protocol.ProjectCheck, len(checks))
	changed := false
	for _, c := range checks {
		want[c.Project] = c
		if p.keys[c.Project] != readiness.Key(c) {
			changed = true
		}
	}
	for project := range p.results {
		if _, ok := want[project]; !ok {
			delete(p.results, project)
			delete(p.keys, project)
		}
	}
	p.want = want
	p.mu.Unlock()
	if changed {
		select {
		case p.kick <- struct{}{}:
		default:
		}
	}
}

// reports are the results for the wanted projects; never nil, so Core knows this node reports readiness.
func (p *projectChecks) reports() []protocol.ProjectReadiness {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []protocol.ProjectReadiness{}
	for project := range p.want {
		if r, ok := p.results[project]; ok {
			out = append(out, r)
		}
	}
	return out
}

// pass runs every wanted check whose result is missing or for another key.
func (p *projectChecks) pass(ctx context.Context) {
	p.mu.Lock()
	var todo []protocol.ProjectCheck
	for project, c := range p.want {
		if p.keys[project] != readiness.Key(c) {
			todo = append(todo, c)
		}
	}
	p.mu.Unlock()
	if len(todo) == 0 {
		return
	}
	for _, c := range todo {
		faults := readiness.Check(ctx, p.run, c)
		if ctx.Err() != nil {
			return
		}
		if len(faults) > 0 {
			slog.Warn("project readiness check failed", "project", c.Project, "generation", c.Generation, "faults", readiness.Summary(faults))
		} else {
			slog.Info("project readiness check passed", "project", c.Project, "generation", c.Generation)
		}
		p.mu.Lock()
		if cur, ok := p.want[c.Project]; ok && readiness.Key(cur) == readiness.Key(c) {
			p.keys[c.Project] = readiness.Key(c)
			p.results[c.Project] = protocol.ProjectReadiness{Project: c.Project, Generation: c.Generation, Faults: faults, CheckedAt: time.Now().Unix()}
		}
		p.mu.Unlock()
	}
	if p.done != nil {
		p.done()
	}
}

// loop runs a pass whenever the wanted checks change, until ctx ends.
func (p *projectChecks) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.kick:
			p.pass(ctx)
		}
	}
}
