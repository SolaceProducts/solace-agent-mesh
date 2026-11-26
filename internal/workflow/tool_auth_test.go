// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"golang.org/x/oauth2"
)

type markedCredentials struct{ auth.NoopCredentialService }

type recordingAuthHandler struct {
	key         string
	interactive bool
	exchangeErr error

	mu     sync.Mutex
	stored map[string]bool
	calls  []tool.Context
}

func newRecordingAuthHandler(key string) *recordingAuthHandler {
	return &recordingAuthHandler{key: key, interactive: true, stored: map[string]bool{}}
}

func slot(toolCtx *tool.Context) string { return toolCtx.AgentName + "|" + toolCtx.UserID }

func (h *recordingAuthHandler) record(toolCtx *tool.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, *toolCtx)
}

func (h *recordingAuthHandler) CredentialKey() string { return h.key }

func (h *recordingAuthHandler) HandleAuthResponse(_ context.Context, resp *a2a.AuthResponseData, toolCtx *tool.Context) error {
	h.record(toolCtx)
	if h.exchangeErr != nil {
		return h.exchangeErr
	}
	if resp.Code == "expired" {
		return errors.New("invalid_grant")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stored[slot(toolCtx)] = true
	return nil
}

func (h *recordingAuthHandler) CheckStoredCredential(_ context.Context, toolCtx *tool.Context) tool.StoredCredential {
	h.record(toolCtx)
	h.mu.Lock()
	defer h.mu.Unlock()
	return tool.StoredCredential{Connected: h.stored[slot(toolCtx)]}
}

func (h *recordingAuthHandler) BuildAuthSignal(_ context.Context, toolCtx *tool.Context) (*a2a.AuthenticationRequiredData, error) {
	h.record(toolCtx)
	return &a2a.AuthenticationRequiredData{CredentialKey: h.key, TargetAgent: toolCtx.AgentName}, nil
}

func (h *recordingAuthHandler) ClearCredential(_ context.Context, toolCtx *tool.Context) error {
	h.record(toolCtx)
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.stored, slot(toolCtx))
	return nil
}

func (h *recordingAuthHandler) SupportsUserAuth() bool  { return h.interactive }
func (h *recordingAuthHandler) AuthType() string        { return "oauth2" }
func (h *recordingAuthHandler) AuthDisplayName() string { return "CRM" }

func (h *recordingAuthHandler) isStored(component, user string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stored[component+"|"+user]
}

func (h *recordingAuthHandler) lastCall() tool.Context {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[len(h.calls)-1]
}

func TestWorkflowToolAuthControlMethods(t *testing.T) {
	creds := &markedCredentials{}
	svc := &stubServices{brk: &capturingBroker{}, creds: creds}
	w := newAuthzWorkflowInstance(t, "orders-flow", svc)
	crm := newRecordingAuthHandler("crm")
	static := newRecordingAuthHandler("static-key")
	static.interactive = false
	host := workflowToolinitHost{w: w}
	host.RegisterAuthHandler(crm)
	host.RegisterMCPAuthHandler(static)
	ctx := t.Context()

	infos := w.ListToolAuth(ctx, "system:eval")
	if len(infos) != 2 || infos[0].CredentialKey != "crm" || !infos[0].Interactive || infos[1].Interactive || infos[0].Status != "not_connected" {
		t.Fatalf("infos = %+v, want both credentials, crm interactive and not yet connected", infos)
	}
	if call := crm.lastCall(); call.AgentName != "orders-flow" || call.UserID != "system:eval" || call.CredentialService != creds {
		t.Errorf("probe ran as %q/%q with %v, want the workflow, the principal and its credential service", call.AgentName, call.UserID, call.CredentialService)
	}

	signal, err := w.BeginToolAuth(ctx, "crm", "system:eval")
	if err != nil || signal.TargetAgent != "orders-flow" {
		t.Fatalf("begin = %+v, %v; want a flow whose callback comes back to the workflow", signal, err)
	}
	if _, err := w.BeginToolAuth(ctx, "static-key", "system:eval"); err == nil {
		t.Error("a static credential began an interactive flow")
	}
	if _, err := w.BeginToolAuth(ctx, "nope", "system:eval"); !errors.Is(err, control.ErrResourceNotFound) {
		t.Errorf("unknown credential: %v, want not found", err)
	}

	crm.stored["orders-flow|system:eval"] = true
	if infos := w.ListToolAuth(ctx, "system:eval"); infos[0].Status != "connected" {
		t.Errorf("a stored credential reads %q", infos[0].Status)
	}
	if err := w.DisconnectToolAuth(ctx, "crm", "system:eval"); err != nil || crm.isStored("orders-flow", "system:eval") {
		t.Errorf("disconnect: %v, still stored = %v", err, crm.isStored("orders-flow", "system:eval"))
	}

	host.DeregisterAuthHandler("crm")
	if _, err := w.BeginToolAuth(ctx, "crm", "system:eval"); !errors.Is(err, control.ErrResourceNotFound) {
		t.Errorf("a deregistered credential can still be begun: %v", err)
	}
}

