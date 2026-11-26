// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	testNS = "test-ns"
	testGW = "test-gw"
)

type webhookRig struct {
	t       *testing.T
	brk     *broker.DevBroker
	core    *entrypoint.GatewayCore
	adapter *WebhookAdapter
	server  *httptest.Server
}

type failSaveStore struct {
	artifact.Service
	errSave error
}

func (s *failSaveStore) Save(_ context.Context, _ artifact.Locator, _ []byte, _ string) (int, error) {
	return 0, s.errSave
}

func newWebhookRig(t *testing.T, adapterCfg map[string]any, artInner ...artifact.Service) *webhookRig {
	t.Helper()
	return newWebhookRigWithAppConfig(t, adapterCfg, nil, artInner...)
}

func newWebhookRigWithAppConfig(t *testing.T, adapterCfg, extraAppConfig map[string]any, artInner ...artifact.Service) *webhookRig {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	adapter, err := NewWebhookAdapter(adapterCfg)
	if err != nil {
		t.Fatalf("NewWebhookAdapter: %v", err)
	}

	var handler http.Handler
	adapter.SetOnHandlerReady(func(h http.Handler) { handler = h })

	appConfig := map[string]any{"namespace": testNS, "gateway_id": testGW}
	maps.Copy(appConfig, extraAppConfig)
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	var inner artifact.Service
	if len(artInner) > 0 {
		inner = artInner[0]
	} else {
		inner = memory.New()
	}
	artSvc := &artifact.ScopedService{Inner: inner, Scope: "namespace", Namespace: "local-dev"}
	rt := runtime.NewRuntime(brk, nil, runtime.WithArtifacts(artSvc))
	core.SetServices(rt.Services())

	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("core.Start: %v", err)
	}
	if handler == nil {
		t.Fatal("adapter did not provide an HTTP handler")
	}

	rig := &webhookRig{t: t, brk: brk, core: core, adapter: adapter, server: httptest.NewServer(handler)}
	t.Cleanup(rig.cleanup)
	return rig
}

func (r *webhookRig) cleanup() {
	r.server.Close()
	if err := r.core.Stop(context.Background()); err != nil {
		r.t.Errorf("core.Stop: %v", err)
	}
}

type agentRequest struct {
	taskID    string
	text      string
	fileParts []a2a.FileContent
	props     a2a.UserProperties
}

func (r *webhookRig) startAgent(agentName string, ch chan<- agentRequest) {
	t := r.t
	q, err := r.brk.CreateQueue("q-"+agentName, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err := r.brk.Subscribe(q, a2a.AgentRequestTopic(testNS, agentName)); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}

	go func() {
		ctx := t.Context()
		for {
			msg, err := r.brk.Receive(ctx, q)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			if msg == nil {
				continue
			}
			msg.Ack()
			select {
			case ch <- parseAgentRequest(msg.Payload, msg.UserProperties):
			case <-ctx.Done():
				return
			}
		}
	}()
}

func parseAgentRequest(payload []byte, userProps map[string]string) agentRequest {
	var req a2a.Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return agentRequest{text: "PARSE_FAILED: " + err.Error()}
	}
	out := agentRequest{taskID: req.ID, props: a2a.DecodeUserProperties(userProps)}
	pj, err := json.Marshal(req.Params)
	if err != nil {
		return agentRequest{text: "PARSE_FAILED: marshal params: " + err.Error()}
	}
	var p struct {
		Message a2a.Message `json:"message"`
	}
	if err := json.Unmarshal(pj, &p); err != nil {
		return agentRequest{text: "PARSE_FAILED: unmarshal message: " + err.Error()}
	}
	for _, part := range p.Message.Parts {
		switch part.Kind {
		case "text":
			if out.text == "" {
				out.text = part.Text
			}
		case "file":
			if part.File != nil {
				out.fileParts = append(out.fileParts, *part.File)
			}
		}
	}
	return out
}

