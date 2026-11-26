// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package credentials

import (
	"context"
	"time"
)

type NoopService struct{}

var _ Service = NoopService{}

func (NoopService) Get(_ context.Context, _ Scope, _ string) (string, error) {
	return "", nil
}

func (NoopService) Set(_ context.Context, _ Scope, _ string, _ string, _ time.Duration) error {
	return nil
}

func (NoopService) Delete(_ context.Context, _ Scope, _ string) error {
	return nil
}

func (NoopService) List(_ context.Context, _ Scope) ([]string, error) {
	return nil, nil
}