func authResponseMessage(taskID string, resp a2a.AuthResponseData, props a2a.UserProperties) *broker.Message {
	resp.Type = "auth_response"
	data := map[string]any{}
	raw, _ := json.Marshal(resp)
	_ = json.Unmarshal(raw, &data)
	req := a2a.NewRequest(taskID, "message/send", map[string]any{
		"message": a2a.Message{Kind: "message", MessageID: "m1", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewDataPart(data)}},
	})
	payload, _ := json.Marshal(req)
	return &broker.Message{Topic: "test-ns/a2a/v1/agent/request/orders-flow", Payload: payload, UserProperties: a2a.EncodeUserProperties(&props)}
}

func replyOn(brk *capturingBroker, topic string) (string, bool) {
	for _, m := range brk.snapshot() {
		if m.Topic == topic {
			return string(m.Payload), true
		}
	}
	return "", false
}

func waitForReply(t *testing.T, brk *capturingBroker, topic string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	reply, ok := replyOn(brk, topic)
	for !ok && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		reply, ok = replyOn(brk, topic)
	}
	if !ok {
		t.Fatalf("no reply on %s", topic)
	}
	return reply
}

func assertNoRunStarted(t *testing.T, brk *capturingBroker) {
	t.Helper()
	for _, m := range brk.snapshot() {
		if agentRequestTopics()[m.Topic] {
			t.Errorf("an auth response started a workflow run: it published to %s", m.Topic)
		}
	}
}

