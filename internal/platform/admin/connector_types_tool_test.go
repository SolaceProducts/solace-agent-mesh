// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestPlatformListComponents_ConnectorTypesFollowTheFeatureFlag(t *testing.T) {
	for _, tc := range []struct {
		name      string
		enabled   bool
		wantEmail bool
	}{{"email off", false, false}, {"email on", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			env := setupControlTestWithDeps(t, func(d *RouteDeps) { d.FeatureRegistry = emailGateRegistry(t, tc.enabled) })

			res, err := (&tools.ListPlatformComponentsTool{}).Execute(t.Context(),
				map[string]any{"component_type": "connector_type"}, &tool.Context{ControlQuerier: env.Client})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if res.Status != tool.StatusSuccess {
				t.Fatalf("status = %s: %s", res.Status, res.Message)
			}

			preview := res.DataObjects[0].Preview
			if got := strings.Contains(preview, "  - email: smtp"); got != tc.wantEmail {
				t.Errorf("preview lists email = %v, want %v:\n%s", got, tc.wantEmail, preview)
			}
			if !strings.Contains(preview, "  - sql: postgres") || !strings.Contains(preview, "Enabled connector types on this deployment: ") {
				t.Errorf("preview lost the ungated types or the headline:\n%s", preview)
			}
		})
	}
}
