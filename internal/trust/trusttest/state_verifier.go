// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trusttest

import (
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func NewStateVerifier(km *trust.KeyManager, componentID string, knownHosts map[string]bool) *trust.StateVerifier {
	registry := trust.NewRegistry()
	registry.AddOrUpdate(&trust.TrustCard{
		ComponentType: "gateway",
		ComponentID:   componentID,
		Namespace:     "test-ns",
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	v := trust.NewVerifier(registry, 5*time.Second, slog.Default())
	hosts := knownHosts
	return trust.NewStateVerifierFromComponents(v, trust.NewInMemoryJTICache(64), func() map[string]bool { return hosts })
}
