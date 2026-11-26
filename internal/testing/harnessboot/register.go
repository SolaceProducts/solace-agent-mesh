// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harnessboot

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
)

func init() {
	Register()
}

func Register() {
	harness.RegisterOIDCHook(func(h *harness.TestHarness) {
		h.SetOIDCSetup(func(ctx context.Context) error {
			idps := StartFakeIDPs(h.T(), h.Scenario().GatewayAuth.FakeIDPs)
			catalog := harness.SynthesizeCatalog(h.Scenario().GatewayAuth, idps)
			handle, err := BootGateway(h.T(), h.Scenario(), catalog)
			if err != nil {
				return fmt.Errorf("enterprise gateway boot: %w", err)
			}
			h.SetOIDCRunState(idps, handle.BaseURL)
			return nil
		})
		h.SetOIDCRun(func(ctx context.Context) ([]harness.CollectedEvent, error) {
			h.RunOIDCFlow(ctx)
			return nil, nil
		})
	})
}
