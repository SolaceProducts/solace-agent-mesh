// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func toolAuthRegisterCore(t *testing.T) (*GatewayCore, *broker.DevBroker) {
	t.Helper()
	core, brk, _ := setupTestGateway(t)
	core.logger = silentLogger()
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core.StartToolAuthRegisterConsumer(t.Context(), silentLogger())
	return core, brk
}

func publishRegistration(t *testing.T, brk broker.Broker, payload []byte, props *a2a.UserProperties) {
	t.Helper()
	msg := &broker.Message{Payload: payload}
	if props != nil {
		msg.UserProperties = a2a.EncodeUserProperties(props)
	}
	if err := brk.PublishGuaranteed(context.Background(), a2a.ToolAuthRegisterTopic("test-ns"), msg); err != nil {
		t.Fatal(err)
	}
}

func publishRegistrationStruct(t *testing.T, brk broker.Broker, reg a2a.ToolAuthStateRegistration, props *a2a.UserProperties) {
	t.Helper()
	payload, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	publishRegistration(t, brk, payload, props)
}

func awaitToolAuthState(t *testing.T, core *GatewayCore, state string) *ToolAuthState {
	t.Helper()
	var got *ToolAuthState
	waitFor(t, 2*time.Second, func() bool {
		var ok bool
		got, ok = core.TakeToolAuthState(context.Background(), state)
		return ok
	})
	return got
}

func awaitLogContains(t *testing.T, capture *logCapture, want string) {
	t.Helper()
	waitFor(t, 2*time.Second, func() bool { return strings.Contains(capture.text(), want) })
}

func requireLogLevel(t *testing.T, capture *logCapture, want, level string) {
	t.Helper()
	for _, line := range capture.lines() {
		msg, _ := line["msg"].(string)
		if !strings.Contains(msg, want) {
			continue
		}
		if got, _ := line["level"].(string); got != level {
			t.Errorf("log %q at level %s, want %s", want, got, level)
		}
		return
	}
	t.Errorf("no log line matching %q", want)
}

func TestStartToolAuthRegisterConsumer_SeedsState(t *testing.T) {
	core, brk := toolAuthRegisterCore(t)

	publishRegistrationStruct(t, brk, a2a.ToolAuthStateRegistration{
		State:          "STATE-SEED",
		TargetAgent:    "agent-x",
		TaskID:         "task-1",
		UserID:         "system:eval",
		ClientID:       "platform",
		FunctionCallID: "call-1",
		CredentialKey:  "tool__oauth2",
		CodeVerifier:   "verifier-1",
	}, &a2a.UserProperties{ClientID: "publisher-gw", UserID: "svc-platform"})

	got := awaitToolAuthState(t, core, "STATE-SEED")
	if got.UserID != "system:eval" {
		t.Errorf("UserID = %q, want the payload credential identity system:eval (not the broker principal)", got.UserID)
	}
	if got.TargetAgent != "agent-x" || got.TaskID != "task-1" {
		t.Errorf("TargetAgent/TaskID = %q/%q, want agent-x/task-1", got.TargetAgent, got.TaskID)
	}
	if got.ClientID != "platform" {
		t.Errorf("ClientID = %q, want the payload value platform", got.ClientID)
	}
	if got.FunctionCallID != "call-1" || got.CredentialKey != "tool__oauth2" || got.CodeVerifier != "verifier-1" {
		t.Errorf("FunctionCallID/CredentialKey/CodeVerifier = %q/%q/%q", got.FunctionCallID, got.CredentialKey, got.CodeVerifier)
	}
	if got.ToolType != ToolAuthTypeMCP {
		t.Errorf("ToolType = %q, want %q", got.ToolType, ToolAuthTypeMCP)
	}
	if got.TaskCallStack != nil {
		t.Errorf("TaskCallStack = %v, want nil", got.TaskCallStack)
	}
}

