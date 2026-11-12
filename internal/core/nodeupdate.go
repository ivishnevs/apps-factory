package core

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// Node updates, Core side (node-packaging-updates design C, tasks 2.2 and 3.4): Core offers each node that is due the
// package of the release it hands out (its own downloads, for the node's platform, with the package's sha256), in the
// poll and heartbeat answers. The company's policy (auto, the default, or approve), approvals, "update now" and
// retries are set from the Nodes page (and Telegram for approvals).

// nodeUpdate is the update node gets now (nil: none due, or this Core has no package for its platform).
func (c *Core) nodeUpdate(ctx context.Context, company, node string) (*protocol.NodeUpdate, error) {
	release := c.nodeRelease()
	if release == "" {
		return nil, nil
	}
	offer, platform, err := c.Store.NodeUpdateDue(ctx, company, node, release)
	if err != nil || offer == nil || platform == "" {
		return nil, err
	}
	name := "factory-node-" + platform + ".tar.gz"
	if !nodePackage.MatchString(name) {
		return nil, nil
	}
	sum := c.nodePackageSum(name)
	if sum == "" {
		return nil, nil
	}
	return &protocol.NodeUpdate{Version: offer.Version, URL: "/downloads/node/" + name, SHA256: sum, Attempt: offer.Attempt, Now: offer.Now}, nil
}

// nodePackageSum is the sha256 of package name in the downloads' SHA256SUMS ("" when it is not listed).
func (c *Core) nodePackageSum(name string) string {
	dir := c.nodeDownloads()
	if dir == "" {
		return ""
	}
	f, err := os.Open(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return ""
	}
	defer f.Close()
	for sc := bufio.NewScanner(f); sc.Scan(); {
		p := strings.Fields(sc.Text())
		if len(p) == 2 && strings.TrimPrefix(p[1], "*") == name && len(p[0]) == 64 {
			return strings.ToLower(p[0])
		}
	}
	return ""
}

func (c *Core) nodeUpdateRoutes(m *http.ServeMux) {
	// The company's node update policy and the release this Core hands out (members read; owners and admins set).
	m.HandleFunc("GET /settings/node-updates", func(w http.ResponseWriter, r *http.Request) {
		p, err := c.Store.NodeUpdatePolicy(r.Context(), c.companyFrom(r))
		reply(w, map[string]string{"policy": p, "release": c.nodeRelease()}, err)
	})
	m.HandleFunc("PUT /settings/node-updates", func(w http.ResponseWriter, r *http.Request) {
		if _, agent := r.Context().Value(runKey{}).(protocol.RunSpec); agent {
			http.Error(w, "people only", http.StatusForbidden)
			return
		}
		var p struct {
			Policy string `json:"policy"`
		}
		if !decode(w, r, &p) {
			return
		}
		company := c.companyFrom(r)
		err := c.Store.SetNodeUpdatePolicy(r.Context(), company, p.Policy, author(r), c.personName(r, company))
		reply(w, map[string]string{"policy": p.Policy, "release": c.nodeRelease()}, err)
	})
	// Owners and admins: approve the node's update to the release (or retry one that failed); now: at once, the drain
	// interrupts the node's live runs, which are queued again.
	m.HandleFunc("POST /nodes/{id}/update", func(w http.ResponseWriter, r *http.Request) {
		if _, agent := r.Context().Value(runKey{}).(protocol.RunSpec); agent {
			http.Error(w, "people only", http.StatusForbidden)
			return
		}
		var p struct {
			Now bool `json:"now,omitempty"`
		}
		if !decode(w, r, &p) {
			return
		}
		company := c.companyFrom(r)
		n, err := c.Store.ApproveNodeUpdate(r.Context(), company, r.PathValue("id"), c.nodeRelease(), p.Now, author(r), c.personName(r, company))
		reply(w, map[string]any{"version": n.UpdateApproved, "now": n.UpdateNow != ""}, err)
	})
}

// personName is the signed-in person's name in company ("" for a token).
func (c *Core) personName(r *http.Request, company string) string {
	if pr := principalOf(r); pr.Kind == "session" {
		return c.personOf(r.Context(), company, pr.User).Name
	}
	return ""
}

// nodeUpdateQuestion is the Telegram button's target of node_update_available (the node's id after the prefix).
const nodeUpdateQuestion = "node-update:"

// approveNodeUpdate approves a node's update from the button of node_update_available.
func (b telegramBackend) approveNodeUpdate(ctx context.Context, node string) (string, error) {
	release := b.c.nodeRelease()
	p := storage.PersonFrom(ctx)
	n, err := b.c.Store.ApproveNodeUpdate(ctx, b.company, node, release, false, "telegram", cmpOr(p.Name, p.ID))
	if err != nil {
		return "", err
	}
	return "Approved: node " + cmpOr(n.Name, node) + " updates to " + release + " after its live runs.", nil
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
