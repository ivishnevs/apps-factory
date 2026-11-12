package node

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

// Network checks (setup-assistant 4.1): a node whose name resolution or route to a runtime's API host is broken
// (a corporate VPN such as Cloudflare WARP, a stale Tailscale resolver, a proxy) passed every other check on
// 2026-11-11 while each Claude run failed with ENOTFOUND. The checks resolve and reach the host through the system
// resolver, as the runtimes do, so the driver is disabled with the cause and its runs stay queued.

// Network resolves and dials for the network checks; tests pass fakes. A nil *Network skips the checks.
type Network struct {
	// Lookup resolves a host name through the system resolver.
	Lookup func(ctx context.Context, host string) ([]string, error)
	// Dial opens a TCP connection to host:port.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Timeout bounds each lookup and dial (default NetTimeout).
	Timeout time.Duration
}

// NetTimeout is how long one lookup or connection attempt of a network check may take.
const NetTimeout = 10 * time.Second

// Hosts the network checks reach.
const (
	ClaudeAPIHost = "api.anthropic.com"
	// CodexAPIHost is the endpoint Codex reaches with a ChatGPT login.
	CodexAPIHost = "chatgpt.com"
	ForgeHost    = "github.com"
)

// SystemNetwork is the machine's resolver (the cgo/libc resolver where the platform has one, as runtimes use)
// and dialer.
func SystemNetwork() *Network {
	r := &net.Resolver{}
	d := &net.Dialer{}
	return &Network{Lookup: r.LookupHost, Dial: d.DialContext}
}

// NetFix is the fix of a failing network check.
const NetFix = "the machine cannot resolve or reach the host: check its VPN or proxy (Cloudflare WARP, a corporate VPN), " +
	"a stale resolver (tailscale up --accept-dns=false, or restart Tailscale), and /etc/resolv.conf or the macOS DNS settings; " +
	"verify with `dscacheutil -q host -a name HOST` (macOS) or `getent hosts HOST` (Linux) and `curl -sI https://HOST`; " +
	"runbook dns-and-proxy of the factory-setup skill"

// NetChecks resolves host (net.dns) and opens a TCP connection to it on port 443 (net.api, or the given check
// name for the forge). A timeout is transient (the node retries before it disables the driver); a name that does
// not resolve or a refused connection is definite.
func (n *Network) NetChecks(ctx context.Context, host, reach string) []CheckResult {
	if n == nil || n.Lookup == nil {
		return nil
	}
	timeout := n.Timeout
	if timeout <= 0 {
		timeout = NetTimeout
	}
	fix := strings.ReplaceAll(NetFix, "HOST", host)
	lctx, cancel := context.WithTimeout(ctx, timeout)
	addrs, err := n.Lookup(lctx, host)
	cancel()
	if err == nil && len(addrs) == 0 {
		err = errors.New("no addresses")
	}
	if err != nil {
		return []CheckResult{
			{Check: "net.dns", Status: Fail, Detail: "cannot resolve " + host + ": " + err.Error(), Hint: fix, Transient: netTransient(err)},
			{Check: reach, Status: Fail, Detail: host + " not reached: its name does not resolve", Hint: fix, Transient: netTransient(err)},
		}
	}
	r := []CheckResult{{Check: "net.dns", Status: Pass, Detail: host + " resolves to " + strings.Join(addrs[:min(len(addrs), 2)], ", ")}}
	if n.Dial == nil {
		return r
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := n.Dial(dctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return append(r, CheckResult{Check: reach, Status: Fail, Detail: "cannot reach " + host + ":443: " + err.Error(), Hint: fix, Transient: netTransient(err)})
	}
	_ = c.Close()
	return append(r, CheckResult{Check: reach, Status: Pass, Detail: host + ":443 reachable"})
}

// netTransient: a timeout says nothing definite (a loaded or briefly offline machine); "no such host" or a refused
// connection is the machine's resolver or route.
func netTransient(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && (dnsErr.IsNotFound || !dnsErr.IsTimeout && !dnsErr.IsTemporary) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// APIHostFromURL is the host of a base URL (ANTHROPIC_BASE_URL), "" when it has none.
func APIHostFromURL(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	if h, _, err := net.SplitHostPort(u); err == nil {
		return h
	}
	return u
}
