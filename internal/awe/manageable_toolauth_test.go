// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

var _ toolAuthManager = (*workflow.WorkflowInstance)(nil)

type mockToolAuthInstance struct {
	*mockInstance
	listUserID        string
	beginUserID       string
	beginCredKey      string
	disconnectUserID  string
	disconnectCredKey string
	listResult        []agent.ToolAuthInfo
	beginResult       *a2a.AuthenticationRequiredData
	disconnectErr     error
}

func (m *mockToolAuthInstance) ListToolAuth(_ context.Context, userID string) []agent.ToolAuthInfo {
	m.listUserID = userID
	return m.listResult
}

func (m *mockToolAuthInstance) BeginToolAuth(_ context.Context, credentialKey, userID string) (*a2a.AuthenticationRequiredData, error) {
	m.beginCredKey = credentialKey
	m.beginUserID = userID
	return m.beginResult, nil
}

type recordingAuditLogger struct {
	auth.NoopAuditLogger
	events []auth.ControlAccessEvent
}

func (r *recordingAuditLogger) LogControlAccess(_ context.Context, event auth.ControlAccessEvent) {
	r.events = append(r.events, event)
}

func (m *mockToolAuthInstance) DisconnectToolAuth(_ context.Context, credentialKey, userID string) error {
	m.disconnectCredKey = credentialKey
	m.disconnectUserID = userID
	return m.disconnectErr
}

func newToolAuthExecutor(t *testing.T) (*Executor, *mockToolAuthInstance) {
	t.Helper()
	exe, inst, _ := newToolAuthExecutorWithAudit(t)
	return exe, inst
}

func newToolAuthExecutorWithAudit(t *testing.T) (*Executor, *mockToolAuthInstance, *recordingAuditLogger) {
	t.Helper()
	audit := &recordingAuditLogger{}
	exe := NewExecutor(runtime.NewRuntime(broker.NewDevBroker(), nil, runtime.WithAuditLogger(audit)).Services())
	inst := &mockToolAuthInstance{
		mockInstance: newMockInstance("auth-agent", "agent"),
		listResult: []agent.ToolAuthInfo{
			{CredentialKey: "cred-1", Interactive: true, Status: "not_connected"},
		},
		beginResult: &a2a.AuthenticationRequiredData{CredentialKey: "cred-1", UserID: "user-1"},
	}
	exe.AddInstance(inst)
	exe.findInstance("auth-agent").setStatus(StatusRunning)
	return exe, inst, audit
}

func toolAuthPath(action string) []string {
	return []string{"instances", "auth-agent", "toolAuth", action}
}

