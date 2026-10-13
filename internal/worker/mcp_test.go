package worker

import (
	"strings"
	"testing"
)

func TestRuntimeCredentialEnvironment(t *testing.T) {
	env := runEnvironment([]string{"PATH=/bin", "FACTORY_TOKEN=owner", "FACTORY_RUN_TOKEN=old", "FACTORY_URL=old", "NATS_URL=secret", "NATS_PASSWORD=secret", "HOME=/tmp"}, "http://localhost:8080", "scoped")
	joined := strings.Join(env, "\n")
	for _, secret := range []string{"owner", "secret", "old"} {
		if strings.Contains(joined, secret) {
			t.Fatal("parent credential inherited", joined)
		}
	}
	for _, required := range []string{"PATH=/bin", "HOME=/tmp", "FACTORY_URL=http://localhost:8080", "FACTORY_RUN_TOKEN=scoped"} {
		if !strings.Contains(joined, required) {
			t.Fatal(required, joined)
		}
	}
}
