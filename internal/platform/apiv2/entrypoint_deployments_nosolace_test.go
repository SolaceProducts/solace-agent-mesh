// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosolace

package apiv2

import (
	"net/http"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
)

func TestEntrypointDeployments_RefusedIs422AndKeepsTheRecord(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	rec := api.call(http.MethodPost, "/entrypoints", map[string]any{
		"name":           "external-mesh",
		"description":    "Event mesh entrypoint on an external broker.",
		"entrypointType": "event_mesh",
		"values": map[string]any{
			"event_mesh_broker_config": map[string]any{
				"broker_url":      "tcps://broker.example.com:55443",
				"broker_vpn":      "default",
				"broker_username": "admin",
				"broker_password": "secret",
			},
			"event_handlers": []any{map[string]any{
				"name":              "orders",
				"subscriptions":     []any{map[string]any{"topic": "orders/created/>", "qos": 1}},
				"input_expression":  "input.payload:",
				"target_agent_name": "OrderValidator",
				"on_success":        "ok",
			}},
			"output_handlers": []any{map[string]any{"name": "ok", "topic_expression": "static:orders/validated"}},
		},
	})
	expectStatus(t, rec, http.StatusCreated)

	rec = api.deploy(decodeData[Entrypoint](t, rec).ID)

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if body := decodeError(t, rec); body.Message != msgDeployFailed || !strings.Contains(strings.Join(body.ValidationDetails["entrypointId"], ""), "Solace-enabled build") {
		t.Errorf("got %q %v, want the handler's sentence with the reason under entrypointId", body.Message, body.ValidationDetails)
	}
	api.expectKeptFailedDeployment(rec, entplatform.DeployFailureRejected, "Solace-enabled build")
}