func TestWorkflowStoresAnAuthResponseWithoutRunning(t *testing.T) {
	brk := &capturingBroker{}
	creds := &markedCredentials{}
	w := newAuthzWorkflowInstance(t, "orders-flow", &stubServices{brk: brk, creds: creds})
	crm := newRecordingAuthHandler("crm")
	workflowToolinitHost{w: w}.RegisterAuthHandler(crm)

	w.routeMessage(t.Context(), authResponseMessage("cb-1", a2a.AuthResponseData{CredentialKey: "crm", Code: "abc", TargetAgent: "orders-flow"}, a2a.UserProperties{UserID: "system:eval", ReplyTo: "test/reply"}))
	reply := waitForReply(t, brk, "test/reply")
	if !strings.Contains(reply, "Authentication successful") || !strings.Contains(reply, `"agent_name":"orders-flow"`) || !strings.Contains(reply, `"is_mcp_credential_save":true`) {
		t.Fatalf("reply = %q, want the workflow's credential-saved completion", reply)
	}
	if call := crm.lastCall(); call.CredentialService != creds {
		t.Errorf("the exchange ran with credential service %v, want the workflow's", call.CredentialService)
	}

	w.routeMessage(t.Context(), authResponseMessage("cb-2", a2a.AuthResponseData{CredentialKey: "crm", Code: "abc"}, a2a.UserProperties{UserID: "system:other", ReplyTo: "test/reply-2"}))
	waitForReply(t, brk, "test/reply-2")
	if !crm.isStored("orders-flow", "system:eval") || !crm.isStored("orders-flow", "system:other") || crm.isStored("orders-flow", "") {
		t.Error("each callback's credential must be stored under the workflow and that callback's own user")
	}

	w.routeMessage(t.Context(), authResponseMessage("cb-3", a2a.AuthResponseData{CredentialKey: "crm", Code: "expired"}, a2a.UserProperties{UserID: "system:late", ReplyTo: "test/reply-3"}))
	if reply := waitForReply(t, brk, "test/reply-3"); !strings.Contains(reply, "authorization could not be completed") || crm.isStored("orders-flow", "system:late") {
		t.Errorf("a failed exchange: reply %q, stored %v; want an error and nothing stored", reply, crm.isStored("orders-flow", "system:late"))
	}

	w.routeMessage(t.Context(), authResponseMessage("cb-4", a2a.AuthResponseData{CredentialKey: "nope", Code: "abc"}, a2a.UserProperties{UserID: "system:eval", ReplyTo: "test/reply-4"}))
	if reply := waitForReply(t, brk, "test/reply-4"); !strings.Contains(reply, `no auth handler for credential key \"nope\"`) {
		t.Errorf("an undeclared credential: reply %q, want the no-handler error", reply)
	}
	assertNoRunStarted(t, brk)
}

