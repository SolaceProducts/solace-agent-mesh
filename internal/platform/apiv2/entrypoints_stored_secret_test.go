// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
)

func TestEntrypoints_TestConnectionRefusesStoredSecretsForAChangedDestination(t *testing.T) {
	core, deps := newTestCoreWithConfig(t, nil)
	api := &entrypointAPI{apiClient: newAPIClient(t, core), gateway: &fakeGateway{}}
	created, err := deps.GatewaySvc.Create(t.Context(), entplatform.CreateGatewayRequest{
		Name: "stored-event-mesh", Description: "Event mesh entrypoint holding a broker password.", Type: "event_mesh",
		Values: map[string]any{
			"broker_url": "tcp://stored.example.com:55555", "broker_vpn": "stored-vpn",
			"broker_username": "stored-user", "broker_password": slackBotToken,
			"event_rules": []any{map[string]any{
				"name": "created", "subscriptions": []any{map[string]any{"topic": "orders/created"}},
				"promptTemplate": "New order {payload}", "targetAgent": "OrderAgent",
			}},
		},
	}, "user1")
	if err != nil {
		t.Fatal(err)
	}

	for personaName, scopes := range map[string][]string{
		"read-only": {rbac.ScopeEntrypointRead},
		"editor":    {rbac.ScopeEntrypointRead, rbac.ScopeEntrypointUpdate},
	} {
		for name, values := range map[string]map[string]any{
			"another host":            {"broker_url": "tcp://127.0.0.1:9"},
			"another host, redacted":  {"broker_url": "tcp://127.0.0.1:9", "broker_password": redacted},
			"the same host, new port": {"broker_url": "tcp://stored.example.com:1"},
			"verification off":        {"tls_skip_verify": true},
		} {
			t.Run(personaName+"/"+name, func(t *testing.T) {
				rec := api.call(http.MethodPut, "/entrypoints/testConnection",
					map[string]any{"entrypointId": created.ID, "values": values}, scopes...)
				expectDetail(t, rec, http.StatusUnprocessableEntity, "values")
				if details := decodeError(t, rec).ValidationDetails["values"]; !strings.Contains(strings.Join(details, " "), "enter the credentials again") {
					t.Errorf("validationDetails.values = %v, want the refusal to enter the credentials again", details)
				}
				if strings.Contains(rec.Body.String(), slackBotToken) {
					t.Error("the stored secret reached the response")
				}
			})
		}
	}
}
