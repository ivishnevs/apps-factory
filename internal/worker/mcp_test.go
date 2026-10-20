package worker

import (
	"strings"
	"testing"
)

func init() {
	// The fake runtimes of these tests are configured through the environment.
	RuntimeEnv = append(RuntimeEnv, "FACTORY_FAKE_*", "FACTORY_RPC_HELPER")
}

// TestRuntimeCredentialEnvironment (explicit-exits-authority D9): the runtime gets the allowlisted part of
// the node's environment plus its run credential; the owner's and node's tokens, the owner CLI's
// configuration, NATS and anything not allowlisted stay out; node.yaml runtime_env extends the list but
// never lets a credential through.
func TestRuntimeCredentialEnvironment(t *testing.T) {
	node := []string{"PATH=/bin", "HOME=/tmp", "LC_ALL=C", "GH_TOKEN=forge", "OPENAI_API_KEY=model", "FACTORY_TOKEN=owner", "FACTORY_NODE_TOKEN=owner",
		"FACTORY_RUN_TOKEN=old", "FACTORY_URL=old", "FACTORY_NODE_CONFIG=/etc/owner", "NATS_URL=secret", "NATS_PASSWORD=secret", "NATS_CREDS=secret",
		"DATABASE_URL=private", "XCODE_TEAM=team"}
	env := runEnvironment(node, []string{"XCODE_*", "NATS_*", "FACTORY_TOKEN"}, "http://localhost:8080", "scoped")
	joined := strings.Join(env, "\n")
	for _, secret := range []string{"owner", "secret", "old", "private"} {
		if strings.Contains(joined, secret) {
			t.Fatal("not allowlisted or credential inherited", joined)
		}
	}
	for _, required := range []string{"PATH=/bin", "HOME=/tmp", "LC_ALL=C", "GH_TOKEN=forge", "OPENAI_API_KEY=model", "XCODE_TEAM=team",
		"FACTORY_URL=http://localhost:8080", "FACTORY_RUN_TOKEN=scoped"} {
		if !strings.Contains(joined, required) {
			t.Fatal(required, joined)
		}
	}
	if without := strings.Join(nodeEnvironment(node, nil), "\n"); strings.Contains(without, "FACTORY_") || strings.Contains(without, "XCODE_TEAM") {
		t.Fatal(without)
	}
}
