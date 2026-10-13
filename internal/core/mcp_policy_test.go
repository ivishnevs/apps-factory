package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestMCPCapturedPermissionsAndAssignAllowlist(t *testing.T) {
	for _, scenario := range []string{"captured-tool-deny", "captured-create-deny", "current-create-deny", "assign-only"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.DB.Close()
			agents, err := config.Load("../..")
			if err != nil {
				t.Fatal(err)
			}
			current := agents["demo"]
			current.Spec.Communication.CanCreateTasks = true
			current.Spec.Communication.CanAssignTasks = true
			captured := current
			name := "factory_task_create"
			args := map[string]any{"title": "child", "request_id": "child"}
			want := 403
			switch scenario {
			case "captured-tool-deny":
				captured.Spec.Tools.Deny = append(captured.Spec.Tools.Deny, "factory.task.create")
			case "captured-create-deny":
				captured.Spec.Communication.CanCreateTasks = false
			case "current-create-deny":
				current.Spec.Communication.CanCreateTasks = false
			case "assign-only":
				current.Spec.Tools.Mode = "allowlist"
				current.Spec.Tools.Allow = []string{"factory.task.assign"}
				captured = current
				name = "factory_task_assign"
				want = 200
			}
			agents["demo"] = current
			c := &Core{Store: s, Agents: agents, Token: "test"}
			if _, err = s.CreateProject(ctx, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/permissions"}); err != nil {
				t.Fatal(err)
			}
			task, err := s.CreateTask(ctx, tasks.Create{ProjectID: "p", Title: "child"})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "assign-only" {
				args = map[string]any{"id": task.ID, "version": task.Version, "assignee": "demo", "request_id": "assign"}
			}
			snapshot, _ := json.Marshal(captured)
			run := protocol.RunSpec{ID: protocol.ID(), Project: "p", Agent: "demo", Workspace: "/tmp/permissions", ConfigSnapshot: snapshot}
			if err = s.Create(ctx, run); err != nil {
				t.Fatal(err)
			}
			lease, err := s.Claim(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			input, _ := json.Marshal(factorytools.Call{Name: name, Arguments: args})
			req := httptest.NewRequest("POST", "/agent/call", bytes.NewReader(input))
			req.Header.Set("Authorization", "Bearer "+c.runToken(run.ID, lease))
			w := httptest.NewRecorder()
			c.Handler().ServeHTTP(w, req)
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if scenario != "assign-only" {
				all, err := s.Tasks(ctx, "p", "")
				if err != nil || len(all) != 1 {
					t.Fatal("denied mutation persisted", all, err)
				}
			}
		})
	}
}
