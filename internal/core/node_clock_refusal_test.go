package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"appfactory/internal/storage"
	"appfactory/internal/testnode"
)

// Only an authentic, clock-refused heartbeat for the signing node grants liveness grace.
func TestClockGraceRequiresOwnAuthenticHeartbeat(t *testing.T) {
	for _, tc := range []struct {
		name, route string
		wrongKey    bool
		wantOnline  bool
	}{
		{name: "own heartbeat", route: "heartbeat", wantOnline: true},
		{name: "invalid signature", route: "heartbeat", wrongKey: true},
		{name: "poll is not a heartbeat", route: "poll"},
		{name: "another node", route: "other/heartbeat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s, fake := nodeCore(t)
			ctx := context.Background()
			n := testnode.Enroll(t, s, storage.DefaultCompany)
			api := clockNode(t, c, s, n, 0, nil)
			if err := api.Do(ctx, "POST", "/nodes/register", registration(n.ID, 1), nil); err != nil {
				t.Fatal(err)
			}
			fake.Advance(55 * time.Second)
			api.Now = func() time.Time { return s.Now().Add(5 * time.Minute) }
			if tc.wrongKey {
				_, key, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				api.Key = key
			}
			path := "/nodes/" + n.ID + "/" + tc.route
			if tc.route == "other/heartbeat" {
				path = "/nodes/another-node/heartbeat"
			}
			if err := api.Do(ctx, "POST", path, storage.NodeHeartbeat{Epoch: 1}, nil); err == nil {
				t.Fatal("request outside the window was accepted")
			}
			fake.Advance(10 * time.Second)
			if err := s.ExpireNodes(ctx, storage.DefaultCompany); err != nil {
				t.Fatal(err)
			}
			nodes, err := s.Nodes(ctx, storage.DefaultCompany)
			if err != nil || len(nodes) != 1 {
				t.Fatal(nodes, err)
			}
			if nodes[0].Online != tc.wantOnline {
				t.Fatalf("online = %v, want %v", nodes[0].Online, tc.wantOnline)
			}
		})
	}
}
