// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func agentPlatformDefaultsGet(t *testing.T, env map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	core, _ := newTestCore(t)
	api := newAPIClient(t, core)
	for key, value := range env {
		t.Setenv(key, value)
	}
	rec := api.call(http.MethodGet, "/agentPlatformDefaults", nil)
	expectStatus(t, rec, http.StatusOK)
	return rec
}

func TestAgentPlatformDefaults_ResolveTheEnvironmentRatherThanReturnPlaceholders(t *testing.T) {
	rec := agentPlatformDefaultsGet(t, map[string]string{
		"SOLACE_BROKER_URL":      "tcps://platform-broker.example.com:55443",
		"SOLACE_BROKER_USERNAME": "svc-user",
		"SOLACE_BROKER_PASSWORD": "svc-pass",
		"SOLACE_BROKER_VPN":      "svc-vpn",
		"LLM_SERVICE_ENDPOINT":   "https://llm.example.com/v1",
		"LLM_SERVICE_API_KEY":    "sk-test",
	})

	if strings.Contains(rec.Body.String(), "${") {
		t.Errorf("body carries an unresolved ${...} placeholder: %s", rec.Body.String())
	}
	got := decodeData[AgentPlatformDefaults](t, rec)
	if got.Broker.BrokerURL != "tcps://platform-broker.example.com:55443" || got.Broker.BrokerUsername != "svc-user" || got.Broker.BrokerVPN != "svc-vpn" {
		t.Errorf("broker = %+v, want the environment's values", got.Broker)
	}
	if got.Model.APIBase == nil || *got.Model.APIBase != "https://llm.example.com/v1" {
		t.Errorf("model.apiBase = %v, want the environment's endpoint", got.Model.APIBase)
	}
}

func TestAgentPlatformDefaults_FallBackWhenTheEnvironmentIsUnset(t *testing.T) {
	rec := agentPlatformDefaultsGet(t, map[string]string{
		"SOLACE_BROKER_URL":      "",
		"SOLACE_BROKER_USERNAME": "",
		"SOLACE_BROKER_VPN":      "",
		"PERSISTENCE_TYPE":       "",
		"ARTIFACT_BASE_PATH":     "",
	})

	got := decodeData[AgentPlatformDefaults](t, rec)
	if got.Broker.BrokerURL != "ws://localhost:8080" || got.Broker.BrokerUsername != "default" || got.Broker.BrokerVPN != "default" {
		t.Errorf("broker = %+v, want the documented fallbacks", got.Broker)
	}
	if got.SessionService.ServiceType != "sql" {
		t.Errorf("sessionService.serviceType = %q, want sql", got.SessionService.ServiceType)
	}
	if got.ArtifactService.BasePath != "/tmp/samv2" {
		t.Errorf("artifactService.basePath = %q, want /tmp/samv2", got.ArtifactService.BasePath)
	}
}

func TestAgentPlatformDefaults_NeverCarryACredential(t *testing.T) {
	rec := agentPlatformDefaultsGet(t, map[string]string{
		"SOLACE_BROKER_PASSWORD": "should-not-appear-in-response",
		"LLM_SERVICE_API_KEY":    "sk-should-not-appear",
		"DATABASE_URL":           "postgres://sam_user:should-not-appear-in-response@localhost:5432/sam",
	})

	if body := rec.Body.String(); strings.Contains(body, "should-not-appear-in-response") || strings.Contains(body, "sk-should-not-appear") {
		t.Fatalf("a secret reached the platform defaults: %s", body)
	}

	const newFieldGuidance = "every field here is readable with agent_builder:*:read; confirm it carries no credential, then add it here"
	allowed := map[string][]string{
		"broker":           {"devMode", "brokerUrl", "brokerUsername", "brokerVpn", "temporaryQueue"},
		"model":            {"model", "apiBase"},
		"sessionService":   {"serviceType", "defaultBehavior"},
		"artifactService":  {"serviceType", "basePath", "bucketName", "endpointUrl", "artifactScope"},
		"log":              {"stdoutLogLevel", "logFileLevel", "logFile"},
		"validationLimits": {"agentInstructionMin", "agentInstructionMax"},
	}
	data := rawData(t, rec)
	for block := range data {
		if _, expected := allowed[block]; !expected {
			t.Errorf("unexpected block %q: %s", block, newFieldGuidance)
		}
	}
	for block, keys := range allowed {
		got, ok := data[block].(map[string]any)
		if !ok {
			t.Errorf("block %q is missing or not an object", block)
			continue
		}
		for key := range got {
			if !slices.Contains(keys, key) {
				t.Errorf("unexpected key %q in block %q: %s", key, block, newFieldGuidance)
			}
		}
	}
}
