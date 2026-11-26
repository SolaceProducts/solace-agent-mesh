// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func init() {
	proxy.RegisterDefaultAdapter(func(cfg *proxy.ProxyConfig, logger *slog.Logger) (proxy.ProtocolAdapter, error) {
		return NewA2AAdapter(cfg, logger)
	})
}
