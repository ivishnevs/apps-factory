package core

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"appfactory/internal/nodesig"
	"appfactory/internal/storage"
)

// Connection by sign-in and node downloads (node-install D3, D5). Both are served before the token gate: a node
// asking for a connection has no credential yet, and a machine installing a node has none at all.

// NodeConnectAnswer is what a node learns when it asks for a connection.
type NodeConnectAnswer struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expires_at"`
	// Fingerprint is the fingerprint of the keys Core received.
	Fingerprint string `json:"fingerprint"`
}

// NodeConnectStatus answers a node's signed status request.
type NodeConnectStatus struct {
	Status  string `json:"status"`
	Company string `json:"company,omitempty"`
	Name    string `json:"name"`
}

func (c *Core) createNodeConnect(w http.ResponseWriter, r *http.Request) {
	if !c.enrollLimit.allow(clientAddress(r), c.Store.Now()) {
		http.Error(w, "too many connection requests: try again in a minute", http.StatusTooManyRequests)
		return
	}
	var in storage.NodeConnectRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sign, _, err := nodesig.DecodePublicKeys(in.SignPub, in.BoxPub)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	proof, err := hex.DecodeString(in.Proof)
	if err != nil || len(proof) != ed25519.SignatureSize || !ed25519.Verify(sign, nodesig.ConnectMessage(in.NodeID, in.Name, in.SignPub, in.BoxPub), proof) {
		http.Error(w, "proof of the signing key is not valid", http.StatusBadRequest)
		return
	}
	v, err := c.Store.CreateNodeConnect(r.Context(), in, clientAddress(r))
	if errors.Is(err, storage.ErrNodeIDTaken) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	public := strings.TrimRight(c.PublicURL, "/")
	if public == "" {
		public = "http://127.0.0.1:3000"
	}
	write(w, NodeConnectAnswer{ID: v.ID, Code: v.Code, URL: public + "/connect/" + v.ID, ExpiresAt: v.ExpiresAt, Fingerprint: v.Fingerprint})
}

// nodeConnectStatus tells the key holder of a request, by a request signed with that key, what became of it.
func (c *Core) nodeConnectStatus(w http.ResponseWriter, r *http.Request, id string) {
	v, err := c.Store.NodeConnectOf(r.Context(), id)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	pub, err := hex.DecodeString(v.SignPub)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<10))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s, err := nodesig.Verify(r, ed25519.PublicKey(pub), body, c.Store.Now()); err != nil || s.Node != v.NodeID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	write(w, NodeConnectStatus{Status: v.Status, Company: v.Company, Name: v.Name})
}

//go:embed install.sh
var installScript string

var installTemplate = template.Must(template.New("install.sh").Delims("{{{", "}}}").Parse(installScript))

// nodeURL is Core's address for nodes: FACTORY_CORE_URL, else the scheme and host of r.
func (c *Core) nodeURL(r *http.Request) string {
	if c.NodeURL != "" {
		return strings.TrimRight(c.NodeURL, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p == "https" || p == "http" {
		scheme = p
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

func (c *Core) nodeDownloads() string {
	if c.NodeDownloads != "" {
		return c.NodeDownloads
	}
	if c.Root == "" {
		return ""
	}
	return filepath.Join(c.Root, "dist", "node")
}

// nodeRelease is the VERSION of the node packages this Core hands out ("" when it has none).
func (c *Core) nodeRelease() string {
	dir := c.nodeDownloads()
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

var nodePackage = regexp.MustCompile(`^factory-node-(darwin|linux)-(arm64|amd64)\.tar\.gz$`)

// nodePackages lists the platforms this Core can hand out, like darwin-arm64.
func (c *Core) nodePackages() []string {
	out := []string{}
	dir := c.nodeDownloads()
	if dir == "" {
		return out
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if m := nodePackage.FindStringSubmatch(e.Name()); m != nil {
			out = append(out, m[1]+"-"+m[2])
		}
	}
	sort.Strings(out)
	return out
}

var hostPattern = regexp.MustCompile(`^https?://[A-Za-z0-9.\-\[\]:]+$`)

func (c *Core) serveNodeDownload(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/install.sh" {
		base := c.nodeURL(r)
		if !hostPattern.MatchString(base) {
			http.Error(w, "Core's address for nodes is not valid: set FACTORY_CORE_URL", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = installTemplate.Execute(w, map[string]string{"CoreURL": base})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/downloads/node/")
	dir := c.nodeDownloads()
	if dir == "" || (name != "SHA256SUMS" && !nodePackage.MatchString(name)) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "this Core has no node package "+name+": build them with scripts/package-node.sh", http.StatusNotFound)
		return
	}
	if name == "SHA256SUMS" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/gzip")
	}
	http.ServeFile(w, r, path)
}
