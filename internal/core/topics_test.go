package core

import (
	"encoding/json"
	"testing"

	"appfactory/internal/storage"
)

// TestTopicsAPI (people-topics D5, D13): people read open topics with their items and actions, an agent completes
// its topic's brief, and people answer an item from the topic.
func TestTopicsAPI(t *testing.T) {
	h := newExitHarness(t)
	h.create("t", "notes", "demo")
	r := h.start("t")
	brief := testBrief()
	brief["title"] = "Store region"
	q := h.mcp(r, "factory_owner_ask", map[string]any{"body": "Which store region first?", "options": []any{"EU", "US"}, "request_id": "q", "brief": brief})
	topicID, _ := q["topic_id"].(string)
	if topicID == "" {
		t.Fatalf("no topic: %+v", q)
	}
	var list []storage.TopicView
	if w := h.do(h.c.Token, "GET", "/topics?open=1", nil, nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].Waiting != 1 || list[0].Brief.Title != "Store region" {
		t.Fatalf("topics %d %s", w.Code, w.Body.String())
	}
	if acts := list[0].Items[0].Actions; len(acts) != 2 || acts[0] != "EU" {
		t.Fatalf("actions %v", acts)
	}
	// The asker restates the topic; another agent may not.
	h.mcp(r, "factory_people_brief", map[string]any{"topic_id": topicID, "brief": map[string]any{"ask": "Which store region do we launch in first?"}, "request_id": "b1"})
	var v storage.TopicView
	_ = json.Unmarshal(h.do(h.c.Token, "GET", "/topics/"+topicID, nil, nil).Body.Bytes(), &v)
	if v.Brief.Ask != "Which store region do we launch in first?" || v.Brief.Title != "Store region" {
		t.Fatalf("brief %+v", v.Brief)
	}
	h.end(r, "succeeded", "accepted")
	h.create("o", "notes", "reviewer")
	other := h.start("o")
	if w := h.do(other.token, "POST", "/agent/call", map[string]any{"name": "factory_people_brief", "arguments": map[string]any{"topic_id": topicID, "brief": map[string]any{"ask": "x"}, "request_id": "b2"}}, nil); w.Code != 403 {
		t.Fatal("a stranger briefed the topic", w.Code, w.Body.String())
	}
	// People answer from the topic.
	item := v.Items[0]
	if w := h.do(h.c.Token, "POST", "/topics/"+topicID+"/decide", map[string]any{"items": []map[string]any{{"kind": item.Kind, "ref": item.Ref, "version": item.Message.Version, "action": "EU"}}, "request_id": "d1"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = json.Unmarshal(h.do(h.c.Token, "GET", "/topics/"+topicID, nil, nil).Body.Bytes(), &v)
	if v.Open || v.Items[0].Status != "ANSWERED" {
		t.Fatalf("after the answer %+v", v)
	}
}
