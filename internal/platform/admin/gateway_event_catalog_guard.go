// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func refuseWhileEventCatalogEntriesPointAtIt(ctx context.Context, db *store.DB, reg *features.Registry, gw *store.Gateway) error {
	if db == nil || gw == nil || reg == nil || !reg.IsEnabled(features.KeyEventCatalog) {
		return nil
	}
	count, err := store.NewEventCatalogRepo(db).CountByEntrypoint(ctx, gw.ID)
	if err != nil {
		return fmt.Errorf("read the entrypoint's event catalog entries: %w", err)
	}
	if count == 0 {
		return nil
	}
	return &ValidationError{
		Message: fmt.Sprintf("cannot delete this entrypoint while event catalog entries point at it (%d); delete them or move them to another entrypoint first", count),
		Field:   fieldEntrypointID,
	}
}
