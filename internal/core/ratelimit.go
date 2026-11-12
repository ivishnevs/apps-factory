package core

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rate limits on the public routes (saas-release-readiness 2.3): the routes served without a credential (sign-in,
// the invitation preview, a node's sign-in and enrollment, the installer and the node downloads) are limited per
// client address and overall, per minute. Core keeps the counts in memory: one Core process serves a company's
// traffic, and a restart forgets them, which only lets more through for a minute. Forwarded addresses can be
// forged (Core trusts the first X-Forwarded-For entry its proxy passes on), so the overall limit is what holds
// against a flood; the per-address limit keeps one noisy client from using it up. A refused request gets 429 with
// Retry-After.

// rateClass is one group of public routes with its limits per minute.
type rateClass struct {
	perAddress, total int
	// what names the routes in the refusal.
	what string
}

var rateClasses = map[string]rateClass{
	// The sign-in: providers, login, callback, logout. A sign-in takes two or three requests.
	"auth": {perAddress: 60, total: 1200, what: "sign-in requests"},
	// An invitation link's preview (GET /auth/invitations/<token>); tokens have 128 random bits, this protects Core.
	"invitation": {perAddress: 20, total: 300, what: "invitation previews"},
	// A node's enrollment and connection request (node-identity D3): proofs of new keys, each a stored row.
	"enroll": {perAddress: enrollPerAddressPerMinute, total: enrollPerMinute, what: "connection requests"},
	// A waiting node's status polls (every 2 to 5 s while a person decides).
	"node-status": {perAddress: 120, total: 3000, what: "status requests"},
	// The install script and the node packages.
	"download": {perAddress: 30, total: 600, what: "downloads"},
}

// publicRateClass names the rate class of a request to a public route, "" for any other route.
func publicRateClass(r *http.Request) string {
	p := r.URL.Path
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case strings.HasPrefix(p, "/auth/invitations/"):
		return "invitation"
	case strings.HasPrefix(p, "/auth/"):
		return "auth"
	case r.Method == http.MethodPost && (p == "/nodes/enroll" || p == "/node-connects"):
		return "enroll"
	case r.Method == http.MethodGet && len(parts) == 3 && ((parts[0] == "node-connects" && parts[2] == "status") || (parts[0] == "nodes" && parts[2] == "enrollment")):
		return "node-status"
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && (p == "/install.sh" || strings.HasPrefix(p, "/downloads/")):
		return "download"
	}
	return ""
}

// rateLimiter counts one class's requests in the current minute, per address and overall.
type rateLimiter struct {
	perAddress, total int
	mu                sync.Mutex
	minute            int64
	count             int
	byAddr            map[string]int
}

func (l *rateLimiter) allow(addr string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if m := now.Unix() / 60; m != l.minute || l.byAddr == nil {
		l.minute, l.count, l.byAddr = m, 0, map[string]int{}
	}
	if l.count >= l.total || l.byAddr[addr] >= l.perAddress {
		return false
	}
	l.count++
	l.byAddr[addr]++
	return true
}

// publicLimits holds a limiter per class; the zero value is ready.
type publicLimits struct {
	once    sync.Once
	byClass map[string]*rateLimiter
}

func (p *publicLimits) allow(class, addr string, now time.Time) bool {
	p.once.Do(func() {
		p.byClass = map[string]*rateLimiter{}
		for name, c := range rateClasses {
			p.byClass[name] = &rateLimiter{perAddress: c.perAddress, total: c.total}
		}
	})
	l := p.byClass[class]
	return l == nil || l.allow(addr, now)
}

// rateLimited answers 429 and reports true when r is a public route over its limit.
func (c *Core) rateLimited(w http.ResponseWriter, r *http.Request) bool {
	class := publicRateClass(r)
	if class == "" {
		return false
	}
	now := time.Now()
	if c.Store != nil {
		now = c.Store.Now()
	}
	if c.limits.allow(class, clientAddress(r), now) {
		return false
	}
	w.Header().Set("Retry-After", "60")
	jsonStatus(w, http.StatusTooManyRequests, map[string]string{"error": "too many " + rateClasses[class].what + ": try again in a minute", "reason": "rate_limited"})
	return true
}