func (r *webhookRig) post(path, body string, headers map[string]string) *http.Response {
	r.t.Helper()
	req, err := http.NewRequest("POST", r.server.URL+path, strings.NewReader(body))
	if err != nil {
		r.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func tokenRoute(path, target string) map[string]any {
	return map[string]any{
		"name":              "r-" + strings.Trim(path, "/"),
		"match":             map[string]any{"path": path, "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "input.payload:message",
		"target_agent_name": target,
		"run_as":            "default",
	}
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func decodeTaskID(t *testing.T, resp *http.Response) string {
	t.Helper()
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	_ = resp.Body.Close()
	return out["taskId"]
}

func TestWebhook_TokenAuth_202_AgentReceives(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"hello via token"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	taskID := decodeTaskID(t, resp)
	if taskID == "" {
		t.Fatal("expected taskId in 202 response")
	}

	select {
	case req := <-ch:
		if req.taskID != taskID {
			t.Errorf("agent taskID = %q, want %q", req.taskID, taskID)
		}
		if req.text != "hello via token" {
			t.Errorf("agent text = %q, want %q", req.text, "hello via token")
		}
		if req.props.UserID != rbac.DefaultSystemUserSub {
			t.Errorf("agent userID = %q, want default system user %q", req.props.UserID, rbac.DefaultSystemUserSub)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}
}

func TestWebhook_SubmitsWithTheEventTrigger(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"hello"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()

	select {
	case req := <-ch:
		if req.props.Trigger != a2a.TriggerEvent {
			t.Errorf("trigger = %q, want %q", req.props.Trigger, a2a.TriggerEvent)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}
}

func TestWebhook_SubmitsWithTheRouteAndTheSendersEvent(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	headers := bearer("dev-webhook-token")
	headers["X-GitHub-Event"] = "check_suite"
	headers["X-GitHub-Delivery"] = "72d3162e-cc78-11e3-81ab-4c9367dc0958"
	resp := rig.post("/token", `{"message":"hello","action":"completed"}`, headers)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()

	select {
	case req := <-ch:
		want := a2a.TriggerSource{
			TriggerRoute:      "r-token",
			TriggerEvent:      "check_suite.completed",
			TriggerDeliveryID: "72d3162e-cc78-11e3-81ab-4c9367dc0958",
		}
		if req.props.TriggerSource != want {
			t.Errorf("trigger source = %+v, want %+v", req.props.TriggerSource, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}
}

func TestWebhook_RecordsOnlyTheRouteForAnUnknownSender(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	headers := bearer("dev-webhook-token")
	headers["X-Custom-Event"] = "order.created"
	resp := rig.post("/token", `{"message":"hello","action":"completed"}`, headers)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()

	select {
	case req := <-ch:
		if want := (a2a.TriggerSource{TriggerRoute: "r-token"}); req.props.TriggerSource != want {
			t.Errorf("trigger source = %+v, want %+v", req.props.TriggerSource, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}
}

func TestWebhook_CustomRunAsSubmitsWithTheRenderedSystemUserRow(t *testing.T) {
	const qaBotID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42"
	route := tokenRoute("/token", "Orchestrator")
	route["run_as"] = "qa-bot"
	rig := newWebhookRigWithAppConfig(t, map[string]any{"routes": []any{route}},
		map[string]any{"run_as_user_uuids": map[string]any{"system:qa-bot": qaBotID}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"hello"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()

	select {
	case req := <-ch:
		if req.props.UserID != "system:qa-bot" || req.props.UserUUID != qaBotID {
			t.Errorf("task ran as %q with user uuid %q, want system:qa-bot with %s", req.props.UserID, req.props.UserUUID, qaBotID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}
}

type stubScopeResolver struct{ byUser map[string][]string }

func (r stubScopeResolver) ResolveScopes(userID string, _ []string) []string { return r.byUser[userID] }

func TestWebhook_EnforcedRBAC_DefaultSystemUserAuthorized(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	rig.core.SetScopeResolver(stubScopeResolver{byUser: map[string][]string{
		rbac.DefaultSystemUserSub: {rbac.AgentInvokeScope("Orchestrator")},
	}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"hi"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (authorized as the default system user)", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case req := <-ch:
		if req.props.UserID != rbac.DefaultSystemUserSub {
			t.Errorf("agent userID = %q, want %q", req.props.UserID, rbac.DefaultSystemUserSub)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task (an authorized request must publish)")
	}
}

func TestWebhook_EnforcedRBAC_UnseededSystemUser_FailsClosed(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	rig.core.SetScopeResolver(stubScopeResolver{byUser: map[string][]string{}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"hi"}`, bearer("dev-webhook-token"))
	if resp.StatusCode == http.StatusAccepted {
		t.Fatalf("status = 202, want failure (a scope-less system user must fail closed)")
	}
	_ = resp.Body.Close()
	select {
	case req := <-ch:
		t.Fatalf("agent unexpectedly received a task from a scope-less system user: %+v", req)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestWebhook_EnforcedRBAC_InvokeDenied_403(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	rig.core.SetScopeResolver(stubScopeResolver{byUser: map[string][]string{}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"hi"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an identity without the agent's invoke scope", resp.StatusCode)
	}
	var env map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	_ = resp.Body.Close()
	if env["message"] != "forbidden" || env["errorId"] == "" {
		t.Errorf("error envelope = %v, want {message: forbidden, errorId: ...}", env)
	}
	select {
	case req := <-ch:
		t.Fatalf("agent unexpectedly received a task: %+v", req)
	case <-time.After(300 * time.Millisecond):
	}
}

type embeddedDefaultsResolver struct{ c *rbac.Composer }

func (r embeddedDefaultsResolver) ResolveScopes(userID string, _ []string) []string {
	res, _ := r.c.Lookup(userID, "", nil)
	return res.Scopes
}

func TestWebhook_EnforcedRBAC_RealEmbeddedDefaults(t *testing.T) {
	roles, users, err := rbac.EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}
	composer := rbac.NewComposer(rbac.YAMLState{Roles: roles, Users: users}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := composer.Rebuild(context.Background()); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}

	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	rig.core.SetScopeResolver(embeddedDefaultsResolver{c: composer})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"hi"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (authorized via real EmbeddedDefaults)", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case req := <-ch:
		if req.props.UserID != rbac.DefaultSystemUserSub {
			t.Errorf("agent userID = %q, want %q", req.props.UserID, rbac.DefaultSystemUserSub)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task; system_defaults.yaml may not grant the default system user")
	}
}

func TestWebhook_NoRunAs_UsesDefaultSystemUser(t *testing.T) {
	route := tokenRoute("/token", "Orchestrator")
	delete(route, "run_as")
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"hi"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (runs as the default system user)", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case req := <-ch:
		if req.props.UserID != rbac.DefaultSystemUserSub {
			t.Errorf("userID = %q, want %q", req.props.UserID, rbac.DefaultSystemUserSub)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent received no task; a run_as-less webhook should run as the default system user")
	}
}

func TestWebhook_SubmitFails_502(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})

	if err := rig.brk.Disconnect(t.Context()); err != nil {
		t.Fatalf("broker disconnect: %v — test premise cannot hold", err)
	}

	resp := rig.post("/token", `{"message":"this should fail"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 when broker is down", resp.StatusCode)
	}
	var env map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	_ = resp.Body.Close()
	if env["message"] != "failed to submit task" || env["errorId"] == "" {
		t.Errorf("error envelope = %v, want {message: failed to submit task, errorId: ...}", env)
	}
}

func TestWebhook_InvalidToken_401_NoSubmit(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/token", "Orchestrator")}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/token", `{"message":"should fail"}`, bearer("wrong-token"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var env map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	_ = resp.Body.Close()
	if env["message"] != "unauthorized" || env["errorId"] == "" {
		t.Errorf("error envelope = %v, want {message:unauthorized, errorId:...}", env)
	}
	select {
	case <-ch:
		t.Fatal("no task should be submitted on auth failure")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWebhook_HMAC(t *testing.T) {
	secret := "dev-hmac-secret-value"
	route := map[string]any{
		"name":  "hmac_route",
		"match": map[string]any{"path": "/hmac", "method": "POST"},
		"auth": map[string]any{"hmac": map[string]any{
			"header": "X-Hub-Signature-256", "algo": "sha256", "secret": secret, "prefix": "sha256=", "encoding": "hex",
		}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	body := `{"message":"hello via hmac"}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	resp := rig.post("/hmac", body, map[string]string{"X-Hub-Signature-256": sig})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("valid HMAC: status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case req := <-ch:
		if req.text != "hello via hmac" {
			t.Errorf("agent text = %q, want %q", req.text, "hello via hmac")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the HMAC task")
	}

	bad := rig.post("/hmac", body, map[string]string{"X-Hub-Signature-256": "sha256=deadbeef"})
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered HMAC: status = %d, want 401", bad.StatusCode)
	}
	_ = bad.Body.Close()
}

func TestWebhook_ConcurrentDistinctTaskIDs(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/concurrent", "Orchestrator")}})
	ch := make(chan agentRequest, 16)
	rig.startAgent("Orchestrator", ch)

	const n = 10
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, err := http.NewRequest("POST", rig.server.URL+"/concurrent", strings.NewReader(fmt.Sprintf(`{"message":"concurrent %d"}`, i)))
			if err != nil {
				t.Errorf("req %d: new request: %v", i, err)
				return
			}
			req.Header.Set("Authorization", "Bearer dev-webhook-token")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("req %d: %v", i, err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusAccepted {
				t.Errorf("req %d: status = %d, want 202", i, resp.StatusCode)
				return
			}
			var out map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Errorf("req %d: decode: %v", i, err)
				return
			}
			ids[i] = out["taskId"]
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, n)
	for _, id := range ids {
		if id == "" {
			t.Fatal("empty taskId")
		}
		if seen[id] {
			t.Fatalf("duplicate taskId %q", id)
		}
		seen[id] = true
	}

	received := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		select {
		case req := <-ch:
			if strings.HasPrefix(req.text, "PARSE_FAILED") {
				t.Errorf("agent received malformed task: %s", req.text)
			}
			received[req.taskID] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d/%d tasks reached the agent", len(received), n)
		}
	}
	for id := range seen {
		if !received[id] {
			t.Errorf("taskId %q submitted but never received by agent", id)
		}
	}
}

func TestWebhook_Base64ArtifactReachesAgent(t *testing.T) {
	route := map[string]any{
		"name":              "upload",
		"match":             map[string]any{"path": "/upload", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
		"artifact_processing": map[string]any{
			"extract_artifacts_expression": "input.payload:files",
			"artifact_definition": map[string]any{
				"filename": "list_item:name", "content": "list_item:data",
				"mime_type": "list_item:type", "content_encoding": "static:base64",
			},
		},
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	body := `{"message":"process this","files":[{"name":"test.txt","data":"` + base64.StdEncoding.EncodeToString([]byte("hello file")) + `","type":"text/plain"}]}`
	resp := rig.post("/upload", body, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()

	select {
	case req := <-ch:
		if len(req.fileParts) != 1 {
			t.Fatalf("agent received %d file parts, want 1", len(req.fileParts))
		}
		if got := req.fileParts[0].Name; got != "test.txt" {
			t.Errorf("file part name = %q, want test.txt", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the upload task")
	}
}

func TestWebhook_WorkflowTarget(t *testing.T) {
	route := map[string]any{
		"name":                 "workflow_route",
		"match":                map[string]any{"path": "/workflow", "method": "POST"},
		"auth":                 map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":     "input.payload:message",
		"target_workflow_name": "TestWorkflow",
		"run_as":               "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("TestWorkflow", ch)

	resp := rig.post("/workflow", `{"message":"run the workflow"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	taskID := decodeTaskID(t, resp)
	select {
	case req := <-ch:
		if req.taskID != taskID {
			t.Errorf("workflow taskID = %q, want %q", req.taskID, taskID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("workflow never received the task")
	}
}

func TestWebhook_TokenAndHMAC_BothRequired(t *testing.T) {
	secret := "dev-hmac-secret-value"
	route := map[string]any{
		"name":  "dual",
		"match": map[string]any{"path": "/dual", "method": "POST"},
		"auth": map[string]any{
			"token": map[string]any{"value": "dev-webhook-token"},
			"hmac":  map[string]any{"header": "X-Sig", "algo": "sha256", "secret": secret, "encoding": "hex"},
		},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	body := `{"message":"dual auth"}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	goodSig := hex.EncodeToString(mac.Sum(nil))

	cases := []struct {
		name       string
		token, sig string
		wantStatus int
	}{
		{"both valid", "dev-webhook-token", goodSig, http.StatusAccepted},
		{"valid token, bad hmac", "dev-webhook-token", "deadbeef", http.StatusUnauthorized},
		{"bad token, valid hmac", "wrong-token", goodSig, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := rig.post("/dual", body, map[string]string{
				"Authorization": "Bearer " + tc.token,
				"X-Sig":         tc.sig,
			})
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			_ = resp.Body.Close()
		})
	}
}

func TestWebhook_TokenCheckedBeforeBodyRead(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{
		"max_body_bytes": float64(16),
		"routes":         []any{tokenRoute("/guard", "Orchestrator")},
	})
	resp := rig.post("/guard", `{"message":"this body is well over the sixteen byte cap"}`, bearer("wrong-token"))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (token must be rejected before the body-size cap)", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestWebhook_ArtifactProcessingError400(t *testing.T) {
	route := map[string]any{
		"name":              "upload",
		"match":             map[string]any{"path": "/upload", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
		"artifact_processing": map[string]any{
			"extract_artifacts_expression": "input.payload:files",
			"artifact_definition": map[string]any{
				"filename": "list_item:name", "content": "list_item:data",
				"content_encoding": "static:base64",
			},
		},
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	body := `{"message":"m","files":[{"name":"x.txt","data":"!!not-base64!!"}]}`
	resp := rig.post("/upload", body, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var env map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	_ = resp.Body.Close()
	if env["message"] != "invalid artifact data" || env["errorId"] == "" {
		t.Errorf("error envelope = %v, want {message: invalid artifact data, errorId: ...}", env)
	}
	select {
	case <-ch:
		t.Fatal("no task should be submitted when artifact processing fails")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWebhook_ArtifactStorageFailure500(t *testing.T) {
	route := map[string]any{
		"name":              "upload",
		"match":             map[string]any{"path": "/upload", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
		"artifact_processing": map[string]any{
			"extract_artifacts_expression": "input.payload:files",
			"artifact_definition": map[string]any{
				"filename": "list_item:name", "content": "list_item:data",
				"content_encoding": "static:base64",
			},
		},
	}
	failStore := &failSaveStore{Service: memory.New(), errSave: fmt.Errorf("simulated storage failure")}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}}, failStore)
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	body := `{"message":"m","files":[{"name":"x.txt","data":"` + base64.StdEncoding.EncodeToString([]byte("hello")) + `"}]}`
	resp := rig.post("/upload", body, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	var env map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	_ = resp.Body.Close()
	if env["message"] != "artifact processing failed" || env["errorId"] == "" {
		t.Errorf("error envelope = %v, want {message: artifact processing failed, errorId: ...}", env)
	}
	select {
	case <-ch:
		t.Fatal("no task should be submitted on storage failure")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWebhook_OversizeBody413(t *testing.T) {
	route := tokenRoute("/tiny", "Orchestrator")
	rig := newWebhookRig(t, map[string]any{
		"max_body_bytes": float64(16),
		"routes":         []any{route},
	})
	resp := rig.post("/tiny", `{"message":"this body is well over sixteen bytes"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestWebhook_PerRouteMaxBodyBytes(t *testing.T) {
	small := map[string]any{
		"name":              "small",
		"match":             map[string]any{"path": "/small", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"max_body_bytes":    float64(16),
	}
	large := tokenRoute("/large", "Orchestrator")

	rig := newWebhookRig(t, map[string]any{"routes": []any{small, large}})

	oversize := `{"message":"this body is well over sixteen bytes"}`

	resp := rig.post("/small", oversize, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("/small: status = %d, want 413 (per-route cap)", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = rig.post("/large", oversize, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("/large: status = %d, want 202 (global cap not hit)", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestWebhook_PathParamsReachAgent(t *testing.T) {
	route := map[string]any{
		"name":              "orders",
		"match":             map[string]any{"path": "/orders/{id}", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "user_data.path:id",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/orders/order-42", `{}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case req := <-ch:
		if req.text != "order-42" {
			t.Errorf("agent text = %q, want %q", req.text, "order-42")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}
}

func TestWebhook_QueryParamsReachAgent(t *testing.T) {
	route := map[string]any{
		"name":              "search",
		"match":             map[string]any{"path": "/search", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "user_data.query:q",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	req, err := http.NewRequest("POST", rig.server.URL+"/search?q=hello+world", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer dev-webhook-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case ar := <-ch:
		if ar.text != "hello world" {
			t.Errorf("agent text = %q, want %q", ar.text, "hello world")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}
}

func TestWebhook_QueryTokenNotLeakedToAgent(t *testing.T) {
	route := map[string]any{
		"name":  "qauth",
		"match": map[string]any{"path": "/qauth", "method": "POST"},
		"auth": map[string]any{
			"token": map[string]any{"location": "query", "name": "api_key", "value": "dev-webhook-token"},
		},
		"input_expression":  "user_data.query:visible",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	req, err := http.NewRequest("POST", rig.server.URL+"/qauth?api_key=dev-webhook-token&visible=yes", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case ar := <-ch:
		if ar.text != "yes" {
			t.Errorf("agent text = %q, want %q (query token must not be visible)", ar.text, "yes")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}
}

func TestWebhook_HeaderAliasReachesAgent(t *testing.T) {
	route := map[string]any{
		"name":              "events",
		"match":             map[string]any{"path": "/events", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "user_data.headers:event_type",
		"headers":           map[string]any{"event_type": "X-Event-Type"},
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/events", `{}`, map[string]string{
		"Authorization": "Bearer dev-webhook-token",
		"X-Event-Type":  "order.created",
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case ar := <-ch:
		if ar.text != "order.created" {
			t.Errorf("agent text = %q, want order.created", ar.text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}
}

func TestWebhook_TokenLocationHeader(t *testing.T) {
	route := map[string]any{
		"name":              "hdr",
		"match":             map[string]any{"path": "/hdr", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"location": "header", "name": "X-Api-Token", "value": "dev-webhook-token"}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	good := rig.post("/hdr", `{"message":"header-token"}`, map[string]string{"X-Api-Token": "dev-webhook-token"})
	if good.StatusCode != http.StatusAccepted {
		t.Fatalf("valid header token: status = %d, want 202", good.StatusCode)
	}
	_ = good.Body.Close()
	select {
	case ar := <-ch:
		if ar.text != "header-token" {
			t.Errorf("text = %q, want header-token", ar.text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}

	bad := rig.post("/hdr", `{"message":"x"}`, map[string]string{"X-Api-Token": "wrong"})
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong header token: status = %d, want 401", bad.StatusCode)
	}
	_ = bad.Body.Close()
}

func TestWebhook_TokenLocationQuery(t *testing.T) {
	route := map[string]any{
		"name":              "qtoken",
		"match":             map[string]any{"path": "/qtoken", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"location": "query", "name": "tok", "value": "dev-webhook-token"}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	good, err := http.NewRequest("POST", rig.server.URL+"/qtoken?tok=dev-webhook-token", strings.NewReader(`{"message":"query-token"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(good)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("valid query token: status = %d, want 202", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case ar := <-ch:
		if ar.text != "query-token" {
			t.Errorf("text = %q, want query-token", ar.text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent never received the task")
	}

	bad, err := http.NewRequest("POST", rig.server.URL+"/qtoken?tok=wrong", strings.NewReader(`{"message":"x"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err = http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong query token: status = %d, want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestWebhook_EmptyInputExpression_400(t *testing.T) {
	rig := newWebhookRig(t, map[string]any{"routes": []any{tokenRoute("/empty-input", "Orchestrator")}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/empty-input", `{"other":"field"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (empty input must not dispatch task)", resp.StatusCode)
	}
	var env map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	_ = resp.Body.Close()
	if env["message"] != "input expression resolved to empty string" || env["errorId"] == "" {
		t.Errorf("error envelope = %v", env)
	}
	select {
	case <-ch:
		t.Fatal("no task should be submitted for empty input")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWebhook_EmptyArtifactContent_500(t *testing.T) {
	route := map[string]any{
		"name":              "upload",
		"match":             map[string]any{"path": "/upload-empty", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
		"artifact_processing": map[string]any{
			"extract_artifacts_expression": "input.payload:files",
			"artifact_definition": map[string]any{
				"filename":         "list_item:name",
				"content":          "list_item:data",
				"content_encoding": "static:utf-8",
			},
		},
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/upload-empty", `{"message":"m","files":[{"name":"f.txt","data":""}]}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for empty artifact content", resp.StatusCode)
	}
	_ = resp.Body.Close()
	select {
	case <-ch:
		t.Fatal("no task should be submitted when artifact content is empty")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWebhook_ArtifactConfigFault500(t *testing.T) {
	route := map[string]any{
		"name":              "cfg_fault",
		"match":             map[string]any{"path": "/cfg-fault", "method": "POST"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "input.payload:message",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
		"artifact_processing": map[string]any{
			"extract_artifacts_expression": "badsource:field",
			"artifact_definition":          map[string]any{"content": "list_item:data"},
		},
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{route}})
	ch := make(chan agentRequest, 1)
	rig.startAgent("Orchestrator", ch)

	resp := rig.post("/cfg-fault", `{"message":"trigger"}`, bearer("dev-webhook-token"))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("config fault: status = %d, want 500 (not 400 which would blame the caller)", resp.StatusCode)
	}
	var env map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	_ = resp.Body.Close()
	if env["message"] != "artifact processing failed" || env["errorId"] == "" {
		t.Errorf("error envelope = %v, want {message: artifact processing failed, errorId: ...}", env)
	}
	select {
	case <-ch:
		t.Fatal("no task should be submitted on config fault")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWebhook_PutAndPatch(t *testing.T) {
	put := map[string]any{
		"name":              "put_route",
		"match":             map[string]any{"path": "/resource", "method": "PUT"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "input.payload:op",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	patch := map[string]any{
		"name":              "patch_route",
		"match":             map[string]any{"path": "/resource", "method": "PATCH"},
		"auth":              map[string]any{"token": map[string]any{"value": "dev-webhook-token"}},
		"input_expression":  "input.payload:op",
		"target_agent_name": "Orchestrator",
		"run_as":            "default",
	}
	rig := newWebhookRig(t, map[string]any{"routes": []any{put, patch}})
	ch := make(chan agentRequest, 2)
	rig.startAgent("Orchestrator", ch)

	for _, method := range []string{"PUT", "PATCH"} {
		t.Run(method, func(t *testing.T) {
			req, err := http.NewRequest(method, rig.server.URL+"/resource", strings.NewReader(`{"op":"`+method+`"}`))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer dev-webhook-token")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			if resp.StatusCode != http.StatusAccepted {
				t.Fatalf("%s: status = %d, want 202", method, resp.StatusCode)
			}
			_ = resp.Body.Close()
			select {
			case ar := <-ch:
				if ar.text != method {
					t.Errorf("agent text = %q, want %q", ar.text, method)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("%s: agent never received the task", method)
			}
		})
	}
}
