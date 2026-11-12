package core

// testBrief is a full brief for people (people-topics D3): every agent call that reaches people in a test carries it.
func testBrief() map[string]any {
	return map[string]any{"title": "Test item", "ask": "Approve the test item?", "after": "the agent continues", "recommendation": "approve: it is a test", "context": "a test needs it"}
}
