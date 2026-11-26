// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func RequestIDFromContext(ctx context.Context) string {
	return httputil.RequestIDFromContext(ctx)
}

var RequestIDMiddleware = httputil.RequestIDMiddleware

var Chain = httputil.Chain
