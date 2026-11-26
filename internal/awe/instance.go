// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type Instance interface {
	Name() string

	DisplayName() string

	Kind() string

	Init(ctx context.Context, cfg config.Config, svc runtime.Services) error

	Start(ctx context.Context) error

	Stop(ctx context.Context) error

	Health() error
}

type Remover interface {
	Remove(ctx context.Context) error
}

type ProcessStopper interface {
	StopForProcessExit(ctx context.Context) error
}

type Replacer interface {
	StopForReplace(ctx context.Context) error
}
