// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type toolsetStorage struct {
	store  ResourceStore
	logger *slog.Logger
}

func newToolsetStorage(store ResourceStore, logger *slog.Logger) *toolsetStorage {
	return &toolsetStorage{store: store, logger: logger}
}

func (s *toolsetStorage) put(ctx context.Context, key string, r io.Reader) error {
	if err := s.store.Put(ctx, key, r); err != nil {
		return fmt.Errorf("toolset put %s: %w", key, err)
	}
	return nil
}

func (s *toolsetStorage) get(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := getRuntimeZIP(ctx, s.store, key)
	if err != nil {
		return nil, fmt.Errorf("toolset get %s: %w", key, err)
	}
	return rc, nil
}

func (s *toolsetStorage) delete(ctx context.Context, key string) error {
	if err := s.store.Delete(ctx, key); err != nil {
		return fmt.Errorf("toolset delete %s: %w", key, err)
	}
	return nil
}

func packageKey(namespace, packageName string) string {
	if namespace != "" {
		return fmt.Sprintf("%s/str-runtime/toolsets/%s/package.zip", namespace, packageName)
	}
	return fmt.Sprintf("str-runtime/toolsets/%s/package.zip", packageName)
}

func createToolsetStore(cfg *platpkg.PlatformConfig, logger *slog.Logger) *objectstore.Store {
	st, err := objectstore.NewResourceStore(cfg.STRStoreRaw, objectstore.ResourceStoreOptions{
		YAMLKey:   "str_store",
		Purpose:   "toolsets",
		EnvBucket: objectstore.ResolveBucket("STR_BUCKET_NAME"),
		Logger:    logger,
	})
	if err != nil {
		logger.Error("toolset store init failed; aborting platform startup", "error", err)
		os.Exit(1)
	}
	return st
}
