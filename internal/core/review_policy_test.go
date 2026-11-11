package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/policies"
	"appfactory/internal/storage"
)

// TestRequiredReview (company-self-service 5.1, 5.2): a review rule that requires review refuses done of a
// repository task without an approval bound to its pull request; submit without a reviewer goes to the rule's
// reviewer; after the approved verdict done is accepted. A task with nothing to merge needs no review.
func TestRequiredReview(t *testing.T) {
	h := newExitHarness(t)
	demo := h.c.Agents["demo"]
	demo.Rules = append(append([]policies.Rule{}, demo.Rules...), policies.Rule{Name: "review-code", Type: "review", Required: true, Reviewer: "reviewer"})
	h.c.Agents["demo"] = demo
	h.create("t", "code", "demo")
	r := h.start("t")
	brief := func(r exitRun) int64 {
		var b struct {
			AckSeq int64 `json:"ack_seq"`
		}
		_ = json.Unmarshal(h.do(r.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &b)
		return b.AckSeq
	}
	exit := func(r exitRun, kind string, args map[string]any, facts *storage.ExitFacts) *httptest.ResponseRecorder {
		return h.do(r.token, "POST", "/agent/exit", map[string]any{"kind": kind, "arguments": args, "ack_seq": brief(r), "facts": facts}, nil)
	}
	merged := &storage.PRFacts{Number: 9, State: "merged", HeadSHA: "h1"}
	if w := exit(r, "done", map[string]any{"summary": "merged without review"}, clean("h1", merged)); w.Code != 409 || refusalOf(t, w).Reason != "review_required" {
		t.Fatal(w.Code, w.Body.String())
	}
	open := &storage.PRFacts{Number: 9, State: "open", HeadSHA: "h1"}
	if w := exit(r, "submit", map[string]any{"summary": "login"}, clean("h1", open)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := h.task("t"); got.Status != "IN_REVIEW" || got.Assignee != "reviewer" {
		t.Fatalf("default reviewer not applied: %+v", got)
	}
	h.end(r, "succeeded", "accepted")
	rv := h.start("t")
	if w := exit(rv, "handoff", map[string]any{"to": "demo", "note": "good", "verdict": "approved"}, clean("h1", open)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(rv, "succeeded", "accepted")
	r = h.start("t")
	if w := exit(r, "done", map[string]any{"summary": "merged"}, clean("h1", merged)); w.Code != 200 || h.task("t").Status != "DONE" {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")

	// Nothing to merge: no pull request, nothing ahead of the default branch.
	h.create("n", "code", "demo")
	r = h.start("n")
	nothing := clean("h2", nil)
	nothing.AheadOfDefault = 0
	if w := exit(r, "done", map[string]any{"summary": "nothing to change"}, nothing); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestReviewCopyApproval (node-ties): a reviewer works in a detached review copy whose facts name no branch or
// pull request; its approved verdict approves the pull request of the task's submission at the head it saw, so
// the implementer's done is accepted.
func TestReviewCopyApproval(t *testing.T) {
	h := newExitHarness(t)
	demo := h.c.Agents["demo"]
	demo.Rules = append(append([]policies.Rule{}, demo.Rules...), policies.Rule{Name: "review-code", Type: "review", Required: true, Reviewer: "reviewer"})
	h.c.Agents["demo"] = demo
	h.create("t", "code", "demo")
	brief := func(r exitRun) (int64, string) {
		var b struct {
			AckSeq int64  `json:"ack_seq"`
			Brief  string `json:"brief"`
		}
		w := h.do(r.token, "GET", "/agent/brief", nil, nil)
		_ = json.Unmarshal(w.Body.Bytes(), &b)
		return b.AckSeq, w.Body.String()
	}
	exit := func(r exitRun, kind string, args map[string]any, facts *storage.ExitFacts) *httptest.ResponseRecorder {
		seq, _ := brief(r)
		return h.do(r.token, "POST", "/agent/exit", map[string]any{"kind": kind, "arguments": args, "ack_seq": seq, "facts": facts}, nil)
	}
	r := h.start("t")
	if w := exit(r, "submit", map[string]any{"summary": "login"}, clean("h1", &storage.PRFacts{Number: 9, State: "open", HeadSHA: "h1"})); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
	rv := h.start("t")
	copyFacts := &storage.ExitFacts{Head: "h1", DefaultBranch: "main", AheadOfDefault: 2, Scan: &storage.ScanFacts{OK: true}}
	if w := exit(rv, "handoff", map[string]any{"to": "demo", "note": "good", "verdict": "approved"}, copyFacts); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(rv, "succeeded", "accepted")
	r = h.start("t")
	if w := exit(r, "done", map[string]any{"summary": "merged"}, clean("h1", &storage.PRFacts{Number: 9, State: "merged", HeadSHA: "h1"})); w.Code != 200 || h.task("t").Status != "DONE" {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestReviewPaths (company-self-service 5.3): a pull request changing files a path rule covers needs the
// approval of that path's reviewer, even after another reviewer approved it.
func TestReviewPaths(t *testing.T) {
	h := newExitHarness(t)
	demo := h.c.Agents["demo"]
	demo.Rules = append(append([]policies.Rule{}, demo.Rules...), policies.Rule{Name: "specs", Type: "review", Reviewer: "reviewer",
		Paths: []policies.ReviewPathRule{{Match: "openspec/specs/**", Reviewer: "manager"}}})
	h.c.Agents["demo"] = demo
	h.create("t", "code", "demo")
	brief := func(r exitRun) int64 {
		var b struct {
			AckSeq int64 `json:"ack_seq"`
		}
		_ = json.Unmarshal(h.do(r.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &b)
		return b.AckSeq
	}
	exit := func(r exitRun, kind string, args map[string]any, facts *storage.ExitFacts) *httptest.ResponseRecorder {
		return h.do(r.token, "POST", "/agent/exit", map[string]any{"kind": kind, "arguments": args, "ack_seq": brief(r), "facts": facts}, nil)
	}
	files := []string{"src/login.go", "openspec/specs/login/spec.md"}
	open := &storage.PRFacts{Number: 4, State: "open", HeadSHA: "h1", Files: files}
	merged := &storage.PRFacts{Number: 4, State: "merged", HeadSHA: "h1", Files: files}
	review := func(by, reviewer string) {
		t.Helper()
		r := h.start("t")
		args := map[string]any{"summary": "login"}
		if reviewer != "" {
			args["reviewer"] = reviewer
		}
		if w := exit(r, "submit", args, clean("h1", open)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		h.end(r, "succeeded", "accepted")
		rv := h.start("t")
		if run, _ := h.s.Get(context.Background(), storage.DefaultCompany, rv.id); run.Spec.Agent != by {
			t.Fatal("review run of", run.Spec.Agent)
		}
		if w := exit(rv, "handoff", map[string]any{"to": "demo", "note": "ok", "verdict": "approved"}, clean("h1", open)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		h.end(rv, "succeeded", "accepted")
	}
	review("reviewer", "")
	r := h.start("t")
	w := exit(r, "done", map[string]any{"summary": "merged"}, clean("h1", merged))
	if ref := refusalOf(t, w); w.Code != 409 || ref.Reason != "review_required" || !strings.Contains(ref.Hint, "openspec/specs/login/spec.md") || !strings.Contains(ref.Hint, "manager") {
		t.Fatal(w.Code, w.Body.String())
	}
	// The same run submits again, to the path's reviewer.
	if w = exit(r, "submit", map[string]any{"summary": "login, specs changed", "reviewer": "manager"}, clean("h1", open)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
	rv := h.start("t")
	if w = exit(rv, "handoff", map[string]any{"to": "demo", "note": "specs ok", "verdict": "approved"}, clean("h1", open)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(rv, "succeeded", "accepted")
	r = h.start("t")
	if w = exit(r, "done", map[string]any{"summary": "merged"}, clean("h1", merged)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
