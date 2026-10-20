package bus

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"

	"github.com/nats-io/nats.go"
)

// TestNodeControlSubjectIsPerNode (node-agent 1.5): under the per-node permission set of
// scripts/nats-secure-test.sh, node A receives a stop on its own control subject (plain NATS, outside the
// streams) within a second and is refused a subscription to node B's; B's stops never reach A.
func TestNodeControlSubjectIsPerNode(t *testing.T) {
	env := secureEnv(t, "NATS_TEST_SECURE_TLS_URL", "NATS_TEST_SECURE_CA", "NATS_TEST_SECURE_SERVER_NAME", "NATS_TEST_ADMIN_SEED", "NATS_TEST_NODE_SEED", "NATS_TEST_NODE_B_SEED")
	tlsOpts := TLS{CAFile: env["NATS_TEST_SECURE_CA"], ServerName: env["NATS_TEST_SECURE_SERVER_NAME"]}
	admin, err := ConnectWith(env["NATS_TEST_SECURE_TLS_URL"], Options{NkeySeedFile: env["NATS_TEST_ADMIN_SEED"], TLS: tlsOpts})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.NC.Close()
	refused := make(chan error, 4)
	a, err := Dial(env["NATS_TEST_SECURE_TLS_URL"], Options{NkeySeedFile: env["NATS_TEST_NODE_SEED"], TLS: tlsOpts}, nats.NoReconnect(),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { refused <- err }))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	own, err := a.SubscribeSync(protocol.NodeControlSubject("default", "node-a"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.SubscribeSync(protocol.NodeControlSubject("default", "node-b"))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Flush(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-refused:
		if !strings.Contains(strings.ToLower(err.Error()), "permissions violation") || !strings.Contains(err.Error(), "node-b") {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("node A subscribed to node B's control subject")
	}
	// Node B's own credentials may subscribe to its subject.
	b, err := Dial(env["NATS_TEST_SECURE_TLS_URL"], Options{NkeySeedFile: env["NATS_TEST_NODE_B_SEED"], TLS: tlsOpts}, nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	bOwn, err := b.SubscribeSync(protocol.NodeControlSubject("default", "node-b"))
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{"node-b", "node-a"} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		err = admin.Notify(ctx, protocol.NodeControlSubject("default", node), []byte(`{"type":"run.stop","to":"`+node+`"}`))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	msg, err := own.NextMsg(time.Second)
	if err != nil || !strings.Contains(string(msg.Data), `"to":"node-a"`) || time.Since(start) > time.Second {
		t.Fatal("node A's stop:", err)
	}
	if msg, err = own.NextMsg(200 * time.Millisecond); err == nil {
		t.Fatalf("node A received %s", msg.Data)
	}
	if _, err = other.NextMsg(200 * time.Millisecond); err == nil {
		t.Fatal("node A received node B's stop")
	}
	if msg, err = bOwn.NextMsg(time.Second); err != nil || !strings.Contains(string(msg.Data), `"to":"node-b"`) {
		t.Fatal("node B's stop:", err)
	}
}
