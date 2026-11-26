// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type stubTaskSigner struct {
	token string
	err   error

	mu             sync.Mutex
	calls          int
	lastTID        string
	lastPrincipal  string
	seenPrincipals []string
	seenUserUUIDs  []string
}

func (s *stubTaskSigner) SignTask(_ context.Context, principal TaskPrincipal, taskID string) (string, error) {
	s.mu.Lock()
	s.seenUserUUIDs = append(s.seenUserUUIDs, principal.UserUUID)
	s.mu.Unlock()
	return s.sign(principal.Subject, taskID)
}

func (s *stubTaskSigner) sign(principal, taskID string) (string, error) {
	s.mu.Lock()
	s.calls++
	s.lastPrincipal = principal
	s.lastTID = taskID
	s.seenPrincipals = append(s.seenPrincipals, principal)
	s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	return s.token, nil
}

func (s *stubTaskSigner) SignTeardown(_ context.Context, principal, taskID string) (string, error) {
	return s.sign(principal, taskID)
}

func (s *stubTaskSigner) signedUserUUIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seenUserUUIDs...)
}

func (s *stubTaskSigner) principals() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seenPrincipals...)
}

func TestBrokerClientPublishAndDispatch(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	ch := cli.RegisterRun("run-1")

	taskID, err := cli.PublishTask(t.Context(), PublishTaskParams{RunID: "run-1", AgentName: "Orchestrator", Prompt: "hello"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if taskID == "" {
		t.Fatalf("empty task id")
	}

	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent sub: %v", err)
	}
	if err := br.Subscribe(q, "ns/a2a/v1/agent/request/Orchestrator"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	reply := "ns/a2a/v1/gateway/response/evals/" + taskID
	if err := br.Publish(t.Context(), reply, &broker.Message{Topic: reply, Payload: []byte("OK")}); err != nil {
		t.Fatalf("publish reply: %v", err)
	}

	select {
	case resp := <-ch:
		if string(resp.Payload) != "OK" {
			t.Fatalf("payload = %q", resp.Payload)
		}
		if resp.Topic != reply {
			t.Fatalf("topic = %q", resp.Topic)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for dispatched response")
	}
}

func publishAndCaptureUserProps(t *testing.T, cli *BrokerClient, br *broker.DevBroker, ns, agentName, principal string) (string, map[string]string) {
	t.Helper()

	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent sub: %v", err)
	}
	if subErr := br.Subscribe(q, ns+"/a2a/v1/agent/request/"+agentName); subErr != nil {
		t.Fatalf("agent subscribe: %v", subErr)
	}

	taskID, err := cli.PublishTask(t.Context(), PublishTaskParams{RunID: "run-1", AgentName: agentName, Prompt: "hi", Principal: principal})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := br.Receive(rcvCtx, q)
	if err != nil || msg == nil {
		t.Fatalf("receive request: msg=%v err=%v", msg, err)
	}
	return taskID, msg.UserProperties
}

func TestPublishTaskSetsAuthTokenWhenSignerConfigured(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	signer := &stubTaskSigner{token: "signed.jwt.value"}
	cli := NewBrokerClient(br, "ns", signer, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)
	_ = cli.RegisterRun("run-1")

	taskID, props := publishAndCaptureUserProps(t, cli, br, "ns", "Orchestrator", "")

	if got := props[a2a.PropAuthToken]; got != "signed.jwt.value" {
		t.Fatalf("authToken = %q, want %q", got, "signed.jwt.value")
	}
	if signer.calls != 1 {
		t.Fatalf("signer.calls = %d, want 1", signer.calls)
	}
	if signer.lastTID != taskID {
		t.Fatalf("signer received taskID %q, want %q", signer.lastTID, taskID)
	}
	if signer.lastPrincipal != rbac.EvalSystemUserSub {
		t.Fatalf("signer received principal %q, want %q", signer.lastPrincipal, rbac.EvalSystemUserSub)
	}
	if got := props[a2a.PropUserID]; got != rbac.EvalSystemUserSub {
		t.Fatalf("userId = %q, want %q (default eval system user)", got, rbac.EvalSystemUserSub)
	}
	if got := props[a2a.PropClientID]; got != "evals" {
		t.Fatalf("clientId = %q, want %q", got, "evals")
	}
}

func TestPublishTaskOmitsAuthTokenWhenSignerNil(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)
	_ = cli.RegisterRun("run-1")

	_, props := publishAndCaptureUserProps(t, cli, br, "ns", "Orchestrator", "")

	if got, ok := props[a2a.PropAuthToken]; ok {
		t.Fatalf("authToken should be unset when signer is nil, got %q", got)
	}
}

func TestPublishTaskPropagatesSignerError(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	signErr := errors.New("kid not found")
	signer := &stubTaskSigner{err: signErr}
	cli := NewBrokerClient(br, "ns", signer, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	_, err := cli.PublishTask(t.Context(), PublishTaskParams{RunID: "run-1", AgentName: "Orchestrator", Prompt: "hi"})
	if err == nil {
		t.Fatalf("expected error from signer failure, got nil")
	}
	if !strings.Contains(err.Error(), "sign task") || !errors.Is(err, signErr) {
		t.Fatalf("error = %v, want wrapped %v with 'sign task' prefix", err, signErr)
	}
}

func receiveCancelEnvelope(t *testing.T, cli *BrokerClient, br *broker.DevBroker, ns, agentName, taskID, principal string) (a2a.Request, map[string]string) {
	t.Helper()
	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent sub: %v", err)
	}
	if subErr := br.Subscribe(q, ns+"/a2a/v1/agent/request/"+agentName); subErr != nil {
		t.Fatalf("agent subscribe: %v", subErr)
	}
	if cerr := cli.CancelTask(t.Context(), agentName, taskID, principal); cerr != nil {
		t.Fatalf("cancel: %v", cerr)
	}
	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := br.Receive(rcvCtx, q)
	if err != nil || msg == nil {
		t.Fatalf("receive cancel: msg=%v err=%v", msg, err)
	}
	var req a2a.Request
	if uerr := json.Unmarshal(msg.Payload, &req); uerr != nil {
		t.Fatalf("decode cancel: %v", uerr)
	}
	return req, msg.UserProperties
}

func TestCancelTaskPublishesCancelEnvelope(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	req, props := receiveCancelEnvelope(t, cli, br, "ns", "Orchestrator", "task-xyz", "")
	if req.Method != "tasks/cancel" {
		t.Errorf("method = %q, want tasks/cancel", req.Method)
	}
	if req.ID != "task-xyz" {
		t.Errorf("id = %q, want task-xyz", req.ID)
	}
	params, _ := req.Params.(map[string]any)
	if params["id"] != "task-xyz" {
		t.Errorf("params.id = %v, want task-xyz", params["id"])
	}
	if props[a2a.PropUserID] != rbac.EvalSystemUserSub {
		t.Errorf("userId = %q, want %q (empty principal defaults to the eval system user)", props[a2a.PropUserID], rbac.EvalSystemUserSub)
	}
	if props[a2a.PropClientID] != "evals" {
		t.Errorf("clientId = %q, want evals", props[a2a.PropClientID])
	}
	if _, ok := props[a2a.PropAuthToken]; ok {
		t.Errorf("authToken should be unset with no signer")
	}
}

func TestCancelTaskSignsWhenSignerConfigured(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	signer := &stubTaskSigner{token: "signed.cancel.jwt"}
	cli := NewBrokerClient(br, "ns", signer, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	_, props := receiveCancelEnvelope(t, cli, br, "ns", "Orchestrator", "task-signed", "")
	if props[a2a.PropAuthToken] != "signed.cancel.jwt" {
		t.Errorf("authToken = %q, want signed.cancel.jwt", props[a2a.PropAuthToken])
	}
	if signer.lastTID != "task-signed" {
		t.Errorf("signer taskID = %q, want task-signed", signer.lastTID)
	}
	if signer.lastPrincipal != rbac.EvalSystemUserSub {
		t.Errorf("signer principal = %q, want %q", signer.lastPrincipal, rbac.EvalSystemUserSub)
	}
}

func TestCancelTaskStampsExplicitPrincipal(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	signer := &stubTaskSigner{token: "signed.cancel.jwt"}
	cli := NewBrokerClient(br, "ns", signer, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	_, props := receiveCancelEnvelope(t, cli, br, "ns", "Orchestrator", "task-ra", "system:qa-bot")
	if got := props[a2a.PropUserID]; got != "system:qa-bot" {
		t.Errorf("userId = %q, want system:qa-bot", got)
	}
	if signer.lastPrincipal != "system:qa-bot" {
		t.Errorf("signer principal = %q, want system:qa-bot", signer.lastPrincipal)
	}
}

func TestCancelTaskPropagatesSignerError(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	signErr := errors.New("kid not found")
	cli := NewBrokerClient(br, "ns", &stubTaskSigner{err: signErr}, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	err := cli.CancelTask(t.Context(), "Orchestrator", "task-1", "")
	if err == nil || !errors.Is(err, signErr) || !strings.Contains(err.Error(), "sign cancel") {
		t.Fatalf("error = %v, want wrapped %v with 'sign cancel'", err, signErr)
	}
}

func TestCancelTaskRejectsWhenNotConnectedOrInvalidName(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.CancelTask(t.Context(), "Orchestrator", "t1", ""); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("error = %v, want 'not connected'", err)
	}

	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)
	if err := cli.CancelTask(t.Context(), "bad/name", "t1", ""); err == nil || !strings.Contains(err.Error(), "agentName") {
		t.Fatalf("error = %v, want agentName validation error", err)
	}
}

func TestPublishTaskAttachesUserConfig(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)
	_ = cli.RegisterRun("run-1")

	q, subErr := br.CreateDirectSubscription()
	if subErr != nil {
		t.Fatalf("agent sub: %v", subErr)
	}
	if err := br.Subscribe(q, "ns/a2a/v1/agent/request/Orchestrator"); err != nil {
		t.Fatalf("agent subscribe: %v", err)
	}

	userConfig := map[string]any{
		"_enterprise_capabilities": []string{"*"},
		"_user_identity":           map[string]any{"id": rbac.EvalSystemUserSub},
		"user_profile":             map[string]any{"id": rbac.EvalSystemUserSub},
	}
	if _, pubErr := cli.PublishTask(t.Context(), PublishTaskParams{RunID: "run-1", AgentName: "Orchestrator", Prompt: "hi", UserConfig: userConfig}); pubErr != nil {
		t.Fatalf("publish: %v", pubErr)
	}

	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, recvErr := br.Receive(rcvCtx, q)
	if recvErr != nil || msg == nil {
		t.Fatalf("receive request: msg=%v err=%v", msg, recvErr)
	}

	raw, ok := msg.UserProperties[a2a.PropA2AUserConfig]
	if !ok {
		t.Fatalf("a2aUserConfig user-property missing; got %v", msg.UserProperties)
	}
	var decoded map[string]any
	if jsonErr := json.Unmarshal([]byte(raw), &decoded); jsonErr != nil {
		t.Fatalf("a2aUserConfig is not valid JSON: %v (raw=%q)", jsonErr, raw)
	}
	caps, _ := decoded["_enterprise_capabilities"].([]any)
	if len(caps) != 1 || caps[0] != "*" {
		t.Errorf("_enterprise_capabilities = %v, want [\"*\"]", caps)
	}
	identity, _ := decoded["_user_identity"].(map[string]any)
	if identity["id"] != rbac.EvalSystemUserSub {
		t.Errorf("_user_identity.id = %v, want %q", identity["id"], rbac.EvalSystemUserSub)
	}
	profile, _ := decoded["user_profile"].(map[string]any)
	if profile["id"] != rbac.EvalSystemUserSub {
		t.Errorf("user_profile.id = %v, want %q", profile["id"], rbac.EvalSystemUserSub)
	}
}

func TestPublishTaskStampsExplicitPrincipal(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	signer := &stubTaskSigner{token: "signed.jwt.value"}
	cli := NewBrokerClient(br, "ns", signer, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)
	_ = cli.RegisterRun("run-1")

	_, props := publishAndCaptureUserProps(t, cli, br, "ns", "Orchestrator", "system:qa-bot")
	if got := props[a2a.PropUserID]; got != "system:qa-bot" {
		t.Errorf("userId = %q, want system:qa-bot", got)
	}
	if signer.lastPrincipal != "system:qa-bot" {
		t.Errorf("signer principal = %q, want system:qa-bot", signer.lastPrincipal)
	}
}

func TestPublishTaskOmitsUserConfigWhenNil(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)
	_ = cli.RegisterRun("run-1")

	_, props := publishAndCaptureUserProps(t, cli, br, "ns", "Orchestrator", "")

	if got, ok := props[a2a.PropA2AUserConfig]; ok {
		t.Fatalf("a2aUserConfig should be unset when userConfig is nil, got %q", got)
	}
}

