// Package redact replaces credential values in run output before a node uploads it (core-owned-project-data
// D6): the exact values the node knows (environment secrets, its tokens, the run credential) and the common
// token shapes a secret scanner looks for. It is a safety net for logs and attachments, not DLP.
package redact

import (
	"bytes"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Marker replaces every redacted value.
const Marker = "[REDACTED]"

// MinValue is the shortest value replaced by exact match; shorter ones would destroy ordinary text.
const MinValue = 8

// patterns are token shapes of the usual providers (GitHub, GitLab, OpenAI/Anthropic-style keys, Slack, AWS)
// and private key blocks.
var patterns = []*regexp.Regexp{
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
}

// Redactor replaces known values (longest first) and token patterns.
type Redactor struct{ values [][]byte }

// New keeps the distinct values of at least MinValue bytes.
func New(values ...string) *Redactor {
	seen := map[string]bool{}
	r := &Redactor{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len(v) < MinValue || seen[v] {
			continue
		}
		seen[v] = true
		r.values = append(r.values, []byte(v))
	}
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
	return r
}

// Values are the exact values (for handing them to the run's MCP adapter).
func (r *Redactor) Values() []string {
	out := make([]string, len(r.values))
	for i, v := range r.values {
		out[i] = string(v)
	}
	return out
}

// Bytes returns b with credentials replaced and whether anything was replaced.
func (r *Redactor) Bytes(b []byte) ([]byte, bool) {
	changed := false
	for _, v := range r.values {
		if bytes.Contains(b, v) {
			b, changed = bytes.ReplaceAll(b, v, []byte(Marker)), true
		}
	}
	for _, p := range patterns {
		if p.Match(b) {
			b, changed = p.ReplaceAll(b, []byte(Marker)), true
		}
	}
	return b, changed
}

// credentialName: variable names that carry credentials.
var credentialName = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|PASSPHRASE|CREDENTIAL|PRIVATE|API_?KEY|ACCESS_?KEY|AUTH)`)

// FromEnv are the credential values of an environment (NAME=value entries): variables named like
// credentials, and the password of URLs with user info (NATS_URL, database URLs).
func FromEnv(env []string) []string {
	var out []string
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok || v == "" {
			continue
		}
		if credentialName.MatchString(k) {
			out = append(out, v)
		}
		for _, field := range strings.Split(v, ",") {
			if u, err := url.Parse(strings.TrimSpace(field)); err == nil && u.User != nil {
				if p, ok := u.User.Password(); ok {
					out = append(out, p)
				}
			}
		}
	}
	return out
}
