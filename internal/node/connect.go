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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"appfactory/internal/nodesig"
)

// Connect is one `factory-node login` (node-install D5): the node asks Core for a connection with new keys, the
// person approves it on the web after signing in, and the node learns its company.
type Connect struct {
	URL      string
	Name     string
	StateDir string
	Client   *http.Client
	Out      io.Writer
	// Open opens the approval page; nil prints it only.
	Open func(url string) error
	Poll time.Duration
	Now  func() time.Time
}

// ConnectAnswer is Core's answer to the request.
type ConnectAnswer struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	URL         string `json:"url"`
	ExpiresAt   int64  `json:"expires_at"`
	Fingerprint string `json:"fingerprint"`
}

// ConnectStatus is what became of the request.
type ConnectStatus struct {
	Status  string `json:"status"`
	Company string `json:"company"`
	Name    string `json:"name"`
}

// OpenBrowser opens url with the system's opener; an error when there is none or no display.
func OpenBrowser(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no display")
		}
		return exec.Command("xdg-open", u).Start()
	}
	return errors.New("no browser opener")
}

// Run asks for the connection and waits until it is approved, refused, expired or ctx ends. A node that already
// belongs to a company is refused: revoke it first.
func (c Connect) Run(ctx context.Context) (ConnectStatus, error) {
	var out ConnectStatus
	if err := CheckCoreURL(c.URL); err != nil {
		return out, err
	}
	if company, err := LoadCompany(c.StateDir); err != nil || company != "" {
		if err == nil {
			err = fmt.Errorf("this node already belongs to company %s", company)
		}
		return out, err
	}
	id, err := NodeID(c.StateDir)
	if err != nil {
		return out, err
	}
	keys, err := LoadKeys(c.StateDir)
	if errors.Is(err, ErrNoKeys) {
		keys, err = GenerateKeys(c.StateDir)
	}
	if err != nil {
		return out, err
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name, _ = os.Hostname()
		name, _, _ = strings.Cut(name, ".")
	}
	signPub, boxPub := hex.EncodeToString(keys.SignPublic()), hex.EncodeToString(keys.BoxPublic())
	body, err := json.Marshal(map[string]string{"node_id": id, "name": name, "os": runtime.GOOS, "arch": runtime.GOARCH, "sign_pub": signPub, "box_pub": boxPub,
		"proof": hex.EncodeToString(ed25519.Sign(keys.Sign, nodesig.ConnectMessage(id, name, signPub, boxPub)))})
	if err != nil {
		return out, err
	}
	e := Enrollment{URL: c.URL, Client: c.Client, Out: c.Out, Now: c.Now}
	base := strings.TrimRight(c.URL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/node-connects", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	var a ConnectAnswer
	if err = e.do(req, &a); err != nil {
		return out, fmt.Errorf("connect: %w", err)
	}
	if a.Fingerprint != keys.Fingerprint() {
		return out, fmt.Errorf("connect: Core answered fingerprint %q for keys with fingerprint %s", a.Fingerprint, keys.Fingerprint())
	}
	w := e.output()
	fmt.Fprintf(w, "Approve this machine (%s) in App Factory:\n\n  %s\n\nType the code %s on that page (or an owner runs: factory nodes connect %s %s). It expires in %d minutes.\n", name, a.URL, a.Code, a.ID, a.Code, max(1, (a.ExpiresAt-e.now().Unix())/60))
	if c.Open != nil {
		if err := c.Open(a.URL); err != nil {
			fmt.Fprintln(w, "Open the address above in a browser where you are signed in.")
		}
	}
	poll := c.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(poll):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/node-connects/"+url.PathEscape(a.ID)+"/status", nil)
		if err != nil {
			return out, err
		}
		if err = nodesig.Sign(req, id, keys.Sign, nil, e.now()); err != nil {
			return out, err
		}
		if err = e.do(req, &out); err != nil {
			// Core may restart while the person decides.
			fmt.Fprintf(w, "status: %v\n", err)
			continue
		}
		switch out.Status {
		case "approved":
			if err = writeDurable(filepath.Join(c.StateDir, CompanyFile), out.Company+"\n"); err != nil {
				return out, err
			}
			fmt.Fprintf(w, "Connected: node %s of company %s.\n", out.Name, out.Company)
			return out, nil
		case "rejected":
			return out, errors.New("the connection was cancelled on the web")
		case "expired":
			return out, errors.New("the connection request expired: run factory-node login again")
		}
	}
}
