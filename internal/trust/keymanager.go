// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"maps"
	"math/big"
	"sync"
	"time"
)

type KeyPair struct {
	KeyID      string
	PrivateKey *ecdsa.PrivateKey
	PublicKey  *ecdsa.PublicKey
	CreatedAt  time.Time
	RetiredAt  time.Time

	jwk map[string]any
}

type KeyOrigin int

const (
	KeyOriginEphemeral KeyOrigin = iota
	KeyOriginSeedDerived
	KeyOriginPersistedLoaded
	KeyOriginPersistedGenerated
	KeyOriginEphemeralDegraded
)

func (o KeyOrigin) String() string {
	switch o {
	case KeyOriginSeedDerived:
		return "seed-derived"
	case KeyOriginPersistedLoaded:
		return "persisted-loaded"
	case KeyOriginPersistedGenerated:
		return "persisted-generated"
	case KeyOriginEphemeralDegraded:
		return "ephemeral-degraded"
	default:
		return "ephemeral"
	}
}

type keyOriginMeta struct {
	origin        KeyOrigin
	persistedPath string
	degradedErr   error
}

type KeyManager struct {
	componentID string
	meta        keyOriginMeta

	mu      sync.RWMutex
	current *KeyPair
	retired []*KeyPair
	keySeq  int64
}

func (km *KeyManager) Origin() KeyOrigin { return km.meta.origin }

func (km *KeyManager) PersistedPath() string { return km.meta.persistedPath }

func (km *KeyManager) DegradedError() error { return km.meta.degradedErr }

func NewKeyManager(componentID string) (*KeyManager, error) {
	km := &KeyManager{componentID: componentID}
	if _, err := km.generateKeyLocked(); err != nil {
		return nil, fmt.Errorf("generate initial key: %w", err)
	}
	return km, nil
}

func NewKeyManagerWithSeed(componentID string, seed []byte) (*KeyManager, error) {
	if len(seed) == 0 {
		return NewKeyManager(componentID)
	}
	km := &KeyManager{componentID: componentID, meta: keyOriginMeta{origin: KeyOriginSeedDerived}}
	if _, err := km.deriveKeyLocked(seed); err != nil {
		return nil, fmt.Errorf("derive initial key: %w", err)
	}
	return km, nil
}

func NewKeyManagerWithSeedOrPersistence(componentID string, cfg SigningKeyConfig) (*KeyManager, error) {
	if len(cfg.Seed) > 0 {
		return NewKeyManagerWithSeed(componentID, cfg.Seed)
	}
	if cfg.PersistenceDir == "" {
		return NewKeyManager(componentID)
	}
	return newPersistedKeyManager(componentID, cfg.PersistenceDir)
}

func (km *KeyManager) CurrentKeyPair() *KeyPair {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.current
}

func (km *KeyManager) RotateKey() (*KeyPair, error) {
	km.mu.Lock()
	defer km.mu.Unlock()
	if km.current != nil {
		old := km.current
		old.RetiredAt = time.Now()
		km.retired = append(km.retired, old)
	}
	return km.generateKeyLocked()
}

func (km *KeyManager) PruneRetired(now time.Time, overlap time.Duration) int {
	km.mu.Lock()
	defer km.mu.Unlock()
	if len(km.retired) == 0 {
		return 0
	}
	kept := km.retired[:0]
	dropped := 0
	for _, kp := range km.retired {
		if kp.RetiredAt.Add(overlap).Before(now) {
			dropped++
			continue
		}
		kept = append(kept, kp)
	}
	km.retired = kept
	return dropped
}

func (km *KeyManager) JWKS() map[string]any {
	km.mu.RLock()
	defer km.mu.RUnlock()

	if km.current == nil {
		return nil
	}

	keys := make([]any, 0, 1+len(km.retired))
	keys = append(keys, maps.Clone(km.current.jwk))
	for _, kp := range km.retired {
		keys = append(keys, maps.Clone(kp.jwk))
	}

	return map[string]any{"keys": keys}
}

func newKeyPair(keyID string, priv *ecdsa.PrivateKey, createdAt time.Time) (*KeyPair, error) {
	jwk, err := jwkEntry(keyID, &priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return &KeyPair{
		KeyID:      keyID,
		PrivateKey: priv,
		PublicKey:  &priv.PublicKey,
		CreatedAt:  createdAt,
		jwk:        jwk,
	}, nil
}

func jwkEntry(keyID string, pub *ecdsa.PublicKey) (map[string]any, error) {
	const coordLen = 32
	uncompressed, err := pub.Bytes()
	if err != nil {
		return nil, fmt.Errorf("encode public key %s: %w", keyID, err)
	}
	x, y := uncompressed[1:1+coordLen], uncompressed[1+coordLen:]
	return map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(x),
		"y":   base64.RawURLEncoding.EncodeToString(y),
		"kid": keyID,
		"use": "sig",
		"alg": "ES256",
	}, nil
}

func (km *KeyManager) generateKeyLocked() (*KeyPair, error) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ecdsa.GenerateKey: %w", err)
	}

	now := time.Now()
	km.keySeq++
	kp, err := newKeyPair(fmt.Sprintf("%s-key-%d-%d", km.componentID, now.UnixMilli(), km.keySeq), privKey, now)
	if err != nil {
		return nil, err
	}
	km.current = kp
	return kp, nil
}

const signingKeyHKDFInfo = "sam-trust-es256-signing-key-v1"

func (km *KeyManager) deriveKeyLocked(seed []byte) (*KeyPair, error) {
	priv, err := deriveP256Key(seed, km.componentID)
	if err != nil {
		return nil, err
	}
	kid, err := deterministicKeyID(km.componentID, &priv.PublicKey)
	if err != nil {
		return nil, err
	}
	kp, err := newKeyPair(kid, priv, time.Now())
	if err != nil {
		return nil, err
	}
	km.current = kp
	return kp, nil
}

func deriveP256Key(seed []byte, componentID string) (*ecdsa.PrivateKey, error) {
	curve := elliptic.P256()
	n := curve.Params().N

	okm, err := hkdf.Key(sha256.New, seed, nil, signingKeyHKDFInfo+"|"+componentID, 64)
	if err != nil {
		return nil, fmt.Errorf("hkdf derive: %w", err)
	}

	d := new(big.Int).SetBytes(okm)
	d.Mod(d, new(big.Int).Sub(n, big.NewInt(1)))
	d.Add(d, big.NewInt(1))

	scalar := make([]byte, (n.BitLen()+7)/8)
	d.FillBytes(scalar)

	priv, err := ecdsa.ParseRawPrivateKey(curve, scalar)
	if err != nil {
		return nil, fmt.Errorf("parse derived scalar: %w", err)
	}
	return priv, nil
}

func deterministicKeyID(componentID string, pub *ecdsa.PublicKey) (string, error) {
	raw, err := pub.Bytes()
	if err != nil {
		return "", fmt.Errorf("encode public key: %w", err)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%s-key-%s", componentID, base64.RawURLEncoding.EncodeToString(sum[:12])), nil
}