func TestPublishTaskOmitsUserConfigWhenEmpty(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)
	_ = cli.RegisterRun("run-1")

	q, subErr := br.CreateDirectSubscription()
	if subErr != nil {
		t.Fatalf("agent sub: %v", subErr)
	}
	if err := br.Subscribe(q, "ns/a2a/v1/agent/request/Orchestrator"); err != nil {
		t.Fatalf("agent subscribe: %v", err)
	}

	if _, pubErr := cli.PublishTask(t.Context(), PublishTaskParams{RunID: "run-1", AgentName: "Orchestrator", Prompt: "hi", UserConfig: map[string]any{}}); pubErr != nil {
		t.Fatalf("publish: %v", pubErr)
	}

	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := br.Receive(rcvCtx, q)
	if err != nil || msg == nil {
		t.Fatalf("receive: %v %v", msg, err)
	}
	if got, ok := msg.UserProperties[a2a.PropA2AUserConfig]; ok {
		t.Fatalf("a2aUserConfig should be unset for empty map, got %q", got)
	}
}

func TestBuildEvalUserConfigShape(t *testing.T) {
	cfg := buildEvalUserConfig(rbac.EvalSystemUserSub)
	caps, ok := cfg["_enterprise_capabilities"].([]string)
	if !ok || len(caps) != 1 || caps[0] != "*" {
		t.Errorf("_enterprise_capabilities = %v, want [\"*\"]", cfg["_enterprise_capabilities"])
	}
	id, ok := cfg["_user_identity"].(map[string]any)
	if !ok || id["id"] != rbac.EvalSystemUserSub {
		t.Errorf("_user_identity = %v, want {id: %q}", cfg["_user_identity"], rbac.EvalSystemUserSub)
	}
	profile, ok := cfg["user_profile"].(map[string]any)
	if !ok || profile["id"] != rbac.EvalSystemUserSub {
		t.Errorf("user_profile = %v, want {id: %q}", cfg["user_profile"], rbac.EvalSystemUserSub)
	}
}

