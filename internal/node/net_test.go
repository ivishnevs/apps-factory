package node

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// fakeNetwork is a Network whose resolver knows hosts (name → addresses) and whose dialer refuses the refused hosts.
func fakeNetwork(hosts map[string][]string, refused ...string) *Network {
	return &Network{
		Lookup: func(_ context.Context, host string) ([]string, error) {
			if a, ok := hosts[host]; ok {
				return a, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		},
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			for _, r := range refused {
				if strings.HasPrefix(addr, r+":") {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
				}
			}
			c, s := net.Pipe()
			_ = s.Close()
			return c, nil
		},
	}
}

func byCheck(rs []CheckResult) map[string]CheckResult {
	m := map[string]CheckResult{}
	for _, r := range rs {
		m[r.Check] = r
	}
	return m
}

// TestNetChecks (setup-assistant 4.1; 2026-11-11 mac2 behind Cloudflare WARP): a name that does not resolve fails
// net.dns and the reach check definitely, with the fix naming VPN and resolver; a refused connection fails the reach
// check; a timeout is transient; a nil Network checks nothing.
func TestNetChecks(t *testing.T) {
	ctx := context.Background()
	ok := byCheck(fakeNetwork(map[string][]string{"api.anthropic.com": {"160.79.104.10"}}).NetChecks(ctx, "api.anthropic.com", "net.api"))
	if ok["net.dns"].Status != Pass || ok["net.api"].Status != Pass {
		t.Fatalf("reachable host: %+v", ok)
	}
	broken := byCheck(fakeNetwork(nil).NetChecks(ctx, "api.anthropic.com", "net.api"))
	for _, c := range []string{"net.dns", "net.api"} {
		r := broken[c]
		if r.Status != Fail || r.Transient || !strings.Contains(r.Detail, "api.anthropic.com") || !strings.Contains(r.Hint, "WARP") || !strings.Contains(r.Hint, "dns-and-proxy") {
			t.Fatalf("%s = %+v", c, r)
		}
	}
	refused := byCheck(fakeNetwork(map[string][]string{"github.com": {"140.82.121.4"}}, "github.com").NetChecks(ctx, "github.com", "net.forge"))
	if refused["net.dns"].Status != Pass || refused["net.forge"].Status != Fail || refused["net.forge"].Transient {
		t.Fatalf("refused: %+v", refused)
	}
	slow := &Network{Lookup: func(context.Context, string) ([]string, error) {
		return nil, &net.DNSError{Err: "i/o timeout", Name: "chatgpt.com", IsTimeout: true}
	}}
	if r := byCheck(slow.NetChecks(ctx, "chatgpt.com", "net.api"))["net.api"]; r.Status != Fail || !r.Transient {
		t.Fatalf("timeout: %+v", r)
	}
	var none *Network
	if rs := none.NetChecks(ctx, "github.com", "net.forge"); len(rs) != 0 {
		t.Fatalf("nil network checked: %+v", rs)
	}
	if APIHostFromURL("https://proxy.example.com:8443/v1") != "proxy.example.com" || APIHostFromURL("") != "" {
		t.Fatal("APIHostFromURL")
	}
}

// TestSelfChecksReachTheForge: the running node's periodic self-checks include net.forge when it has a Network.
func TestSelfChecksReachTheForge(t *testing.T) {
	d := Doctor{Net: fakeNetwork(nil)}
	r := byCheck(d.SelfChecks(context.Background()))
	if f := r["net.forge"]; f.Status != Fail || !strings.Contains(f.Detail, "github.com") {
		t.Fatalf("net.forge = %+v", r)
	}
}
