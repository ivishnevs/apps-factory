package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"path/filepath"

	"appfactory/internal/artifacts"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func (c *Core) artifactRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /projects/{project}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Artifacts(r.Context(), c.companyFrom(r), r.PathValue("project"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /projects/{project}/artifacts/{artifact}/{version}", func(w http.ResponseWriter, r *http.Request) {
		c.artifactView(w, r, r.PathValue("project"), r.PathValue("artifact"), r.PathValue("version"))
	})
	m.HandleFunc("POST /projects/{project}/artifacts", func(w http.ResponseWriter, r *http.Request) {
		var p artifacts.Register
		if !decode(w, r, &p) {
			return
		}
		p.Actor = "owner"
		v, err := c.Store.RegisterArtifact(r.Context(), c.companyFrom(r), r.PathValue("project"), p)
		reply(w, v, err)
	})
	m.HandleFunc("POST /projects/{project}/artifacts/approval", func(w http.ResponseWriter, r *http.Request) {
		var p artifacts.ApprovalRequest
		if !decode(w, r, &p) {
			return
		}
		id, version := p.ArtifactID, p.ArtifactVersion
		if id == "" && version == "" && len(p.Options) > 0 {
			// A choice approval (artifact-choice-approvals): its first option names the producing agent.
			id, version = p.Options[0].ArtifactID, p.Options[0].ArtifactVersion
		}
		record, err := c.Store.Artifact(r.Context(), c.companyFrom(r), r.PathValue("project"), id, version)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if p.Agent == "" {
			p.Agent = record.CreatedBy
			if p.Agent == "owner" {
				t, err := c.Store.Task(r.Context(), c.companyFrom(r), record.TaskID)
				if err != nil {
					reply(w, nil, err)
					return
				}
				p.Agent = t.Assignee
			}
		}
		if !c.agentExists(w, c.companyFrom(r), p.Agent) {
			return
		}
		if p.Blocking {
			http.Error(w, "owner request cannot block an agent run", 400)
			return
		}
		v, err := c.Store.RequestArtifactApproval(r.Context(), c.companyFrom(r), record.ProjectID, p)
		reply(w, v, err)
	})
}
func (c *Core) artifactView(w http.ResponseWriter, r *http.Request, project, id, version string) {
	record, err := c.Store.Artifact(r.Context(), c.companyFrom(r), project, id, version)
	if err != nil {
		reply(w, nil, err)
		return
	}
	a, err := c.Store.ArtifactApproval(r.Context(), c.companyFrom(r), project, id, version)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		reply(w, nil, err)
		return
	}
	var approval any
	if err == nil {
		approval = a
	}
	out := map[string]any{"artifact": record, "approval": approval}
	if record.ArchivedAt > 0 {
		// artifact-retention: the agent learns the content is gone and which version to use instead.
		out["archived"] = c.Store.ArchivedNote(r.Context(), c.companyFrom(r), project, record)
	}
	write(w, out)
}
func (c *Core) artifactTool(w http.ResponseWriter, r *http.Request, p factorytools.Call, run storage.Run, snapshot, current config.Agent) {
	switch p.Name {
	case "factory_artifact_list":
		v, err := c.Store.Artifacts(r.Context(), c.companyFrom(r), run.Spec.Project)
		reply(w, v, err)
	case "factory_artifact_get":
		c.artifactView(w, r, run.Spec.Project, p.Arguments["artifact_id"].(string), p.Arguments["artifact_version"].(string))
	case "factory_artifact_register":
		if run.Spec.TaskID == "" {
			http.Error(w, "artifact registration requires a current task", 403)
			return
		}
		var in artifacts.Register
		b, _ := json.Marshal(p.Arguments)
		if err := json.Unmarshal(b, &in); err != nil {
			http.Error(w, "invalid artifact", 400)
			return
		}
		in.Actor = run.Spec.Agent
		in.TaskID = run.Spec.TaskID
		in.RunID = run.Spec.ID
		v, err := c.Store.RegisterArtifact(r.Context(), c.companyFrom(r), run.Spec.Project, in)
		reply(w, v, err)
	case "factory_artifact_upload":
		// The node's adapter uploaded the worktree file and filled in its digest (core-owned-project-data D4).
		if run.Spec.TaskID == "" {
			http.Error(w, "artifact upload requires a current task", 403)
			return
		}
		sha := stringArg(p.Arguments, "sha256")
		if sha == "" {
			http.Error(w, "upload the file through the node's Factory MCP adapter (no sha256)", 400)
			return
		}
		id, version := stringArg(p.Arguments, "artifact_id"), stringArg(p.Arguments, "artifact_version")
		a := tasks.Artifact{ID: id, Version: version, URI: storage.UploadURI(id, version), SHA256: sha, Name: path.Base(filepath.ToSlash(stringArg(p.Arguments, "path")))}
		v, err := c.Store.RegisterArtifact(r.Context(), c.companyFrom(r), run.Spec.Project, artifacts.Register{Artifact: a, TaskID: run.Spec.TaskID, RunID: run.Spec.ID, Actor: run.Spec.Agent, RequestID: stringArg(p.Arguments, "request_id"), Includes: includesArg(p.Arguments)})
		reply(w, v, err)
	case "factory_artifact_request_approval":
		var in artifacts.ApprovalRequest
		b, _ := json.Marshal(p.Arguments)
		if err := json.Unmarshal(b, &in); err != nil {
			http.Error(w, "invalid artifact approval", 400)
			return
		}
		in.Agent = run.Spec.Agent
		in.TaskID = run.Spec.TaskID
		in.RunID = run.Spec.ID
		if in.Audience != "" && in.Audience != "internal" && in.Audience != "external" {
			http.Error(w, "audience must be internal (the team's own material) or external (what leaves the company)", 400)
			return
		}
		rd := requestDelegation{audience: in.Audience, requester: run.Spec.Agent}
		c.artifactDelegate(r.Context(), c.companyFrom(r), run.Spec.Project, &rd)
		in.Delegate, in.Setting = rd.delegate, rd.setting
		if in.Delegate == "" && !ownerAllowed(snapshot, current) {
			// Strict delegation (people-topics D17): it would reach people; an agent that may not bring items to people
			// has its manager decide it instead.
			m := c.escalationTargetIn(c.companyFrom(r), run.Spec.Agent)
			if m == "" || m == "owner" {
				http.Error(w, "owner communication permission required", 403)
				return
			}
			in.Delegate, in.Setting = m, "strict delegation: "+run.Spec.Agent+" may not bring items to people, its manager "+m+" decides"
		}
		in.Strict = true
		v, err := c.Store.RequestArtifactApproval(r.Context(), c.companyFrom(r), run.Spec.Project, in)
		reply(w, v, err)
	default:
		http.Error(w, "unknown artifact tool", 400)
	}
}

// includesArg reads a composite's parts (people-topics D11).
func includesArg(args map[string]any) []artifacts.Part {
	var out []artifacts.Part
	list, _ := args["includes"].([]any)
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			id, _ := m["id"].(string)
			v, _ := m["version"].(string)
			out = append(out, artifacts.Part{ID: id, Version: v})
		}
	}
	return out
}
