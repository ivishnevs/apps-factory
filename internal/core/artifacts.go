package core

import (
	"encoding/json"
	"errors"
	"net/http"

	"appfactory/internal/artifacts"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
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
		record, err := c.Store.Artifact(r.Context(), c.companyFrom(r), r.PathValue("project"), p.ArtifactID, p.ArtifactVersion)
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
		if !c.agentExists(w, p.Agent) {
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
	write(w, map[string]any{"artifact": record, "approval": approval})
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
	case "factory_artifact_request_approval":
		for _, a := range []config.Agent{snapshot, current} {
			owner := false
			for _, name := range a.Spec.Communication.CanMessage {
				if name == "owner" {
					owner = true
				}
			}
			if !owner {
				http.Error(w, "owner communication permission required", 403)
				return
			}
		}
		var in artifacts.ApprovalRequest
		b, _ := json.Marshal(p.Arguments)
		if err := json.Unmarshal(b, &in); err != nil {
			http.Error(w, "invalid artifact approval", 400)
			return
		}
		in.Agent = run.Spec.Agent
		in.TaskID = run.Spec.TaskID
		in.RunID = run.Spec.ID
		v, err := c.Store.RequestArtifactApproval(r.Context(), c.companyFrom(r), run.Spec.Project, in)
		reply(w, v, err)
	default:
		http.Error(w, "unknown artifact tool", 400)
	}
}
