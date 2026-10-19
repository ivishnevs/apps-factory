package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"appfactory/internal/bus"
	"github.com/nats-io/nats.go"
)

// RemoteTimeout bounds each Core and NATS check.
const RemoteTimeout = 5 * time.Second

// core checks that Core answers an authenticated GET /health (cheap, read-only; 200 also means Core reaches its
// database and NATS) with the node token (Doctor.Token, else FACTORY_TOKEN). Skipped without FACTORY_URL.
func (d Doctor) core(ctx context.Context) CheckResult {
	base := d.Host.Getenv("FACTORY_URL")
	if base == "" {
		return CheckResult{Check: "core", Status: Skip, Detail: "FACTORY_URL not set"}
	}
	u, err := url.Parse(strings.TrimRight(base, "/") + "/health")
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return CheckResult{Check: "core", Status: Fail, Detail: "FACTORY_URL is not an http(s) URL", Hint: "FACTORY_URL=http://core-host:8080"}
	}
	shown := u.Redacted()
	token, source := d.Token, d.TokenSource
	if token == "" {
		token, source = d.Host.Getenv("FACTORY_TOKEN"), "FACTORY_TOKEN"
	}
	if token == "" {
		return CheckResult{Check: "core", Status: Fail, Detail: "no node token", Hint: "set FACTORY_NODE_TOKEN or node.yaml token_file (factory nodes token create), or the shared FACTORY_TOKEN"}
	}
	ctx, cancel := context.WithTimeout(ctx, RemoteTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return CheckResult{Check: "core", Status: Fail, Detail: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := d.HTTP
	if client == nil {
		client = &http.Client{Timeout: RemoteTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{Check: "core", Status: Fail, Detail: "unreachable: " + shown + ": " + rootCause(err),
			Hint: "start Core (make up) or fix FACTORY_URL; check the network path from this node"}
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return CheckResult{Check: "core", Status: Pass, Detail: shown + " healthy"}
	case http.StatusUnauthorized, http.StatusForbidden:
		return CheckResult{Check: "core", Status: Fail, Detail: fmt.Sprintf("%s: %d, %s rejected", shown, resp.StatusCode, source),
			Hint: "use a live node token of this machine (factory nodes token list) or the worker token Core was started with"}
	case http.StatusServiceUnavailable:
		return CheckResult{Check: "core", Status: Fail, Detail: shown + ": 503, Core cannot reach its database or NATS", Hint: "check Core's logs"}
	}
	return CheckResult{Check: "core", Status: Fail, Detail: fmt.Sprintf("%s: unexpected status %d", shown, resp.StatusCode), Hint: "is FACTORY_URL Core's API address?"}
}

// natsCheck connects once, without reconnects, with the node's NATS settings (nats.url, else NATS_URL; creds,
// nkey, TLS as the node uses them) and reads the JetStream account info; nothing is created or published.
// Skipped without nats.url and NATS_URL.
func (d Doctor) natsCheck(ctx context.Context) []CheckResult {
	raw := d.NATS.URL
	if raw == "" {
		raw = d.Host.Getenv("NATS_URL")
	}
	if raw == "" {
		return []CheckResult{{Check: "nats", Status: Skip, Detail: "neither nats.url nor NATS_URL set"}}
	}
	var r []CheckResult
	if loose := d.NATS.LooseFiles(d.Host.Stat); len(loose) > 0 {
		r = append(r, CheckResult{Check: "nats_credentials", Status: Warn, Detail: "readable by group or others: " + strings.Join(loose, ", "),
			Hint: "chmod 600 the NATS credentials, nkey seed and TLS key files"})
	}
	shown := bus.Redact(raw)
	nc, err := bus.Dial(raw, d.NATS.Options, nats.Name("factory-node-doctor"), nats.Timeout(RemoteTimeout), nats.NoReconnect())
	if err != nil {
		hint := "start NATS with JetStream (make up) or fix nats.url / NATS_URL"
		if errors.Is(err, nats.ErrAuthorization) || strings.Contains(strings.ToLower(err.Error()), "authorization") {
			hint = "the server rejected this node's user: check creds_file / nkey_seed_file and the server's users"
		}
		return append(r, CheckResult{Check: "nats", Status: Fail, Detail: shown + ": " + natsCause(err), Hint: hint})
	}
	defer nc.Close()
	return append(r, jetStreamCheck(ctx, nc, shown))
}

func jetStreamCheck(ctx context.Context, nc *nats.Conn, shown string) CheckResult {
	js, err := nc.JetStream()
	if err != nil {
		return CheckResult{Check: "nats", Status: Fail, Detail: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, RemoteTimeout)
	defer cancel()
	info, err := js.AccountInfo(nats.Context(ctx))
	switch {
	case errors.Is(err, nats.ErrJetStreamNotEnabled), errors.Is(err, nats.ErrJetStreamNotEnabledForAccount):
		return CheckResult{Check: "nats", Status: Fail, Detail: shown + ": JetStream not enabled", Hint: "run nats-server with -js"}
	case err != nil:
		return CheckResult{Check: "nats", Status: Fail, Detail: shown + ": JetStream account info: " + err.Error()}
	}
	return CheckResult{Check: "nats", Status: Pass, Detail: fmt.Sprintf("%s, JetStream: %d streams, %d consumers", shown, info.Streams, info.Consumers)}
}

// natsCause is the root cause of a dial error, prefixed "unreachable" for network errors.
func natsCause(err error) string {
	var oe *net.OpError
	if errors.As(err, &oe) || errors.Is(err, nats.ErrNoServers) {
		return "unreachable: " + rootCause(err)
	}
	return err.Error()
}

// rootCause drops the wrapping of url and net errors down to the last ": " part.
func rootCause(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}
