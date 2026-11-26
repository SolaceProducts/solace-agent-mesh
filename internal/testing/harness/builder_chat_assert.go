// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type BuilderChatFlow struct {
	Builder string `yaml:"builder"`

	InitCheck *BuilderInitCheck `yaml:"init_check"`

	Turns []BuilderChatTurn `yaml:"turns"`
}

type BuilderInitCheck struct {
	ExpectMessageContains string   `yaml:"expect_message_contains"`
	ExpectConfidence      *float64 `yaml:"expect_confidence"`
	ForbiddenFields       []string `yaml:"forbidden_fields"`
}

type BuilderChatTurn struct {
	Request map[string]any `yaml:"request"`

	Expect BuilderTurnExpect `yaml:"expect"`
}

type BuilderTurnExpect struct {
	Status int `yaml:"status"`

	MessageContains string `yaml:"message_contains"`
	MessageEquals   string `yaml:"message_equals"`

	Confidence *float64 `yaml:"confidence"`

	ReadyToSave *bool `yaml:"ready_to_save"`

	IsError *bool `yaml:"is_error"`

	TemplateUpdatesContain map[string]any `yaml:"template_updates_contain"`

	TaskUpdatesContain map[string]any `yaml:"task_updates_contain"`
}

func (h *TestHarness) AssertBuilderChatFlow(t *testing.T) {
	t.Helper()

	flow := h.scenario.BuilderChatFlow
	if flow == nil {
		return
	}

	httpGW, ok := h.gateway.(*HTTPGateway)
	if !ok {
		t.Error("builder_chat_flow requires go-http gateway mode")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base := httpGW.baseURL
	chatPath, initPath := builderEndpoints(flow.Builder)
	if chatPath == "" {
		t.Fatalf("builder_chat_flow: unknown builder %q (want 'prompt' or 'task')", flow.Builder)
	}

	if flow.InitCheck != nil {
		h.runInitCheck(ctx, t, httpGW, base+initPath, flow.InitCheck)
	}

	for i, turn := range flow.Turns {
		h.runChatTurn(ctx, t, httpGW, base+chatPath, i, turn)
	}
}

func builderEndpoints(builder string) (chat, init string) {
	switch builder {
	case "prompt":
		return "/api/v1/prompts/chat", "/api/v1/prompts/chat/init"
	case "task":
		return "/api/v1/scheduledTasks/builder/chat", "/api/v1/scheduledTasks/builder/greeting"
	}
	return "", ""
}

func (h *TestHarness) runInitCheck(ctx context.Context, t *testing.T, gw *HTTPGateway, url string, check *BuilderInitCheck) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("init_check: create request: %v", err)
	}
	req.Header.Set("X-SAM-User-ID", "test-user")

	resp, err := gw.client.Do(req)
	if err != nil {
		t.Fatalf("init_check: GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("init_check: status %d: %s", resp.StatusCode, string(body))
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("init_check: decode body: %v; body=%s", err, string(body))
	}

	if check.ExpectMessageContains != "" {
		msg, _ := raw["message"].(string)
		if !strings.Contains(msg, check.ExpectMessageContains) {
			t.Errorf("init_check: message=%q, want to contain %q", msg, check.ExpectMessageContains)
		}
	}

	if check.ExpectConfidence != nil {
		conf, _ := raw["confidence"].(float64)
		if conf != *check.ExpectConfidence {
			t.Errorf("init_check: confidence=%v, want %v", conf, *check.ExpectConfidence)
		}
	}

	for _, forbidden := range check.ForbiddenFields {
		if _, ok := raw[forbidden]; ok {
			t.Errorf("init_check: response must not include field %q (Python omits it)", forbidden)
		}
	}
}

func (h *TestHarness) runChatTurn(ctx context.Context, t *testing.T, gw *HTTPGateway, url string, idx int, turn BuilderChatTurn) {
	t.Helper()

	prefix := fmt.Sprintf("turn[%d]", idx)

	bodyData, err := json.Marshal(turn.Request)
	if err != nil {
		t.Fatalf("%s: marshal request: %v", prefix, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyData))
	if err != nil {
		t.Fatalf("%s: create request: %v", prefix, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SAM-User-ID", "test-user")

	resp, err := gw.client.Do(req)
	if err != nil {
		t.Fatalf("%s: POST %s: %v", prefix, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)

	expectedStatus := turn.Expect.Status
	if expectedStatus == 0 {
		expectedStatus = http.StatusOK
	}
	if resp.StatusCode != expectedStatus {
		t.Fatalf("%s: status=%d want=%d; body=%s", prefix, resp.StatusCode, expectedStatus, string(respBody))
	}

	if resp.StatusCode >= 400 {
		return
	}

	var raw map[string]any
	if err := json.Unmarshal(respBody, &raw); err != nil {
		t.Fatalf("%s: decode body: %v; body=%s", prefix, err, string(respBody))
	}

	exp := turn.Expect
	if exp.MessageContains != "" {
		msg, _ := raw["message"].(string)
		if !strings.Contains(msg, exp.MessageContains) {
			t.Errorf("%s: message=%q, want to contain %q", prefix, msg, exp.MessageContains)
		}
	}
	if exp.MessageEquals != "" {
		msg, _ := raw["message"].(string)
		if msg != exp.MessageEquals {
			t.Errorf("%s: message=%q, want %q", prefix, msg, exp.MessageEquals)
		}
	}
	if exp.Confidence != nil {
		conf, _ := raw["confidence"].(float64)
		if conf != *exp.Confidence {
			t.Errorf("%s: confidence=%v, want %v", prefix, conf, *exp.Confidence)
		}
	}
	if exp.ReadyToSave != nil {
		ready, _ := raw["readyToSave"].(bool)
		if ready != *exp.ReadyToSave {
			t.Errorf("%s: readyToSave=%v, want %v", prefix, ready, *exp.ReadyToSave)
		}
	}
	if exp.IsError != nil {
		isErr, _ := raw["isError"].(bool)
		if isErr != *exp.IsError {
			t.Errorf("%s: isError=%v, want %v", prefix, isErr, *exp.IsError)
		}
	}
	if len(exp.TemplateUpdatesContain) > 0 {
		assertSubset(t, prefix+".templateUpdates", raw["templateUpdates"], exp.TemplateUpdatesContain)
	}
	if len(exp.TaskUpdatesContain) > 0 {
		assertSubset(t, prefix+".taskUpdates", raw["taskUpdates"], exp.TaskUpdatesContain)
	}
}

func assertSubset(t *testing.T, prefix string, actual any, expected map[string]any) {
	t.Helper()

	actualMap, ok := actual.(map[string]any)
	if !ok {
		t.Errorf("%s: expected object, got %T (%v)", prefix, actual, actual)
		return
	}
	for k, want := range expected {
		got, present := actualMap[k]
		if !present {
			t.Errorf("%s: missing key %q", prefix, k)
			continue
		}
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			t.Errorf("%s.%s = %v (%T), want %v (%T)", prefix, k, got, got, want, want)
		}
	}
}