func TestBrokerClientRegisterRunIsIdempotent(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	first := cli.RegisterRun("run-dupe")
	second := cli.RegisterRun("run-dupe")
	if first != second {
		t.Fatal("RegisterRun returned different channels for same runID — drain forked")
	}
	cli.DeregisterRun("run-dupe")
}

func TestBrokerClientDeregisterRunUnblocksOnDeadConsumer(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	_ = cli.RegisterRun("run-dead")
	cli.bindTask("run-dead", "task-x")

	for i := 0; i < 200; i++ {
		topic := "ns/a2a/v1/gateway/status/evals/task-x"
		if perr := br.Publish(t.Context(), topic, &broker.Message{Topic: topic, Payload: []byte("x")}); perr != nil {
			t.Fatalf("publish: %v", perr)
		}
	}
	time.Sleep(150 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		cli.DeregisterRun("run-dead")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("DeregisterRun hung — drain did not unblock on cancel signal")
	}
}

func TestBrokerClientPerRunBackpressureIsolation(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	slowCh := cli.RegisterRun("run-slow")
	cli.bindTask("run-slow", "task-x")
	fastCh := cli.RegisterRun("run-fast")
	cli.bindTask("run-fast", "task-y")

	for i := 0; i < 200; i++ {
		topic := "ns/a2a/v1/gateway/status/evals/task-x"
		if perr := br.Publish(t.Context(), topic, &broker.Message{Topic: topic, Payload: []byte("slow")}); perr != nil {
			t.Fatalf("publish slow: %v", perr)
		}
	}
	topicFast := "ns/a2a/v1/gateway/status/evals/task-y"
	if perr := br.Publish(t.Context(), topicFast, &broker.Message{Topic: topicFast, Payload: []byte("fast")}); perr != nil {
		t.Fatalf("publish fast: %v", perr)
	}

	select {
	case resp := <-fastCh:
		if string(resp.Payload) != "fast" {
			t.Fatalf("fast payload = %q", resp.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fast run starved by slow consumer back-pressure (regression)")
	}

	if depth := cli.RunQueueDepth("run-slow"); depth == 0 {
		t.Errorf("slow queue depth = 0, expected non-zero accumulation")
	}
	_ = slowCh
}

func TestPublishTaskRepliesOnGatewayTopicsUnderA2ANamespace(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent sub: %v", err)
	}
	if subErr := br.Subscribe(q, "ns/a2a/v1/agent/request/Orchestrator"); subErr != nil {
		t.Fatalf("agent subscribe: %v", subErr)
	}
	ch := cli.RegisterRun("run-1")

	taskID, err := cli.PublishTask(t.Context(), PublishTaskParams{RunID: "run-1", AgentName: "Orchestrator", Prompt: "hi"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req, err := br.Receive(rcvCtx, q)
	if err != nil || req == nil {
		t.Fatalf("receive request: msg=%v err=%v", req, err)
	}

	wantReply := "ns/a2a/v1/gateway/response/evals/" + taskID
	wantStatus := "ns/a2a/v1/gateway/status/evals/" + taskID
	if req.ReplyTo != wantReply {
		t.Errorf("replyTo = %q, want %q", req.ReplyTo, wantReply)
	}
	if got := req.UserProperties[a2a.PropReplyTo]; got != wantReply {
		t.Errorf("replyTo user property = %q, want %q", got, wantReply)
	}
	if got := req.UserProperties[a2a.PropA2AStatusTopic]; got != wantStatus {
		t.Errorf("a2aStatusTopic = %q, want %q", got, wantStatus)
	}
	taskLoggerSubscription := strings.TrimSuffix(a2a.AllNamespaceA2ASubscription("ns"), ">")
	for _, topic := range []string{req.ReplyTo, req.UserProperties[a2a.PropA2AStatusTopic]} {
		if !strings.HasPrefix(topic, taskLoggerSubscription) {
			t.Errorf("reply topic %q is outside the task logger subscription %q", topic, taskLoggerSubscription)
		}
	}

	if perr := br.Publish(t.Context(), wantStatus, &broker.Message{Topic: wantStatus, Payload: []byte("status")}); perr != nil {
		t.Fatalf("publish status: %v", perr)
	}
	select {
	case resp := <-ch:
		if string(resp.Payload) != "status" || resp.Topic != wantStatus {
			t.Fatalf("dispatched %q on %q, want status update on %q", resp.Payload, resp.Topic, wantStatus)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for dispatched status update")
	}

	stray := "ns/a2a/v1/gateway/status/evals/task-nobody-published"
	if perr := br.Publish(t.Context(), stray, &broker.Message{Topic: stray, Payload: []byte("stray")}); perr != nil {
		t.Fatalf("publish stray: %v", perr)
	}
	select {
	case resp := <-ch:
		t.Fatalf("reply for an unpublished task dispatched to run-1: %q on %q", resp.Payload, resp.Topic)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDeregisterRunReleasesTaskBindings(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	_ = cli.RegisterRun("run-1")
	taskID, err := cli.PublishTask(t.Context(), PublishTaskParams{RunID: "run-1", AgentName: "Orchestrator", Prompt: "hi"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	cli.DeregisterRun("run-1")

	cli.mu.Lock()
	_, bound := cli.taskRuns[taskID]
	cli.mu.Unlock()
	if bound {
		t.Fatalf("task %s still bound after its run was deregistered", taskID)
	}
}

func TestPublishTaskRefusesUnregisteredRun(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	_ = cli.RegisterRun("run-1")
	cli.DeregisterRun("run-1")

	registered := false
	taskID, err := cli.PublishTask(t.Context(), PublishTaskParams{
		RunID: "run-1", AgentName: "Orchestrator", Prompt: "hi",
		BeforePublish: func(string, string) { registered = true },
	})
	if err == nil {
		t.Fatalf("publish for a deregistered run succeeded with task %s, want refusal", taskID)
	}
	if registered {
		t.Error("beforePublish fired for a refused publish")
	}
	cli.mu.Lock()
	bindings := len(cli.taskRuns)
	cli.mu.Unlock()
	if bindings != 0 {
		t.Fatalf("%d task bindings left after a refused publish, want none", bindings)
	}
}

func TestBrokerClientCancelTaskPublishesA2ACancel(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)

	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	if serr := br.Subscribe(q, "ns/a2a/v1/agent/request/Orchestrator"); serr != nil {
		t.Fatalf("subscribe: %v", serr)
	}

	if cerr := cli.CancelTask(t.Context(), "Orchestrator", "task-xyz", ""); cerr != nil {
		t.Fatalf("cancel: %v", cerr)
	}

	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := br.Receive(rcvCtx, q)
	if err != nil || msg == nil {
		t.Fatalf("receive: msg=%v err=%v", msg, err)
	}
	var payload struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      string         `json:"id"`
		Method  string         `json:"method"`
		Params  map[string]any `json:"params"`
	}
	if uerr := json.Unmarshal(msg.Payload, &payload); uerr != nil {
		t.Fatalf("unmarshal: %v", uerr)
	}
	if payload.Method != "tasks/cancel" {
		t.Errorf("method = %q, want tasks/cancel", payload.Method)
	}
	if payload.ID != "task-xyz" {
		t.Errorf("id = %q, want task-xyz", payload.ID)
	}
	if got, _ := payload.Params["id"].(string); got != "task-xyz" {
		t.Errorf("params.id = %q, want task-xyz", got)
	}
	if got := msg.UserProperties[a2a.PropUserID]; got != rbac.EvalSystemUserSub {
		t.Errorf("userId = %q, want %q", got, rbac.EvalSystemUserSub)
	}
	if got := msg.UserProperties[a2a.PropClientID]; got != "evals" {
		t.Errorf("clientId = %q, want evals", got)
	}
}

func TestTriggerPublisherPublishCancel(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	pub := NewTriggerPublisher(br, "ns")
	if pub.CancelTopic() != "ns/eval/v1/run/cancel" {
		t.Fatalf("cancel topic = %q, want ns/eval/v1/run/cancel", pub.CancelTopic())
	}

	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	if serr := br.Subscribe(q, pub.CancelTopic()); serr != nil {
		t.Fatalf("subscribe: %v", serr)
	}

	if perr := pub.PublishCancel(t.Context(), "run-abc"); perr != nil {
		t.Fatalf("publish cancel: %v", perr)
	}
	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := br.Receive(rcvCtx, q)
	if err != nil || msg == nil {
		t.Fatalf("receive: msg=%v err=%v", msg, err)
	}
	var payload struct {
		RunID string `json:"run_id"`
	}
	if uerr := json.Unmarshal(msg.Payload, &payload); uerr != nil {
		t.Fatalf("unmarshal: %v", uerr)
	}
	if payload.RunID != "run-abc" {
		t.Fatalf("run_id = %q, want run-abc", payload.RunID)
	}
}

func TestTriggerPublisherEmitsRunID(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	pub := NewTriggerPublisher(br, "ns")

	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	if serr := br.Subscribe(q, pub.TriggerTopic()); serr != nil {
		t.Fatalf("subscribe: %v", serr)
	}

	if perr := pub.PublishRunTrigger(t.Context(), "run-xyz"); perr != nil {
		t.Fatalf("publish: %v", perr)
	}
	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := br.Receive(rcvCtx, q)
	if err != nil || msg == nil {
		t.Fatalf("receive: msg=%v err=%v", msg, err)
	}
	if string(msg.Payload) == "" || !strings.Contains(string(msg.Payload), "run-xyz") {
		t.Fatalf("payload = %q, want contains run-xyz", msg.Payload)
	}
}

func publishAndReceiveA2AMessage(t *testing.T, params PublishTaskParams) a2a.Message {
	t.Helper()
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })

	cli := NewBrokerClient(br, "ns", nil, nil)
	if err := cli.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(cli.Stop)
	_ = cli.RegisterRun(params.RunID)

	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent sub: %v", err)
	}
	if subErr := br.Subscribe(q, "ns/a2a/v1/agent/request/"+params.AgentName); subErr != nil {
		t.Fatalf("agent subscribe: %v", subErr)
	}
	if _, pubErr := cli.PublishTask(t.Context(), params); pubErr != nil {
		t.Fatalf("publish: %v", pubErr)
	}

	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := br.Receive(rcvCtx, q)
	if err != nil || msg == nil {
		t.Fatalf("receive request: msg=%v err=%v", msg, err)
	}
	var req struct {
		Params struct {
			Message a2a.Message `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return req.Params.Message
}

func TestPublishTaskAttachesInputFileByURINeverInlineBytes(t *testing.T) {
	const uri = "artifact://ns/eval-runner/eval-example-abc/config.yaml?version=0"
	got := publishAndReceiveA2AMessage(t, PublishTaskParams{
		RunID:     "run-1",
		AgentName: "Orchestrator",
		Prompt:    "review the attached",
		File: &TaskFileAttachment{
			Filename: "config.yaml",
			MIMEType: "application/x-yaml",
			URI:      uri,
		},
	})

	if len(got.Parts) != 2 {
		t.Fatalf("parts = %d, want 2 (text then file)", len(got.Parts))
	}
	if got.Parts[0].Kind != "text" || got.Parts[0].Text != "review the attached" {
		t.Errorf("part 0 = %+v, want the prompt text", got.Parts[0])
	}
	file := got.Parts[1]
	if file.Kind != "file" || file.File == nil {
		t.Fatalf("part 1 = %+v, want a file part", file)
	}
	if file.File.URI != uri {
		t.Errorf("file.uri = %q, want %q", file.File.URI, uri)
	}
	if file.File.Name != "config.yaml" || file.File.MIMEType != "application/x-yaml" {
		t.Errorf("file = %+v, want config.yaml / application/x-yaml", file.File)
	}
	if file.File.Bytes != "" {
		t.Errorf("file.bytes = %q, want empty — the URI is the only delivery mechanism", file.File.Bytes)
	}
}

func TestPublishTaskOmitsFilePartWithoutAnInputFile(t *testing.T) {
	got := publishAndReceiveA2AMessage(t, PublishTaskParams{
		RunID: "run-1", AgentName: "Orchestrator", Prompt: "text only",
	})
	if len(got.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(got.Parts))
	}
	if got.Parts[0].Kind != "text" {
		t.Errorf("part 0 kind = %q, want text", got.Parts[0].Kind)
	}
}

func TestPublishTaskNeverAttachesTheExpectedFile(t *testing.T) {
	example := &store.EvalExample{
		ID: "abc", Prompt: "review the attached",
		Artifacts: []*store.EvalExampleArtifact{
			{ExampleID: "abc", Role: store.EvalArtifactRoleInput,
				Filename: "config.yaml", MIMEType: "application/x-yaml",
				Version: 0, SessionID: "eval-example-abc-input"},
			{ExampleID: "abc", Role: store.EvalArtifactRoleExpected,
				Filename: "reference.md", MIMEType: "text/markdown",
				Version: 0, SessionID: "eval-example-abc-expected"},
		},
	}
	svc := &ExecutionService{artifacts: &artifact.ScopedService{
		Inner: memory.New(), Scope: "namespace", Namespace: "ns",
	}}
	att, err := svc.exampleInputFile(example)
	if err != nil {
		t.Fatalf("exampleInputFile: %v", err)
	}
	if att == nil || att.Filename != "config.yaml" {
		t.Fatalf("attachment = %+v, want the input file", att)
	}

	got := publishAndReceiveA2AMessage(t, PublishTaskParams{
		RunID: "run-1", AgentName: "Orchestrator",
		Prompt: example.Prompt, File: att,
	})
	if len(got.Parts) != 2 {
		t.Fatalf("parts = %d, want 2 (text then the input file)", len(got.Parts))
	}
	file := got.Parts[1]
	if file.Kind != "file" || file.File == nil || file.File.Name != "config.yaml" {
		t.Fatalf("part 1 = %+v, want the input file", file)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	for _, leak := range []string{"reference.md", "-expected"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("published message mentions %q — the expected file must never reach the agent:\n%s", leak, raw)
		}
	}
}

func TestPublishTaskStampsAgentNameMetadata(t *testing.T) {
	got := publishAndReceiveA2AMessage(t, PublishTaskParams{
		RunID: "run-1", AgentName: "Orchestrator", Prompt: "hello",
	})
	if name, _ := got.Metadata["agent_name"].(string); name != "Orchestrator" {
		t.Fatalf("metadata agent_name = %q, want Orchestrator", name)
	}
	if _, present := got.Metadata["model_override"]; present {
		t.Fatal("model_override present without a ModelID")
	}

	withModel := publishAndReceiveA2AMessage(t, PublishTaskParams{
		RunID: "run-2", AgentName: "Orchestrator", Prompt: "hello", ModelID: "openai/gpt",
	})
	if name, _ := withModel.Metadata["agent_name"].(string); name != "Orchestrator" {
		t.Fatalf("metadata agent_name with model override = %q, want Orchestrator", name)
	}
	if _, present := withModel.Metadata["model_override"]; !present {
		t.Fatal("model_override missing when a ModelID was given")
	}
}

func TestTriggerPublisherPublishGenerationTrigger(t *testing.T) {
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(t.Context()) })
	pub := NewTriggerPublisher(br, "ns")
	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	if serr := br.Subscribe(q, pub.TriggerTopic()); serr != nil {
		t.Fatalf("subscribe: %v", serr)
	}

	if perr := pub.PublishGenerationTrigger(t.Context(), "op-1"); perr != nil {
		t.Fatalf("publish generation trigger: %v", perr)
	}
	rcvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := br.Receive(rcvCtx, q)
	if err != nil || msg == nil {
		t.Fatalf("receive: msg=%v err=%v", msg, err)
	}
	var payload struct {
		GenerationID string `json:"generation_id"`
	}
	if uerr := json.Unmarshal(msg.Payload, &payload); uerr != nil || payload.GenerationID != "op-1" {
		t.Fatalf("payload = %s, %v; want generation_id op-1", msg.Payload, uerr)
	}
}

func TestTriggerPublisherWithoutBrokerFails(t *testing.T) {
	if err := NewTriggerPublisher(nil, "ns").PublishGenerationTrigger(t.Context(), "op-1"); err == nil {
		t.Fatal("publish without a broker returned no error")
	}
}
