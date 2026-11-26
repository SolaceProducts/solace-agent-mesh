// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	authzOutboxPrefix       = "authz."
	authzOutboxRolesChanged = authzOutboxPrefix + "roles.changed"
	authzOutboxUsersChanged = authzOutboxPrefix + "users.changed"

	authzOutboxAggregateDefaultRoles = "00000000-0000-0000-0000-000000000000"
)

type txRunner interface {
	WithTx(ctx context.Context, fn func(tx *dbutil.Tx) error) error
}

type authzChangeOutbox struct {
	db       txRunner
	repo     *store.OutboxRepo
	notifier platpkg.OutboxNotifier
}

func (o *authzChangeOutbox) commit(ctx context.Context, eventType string, write func(tx *dbutil.Tx) (string, error)) error {
	recorded := false
	err := o.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		aggregateID, err := write(tx)
		if err != nil || aggregateID == "" || o.repo == nil {
			return err
		}
		if err := o.repo.CreateInTx(ctx, tx, &store.OutboxEvent{
			EventType:   eventType,
			AggregateID: aggregateID,
			Payload:     "{}",
		}); err != nil {
			return fmt.Errorf("write outbox event %s: %w", eventType, err)
		}
		recorded = true
		return nil
	})
	if err == nil && recorded && o.notifier != nil {
		o.notifier.Notify()
	}
	return err
}