func TestWorkflowAuthResponseFailureLogOmitsTheProvidersAnswer(t *testing.T) {
	const canary = "PROVIDER-FREE-TEXT-CANARY"
	for name, answer := range map[string]*oauth2.RetrieveError{
		"html page": {
			Response: &http.Response{StatusCode: http.StatusBadGateway},
			Body:     []byte("<html>" + canary + "</html>"),
		},
		"json without an error code": {
			Response: &http.Response{StatusCode: http.StatusBadRequest},
			Body:     []byte(`{"message":"` + canary + `"}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			brk := &capturingBroker{}
			w := newAuthzWorkflowInstance(t, "orders-flow", &stubServices{brk: brk})
			var logged strings.Builder
			w.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &sync.Mutex{}}, nil))
			crm := newRecordingAuthHandler("crm")
			crm.exchangeErr = fmt.Errorf("code exchange failed: %w", answer)
			workflowToolinitHost{w: w}.RegisterAuthHandler(crm)

			w.routeMessage(t.Context(), authResponseMessage("cb-1", a2a.AuthResponseData{CredentialKey: "crm", Code: "abc"}, a2a.UserProperties{UserID: "system:eval", ReplyTo: "test/reply"}))
			waitForReply(t, brk, "test/reply")

			got := logged.String()
			if !strings.Contains(got, "workflow tool credential exchange failed") {
				t.Fatalf("no exchange-failure log line; logs were:\n%s", got)
			}
			if strings.Contains(got, canary) {
				t.Errorf("the provider's answer reached the log:\n%s", got)
			}
			if !strings.Contains(got, fmt.Sprintf("httpStatus=%d", answer.Response.StatusCode)) {
				t.Errorf("the log does not carry the endpoint's status:\n%s", got)
			}
		})
	}
}

func TestWorkflowAuthResponseUnderRBAC(t *testing.T) {
	newWorkflow := func(t *testing.T, authn *fakeAuthn) (*WorkflowInstance, *capturingBroker, *recordingAuthHandler) {
		brk := &capturingBroker{}
		w := newAuthzWorkflowInstance(t, authzWorkflowCard, &stubServices{
			brk:       brk,
			authn:     authn,
			agentAuth: rbac.NewAgentAuth(nil, slog.Default()),
		})
		crm := newRecordingAuthHandler("crm")
		workflowToolinitHost{w: w}.RegisterAuthHandler(crm)
		return w, brk, crm
	}
	callback := a2a.AuthResponseData{CredentialKey: "crm", Code: "abc", TargetAgent: authzWorkflowCard}

	t.Run("scope-less signed callback stores the credential", func(t *testing.T) {
		w, brk, crm := newWorkflow(t, &fakeAuthn{identity: &auth.Identity{UserID: "system:eval"}})
		w.routeMessage(t.Context(), authResponseMessage("cb-1", callback, a2a.UserProperties{UserID: "system:eval", AuthToken: "signed-jwt"}))
		deadline := time.Now().Add(3 * time.Second)
		for !crm.isStored(authzWorkflowCard, "system:eval") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if !crm.isStored(authzWorkflowCard, "system:eval") {
			t.Fatal("the gateway's scope-less callback did not store the credential")
		}
		if n := len(brk.snapshot()); n != 0 {
			t.Errorf("published %d messages for a callback with no reply topic", n)
		}
	})

	t.Run("callback for another user than the signed one stores nothing", func(t *testing.T) {
		w, brk, crm := newWorkflow(t, &fakeAuthn{identity: &auth.Identity{UserID: "system:eval"}})
		w.routeMessage(t.Context(), authResponseMessage("cb-2", callback, a2a.UserProperties{UserID: "system:other", AuthToken: "signed-jwt", ReplyTo: "test/reply"}))
		if reply := waitForReply(t, brk, "test/reply"); !strings.Contains(reply, "does not match the signed identity") {
			t.Errorf("reply = %q, want the identity mismatch error", reply)
		}
		if crm.isStored(authzWorkflowCard, "system:other") || crm.isStored(authzWorkflowCard, "system:eval") {
			t.Error("a callback naming another user stored a credential")
		}
	})

	t.Run("unauthenticated callback stores nothing", func(t *testing.T) {
		w, brk, crm := newWorkflow(t, &fakeAuthn{err: errors.New("bad token")})
		w.routeMessage(t.Context(), authResponseMessage("cb-3", callback, a2a.UserProperties{UserID: "system:eval", AuthToken: "forged", ReplyTo: "test/reply"}))
		waitForReply(t, brk, "test/reply")
		if crm.isStored(authzWorkflowCard, "system:eval") {
			t.Error("a callback that failed authentication stored a credential")
		}
	})
}

func TestToolNodeContextCarriesCredentialsAndTrigger(t *testing.T) {
	creds := &markedCredentials{}
	w := newAuthzWorkflowInstance(t, "orders-flow", &stubServices{brk: &capturingBroker{}, creds: creds})
	for _, tc := range []struct{ wire, want string }{
		{a2a.TriggerEval, a2a.TriggerEval},
		{"made-up", a2a.TriggerUnknown},
	} {
		e := &DAGExecutor{cfg: w.cfg, state: NewExecutionState("orders-flow", "exec-1"), instance: w, execCtx: NewExecutionContext(t.Context(), "task-1", "ctx-1", nil, a2a.UserProperties{UserID: "system:eval", Trigger: tc.wire}, false)}
		toolCtx := e.buildToolContext(&NodeConfig{ID: "lookup", Type: "tool", ToolName: "crm_lookup"}, NodeCompletion{NodeID: "lookup", SubTaskID: "sub-1"})
		if toolCtx.CredentialService != creds {
			t.Errorf("tool context credential service = %v, want the workflow's", toolCtx.CredentialService)
		}
		if toolCtx.Trigger != tc.want {
			t.Errorf("trigger %q reached the tool as %q, want %q", tc.wire, toolCtx.Trigger, tc.want)
		}
		if !toolCtx.AuthPromptUnavailable {
			t.Error("a tool node's context must say it cannot prompt, so an auth wall starts no flow and keeps the credential")
		}
	}
}
