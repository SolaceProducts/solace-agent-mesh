// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
)

func countSkippedKind(ctx context.Context, c *reconcile.Client, kind string) (int, error) {
	if kind != "toolset" {
		return 0, nil
	}
	ts, err := c.ListToolsets(ctx)
	if err != nil {
		return 0, err
	}
	return len(ts), nil
}
