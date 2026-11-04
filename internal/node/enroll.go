package node

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"appfactory/internal/nodesig"
)

// CompanyFile holds the company Core named at enrollment (node-identity D2).
const CompanyFile = "company"

// Enrollment is one `factory-node enroll`.
type Enrollment struct {
	URL      string
	Code     string
	StateDir string
	Client   *http.Client
	// Out receives what the owner must see: the fingerprint and the wait.
	Out io.Writer
	// Poll is the wait between status requests; zero is five seconds.
	Poll time.Duration
	// Now is the clock of signatures; nil is the system clock.
	Now func() time.Time
}

// EnrollmentStatus is Core's answer about this node's keys.
type EnrollmentStatus struct {
	NodeID      string `json:"node_id"`
	Company     string `json:"company"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Status      string `json:"status"`
	// NextFingerprint names keys a rotation proposes, still waiting for an owner.
	NextFingerprint string `json:"next_fingerprint,omitempty"`
}

// Run registers this node's keys, creating them on first use, and waits until an owner approves them or ctx ends.
// A code refused by Core keeps the keys, so the owner can issue a new code without a new identity.
func (e Enrollment) Run(ctx context.Context) (EnrollmentStatus, error) {
	var out EnrollmentStatus
	if err := CheckCoreURL(e.URL); err != nil {
		return out, err
	}
	id, err := NodeID(e.StateDir)
	if err != nil {
		return out, err
	}
	keys, err := LoadKeys(e.StateDir)
	if errors.Is(err, ErrNoKeys) {
		keys, err = GenerateKeys(e.StateDir)
	}
	if err != nil {
		return out, err
	}
	signPub, boxPub := hex.EncodeToString(keys.SignPublic()), hex.EncodeToString(keys.BoxPublic())
	body, err := json.Marshal(map[string]string{"code": e.Code, "node_id": id, "sign_pub": signPub, "box_pub": boxPub,
		"proof": hex.EncodeToString(ed25519.Sign(keys.Sign, nodesig.EnrollMessage(e.Code, id, signPub, boxPub)))})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.URL, "/")+"/nodes/enroll", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err = e.do(req, &out); err != nil {
		return out, fmt.Errorf("enroll: %w", err)
	}
	if out.Company == "" || out.Fingerprint != keys.Fingerprint() {
		return out, fmt.Errorf("enroll: Core answered fingerprint %q for keys with fingerprint %s", out.Fingerprint, keys.Fingerprint())
	}
	if err = writeDurable(filepath.Join(e.StateDir, CompanyFile), out.Company+"\n"); err != nil {
		return out, err
	}
	fmt.Fprintf(e.output(), "node %s of company %s\nfingerprint: %s\nwaiting for an owner to approve it: compare this fingerprint on the Nodes page or run factory nodes approve %s %s\n",
		out.Name, out.Company, out.Fingerprint, id, out.Fingerprint)
	poll := e.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	for out.Status != "active" {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(poll):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(e.URL, "/")+"/nodes/"+url.PathEscape(id)+"/enrollment", nil)
		if err != nil {
			return out, err
		}
		if err = nodesig.Sign(req, id, keys.Sign, nil, e.now()); err != nil {
			return out, err
		}
		err = e.do(req, &out)
		var status *enrollHTTPError
		switch {
		case errors.As(err, &status) && status.code == http.StatusUnauthorized:
			return out, errors.New("Core no longer knows these keys: the owner rejected them or they waited too long; ask for a new code")
		case err != nil:
			// Core may restart while the owner decides.
			fmt.Fprintf(e.output(), "status: %v\n", err)
		}
	}
	fmt.Fprintln(e.output(), "approved")
	return out, nil
}

type enrollHTTPError struct {
	code int
	text string
}

func (e *enrollHTTPError) Error() string { return fmt.Sprintf("%d: %s", e.code, e.text) }

func (e Enrollment) do(req *http.Request, result any) error {
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: RemoteTimeout}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return &enrollHTTPError{res.StatusCode, strings.TrimSpace(string(b))}
	}
	return json.Unmarshal(b, result)
}

func (e Enrollment) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Enrollment) output() io.Writer {
	if e.Out != nil {
		return e.Out
	}
	return io.Discard
}

// CheckCoreURL refuses to send a node's requests to Core in the clear, except on this machine (node-identity D4).
func CheckCoreURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("Core URL %q is not valid", raw)
	}
	host := u.Hostname()
	if u.Scheme == "https" || (u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
		return nil
	}
	return fmt.Errorf("Core URL %s must be https outside this machine", u.Redacted())
}

// LoadCompany reads the company stored at enrollment; "" when the node has not enrolled.
func LoadCompany(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, CompanyFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	company := strings.TrimSpace(string(b))
	if !companyName.MatchString(company) {
		return "", fmt.Errorf("%s: invalid company %q", filepath.Join(dir, CompanyFile), company)
	}
	return company, nil
}

// Rotation is one `factory-node rotate-keys` (node-identity D5).
type Rotation struct {
	URL      string
	StateDir string
	Client   *http.Client
	Out      io.Writer
	Poll     time.Duration
	Now      func() time.Time
}

// Run proposes new keys with a request signed by the current key and waits for an owner. On approval the new keys
// replace the current ones on disk, and a running node signs with them from its next request. A refusal removes
// the proposal. Running it again continues a proposal that is still waiting.
func (r Rotation) Run(ctx context.Context) (EnrollmentStatus, error) {
	var out EnrollmentStatus
	e := Enrollment{URL: r.URL, StateDir: r.StateDir, Client: r.Client, Out: r.Out, Poll: r.Poll, Now: r.Now}
	if err := CheckCoreURL(r.URL); err != nil {
		return out, err
	}
	id, err := NodeID(r.StateDir)
	if err != nil {
		return out, err
	}
	current, err := LoadKeys(r.StateDir)
	if err != nil {
		return out, err
	}
	nextDir := filepath.Join(r.StateDir, KeysDir, RotatingDir)
	next, err := LoadKeys(nextDir)
	if errors.Is(err, ErrNoKeys) {
		next, err = GenerateKeys(nextDir)
	}
	if err != nil {
		return out, err
	}
	base := strings.TrimRight(r.URL, "/") + "/nodes/" + url.PathEscape(id)
	body, err := json.Marshal(map[string]string{"sign_pub": hex.EncodeToString(next.SignPublic()), "box_pub": hex.EncodeToString(next.BoxPublic())})
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/keys", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err = nodesig.Sign(req, id, current.Sign, body, e.now()); err != nil {
		return out, err
	}
	if err = e.do(req, &out); err != nil {
		return out, fmt.Errorf("rotate: %w", err)
	}
	fmt.Fprintf(e.output(), "new fingerprint: %s\nwaiting for an owner to approve it: compare it on the Nodes page or run factory nodes approve %s %s\n", next.Fingerprint(), id, next.Fingerprint())
	poll := r.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	status := func(key ed25519.PrivateKey) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/enrollment", nil)
		if err != nil {
			return err
		}
		if err = nodesig.Sign(req, id, key, nil, e.now()); err != nil {
			return err
		}
		// A fresh answer: next_fingerprint is left out once the proposal is gone.
		var answer EnrollmentStatus
		if err = e.do(req, &answer); err != nil {
			return err
		}
		out = answer
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(poll):
		}
		err := status(current.Sign)
		var refused *enrollHTTPError
		switch {
		case errors.As(err, &refused) && refused.code == http.StatusUnauthorized:
			// Core stopped accepting the current key: either the new keys are active, or this node is revoked.
			if err = status(next.Sign); err != nil {
				return out, fmt.Errorf("Core accepts neither key: this node was revoked; enroll it again: %w", err)
			}
			if err = moveRotatedKeys(r.StateDir); err != nil {
				return out, err
			}
			fmt.Fprintln(e.output(), "approved")
			return out, nil
		case err != nil:
			fmt.Fprintf(e.output(), "status: %v\n", err)
		case out.NextFingerprint != next.Fingerprint():
			if rmErr := os.RemoveAll(nextDir); rmErr != nil {
				return out, rmErr
			}
			return out, errors.New("the new keys were rejected or waited too long; the current keys stay")
		}
	}
}

// moveRotatedKeys puts approved keys in place. The signing key moves last, and a running node reloads on it.
func moveRotatedKeys(dir string) error {
	keys := filepath.Join(dir, KeysDir)
	next := filepath.Join(keys, RotatingDir, KeysDir)
	for _, name := range []string{BoxKeyFile, SignKeyFile} {
		if err := os.Rename(filepath.Join(next, name), filepath.Join(keys, name)); err != nil {
			return err
		}
	}
	return os.RemoveAll(filepath.Join(keys, RotatingDir))
}
