package node

import (
	"os"
	"path/filepath"
	"testing"
)

// work-and-conversation-runs 1.3: one conversation slot by default, 0 turns them off, a negative count is refused.
func TestConversationSlotsConfig(t *testing.T) {
	if Defaults().ConversationSlots != 1 {
		t.Fatal("default conversation slots", Defaults().ConversationSlots)
	}
	dir := t.TempDir()
	for text, want := range map[string]int{"name: n1\nstate_dir: s\n": 1, "name: n1\nstate_dir: s\nconversation_slots: 0\n": 0, "name: n1\nstate_dir: s\nconversation_slots: 3\n": 3, "name: n1\nstate_dir: s\nconversation_slots: -1\n": -1} {
		p := filepath.Join(dir, "node.yaml")
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := LoadConfig(p)
		if want < 0 {
			if err == nil {
				t.Fatal("negative conversation_slots accepted")
			}
			continue
		}
		if err != nil || c.ConversationSlots != want {
			t.Fatalf("%q: %d %v", text, c.ConversationSlots, err)
		}
	}
}
