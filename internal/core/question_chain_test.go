package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

// TestQuestionChainRefusedThroughTool (question-chain-limit 1.2): an agent's ninth question to the same agent about one
// task gets a 409 with question_limit and a hint it can act on; nothing is sent.
func TestQuestionChainRefusedThroughTool(t *testing.T) {
	h := newExitHarness(t)
	h.create("q", "notes", "demo")
	for i := range 8 {
		if _, err := h.s.SendMessage(context.Background(), storage.DefaultCompany, messaging.Send{ProjectID: "notes", TaskID: "q", Sender: "demo", Recipient: "manager", MessageType: "question", Body: fmt.Sprint("round ", i), RequestID: fmt.Sprint("round-", i)}); err != nil {
			t.Fatal(i, err)
		}
	}
	r := h.start("q")
	code, body := h.callCode(r, "factory_message_ask", map[string]any{"recipient": "manager", "body": "one more round?", "task_id": "q", "request_id": "round-9"})
	if code != 409 || !strings.Contains(body, "question_limit") || !strings.Contains(body, "manager") {
		t.Fatal(code, body)
	}
	h.end(r, "succeeded", "accepted")
}
