// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func persistedKeyFilePath(persistDir, componentID string) (string, error) {
	path := filepath.Join(persistDir, fmt.Sprintf("trust_signing_%s.pem", componentID))
	if filepath.Dir(path) != filepath.Clean(persistDir) {
		return "", fmt.Errorf("componentID %q would escape KeyPersistenceDir", componentID)
	}
	return path, nil
}

func newPersistedKeyManager(componentID, persistDir string) (*KeyManager, error) {
	path, err := persistedKeyFilePath(persistDir, componentID)
	if err != nil {
		return degradedKeyManager(componentID, err)
	}

	if priv, loadErr := loadPersistedKey(path); loadErr == nil {
		return newKeyManagerFromPrivateKey(componentID, priv, KeyOriginPersistedLoaded, path)
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate trust signing key: %w", err)
	}
	switch err := savePersistedKey(path, priv); {
	case err == nil:
		return newKeyManagerFromPrivateKey(componentID, priv, KeyOriginPersistedGenerated, path)
	case errors.Is(err, os.ErrExist):
		winner, loadErr := reloadRaceWinnerKey(path)
		if loadErr != nil {
			return degradedKeyManager(componentID, fmt.Errorf("reload trust signing key %s: %w", path, loadErr))
		}
		return newKeyManagerFromPrivateKey(componentID, winner, KeyOriginPersistedLoaded, path)
	default:
		return degradedKeyManager(componentID, fmt.Errorf("persist trust signing key %s: %w", path, err))
	}
}

func degradedKeyManager(componentID string, reason error) (*KeyManager, error) {
	km, err := NewKeyManager(componentID)
	if err != nil {
		return nil, err
	}
	km.meta = keyOriginMeta{origin: KeyOriginEphemeralDegraded, degradedErr: reason}
	return km, nil
}

func newKeyManagerFromPrivateKey(componentID string, priv *ecdsa.PrivateKey, origin KeyOrigin, path string) (*KeyManager, error) {
	kid, err := deterministicKeyID(componentID, &priv.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("derive key id: %w", err)
	}
	kp, err := newKeyPair(kid, priv, time.Now())
	if err != nil {
		return nil, err
	}
	return &KeyManager{
		componentID: componentID,
		meta:        keyOriginMeta{origin: origin, persistedPath: path},
		current:     kp,
	}, nil
}

func reloadRaceWinnerKey(path string) (*ecdsa.PrivateKey, error) {
	const attempts = 50
	var err error
	for range attempts {
		var priv *ecdsa.PrivateKey
		if priv, err = loadPersistedKey(path); err == nil {
			return priv, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil, err
}

func loadPersistedKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", path)
	}

	var priv *ecdsa.PrivateKey
	switch block.Type {
	case "EC PRIVATE KEY":
		priv, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var k any
		if k, err = x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			ecKey, ok := k.(*ecdsa.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("%s contains a %T key, want *ecdsa.PrivateKey", path, k)
			}
			priv = ecKey
		}
	default:
		return nil, fmt.Errorf("unsupported PEM block type %q in %s; want EC PRIVATE KEY or PRIVATE KEY", block.Type, path)
	}
	if err != nil {
		return nil, fmt.Errorf("parse %s (%s): %w", path, block.Type, err)
	}
	if priv.Curve != elliptic.P256() {
		return nil, fmt.Errorf("persisted signing key %s must use P-256; got %s", path, priv.Curve.Params().Name)
	}
	return priv, nil
}

func savePersistedKey(path string, priv *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal EC private key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})

	if mkdirErr := os.MkdirAll(filepath.Dir(path), 0700); mkdirErr != nil {
		return fmt.Errorf("create key directory: %w", mkdirErr)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(pemBytes); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
