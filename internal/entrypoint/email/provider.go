// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"time"
)

type EmailProvider interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	IsConnected() bool
	LastSuccessfulAuthAt() time.Time
	FetchNew(ctx context.Context, limit int) ([]RawMessage, error)
	MarkProcessed(ctx context.Context, uid ProviderUID) error
	SupportsIdle() bool
}
