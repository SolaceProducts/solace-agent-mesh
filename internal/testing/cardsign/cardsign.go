// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package cardsign

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2acrypto"
)

type Key struct {
	ID      string
	private *ecdsa.PrivateKey
}

func NewKey(id string) (*Key, error) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Key{ID: id, private: private}, nil
}

func JWKS(keys ...*Key) []byte {
	set := struct {
		Keys []map[string]string `json:"keys"`
	}{}
	for _, k := range keys {
		point, err := k.private.PublicKey.Bytes()
		if err != nil {
			panic(err)
		}
		set.Keys = append(set.Keys, map[string]string{
			"kty": "EC",
			"crv": "P-256",
			"alg": "ES256",
			"use": "sig",
			"kid": k.ID,
			"x":   base64.RawURLEncoding.EncodeToString(point[1:33]),
			"y":   base64.RawURLEncoding.EncodeToString(point[33:65]),
		})
	}
	out, err := json.Marshal(set)
	if err != nil {
		panic(err)
	}
	return out
}

func Sign(card map[string]any, key *Key, jku string) (map[string]any, error) {
	unsigned := make(map[string]any, len(card))
	for k, v := range card {
		if k != "signatures" {
			unsigned[k] = v
		}
	}
	raw, err := json.Marshal(unsigned)
	if err != nil {
		return nil, err
	}
	signer, err := a2acrypto.NewSigner(a2acrypto.SignerConfig{PrivateKey: key.private, KeyID: key.ID, JWKSURL: jku})
	if err != nil {
		return nil, err
	}
	sig, err := signer.Sign(context.Background(), raw)
	if err != nil {
		return nil, fmt.Errorf("sign card: %w", err)
	}
	signed := make(map[string]any, len(card)+1)
	for k, v := range card {
		signed[k] = v
	}
	existing, _ := card["signatures"].([]any)
	signed["signatures"] = append(existing, map[string]any{"protected": sig.Protected, "signature": sig.Signature})
	return signed, nil
}
