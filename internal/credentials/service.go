// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package credentials

import (
	"context"
	"time"
)

type Service interface {
	Get(ctx context.Context, scope Scope, key string) (string, error)

	Set(ctx context.Context, scope Scope, key string, value string, ttl time.Duration) error

	Delete(ctx context.Context, scope Scope, key string) error

	List(ctx context.Context, scope Scope) ([]string, error)
}

type Scope struct {
	AgentName string
	AppName   string
	UserID    string
}
