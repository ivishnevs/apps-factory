package runner

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/driver/claude"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// brokenDNS resolves nothing, like mac2 behind Cloudflare WARP on 2026-11-11.
func brokenDNS() *node.Network {
	return &node.Network{Lookup: func(_ context.Context, host string) ([]string, error) {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}}
}

// loggedInClaude is a Claude driver on a fake machine whose probe passes: Node 22, the shim, a supported version and a
// subscription login.
func loggedInClaude(n *node.Network) claude.Driver {
	h := node.Host{
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		Stat: func(p string) (fs.FileInfo, error) {
			if p == "/opt/node" || p == "/opt/shim.js" {
				return os.Stat("net_test.go")
			}
			return nil, fs.ErrNotExist
		},
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			switch strings.Join(append([]string{name}, args...), " ") {
			case "/opt/node --version":
				return "v22.4.0\n", "", nil
			case "/opt/node /opt/shim.js --probe":
				return `{"event":"probe","claude_code_version":"` + claude.SupportedClaudeCode + `","sdk_version":"` + claude.SupportedSDK + `","account":{"subscriptionType":"max","apiKeySource":"none"}}` + "\n", "", nil
			}
			return "", "", errors.New("unexpected command")
		},
	}
	return claude.Driver{Host: &h, Node: "/opt/node", Shim: "/opt/shim.js", Net: n}
}

// TestBrokenDNSDisablesClaude (setup-assistant 4.1, 5.2; 2026-11-11): a node that cannot resolve api.anthropic.com
// passes every other Claude check, yet its probe fails net.dns and net.api, so the driver is disabled with the cause
// and the fix and its slots claim no Claude run (the runs stay queued with their attempts).
func TestBrokenDNSDisablesClaude(t *testing.T) {
	ctx := context.Background()
	healthy := loggedInClaude(&node.Network{Lookup: func(context.Context, string) ([]string, error) { return []string{"160.79.104.10"}, nil }})
	h := newDriverHealth([]driver.Driver{healthy}, node.Defaults().Probes)
	if h.check(ctx, false); !h.usable("claude") {
		t.Fatalf("healthy node: %+v", h.infos())
	}
	broken := loggedInClaude(brokenDNS())
	var api *node.CheckResult
	for _, r := range broken.Probe(ctx) {
		if r.Check == "net.api" {
			api = &r
		} else if r.Check != "net.dns" && r.Status != node.Pass {
			t.Fatalf("only the network may fail: %+v", r)
		}
	}
	if api == nil || api.Status != node.Fail || !strings.Contains(api.Detail, "api.anthropic.com") {
		t.Fatalf("net.api = %+v", api)
	}
	h = newDriverHealth([]driver.Driver{broken}, node.Defaults().Probes)
	h.check(ctx, false)
	if h.usable("claude") || h.anyUsable() {
		t.Fatal("a node without DNS keeps Claude usable")
	}
	info := h.infos()[0]
	if info.OK || info.Check != "probe" || !strings.Contains(info.Detail, "net.api") || !strings.Contains(info.Detail, "api.anthropic.com") {
		t.Fatalf("driver info = %+v", info)
	}
}

// TestNetworkFaultRechecksAtOnce (setup-assistant 4.2): a run that ended on a network error asks for a probe now
// instead of at the next interval.
func TestNetworkFaultRechecksAtOnce(t *testing.T) {
	d := newFlip(protocol.RuntimeClaude)
	h := newDriverHealth([]driver.Driver{d}, node.Probes{Interval: time.Hour, VersionInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.run(ctx)
	before := d.probes.Load()
	h.recheck()
	h.recheck() // coalesced while one is pending
	eventually(t, "probe after a network fault", func() bool { return d.probes.Load() > before })
}
