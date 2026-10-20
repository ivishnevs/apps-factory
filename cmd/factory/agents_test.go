package main

import (
	"encoding/json"
	"io"
	"os"
	"testing"
)

// TestAgentsAuthorizeShowsRecovery (reliable-recovery 2.1): factory agents authorize prints the agent's
// effective recovery mode next to the decision.
func TestAgentsAuthorizeShowsRecovery(t *testing.T) {
	t.Setenv("FACTORY_ROOT", "../..")
	args, stdout := os.Args, os.Stdout
	defer func() { os.Args, os.Stdout = args, stdout }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Args, os.Stdout = []string{"factory", "agents", "authorize", "demo", "factory.release.publish"}, w
	err = run()
	w.Close()
	os.Stdout = stdout
	out, _ := io.ReadAll(r)
	var got struct {
		Effect   string `json:"effect"`
		Recovery string `json:"recovery"`
	}
	if err != nil || json.Unmarshal(out, &got) != nil || got.Effect != "approval" || got.Recovery != "retry" {
		t.Fatal(err, string(out))
	}
}
