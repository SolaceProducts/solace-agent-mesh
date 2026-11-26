// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func pullGatewayMock(t *testing.T, gws []wire.EntrypointResponse) *reconcile.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/entrypoints", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, gws)
	})
	return newV2PlatformClient(t, mux)
}

func pullOneGatewayYAML(t *testing.T, gw wire.EntrypointResponse) string {
	t.Helper()
	c := pullGatewayMock(t, []wire.EntrypointResponse{gw})
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullGateways(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullGateways: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 gateway entry, got %d", len(entries))
	}
	return string(entries[0].content)
}

func TestPullGateways_DropsAutoDefaultSlug(t *testing.T) {
	const id = "019700aa-bbbb-7ccc-8ddd-eeeeeeeeeeee"

	t.Run("unset slug is omitted", func(t *testing.T) {
		yaml := pullOneGatewayYAML(t, wire.EntrypointResponse{
			ID: id, Name: "no-slug-gw", Description: "auto-default gateway", EntrypointType: "httpsse",
		})
		if strings.Contains(yaml, "slug:") {
			t.Errorf("pulled YAML should omit slug for an unset-slug gateway:\n%s", yaml)
		}
	})

	t.Run("legacy slug==id is omitted", func(t *testing.T) {
		yaml := pullOneGatewayYAML(t, wire.EntrypointResponse{
			ID: id, Slug: new(id), Name: "legacy-gw", Description: "migration-backfilled gateway", EntrypointType: "httpsse",
		})
		if strings.Contains(yaml, "slug:") {
			t.Errorf("pulled YAML should omit slug when slug==id (legacy backfill):\n%s", yaml)
		}
	})

	t.Run("operator slug is emitted", func(t *testing.T) {
		yaml := pullOneGatewayYAML(t, wire.EntrypointResponse{
			ID: id, Slug: new("my-gateway"), Name: "pinned-gw", Description: "operator-chosen slug", EntrypointType: "httpsse",
		})
		if !strings.Contains(yaml, "slug: my-gateway") {
			t.Errorf("pulled YAML must preserve an operator-chosen slug:\n%s", yaml)
		}
	})
}

func TestPullGateways_DropsTheStampedMCPStateRedirection(t *testing.T) {
	const id = "019700aa-bbbb-7ccc-8ddd-eeeeeeeeeeee"

	yaml := pullOneGatewayYAML(t, wire.EntrypointResponse{
		ID: id, Slug: new("ide-access"), Name: "ide-access", EntrypointType: "mcp",
		Values: map[string]any{
			"enableAuth":              "true",
			"oauthDispatcherUrl":      "https://sam.example.com/api/v1/auth/callback",
			"stateRedirectionEnabled": "true",
		},
	})
	if strings.Contains(yaml, "stateRedirectionEnabled") {
		t.Errorf("pulled YAML carries the stamped stateRedirectionEnabled:\n%s", yaml)
	}
	if !strings.Contains(yaml, "oauthDispatcherUrl") {
		t.Errorf("pulled YAML dropped the operator's oauthDispatcherUrl:\n%s", yaml)
	}
}

func TestPullGateways_DropsTheDerivedSenderIdentityMode(t *testing.T) {
	const id = "019700aa-bbbb-7ccc-8ddd-ffffffffffff"

	yaml := pullOneGatewayYAML(t, wire.EntrypointResponse{
		ID: id, Slug: new("wa-support"), Name: "wa-support", EntrypointType: "whatsapp",
		Values: map[string]any{
			"phone_number_id":                 "1234567890",
			"run_as":                          "ops",
			entplatform.SenderIdentityModeKey: entplatform.SenderIdentitySystemUser,
		},
	})
	if strings.Contains(yaml, entplatform.SenderIdentityModeKey) {
		t.Errorf("pulled YAML carries the derived sender-identity mode:\n%s", yaml)
	}
	if !strings.Contains(yaml, "run_as: ops") {
		t.Errorf("pulled YAML dropped the operator's run_as:\n%s", yaml)
	}
}
