package node

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"appfactory/internal/nodesig"
)

// Node keys (node-identity D1): <state_dir>/keys holds the private keys that prove who this node is. They are
// created once by enrollment and never leave the machine; Core stores only the public keys.
const (
	// KeysDir is the directory of the private keys under the node's state_dir.
	KeysDir = "keys"
	// SignKeyFile and BoxKeyFile have distinctive names so the node-keys policy can match them in a command line.
	SignKeyFile = "factory-node-sign.key"
	BoxKeyFile  = "factory-node-box.key"
)

// ErrNoKeys: the node has no keys yet and must enroll.
var ErrNoKeys = errors.New("node keys not found: enroll this node with factory-node enroll")

// Keys are a node's private keys: Sign signs its HTTP requests, Box opens what Core seals for it.
type Keys struct {
	Sign ed25519.PrivateKey
	Box  *ecdh.PrivateKey
}

// GenerateKeys creates the node's keys under dir (a state_dir). It refuses to replace existing keys: a new key
// pair is a new identity, which Core accepts only through enrollment or a confirmed rotation.
func GenerateKeys(dir string) (Keys, error) {
	keys := filepath.Join(dir, KeysDir)
	for _, name := range []string{SignKeyFile, BoxKeyFile} {
		if _, err := os.Lstat(filepath.Join(keys, name)); err == nil {
			return Keys{}, fmt.Errorf("%s already exists: this node already has keys", filepath.Join(keys, name))
		} else if !errors.Is(err, os.ErrNotExist) {
			return Keys{}, err
		}
	}
	_, sign, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Keys{}, err
	}
	box, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Keys{}, err
	}
	// writeDurable creates the directory 0700 and the file 0600.
	if err = writeDurable(filepath.Join(keys, SignKeyFile), hex.EncodeToString(sign.Seed())+"\n"); err != nil {
		return Keys{}, err
	}
	if err = writeDurable(filepath.Join(keys, BoxKeyFile), hex.EncodeToString(box.Bytes())+"\n"); err != nil {
		return Keys{}, err
	}
	return Keys{Sign: sign, Box: box}, nil
}

// LoadKeys reads the node's keys from dir (a state_dir). No keys at all is ErrNoKeys; a key file others may access
// is refused. Errors name the file, never its content.
func LoadKeys(dir string) (Keys, error) {
	keys := filepath.Join(dir, KeysDir)
	signPath, boxPath := filepath.Join(keys, SignKeyFile), filepath.Join(keys, BoxKeyFile)
	_, signErr := os.Lstat(signPath)
	_, boxErr := os.Lstat(boxPath)
	if errors.Is(signErr, os.ErrNotExist) && errors.Is(boxErr, os.ErrNotExist) {
		return Keys{}, ErrNoKeys
	}
	seed, err := readKeyFile(signPath, ed25519.SeedSize)
	if err != nil {
		return Keys{}, err
	}
	b, err := readKeyFile(boxPath, 32)
	if err != nil {
		return Keys{}, err
	}
	box, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return Keys{}, fmt.Errorf("%s: invalid key", boxPath)
	}
	return Keys{Sign: ed25519.NewKeyFromSeed(seed), Box: box}, nil
}

func readKeyFile(path string, size int) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s has mode %04o: it must be readable by its owner only (chmod 600)", path, st.Mode().Perm())
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(text)))
	if err != nil || len(b) != size {
		return nil, fmt.Errorf("%s: invalid key", path)
	}
	return b, nil
}

// SignPublic is the public part of the signing key.
func (k Keys) SignPublic() ed25519.PublicKey { return k.Sign.Public().(ed25519.PublicKey) }

// BoxPublic is the public part of the encryption key.
func (k Keys) BoxPublic() []byte { return k.Box.PublicKey().Bytes() }

// Fingerprint is what the owner compares with Core's view before approving the node.
func (k Keys) Fingerprint() string { return nodesig.Fingerprint(k.SignPublic(), k.BoxPublic()) }

// keysCheck is the doctor's view of the node's keys. Missing keys only warn until enrollment is required.
func keysCheck(dir string) CheckResult {
	if dir == "" {
		return CheckResult{Check: "node.keys", Status: Skip, Detail: "no state_dir"}
	}
	k, err := LoadKeys(dir)
	switch {
	case errors.Is(err, ErrNoKeys):
		return CheckResult{Check: "node.keys", Status: Warn, Detail: "no keys yet", Hint: "enroll this node with factory-node enroll"}
	case err != nil:
		return CheckResult{Check: "node.keys", Status: Fail, Detail: err.Error(), Hint: "fix the file or enroll this node again"}
	}
	return CheckResult{Check: "node.keys", Status: Pass, Detail: "fingerprint " + k.Fingerprint()}
}