func TestHandleToolAuthQuery(t *testing.T) {
	exe, inst := newToolAuthExecutor(t)

	result, err := exe.HandleManagement(t.Context(), "post", toolAuthPath("query"),
		json.RawMessage(`{"userId":"system:eval"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	infos, ok := result.([]agent.ToolAuthInfo)
	if !ok {
		t.Fatalf("expected []agent.ToolAuthInfo, got %T", result)
	}
	if len(infos) != 1 || infos[0].CredentialKey != "cred-1" {
		t.Errorf("infos = %+v", infos)
	}
	if inst.listUserID != "system:eval" {
		t.Errorf("userID passed = %q", inst.listUserID)
	}
}

func TestHandleToolAuthBegin(t *testing.T) {
	exe, inst := newToolAuthExecutor(t)

	result, err := exe.HandleManagement(t.Context(), "post", toolAuthPath("begin"),
		json.RawMessage(`{"credentialKey":"cred-1","userId":"system:reporting"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	signal, ok := result.(*a2a.AuthenticationRequiredData)
	if !ok {
		t.Fatalf("expected *a2a.AuthenticationRequiredData, got %T", result)
	}
	if signal.CredentialKey != "cred-1" {
		t.Errorf("signal = %+v", signal)
	}
	if inst.beginCredKey != "cred-1" || inst.beginUserID != "system:reporting" {
		t.Errorf("begin args = %q, %q", inst.beginCredKey, inst.beginUserID)
	}
}

func TestHandleToolAuthUnknownInstance(t *testing.T) {
	exe, _ := newToolAuthExecutor(t)

	_, err := exe.HandleManagement(t.Context(), "post",
		[]string{"instances", "ghost", "toolAuth", "query"},
		json.RawMessage(`{"userId":"u"}`), nil)
	if !errors.Is(err, control.ErrResourceNotFound) {
		t.Errorf("err = %v, want ErrResourceNotFound", err)
	}
}

func TestHandleToolAuthInstanceNotYetRunning(t *testing.T) {
	bodies := map[string]json.RawMessage{
		"query":      json.RawMessage(`{"userId":"system:eval"}`),
		"begin":      json.RawMessage(`{"credentialKey":"cred-1","userId":"system:eval"}`),
		"disconnect": json.RawMessage(`{"credentialKey":"cred-1","userId":"system:eval"}`),
	}
	for _, status := range []string{StatusCreated, StatusStarting, StatusStopping, StatusStopped, StatusError} {
		t.Run(status, func(t *testing.T) {
			exe, inst := newToolAuthExecutor(t)
			exe.findInstance("auth-agent").setStatus(status)

			for action, body := range bodies {
				_, err := exe.HandleManagement(t.Context(), "post", toolAuthPath(action), body, nil)
				if !errors.Is(err, control.ErrResourceNotFound) || !strings.Contains(err.Error(), control.InstanceNotRunning) {
					t.Errorf("%s: err = %v, want ErrResourceNotFound saying %q", action, err, control.InstanceNotRunning)
				}
			}
			if inst.listUserID != "" || inst.beginUserID != "" || inst.disconnectUserID != "" {
				t.Errorf("a handler ran on an instance that is not running: %+v", inst)
			}
		})
	}
}

func TestHandleToolAuthUnsupportedInstance(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("plain-agent", "agent"))

	_, err := exe.HandleManagement(t.Context(), "post",
		[]string{"instances", "plain-agent", "toolAuth", "query"},
		json.RawMessage(`{"userId":"u"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "does not support tool auth management") {
		t.Errorf("err = %v", err)
	}
}

func TestHandleToolAuthBadBodies(t *testing.T) {
	exe, _ := newToolAuthExecutor(t)

	cases := []struct {
		name   string
		action string
		body   json.RawMessage
	}{
		{"query empty body", "query", nil},
		{"query invalid json", "query", json.RawMessage(`not json`)},
		{"query unknown field", "query", json.RawMessage(`{"userId":"u","extra":true}`)},
		{"query missing userId", "query", json.RawMessage(`{}`)},
		{"query non-system userId", "query", json.RawMessage(`{"userId":"alice@example.com"}`)},
		{"begin missing userId", "begin", json.RawMessage(`{"credentialKey":"k"}`)},
		{"begin missing credentialKey", "begin", json.RawMessage(`{"userId":"system:eval"}`)},
		{"begin non-system userId", "begin", json.RawMessage(`{"credentialKey":"k","userId":"alice@example.com"}`)},
		{"disconnect missing userId", "disconnect", json.RawMessage(`{"credentialKey":"k"}`)},
		{"disconnect missing credentialKey", "disconnect", json.RawMessage(`{"userId":"system:eval"}`)},
		{"disconnect non-system userId", "disconnect", json.RawMessage(`{"credentialKey":"k","userId":"alice@example.com"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exe.HandleManagement(t.Context(), "post", toolAuthPath(tc.action), tc.body, nil); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestHandleToolAuthScopeGate(t *testing.T) {
	cases := []struct {
		name        string
		identity    *auth.Identity
		wantAllowed bool
	}{
		{"rbac not configured", nil, true},
		{"identity without scopes", &auth.Identity{UserID: "caller"}, true},
		{"eval invoke scope", &auth.Identity{UserID: "caller", Scopes: []string{rbac.ScopeEvaluationInvoke}}, true},
		{"unrelated scope", &auth.Identity{UserID: "caller", Scopes: []string{rbac.ScopeToolRead}}, false},
	}
	for _, tc := range cases {
		for _, action := range []string{"query", "begin", "disconnect"} {
			t.Run(tc.name+"/"+action, func(t *testing.T) {
				exe, _, audit := newToolAuthExecutorWithAudit(t)
				body := json.RawMessage(`{"userId":"system:eval"}`)
				if action != "query" {
					body = json.RawMessage(`{"credentialKey":"cred-1","userId":"system:eval"}`)
				}

				_, err := exe.HandleManagement(t.Context(), "post", toolAuthPath(action), body, tc.identity)
				if tc.wantAllowed && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if !tc.wantAllowed && (err == nil || !strings.Contains(err.Error(), rbac.ScopeEvaluationInvoke)) {
					t.Fatalf("err = %v, want a denial naming %s", err, rbac.ScopeEvaluationInvoke)
				}

				if len(audit.events) != 1 {
					t.Fatalf("audit events = %+v, want exactly one", audit.events)
				}
				got := audit.events[0]
				wantResource := "awe/instances/auth-agent/toolAuth/" + action
				if got.Allowed != tc.wantAllowed || got.AppName != wantResource || got.Method != "post" {
					t.Errorf("audit event = %+v, want allowed=%v appName=%q", got, tc.wantAllowed, wantResource)
				}
			})
		}
	}
}

func TestHandleToolAuthUnsupportedActionAndMethod(t *testing.T) {
	exe, _ := newToolAuthExecutor(t)

	if _, err := exe.HandleManagement(t.Context(), "post", toolAuthPath("revoke"),
		json.RawMessage(`{"userId":"u"}`), nil); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("err = %v", err)
	}
	if _, err := exe.HandleManagement(t.Context(), "get", toolAuthPath("query"),
		json.RawMessage(`{"userId":"u"}`), nil); err == nil {
		t.Error("expected error for non-post method")
	}
}

func TestHandleToolAuthDisconnect(t *testing.T) {
	exe, inst := newToolAuthExecutor(t)

	result, err := exe.HandleManagement(t.Context(), "post", toolAuthPath("disconnect"),
		json.RawMessage(`{"credentialKey":"cred-1","userId":"system:eval"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil (the platform replies 204)", result)
	}
	if inst.disconnectCredKey != "cred-1" || inst.disconnectUserID != "system:eval" {
		t.Errorf("disconnect args = %q, %q", inst.disconnectCredKey, inst.disconnectUserID)
	}
}

func TestHandleToolAuthDisconnectPropagatesFailure(t *testing.T) {
	exe, inst := newToolAuthExecutor(t)
	inst.disconnectErr = errors.New("credential store unavailable")

	_, err := exe.HandleManagement(t.Context(), "post", toolAuthPath("disconnect"),
		json.RawMessage(`{"credentialKey":"cred-1","userId":"system:eval"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "credential store unavailable") {
		t.Errorf("err = %v, want the instance's own error", err)
	}
}
