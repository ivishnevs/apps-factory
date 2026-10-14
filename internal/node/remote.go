package node

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// RemoteTimeout bounds each Core and NATS check.
const RemoteTimeout = 5 * time.Second

// core checks that Core answers an authenticated GET /health (cheap, read-only; 200 also means Core reaches its
// database and NATS) with FACTORY_TOKEN. Skipped without FACTORY_URL.
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
	token := d.Host.Getenv("FACTORY_TOKEN")
	if token == "" {
		return CheckResult{Check: "core", Status: Fail, Detail: "FACTORY_TOKEN not set", Hint: "export FACTORY_TOKEN (the worker token Core was started with)"}
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
		return CheckResult{Check: "core", Status: Fail, Detail: fmt.Sprintf("%s: %d, FACTORY_TOKEN rejected", shown, resp.StatusCode),
			Hint: "use the worker token Core was started with"}
	case http.StatusServiceUnavailable:
		return CheckResult{Check: "core", Status: Fail, Detail: shown + ": 503, Core cannot reach its database or NATS", Hint: "check Core's logs"}
	}
	return CheckResult{Check: "core", Status: Fail, Detail: fmt.Sprintf("%s: unexpected status %d", shown, resp.StatusCode), Hint: "is FACTORY_URL Core's API address?"}
}

// natsCheck connects to NATS_URL once, without reconnects, and reads the JetStream account info; nothing is
// created or published. Skipped without NATS_URL.
func (d Doctor) natsCheck(ctx context.Context) CheckResult {
	raw := d.Host.Getenv("NATS_URL")
	if raw == "" {
		return CheckResult{Check: "nats", Status: Skip, Detail: "NATS_URL not set"}
	}
	shown := raw
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		shown = u.Redacted()
	}
	nc, err := nats.Connect(raw, nats.Name("factory-node-doctor"), nats.Timeout(RemoteTimeout), nats.NoReconnect())
	if err != nil {
		return CheckResult{Check: "nats", Status: Fail, Detail: "unreachable: " + shown + ": " + rootCause(err),
			Hint: "start NATS with JetStream (make up) or fix NATS_URL"}
	}
	defer nc.Close()
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

// rootCause drops the wrapping of url and net errors down to the last ": " part.
func rootCause(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}
