// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const modelsOutboxAPIKey = "sk-outbox-test"

func modelsStartOutboxPoller(t *testing.T, deps *entplatform.RouteDeps) {
	t.Helper()
	for _, svc := range deps.BackgroundServices {
		poller, ok := svc.(*platpkg.OutboxPoller)
		if !ok {
			continue
		}
		if err := poller.Start(t.Context()); err != nil {
			t.Fatalf("start outbox poller: %v", err)
		}
		t.Cleanup(func() { _ = poller.Stop(context.Background()) })
		return
	}
	t.Fatal("the platform registered no outbox poller")
}

func modelsTopicQueue(t *testing.T, brk broker.Broker, topic string) broker.Queue {
	t.Helper()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if err := brk.Subscribe(q, topic); err != nil {
		t.Fatalf("subscribe %s: %v", topic, err)
	}
	return q
}

func modelsReceiveChange(t *testing.T, brk broker.Broker, q broker.Queue) (map[string]any, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("decode payload %s: %v", msg.Payload, err)
	}
	return payload, true
}

func modelsOutboxCreateResearch(api *apiClient) Model {
	api.t.Helper()
	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "research", "provider": "openai", "modelName": "gpt-4o",
		"apiBase":    "https://llm.example.com/v1",
		"authConfig": map[string]any{"type": "apikey", "api_key": modelsOutboxAPIKey},
	})
	return expectCreated[Model](api.t, rec, "models")
}

func TestModels_SaveIsBroadcastWithCredentialsOnlyForPythonAgents(t *testing.T) {
	for _, servesPythonAgents := range []bool{false, true} {
		t.Run(fmt.Sprintf("servesPythonAgents=%v", servesPythonAgents), func(t *testing.T) {
			t.Setenv(features.EnvVarName(features.KeyLegacyPythonModelBootstrap), strconv.FormatBool(servesPythonAgents))
			core, deps := newTestCore(t)
			modelsStartOutboxPoller(t, deps)
			brk := core.Services().Broker()
			q := modelsTopicQueue(t, brk, a2a.ModelConfigUpdateTopic("test-ns", "research"))

			modelsOutboxCreateResearch(newAPIClient(t, core))

			payload, ok := modelsReceiveChange(t, brk, q)
			if !ok {
				t.Fatal("the save was not published on the model's alias topic")
			}
			if payload["alias"] != "research" || payload["model_id"] == nil {
				t.Fatalf("published payload = %v, want the model's keys", payload)
			}
			encoded, _ := json.Marshal(payload)
			_, carriesConfig := payload["model_config"]
			carriesKey := bytes.Contains(encoded, []byte(modelsOutboxAPIKey))
			if carriesConfig != servesPythonAgents || carriesKey != servesPythonAgents {
				t.Errorf("model_config present = %v, API key present = %v, want both %v: %s",
					carriesConfig, carriesKey, servesPythonAgents, encoded)
			}
		})
	}
}

func TestModels_ChangesBroadcastNoCredentialsByDefault(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeyLegacyPythonModelBootstrap), "")
	core, deps := newTestCore(t)
	modelsStartOutboxPoller(t, deps)
	brk := core.Services().Broker()
	everything := modelsTopicQueue(t, brk, "test-ns/>")
	api := newAPIClient(t, core)

	created := modelsOutboxCreateResearch(api)
	idQ := modelsTopicQueue(t, brk, a2a.ModelConfigUpdateTopic("test-ns", created.ID))
	expectStatus(t, api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{"modelName": "gpt-4.1"}), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, "/models/"+created.ID, nil), http.StatusNoContent)

	for {
		payload, ok := modelsReceiveChange(t, brk, idQ)
		if !ok {
			t.Fatal("the delete was not published on the model's id topic")
		}
		if cfg, present := payload["model_config"]; present && cfg == nil {
			break
		}
	}

	seen := 0
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		msg, err := brk.Receive(ctx, everything)
		cancel()
		if err != nil || msg == nil {
			break
		}
		seen++
		if strings.Contains(msg.Topic, "/sam/v1/control/") {
			continue
		}
		if bytes.Contains(msg.Payload, []byte(modelsOutboxAPIKey)) {
			t.Errorf("message on %s carries the model's API key: %s", msg.Topic, msg.Payload)
		}
	}
	if seen == 0 {
		t.Fatal("the namespace wildcard saw no messages, so it proves nothing")
	}
}