func TestStartToolAuthRegisterConsumer_ClientIDFallback(t *testing.T) {
	core, brk := toolAuthRegisterCore(t)

	publishRegistrationStruct(t, brk, a2a.ToolAuthStateRegistration{
		State:         "STATE-FALLBACK",
		TargetAgent:   "agent-x",
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
	}, &a2a.UserProperties{ClientID: "publisher-gw"})

	if got := awaitToolAuthState(t, core, "STATE-FALLBACK"); got.ClientID != "publisher-gw" {
		t.Errorf("ClientID = %q, want the broker-stamped publisher-gw", got.ClientID)
	}
}

func TestToolAuthStateFromRegistration_ToolTypeFromCredentialKey(t *testing.T) {
	tests := []struct {
		credentialKey string
		want          string
	}{
		{"tool__oauth2", ToolAuthTypeMCP},
		{"openapi_svc_oauth2", ToolAuthTypeOpenAPI},
	}
	for _, tt := range tests {
		t.Run(tt.credentialKey, func(t *testing.T) {
			got := toolAuthStateFromRegistration(&a2a.ToolAuthStateRegistration{CredentialKey: tt.credentialKey}, "")
			if got.ToolType != tt.want {
				t.Errorf("ToolType = %q, want %q", got.ToolType, tt.want)
			}
		})
	}
}

func TestValidateToolAuthRegistration(t *testing.T) {
	valid := a2a.ToolAuthStateRegistration{
		State:         "STATE-VALID",
		TargetAgent:   "agent-x",
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
	}
	if err := validateToolAuthRegistration(&valid); err != nil {
		t.Fatalf("valid registration rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*a2a.ToolAuthStateRegistration)
	}{
		{"missing state", func(r *a2a.ToolAuthStateRegistration) { r.State = "" }},
		{"missing target_agent", func(r *a2a.ToolAuthStateRegistration) { r.TargetAgent = "" }},
		{"missing task_id", func(r *a2a.ToolAuthStateRegistration) { r.TaskID = "" }},
		{"missing user_id", func(r *a2a.ToolAuthStateRegistration) { r.UserID = "" }},
		{"missing credential_key", func(r *a2a.ToolAuthStateRegistration) { r.CredentialKey = "" }},
		{"oversized field", func(r *a2a.ToolAuthStateRegistration) {
			r.CodeVerifier = strings.Repeat("x", maxToolAuthRegistrationFieldLen+1)
		}},
		{"target_agent with topic separators", func(r *a2a.ToolAuthStateRegistration) {
			r.TargetAgent = "agent/../other"
		}},
		{"target_agent with whitespace", func(r *a2a.ToolAuthStateRegistration) {
			r.TargetAgent = "agent x"
		}},
		{"target_agent over the agent-name cap", func(r *a2a.ToolAuthStateRegistration) {
			r.TargetAgent = strings.Repeat("a", maxToolAuthTargetAgentLen+1)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := valid
			tt.mutate(&reg)
			if err := validateToolAuthRegistration(&reg); err == nil {
				t.Error("want rejection, got nil")
			}
		})
	}
}

func TestStartToolAuthRegisterConsumer_SurvivesBadPayload(t *testing.T) {
	core, brk := toolAuthRegisterCore(t)

	publishRegistration(t, brk, []byte(`{not json`), nil)
	publishRegistrationStruct(t, brk, a2a.ToolAuthStateRegistration{
		State:       "STATE-INVALID",
		TargetAgent: "agent-x",
	}, nil)
	publishRegistrationStruct(t, brk, a2a.ToolAuthStateRegistration{
		State:         "STATE-SENTINEL",
		TargetAgent:   "agent-x",
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
	}, nil)

	awaitToolAuthState(t, core, "STATE-SENTINEL")
	if _, ok := core.TakeToolAuthState(context.Background(), "STATE-INVALID"); ok {
		t.Error("an invalid registration was seeded anyway")
	}
}

func TestStartToolAuthRegisterConsumer_Gating(t *testing.T) {
	(*GatewayCore)(nil).StartToolAuthRegisterConsumer(context.Background(), silentLogger())

	core := NewGatewayCore(&LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "gw"})
	core.StartToolAuthRegisterConsumer(context.Background(), silentLogger())
}

