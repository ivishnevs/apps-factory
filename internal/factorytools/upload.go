package factorytools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"appfactory/internal/redact"
)

// MaxUpload bounds one uploaded file (an artifact version or a report attachment), as Core does.
const MaxUpload = 64 << 20

// Uploads (core-owned-project-data D4, D6): factory_artifact_upload and factory_report_create attachments name
// files of the run's worktree; the adapter streams them to Core (/agent/blobs) and forwards the call with
// their digests. Core never reads the node's files. Attachments are redacted first; artifact versions are
// uploaded as they are (their digest is what the owner approves).

// upload replaces the file paths of c with uploaded blobs.
func (a *Adapter) upload(ctx context.Context, c *Call) error {
	if c.Arguments == nil {
		return nil
	}
	switch c.Name {
	case "factory_artifact_upload":
		p, _ := c.Arguments["path"].(string)
		b, err := a.readFile(p)
		if err != nil {
			return err
		}
		blob, err := a.putBlob(ctx, b)
		if err != nil {
			return err
		}
		c.Arguments["sha256"], c.Arguments["size"] = blob.SHA256, blob.Size
	case "factory_report_create":
		list, _ := c.Arguments["attachments"].([]any)
		for _, item := range list {
			att, ok := item.(map[string]any)
			if !ok {
				continue // Core's validation answers
			}
			p, _ := att["path"].(string)
			b, err := a.readFile(p)
			if err != nil {
				return err
			}
			b, redacted := a.redactor().Bytes(b)
			blob, err := a.putBlob(ctx, b)
			if err != nil {
				return err
			}
			att["sha256"], att["size"] = blob.SHA256, blob.Size
			if redacted {
				att["redacted"] = true
			}
		}
	}
	return nil
}

// readFile reads a regular file of the worktree, confined to it (no traversal or symlink escape).
func (a *Adapter) readFile(p string) ([]byte, error) {
	if a.Workdir == "" || p == "" {
		return nil, errors.New("path of a file in your worktree required")
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(a.Workdir, p)
		if err != nil {
			return nil, err
		}
		p = rel
	}
	root, err := os.OpenRoot(a.Workdir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.Clean(p))
	if err != nil {
		return nil, fmt.Errorf("%s: %w (a file inside your worktree is required)", p, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxUpload {
		return nil, fmt.Errorf("%s: a regular file of at most %d bytes is required", p, MaxUpload)
	}
	return io.ReadAll(io.LimitReader(f, MaxUpload+1))
}

type uploaded struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func (a *Adapter) putBlob(ctx context.Context, b []byte) (uploaded, error) {
	var out uploaded
	req, err := http.NewRequestWithContext(ctx, "PUT", strings.TrimRight(a.URL, "/")+"/agent/blobs", bytes.NewReader(b))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := a.client().Do(req)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode != 200 {
		return out, fmt.Errorf("upload: Core HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return out, json.Unmarshal(body, &out)
}

// redactor replaces the credential values the node handed the adapter (FACTORY_REDACT_FILE) and token shapes.
func (a *Adapter) redactor() *redact.Redactor {
	return redact.New(append([]string{a.Token}, a.Redact...)...)
}
