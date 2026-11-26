// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

func NewFromObjectStore(c *objectstore.Client) (Client, error) {
	if c == nil {
		return nil, errors.New("storage: nil objectstore client")
	}
	return &objectStoreAdapter{store: objectstore.NewStore(c)}, nil
}

type objectStoreAdapter struct {
	store *objectstore.Store
}

func (a *objectStoreAdapter) PutObject(key string, body []byte) error {
	if key == "" {
		return errors.New("storage: empty key")
	}
	return a.store.Put(context.Background(), key, bytes.NewReader(body))
}

func (a *objectStoreAdapter) GetObject(key string) ([]byte, error) {
	rc, err := a.store.Get(context.Background(), key)
	if err != nil {
		if objectstore.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get %q: %w", key, err)
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func (a *objectStoreAdapter) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	return a.store.DeletePrefix(ctx, prefix)
}