func TestResolveToolAuthRegistrationTTL(t *testing.T) {
	tests := []struct {
		name  string
		ttlMs int64
		want  time.Duration
	}{
		{"zero falls back to default", 0, mcpAuthStateTTL},
		{"negative falls back to default", -1, mcpAuthStateTTL},
		{"below floor clamps up", 1, minToolAuthStateTTL},
		{"in range passes through", 600_000, 10 * time.Minute},
		{"above ceiling clamps down", 3_600_000, maxToolAuthStateTTL},
		{"max int64 cannot overflow into a negative duration", math.MaxInt64, maxToolAuthStateTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveToolAuthRegistrationTTL(tt.ttlMs); got != tt.want {
				t.Errorf("resolveToolAuthRegistrationTTL(%d) = %v, want %v", tt.ttlMs, got, tt.want)
			}
		})
	}
}

func TestValidateToolAuthRegistration_OversizedReasonNamesFieldNotValue(t *testing.T) {
	padding := strings.Repeat("x", maxToolAuthRegistrationFieldLen+1)
	err := validateToolAuthRegistration(&a2a.ToolAuthStateRegistration{
		State:         "STATE-VALID",
		TargetAgent:   padding,
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
	})
	if err == nil {
		t.Fatal("want rejection, got nil")
	}
	if !strings.Contains(err.Error(), "target_agent") {
		t.Errorf("reason does not name the field: %q", err.Error())
	}
	if strings.Contains(err.Error(), padding) {
		t.Error("reason quotes the oversized value")
	}
}

func TestStartToolAuthRegisterConsumer_LogsNeverCarrySecrets(t *testing.T) {
	const (
		secretState    = "STATE-SECRET-DO-NOT-LOG"
		secretVerifier = "VERIFIER-SECRET-DO-NOT-LOG"
	)
	capture := newLogCapture()

	core, brk, _ := setupTestGateway(t)
	core.logger = capture.logger
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core.StartToolAuthRegisterConsumer(t.Context(), capture.logger)

	publishRegistrationStruct(t, brk, a2a.ToolAuthStateRegistration{
		State:        secretState + "-REJECTED",
		TargetAgent:  "agent-x",
		TaskID:       "task-1",
		UserID:       "system:eval",
		CodeVerifier: secretVerifier + "-REJECTED",
	}, nil)
	publishRegistrationStruct(t, brk, a2a.ToolAuthStateRegistration{
		State:         secretState,
		TargetAgent:   "agent-x",
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
		CodeVerifier:  secretVerifier,
	}, nil)
	awaitToolAuthState(t, core, secretState)
	awaitLogContains(t, capture, toolAuthStateFP(secretState))

	logs := capture.text()
	if strings.Contains(logs, secretState) {
		t.Errorf("logs carry the raw state token:\n%s", logs)
	}
	if strings.Contains(logs, secretVerifier) {
		t.Errorf("logs carry the PKCE code_verifier:\n%s", logs)
	}
	if !strings.Contains(logs, "invalid registration") {
		t.Errorf("the rejected registration logged no drop line:\n%s", logs)
	}
	if !strings.Contains(logs, toolAuthStateFP(secretState+"-REJECTED")) {
		t.Errorf("the drop line carries no stateFP to correlate on:\n%s", logs)
	}
	requireLogLevel(t, capture, "invalid registration", "WARN")
	requireLogLevel(t, capture, "seeded tool auth state", "INFO")
}

func TestStartToolAuthRegisterConsumer_WarnsOnTTLClamp(t *testing.T) {
	capture := newLogCapture()

	core, brk, _ := setupTestGateway(t)
	core.logger = capture.logger
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core.StartToolAuthRegisterConsumer(t.Context(), capture.logger)

	publishRegistrationStruct(t, brk, a2a.ToolAuthStateRegistration{
		State:         "STATE-CLAMPED",
		TargetAgent:   "agent-x",
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
		TTLMs:         (4 * time.Hour).Milliseconds(),
	}, nil)
	awaitToolAuthState(t, core, "STATE-CLAMPED")
	awaitLogContains(t, capture, "clamped")
	requireLogLevel(t, capture, "clamped", "WARN")
}
