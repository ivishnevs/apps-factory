// Package secrets keeps company secrets encrypted in Core (core-secret-store D1): a master key from Core's
// environment wraps one random key per company, and each value is AES-256-GCM under its company key with the
// company, name and version as additional data, so a ciphertext moved to another row does not decrypt.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Environment variables of the master key (D1).
const (
	MasterKeyEnv    = "FACTORY_SECRETS_KEY"
	NewMasterKeyEnv = "FACTORY_SECRETS_KEY_NEW"
)

// KeySize is the size of master and company keys.
const KeySize = 32

// ErrNoMasterKey: Core runs without FACTORY_SECRETS_KEY; secret routes and deliveries are unavailable.
var ErrNoMasterKey = errors.New("no master key: set " + MasterKeyEnv + " on Core")

// ErrDecrypt: a wrapped key or a value does not open (another key, another row, damaged data).
var ErrDecrypt = errors.New("secret does not decrypt")

// KeyProvider wraps and unwraps company keys; a KMS provider can replace the environment's master key without
// touching callers.
type KeyProvider interface {
	Wrap(key []byte) ([]byte, error)
	Unwrap(wrapped []byte) ([]byte, error)
}

// MasterKeys is the environment's master key and, during a rotation, the new one (D1): keys are wrapped under
// the new key when there is one, and unwrapped with either.
type MasterKeys struct {
	current, next []byte
}

// FromEnv reads FACTORY_SECRETS_KEY and FACTORY_SECRETS_KEY_NEW (base64 of 32 bytes); nil without a master key.
func FromEnv(getenv func(string) string) (*MasterKeys, error) {
	cur, err := decodeKey(MasterKeyEnv, getenv(MasterKeyEnv))
	if err != nil || cur == nil {
		return nil, err
	}
	next, err := decodeKey(NewMasterKeyEnv, getenv(NewMasterKeyEnv))
	if err != nil {
		return nil, err
	}
	return &MasterKeys{current: cur, next: next}, nil
}

// NewMasterKeys is a provider over raw keys (tests, rotation); next may be nil.
func NewMasterKeys(current, next []byte) (*MasterKeys, error) {
	if len(current) != KeySize || next != nil && len(next) != KeySize {
		return nil, fmt.Errorf("master keys must be %d bytes", KeySize)
	}
	return &MasterKeys{current: current, next: next}, nil
}

func decodeKey(name, v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	k, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(k) != KeySize {
		return nil, fmt.Errorf("%s must be the base64 of %d random bytes (openssl rand -base64 32)", name, KeySize)
	}
	return k, nil
}

// Rotating reports whether a new master key is set (D1: rotate-master re-wraps under it).
func (m *MasterKeys) Rotating() bool { return m != nil && m.next != nil }

// Wrap wraps a company key under the new master key during a rotation, else under the current one.
func (m *MasterKeys) Wrap(key []byte) ([]byte, error) {
	if m == nil {
		return nil, ErrNoMasterKey
	}
	k := m.current
	if m.next != nil {
		k = m.next
	}
	return seal(k, key, []byte("company-key"))
}

// Unwrap opens a company key with the current master key, else with the new one.
func (m *MasterKeys) Unwrap(wrapped []byte) ([]byte, error) {
	if m == nil {
		return nil, ErrNoMasterKey
	}
	for _, k := range [][]byte{m.current, m.next} {
		if k == nil {
			continue
		}
		if key, err := open(k, wrapped, []byte("company-key")); err == nil {
			return key, nil
		}
	}
	return nil, ErrDecrypt
}

// RewrapUnder unwraps with any known key and wraps under the new master key only (rotate-master).
func (m *MasterKeys) RewrapUnder(wrapped []byte) ([]byte, error) {
	if !m.Rotating() {
		return nil, fmt.Errorf("set %s to rotate the master key", NewMasterKeyEnv)
	}
	key, err := m.Unwrap(wrapped)
	if err != nil {
		return nil, err
	}
	return seal(m.next, key, []byte("company-key"))
}

// NewCompanyKey is a random company key.
func NewCompanyKey() []byte {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return k
}

// AAD binds a value to its company, name and version.
func AAD(company, name string, version int64) []byte {
	return []byte(company + "|" + name + "|" + strconv.FormatInt(version, 10))
}

// Encrypt encrypts value under the company key for (company, name, version); the result holds the nonce.
func Encrypt(companyKey []byte, company, name string, version int64, value []byte) ([]byte, error) {
	return seal(companyKey, value, AAD(company, name, version))
}

// Decrypt opens a value Encrypt produced for the same (company, name, version).
func Decrypt(companyKey []byte, company, name string, version int64, sealed []byte) ([]byte, error) {
	return open(companyKey, sealed, AAD(company, name, version))
}

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("keys must be %d bytes", KeySize)
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func seal(key, plain, aad []byte) ([]byte, error) {
	a, err := gcm(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plain, aad), nil
}

func open(key, sealed, aad []byte) ([]byte, error) {
	a, err := gcm(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < a.NonceSize()+a.Overhead() {
		return nil, ErrDecrypt
	}
	out, err := a.Open(nil, sealed[:a.NonceSize()], sealed[a.NonceSize():], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}
