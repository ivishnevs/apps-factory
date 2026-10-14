package node

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Node identity of runtime protocol v2 §2: <state_dir>/node_id is generated once and never changes;
// <state_dir>/epoch grows by one on every node start and is durable before register.

var nodeIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NodeID reads <dir>/node_id, generating it on first use. A damaged file is an error, never a new identity.
func NodeID(dir string) (string, error) {
	path := filepath.Join(dir, "node_id")
	b, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(b))
		if !nodeIDPattern.MatchString(id) {
			return "", fmt.Errorf("%s: invalid node id %q", path, id)
		}
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	r := make([]byte, 16)
	if _, err = rand.Read(r); err != nil {
		return "", err
	}
	id := hex.EncodeToString(r)
	return id, writeDurable(path, id+"\n")
}

// NextEpoch increments <dir>/epoch (0 when missing) and returns the new value once it is on disk.
func NextEpoch(dir string) (int64, error) {
	path := filepath.Join(dir, "epoch")
	var epoch int64
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if epoch, err = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err != nil || epoch < 0 {
			return 0, fmt.Errorf("%s: invalid epoch %q", path, strings.TrimSpace(string(b)))
		}
	case !errors.Is(err, os.ErrNotExist):
		return 0, err
	}
	epoch++
	return epoch, writeDurable(path, strconv.FormatInt(epoch, 10)+"\n")
}

// writeDurable replaces path atomically and fsyncs the file and its directory.
func writeDurable(path, body string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(body); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
