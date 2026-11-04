package worker

import (
	"context"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/testnode"
)

// TestCompaniesDoNotShareRuns (postgres-tenancy 2.5, http-node-transport): each company's node is offered only its
// company's queued runs, and a node of one company can neither claim nor send events for another company's run.
func TestCompaniesDoNotShareRuns(t *testing.T) {
	s := storagetest.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const acme = "acme"
	if err := s.CreateCompany(ctx, acme, "Acme"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&core.Core{Store: s, Token: "test-token"}).Handler())
	defer server.Close()
	companies := []string{storage.DefaultCompany, acme}
	apis := map[string]API{}
	for _, co := range companies {
		for i := range 3 {
			spec := protocol.RunSpec{ID: co + "-run-" + string(rune('a'+i)), Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: "/tmp/" + co + "-" + string(rune('a'+i)), Prompt: "work", TimeoutSeconds: 30}
			if err := s.Create(ctx, co, spec); err != nil {
				t.Fatal(err)
			}
		}
		n := testnode.Enroll(t, s, co)
		if _, err := s.RegisterNode(ctx, co, storage.NodeRegistration{NodeID: n.ID, Name: n.ID, Epoch: 1, ProtocolVersion: 2, Slots: 4,
			Drivers: []storage.NodeDriver{{Name: "codex", Version: "test", OK: true}}, Resources: map[string]int{}, Labels: []string{}}); err != nil {
			t.Fatal(err)
		}
		apis[co] = API{URL: server.URL, Node: n.ID, Key: n.Key, Now: s.Now}
	}
	for _, co := range companies {
		var a struct {
			Offers []struct {
				RunID string `json:"run_id"`
			} `json:"offers"`
		}
		if err := apis[co].Do(ctx, "POST", "/nodes/"+apis[co].Node+"/poll", map[string]any{"epoch": 1, "free": map[string]int{"codex": 10}, "wait_seconds": 1}, &a); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, o := range a.Offers {
			got = append(got, o.RunID)
		}
		slices.Sort(got)
		if want := []string{co + "-run-a", co + "-run-b", co + "-run-c"}; !slices.Equal(got, want) {
			t.Fatalf("%s's node was offered %v, want %v", co, got, want)
		}
	}
	// acme's node cannot take or report the default company's run.
	w := &Worker{Company: acme, API: apis[acme], Inventory: node.NewInventory(apis[acme].Node, 1)}
	_, _, err := w.ClaimV2(ctx, storage.DefaultCompany+"-run-a", 0)
	var h *HTTPError
	if !errors.As(err, &h) || h.Status != 404 {
		t.Fatalf("cross-company claim: %v", err)
	}
	lease := "some-lease"
	err = apis[acme].Do(ctx, "POST", "/runs/"+storage.DefaultCompany+"-run-a/events", map[string]any{"lease": lease, "events": []storage.SeqEvent{{Seq: 1,
		Event: protocol.Wrap("agent.completed", storage.DefaultCompany+"-run-a", "", protocol.Event{RunID: storage.DefaultCompany + "-run-a", Lease: lease, Status: "ENDED", Outcome: "succeeded"})}}}, nil)
	if !errors.As(err, &h) || h.Status != 404 && h.Status != 403 {
		t.Fatalf("cross-company events: %v", err)
	}
	r, err := s.Get(ctx, storage.DefaultCompany, storage.DefaultCompany+"-run-a")
	if err != nil || r.Status != "QUEUED" || strings.Contains(r.Result, "succeeded") {
		t.Fatalf("%+v %v", r, err)
	}
}
