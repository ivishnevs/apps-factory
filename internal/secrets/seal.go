package secrets

import (
	"crypto/ecdh"
	"crypto/hpke"
	"errors"
	"strconv"
)

// Delivery to a node (core-secret-store D4): each value Core decrypts for a claim is sealed with HPKE (RFC 9180:
// DHKEM(X25519, HKDF-SHA256), HKDF-SHA256, ChaCha20-Poly1305) to the claiming node's encryption key, with info
// binding it to the company, run, node, name and version: it opens only on that node and only for that run.

// DeliveryInfo is the HPKE info of a delivered value.
func DeliveryInfo(company, run, node, name string, version int64) []byte {
	return []byte("factory-secret|" + company + "|" + run + "|" + node + "|" + name + "|" + strconv.FormatInt(version, 10))
}

// SealToNode seals value to the node's X25519 public key (32 bytes) for info.
func SealToNode(nodePub, info, value []byte) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(nodePub)
	if err != nil {
		return nil, err
	}
	pk, err := hpke.NewDHKEMPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return hpke.Seal(pk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), info, value)
}

// OpenOnNode opens a sealed value with the node's private key for the same info.
func OpenOnNode(priv *ecdh.PrivateKey, info, sealed []byte) ([]byte, error) {
	if priv == nil {
		return nil, errors.New("no node encryption key")
	}
	k, err := hpke.NewDHKEMPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	out, err := hpke.Open(k, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), info, sealed)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}
