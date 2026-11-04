package node

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"appfactory/internal/nodesig"
)

// RemoteTimeout bounds the Core check.
const RemoteTimeout = 5 * time.Second

// core checks that Core answers an authenticated GET /health (cheap, read-only; 200 also means Core reaches its
// database) with a request signed by the node's keys. Skipped without FACTORY_URL.
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
	keys, err := LoadKeys(d.StateDir)
	if err != nil {
		return CheckResult{Check: "core", Status: Fail, Detail: "no usable node keys: " + err.Error(), Hint: "enroll this node with factory-node enroll"}
	}
	id, err := NodeID(d.StateDir)
	if err != nil {
		return CheckResult{Check: "core", Status: Fail, Detail: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, RemoteTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return CheckResult{Check: "core", Status: Fail, Detail: err.Error()}
	}
	if err = nodesig.Sign(req, id, keys.Sign, nil, time.Now()); err != nil {
		return CheckResult{Check: "core", Status: Fail, Detail: err.Error()}
	}
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
		if c, bad := clockOffsetCheck(resp.Header.Get("Date"), time.Now()); bad {
			return c
		}
		return CheckResult{Check: "core", Status: Pass, Detail: shown + " healthy"}
	case http.StatusUnauthorized, http.StatusForbidden:
		return CheckResult{Check: "core", Status: Fail, Detail: fmt.Sprintf("%s: %d, this node's signature was refused", shown, resp.StatusCode),
			Hint: "check that an owner approved this node, that it was not revoked, and the machine's clock"}
	case http.StatusServiceUnavailable:
		return CheckResult{Check: "core", Status: Fail, Detail: shown + ": 503, Core cannot reach its database", Hint: "check Core's logs"}
	}
	return CheckResult{Check: "core", Status: Fail, Detail: fmt.Sprintf("%s: unexpected status %d", shown, resp.StatusCode), Hint: "is FACTORY_URL Core's API address?"}
}

// clockOffsetCheck compares this machine's clock with Core's Date header (node-identity D4): Core refuses a signature
// created more than nodesig.Window from its clock. The second result says the check is not a pass.
func clockOffsetCheck(date string, now time.Time) (CheckResult, bool) {
	at, err := http.ParseTime(date)
	if err != nil {
		return CheckResult{}, false
	}
	offset := now.Sub(at).Round(time.Second)
	if offset < 0 {
		offset = -offset
	}
	hint := "set this machine's clock: Core refuses signed requests more than " + nodesig.Window.String() + " away from its own clock"
	switch {
	case offset > nodesig.Window:
		return CheckResult{Check: "core", Status: Fail, Detail: "clock is " + offset.String() + " from Core", Hint: hint}, true
	case offset > nodesig.Window/2:
		return CheckResult{Check: "core", Status: Warn, Detail: "clock is " + offset.String() + " from Core", Hint: hint}, true
	}
	return CheckResult{}, false
}

// rootCause drops the wrapping of url and net errors down to the last ": " part.
func rootCause(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}
