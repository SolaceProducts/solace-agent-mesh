// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const testNamespace = "test"

type stubComponent struct {
	name string
}

func (s *stubComponent) Init(_ context.Context, _ config.Config) error { return nil }
func (s *stubComponent) Start(_ context.Context) error                 { return nil }
func (s *stubComponent) Stop(_ context.Context) error                  { return nil }
func (s *stubComponent) Health() error                                 { return nil }

type manageableComponent struct {
	stubComponent
	endpoints []Endpoint
	handler   func(ctx context.Context, method string, path []string, body json.RawMessage) (any, error)
}

func (m *manageableComponent) ManagementEndpoints() []Endpoint {
	return m.endpoints
}

func (m *manageableComponent) HandleManagement(ctx context.Context, method string, path []string, body json.RawMessage, _ *auth.Identity) (any, error) {
	if m.handler != nil {
		return m.handler(ctx, method, path, body)
	}
	return map[string]string{"status": "ok"}, nil
}

func setupControlTest(t *testing.T, components ...struct {
	name string
	comp runtime.Component
}) (*runtime.Runtime, *Service, broker.Broker) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	for _, c := range components {
		rt.Register(c.name, c.comp)
	}

	svc := NewService(rt, testNamespace)
	rt.Register("control", svc)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	return rt, svc, brk
}

func doControlRequest(t *testing.T, brk broker.Broker, topic string, body []byte) *Response {
	t.Helper()

	client := NewClient(brk, testNamespace, WithTimeout(5*time.Second))

	replyTo := testNamespace + "/sam/v1/control/reply/test-corr"
	msg := &broker.Message{
		Payload:       body,
		ReplyTo:       replyTo,
		CorrelationID: "test-corr",
	}

	resp, err := brk.Request(context.Background(), topic, msg, 5*time.Second)
	if err != nil {
		t.Fatal("request failed:", err)
	}

	var envelope Response
	if err := json.Unmarshal(resp.Payload, &envelope); err != nil {
		t.Fatal("unmarshal response:", err)
	}
	_ = client
	return &envelope
}

func TestService_ListApps(t *testing.T) {
	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"myagent", &stubComponent{name: "myagent"}},
	)

	topic := a2a.ControlAppsTopic(testNamespace, "get")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var result struct {
		Apps []AppInfo `json:"apps"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, app := range result.Apps {
		if app.Name == "myagent" {
			found = true
		}
		if app.Name == "control" {
			t.Error("control service should not appear in app list")
		}
	}
	if !found {
		t.Error("expected myagent in app list")
	}
}

func TestService_GetApp(t *testing.T) {
	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"myagent", &stubComponent{name: "myagent"}},
	)

	topic := a2a.ControlAppTopic(testNamespace, "myagent", "get")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var info AppInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "myagent" {
		t.Errorf("name = %q, want myagent", info.Name)
	}
}

func TestService_GetApp_NonExistent_DroppedByOwnershipGate(t *testing.T) {
	_, _, brk := setupControlTest(t)

	topic := a2a.ControlAppTopic(testNamespace, "nonexistent", "get")
	replyTo := testNamespace + "/sam/v1/control/reply/nonexistent-test"
	msg := &broker.Message{
		Payload:       nil,
		ReplyTo:       replyTo,
		CorrelationID: "nonexistent-test",
	}

	_, err := brk.Request(context.Background(), topic, msg, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected request to time out when no pod owns the target app, got a reply")
	}
}

func TestService_OwnershipGate_OnlyOwnerResponds(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	ownerComp := &manageableComponent{
		stubComponent: stubComponent{name: "awe"},
		handler: func(_ context.Context, _ string, _ []string, _ json.RawMessage) (any, error) {
			return map[string]string{"owner": "awe"}, nil
		},
	}
	startControlRuntime(t, brk, "awe", ownerComp)

	startControlRuntime(t, brk, "platform", &stubComponent{name: "platform"})

	replyTopic := testNamespace + "/sam/v1/control/reply/ownership-gate-test"
	replyQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(replyQueue, replyTopic); err != nil {
		t.Fatal(err)
	}

	topic := a2a.ControlAppCustomTopic(testNamespace, "awe", "get", "status")
	if err := brk.Publish(context.Background(), topic, &broker.Message{
		ReplyTo:       replyTopic,
		CorrelationID: "ownership-gate-test",
	}); err != nil {
		t.Fatal(err)
	}

	replies := drainReplies(t, brk, replyQueue, 300*time.Millisecond)
	if len(replies) != 1 {
		t.Fatalf("expected exactly one reply (from the owner), got %d", len(replies))
	}

	var env Response
	if err := json.Unmarshal(replies[0].Payload, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error != nil {
		t.Fatalf("owner should succeed, got error: %+v", env.Error)
	}
	data, _ := json.Marshal(env.Result)
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result["owner"] != "awe" {
		t.Errorf("owner = %q, want awe", result["owner"])
	}
}

func TestService_OwnershipGate_DrainingPodDisowns(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	rt.Register("awe", &stubComponent{name: "awe"})
	svc := NewService(rt, testNamespace)

	if !svc.ownsApp("awe") {
		t.Fatal("expected the pod to own awe before draining")
	}

	if err := rt.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	if svc.ownsApp("awe") {
		t.Error("a draining pod must disown awe so it stops answering control requests")
	}
}

func TestService_OwnershipGate_NoOwnerTimesOut(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	startControlRuntime(t, brk, "awe", &stubComponent{name: "awe"})
	startControlRuntime(t, brk, "platform", &stubComponent{name: "platform"})

	replyTopic := testNamespace + "/sam/v1/control/reply/no-owner-test"
	replyQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(replyQueue, replyTopic); err != nil {
		t.Fatal(err)
	}

	topic := a2a.ControlAppCustomTopic(testNamespace, "ghost", "get", "status")
	if err := brk.Publish(context.Background(), topic, &broker.Message{
		ReplyTo:       replyTopic,
		CorrelationID: "no-owner-test",
	}); err != nil {
		t.Fatal(err)
	}

	replies := drainReplies(t, brk, replyQueue, 300*time.Millisecond)
	if len(replies) != 0 {
		t.Fatalf("expected zero replies when no pod owns the app, got %d", len(replies))
	}
}

func startControlRuntime(t *testing.T, brk broker.Broker, compName string, comp runtime.Component) {
	t.Helper()

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	rt.Register(compName, comp)
	svc := NewService(rt, testNamespace)
	rt.Register("control", svc)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })
}

func drainReplies(t *testing.T, brk broker.Broker, queue broker.Queue, window time.Duration) []*broker.Message {
	t.Helper()
	var out []*broker.Message
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		recvCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		m, err := brk.Receive(recvCtx, queue)
		cancel()
		if err != nil || m == nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

func TestService_CreateApp_NotImplemented(t *testing.T) {
	_, _, brk := setupControlTest(t)

	topic := a2a.ControlAppsTopic(testNamespace, "post")
	resp := doControlRequest(t, brk, topic, []byte(`{"name":"new"}`))

	if resp.Error == nil {
		t.Fatal("expected error for unimplemented create")
	}
	if resp.Error.Code != ErrMethodNotAllowed {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrMethodNotAllowed)
	}
}

func TestService_DeleteApp_NotImplemented(t *testing.T) {
	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"myagent", &stubComponent{name: "myagent"}},
	)

	topic := a2a.ControlAppTopic(testNamespace, "myagent", "delete")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error == nil {
		t.Fatal("expected error for unimplemented delete")
	}
}

func TestService_PatchApp_NotImplemented(t *testing.T) {
	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"myagent", &stubComponent{name: "myagent"}},
	)

	topic := a2a.ControlAppTopic(testNamespace, "myagent", "patch")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error == nil {
		t.Fatal("expected error for unimplemented patch")
	}
}

func TestService_PutApp_NotImplemented(t *testing.T) {
	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"myagent", &stubComponent{name: "myagent"}},
	)

	topic := a2a.ControlAppTopic(testNamespace, "myagent", "put")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error == nil {
		t.Fatal("expected error for unimplemented put")
	}
}

func TestService_CustomEndpoint(t *testing.T) {
	mc := &manageableComponent{
		stubComponent: stubComponent{name: "myagent"},
		endpoints: []Endpoint{
			{Method: "get", Path: "status", Description: "Get status"},
		},
		handler: func(_ context.Context, method string, path []string, _ json.RawMessage) (any, error) {
			return map[string]any{
				"method": method,
				"path":   path,
			}, nil
		},
	}

	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"myagent", mc},
	)

	topic := a2a.ControlAppCustomTopic(testNamespace, "myagent", "get", "status")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result["method"] != "get" {
		t.Errorf("method = %v, want get", result["method"])
	}
}

func TestService_CustomEndpoint_NotManageable(t *testing.T) {
	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"myagent", &stubComponent{name: "myagent"}},
	)

	topic := a2a.ControlAppCustomTopic(testNamespace, "myagent", "get", "status")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error == nil {
		t.Fatal("expected error for non-manageable component")
	}
	if resp.Error.Code != ErrMethodNotAllowed {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrMethodNotAllowed)
	}
}

func TestService_ListApps_WithManagementEndpoints(t *testing.T) {
	mc := &manageableComponent{
		stubComponent: stubComponent{name: "myagent"},
		endpoints: []Endpoint{
			{Method: "get", Path: "status", Description: "Get status"},
			{Method: "post", Path: "reload", Description: "Reload config"},
		},
	}

	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"myagent", mc},
	)

	topic := a2a.ControlAppsTopic(testNamespace, "get")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	data, _ := json.Marshal(resp.Result)
	var result struct {
		Apps []AppInfo `json:"apps"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}

	for _, app := range result.Apps {
		if app.Name == "myagent" {
			if len(app.ManagementEndpoints) != 2 {
				t.Errorf("expected 2 management endpoints, got %d", len(app.ManagementEndpoints))
			}
			return
		}
	}
	t.Error("myagent not found in app list")
}

func TestClient_Options(t *testing.T) {
	brk := broker.NewDevBroker()
	client := NewClient(brk, "ns",
		WithTimeout(10*time.Second),
		WithUserProperties(map[string]string{"auth": "token123"}),
	)
	if client.timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", client.timeout)
	}
	if client.userProperties["auth"] != "token123" {
		t.Error("expected auth user property")
	}
}

func TestClientError_Error(t *testing.T) {
	err := &ClientError{Code: ErrNotFound, Message: "not found"}
	s := err.Error()
	if s != "control error -32001: not found" {
		t.Errorf("error string = %q", s)
	}
}

func TestService_CustomEndpoint_NotFoundWrappedErrorMapsToDebug(t *testing.T) {
	var buf bytes.Buffer
	captured := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	originalDefault := slog.Default()
	slog.SetDefault(captured)
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	mc := &manageableComponent{
		stubComponent: stubComponent{name: "awe"},
		endpoints: []Endpoint{
			{Method: "delete", Path: "instances/{name}", Description: "Remove instance"},
		},
		handler: func(_ context.Context, _ string, path []string, _ json.RawMessage) (any, error) {
			if len(path) >= 2 {
				return nil, fmt.Errorf("instance %q: %w", path[1], ErrResourceNotFound)
			}
			return nil, nil
		},
	}

	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"awe", mc},
	)

	topic := a2a.ControlAppCustomTopic(testNamespace, "awe", "delete", "instances/ghost-agent")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error == nil {
		t.Fatal("expected error response for missing instance")
	}
	if resp.Error.Code != ErrNotFound {
		t.Errorf("response code = %d, want %d (ErrNotFound)", resp.Error.Code, ErrNotFound)
	}

	logs := buf.String()

	if strings.Contains(logs, "level=WARN") && strings.Contains(logs, "HandleManagement") {
		t.Fatalf("not-found error must not produce WARN logs (Issue #392). logs:\n%s", logs)
	}
}

func TestService_CustomEndpoint_NonNotFoundErrorStillWarns(t *testing.T) {
	var buf bytes.Buffer
	captured := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	originalDefault := slog.Default()
	slog.SetDefault(captured)
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	mc := &manageableComponent{
		stubComponent: stubComponent{name: "awe"},
		endpoints: []Endpoint{
			{Method: "post", Path: "instances", Description: "Add instance"},
		},
		handler: func(_ context.Context, _ string, _ []string, _ json.RawMessage) (any, error) {
			return nil, errors.New("broker subscription failed")
		},
	}

	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"awe", mc},
	)

	topic := a2a.ControlAppCustomTopic(testNamespace, "awe", "post", "instances")
	resp := doControlRequest(t, brk, topic, []byte("{}"))

	if resp.Error == nil || resp.Error.Code == ErrNotFound {
		t.Fatalf("expected non-NotFound error response; got %+v", resp.Error)
	}

	logs := buf.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "HandleManagement") {
		t.Fatalf("expected a WARN log for non-NotFound error; got:\n%s", logs)
	}
}

func TestService_ConcurrentDispatch(t *testing.T) {
	const (
		concurrency      = 16
		handlerSleep     = 200 * time.Millisecond
		wallClockCeiling = 1500 * time.Millisecond
	)

	var inFlight, peakInFlight, calls atomic.Int32
	mc := &manageableComponent{
		stubComponent: stubComponent{name: "awe"},
		endpoints:     []Endpoint{{Method: "post", Path: "instances", Description: "Add instance"}},
		handler: func(_ context.Context, _ string, _ []string, _ json.RawMessage) (any, error) {
			calls.Add(1)
			cur := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				prev := peakInFlight.Load()
				if cur <= prev || peakInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(handlerSleep)
			return map[string]string{"ok": "true"}, nil
		},
	}

	_, _, brk := setupControlTest(t,
		struct {
			name string
			comp runtime.Component
		}{"awe", mc},
	)

	topic := a2a.ControlAppCustomTopic(testNamespace, "awe", "post", "instances")

	start := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)
	for i := range concurrency {
		wg.Go(func() {
			replyTopic := fmt.Sprintf("%s/sam/v1/control/reply/concurrent-%d", testNamespace, i)
			corrID := fmt.Sprintf("concurrent-%d", i)
			msg := &broker.Message{
				Payload:       []byte("{}"),
				ReplyTo:       replyTopic,
				CorrelationID: corrID,
			}
			resp, err := brk.Request(context.Background(), topic, msg, 5*time.Second)
			if err != nil {
				errs <- fmt.Errorf("request %d: %w", i, err)
				return
			}
			var env Response
			if uErr := json.Unmarshal(resp.Payload, &env); uErr != nil {
				errs <- fmt.Errorf("unmarshal %d: %w", i, uErr)
				return
			}
			if env.Error != nil {
				errs <- fmt.Errorf("request %d returned error: %+v", i, env.Error)
			}
		})
	}
	wg.Wait()
	close(errs)
	elapsed := time.Since(start)

	for err := range errs {
		t.Error(err)
	}
	if calls.Load() != concurrency {
		t.Fatalf("handler called %d times, want %d", calls.Load(), concurrency)
	}
	if elapsed > wallClockCeiling {
		t.Fatalf("dispatch took %v, want under %v — concurrency is not in effect", elapsed, wallClockCeiling)
	}
	if peakInFlight.Load() < 2 {
		t.Fatalf("peak in-flight = %d; expected >1, suggesting handlers ran sequentially", peakInFlight.Load())
	}
}

func TestService_ConcurrencyCap(t *testing.T) {
	const (
		cap          = 4
		total        = 8
		handlerSleep = 200 * time.Millisecond
	)

	var inFlight, peakInFlight atomic.Int32
	mc := &manageableComponent{
		stubComponent: stubComponent{name: "awe"},
		endpoints:     []Endpoint{{Method: "post", Path: "instances", Description: "Add"}},
		handler: func(_ context.Context, _ string, _ []string, _ json.RawMessage) (any, error) {
			cur := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				prev := peakInFlight.Load()
				if cur <= prev || peakInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(handlerSleep)
			return map[string]string{"ok": "true"}, nil
		},
	}

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, config.FromMap(map[string]any{
		"control": map[string]any{
			"max_concurrent_handlers": cap,
		},
	}))
	rt.Register("awe", mc)
	svc := NewService(rt, testNamespace)
	rt.Register("control", svc)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	if svc.cfg.MaxConcurrentHandlers != cap {
		t.Fatalf("MaxConcurrentHandlers = %d, want %d", svc.cfg.MaxConcurrentHandlers, cap)
	}

	topic := a2a.ControlAppCustomTopic(testNamespace, "awe", "post", "instances")
	start := time.Now()
	var wg sync.WaitGroup
	for i := range total {
		wg.Go(func() {
			msg := &broker.Message{
				Payload:       []byte("{}"),
				ReplyTo:       fmt.Sprintf("%s/sam/v1/control/reply/cap-%d", testNamespace, i),
				CorrelationID: fmt.Sprintf("cap-%d", i),
			}
			_, _ = brk.Request(context.Background(), topic, msg, 5*time.Second)
		})
	}
	wg.Wait()
	elapsed := time.Since(start)

	if peak := peakInFlight.Load(); peak > int32(cap) {
		t.Errorf("peak in-flight = %d, expected ≤ %d (cap)", peak, cap)
	}
	minExpected := 2 * handlerSleep
	if elapsed < minExpected {
		t.Errorf("wall clock = %v, expected at least %v (cap should serialise into batches)", elapsed, minExpected)
	}
}

func TestService_StopDrainsInflight(t *testing.T) {
	handlerStarted := make(chan struct{})
	handlerProceed := make(chan struct{})
	handlerFinished := atomic.Bool{}

	mc := &manageableComponent{
		stubComponent: stubComponent{name: "awe"},
		endpoints:     []Endpoint{{Method: "post", Path: "instances", Description: "Add"}},
		handler: func(_ context.Context, _ string, _ []string, _ json.RawMessage) (any, error) {
			close(handlerStarted)
			<-handlerProceed
			handlerFinished.Store(true)
			return map[string]string{"ok": "true"}, nil
		},
	}

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	rt.Register("awe", mc)
	svc := NewService(rt, testNamespace)
	rt.Register("control", svc)
	if err := rt.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	topic := a2a.ControlAppCustomTopic(testNamespace, "awe", "post", "instances")
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		_, _ = brk.Request(context.Background(), topic, &broker.Message{
			Payload:       []byte("{}"),
			ReplyTo:       testNamespace + "/sam/v1/control/reply/drain",
			CorrelationID: "drain",
		}, 5*time.Second)
	}()

	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never started")
	}

	stopDone := make(chan error, 1)
	go func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		stopDone <- rt.Stop(stopCtx)
	}()

	select {
	case <-stopDone:
		t.Fatal("rt.Stop returned before in-flight handler finished")
	case <-time.After(150 * time.Millisecond):
	}

	close(handlerProceed)

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("rt.Stop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rt.Stop did not return after handler finished")
	}

	if !handlerFinished.Load() {
		t.Fatal("handler did not run to completion")
	}
	<-requestDone
}

func setupControlTestWithAuthorizer(t *testing.T, authorizer auth.ControlAuthorizer, components ...struct {
	name string
	comp runtime.Component
}) broker.Broker {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, config.FromMap(nil), runtime.WithControlAuthorizer(authorizer))
	for _, c := range components {
		rt.Register(c.name, c.comp)
	}

	svc := NewService(rt, testNamespace)
	rt.Register("control", svc)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	return brk
}

func TestService_AuthDenied_ListApps(t *testing.T) {
	brk := setupControlTestWithAuthorizer(t, auth.DenyAllControlAuthorizer{},
		struct {
			name string
			comp runtime.Component
		}{"myagent", &stubComponent{name: "myagent"}},
	)

	topic := a2a.ControlAppsTopic(testNamespace, "get")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error == nil {
		t.Fatal("expected auth-denied error, got success")
	}
	if resp.Error.Code != ErrAuthDenied {
		t.Fatalf("error code = %d, want %d (ErrAuthDenied)", resp.Error.Code, ErrAuthDenied)
	}
}

func TestService_AuthDenied_GetApp(t *testing.T) {
	brk := setupControlTestWithAuthorizer(t, auth.DenyAllControlAuthorizer{},
		struct {
			name string
			comp runtime.Component
		}{"myagent", &stubComponent{name: "myagent"}},
	)

	topic := a2a.ControlAppTopic(testNamespace, "myagent", "get")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error == nil {
		t.Fatal("expected auth-denied error, got success")
	}
	if resp.Error.Code != ErrAuthDenied {
		t.Fatalf("error code = %d, want %d (ErrAuthDenied)", resp.Error.Code, ErrAuthDenied)
	}
}

func TestService_AuthSkipped_CustomEndpoint(t *testing.T) {
	mc := &manageableComponent{
		stubComponent: stubComponent{name: "myagent"},
		endpoints: []Endpoint{
			{Method: "get", Path: "status", Description: "Get status"},
		},
	}

	brk := setupControlTestWithAuthorizer(t, auth.DenyAllControlAuthorizer{},
		struct {
			name string
			comp runtime.Component
		}{"myagent", mc},
	)

	topic := a2a.ControlAppCustomTopic(testNamespace, "myagent", "get", "status")
	resp := doControlRequest(t, brk, topic, nil)

	if resp.Error != nil {
		t.Fatalf("custom endpoints bypass ControlAuthorizer; unexpected error: %+v", resp.Error)
	}
}

func TestService_TargetedRequestAndResponderProcess(t *testing.T) {
	mc := &manageableComponent{
		stubComponent: stubComponent{name: "myagent"},
		endpoints:     []Endpoint{{Method: "get", Path: "status"}},
		handler: func(context.Context, string, []string, json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	}
	_, _, brk := setupControlTest(t, struct {
		name string
		comp runtime.Component
	}{"myagent", mc})
	client := NewClient(brk, testNamespace, WithTimeout(500*time.Millisecond))
	ctx := context.Background()

	if _, err := client.CustomTo(ctx, "another-process", "myagent", "get", "status", nil); err == nil {
		t.Fatal("a request addressed to another process was answered")
	}
	if _, err := client.CustomTo(ctx, runtime.ProcessID(), "myagent", "get", "status", nil); err != nil {
		t.Fatalf("a request addressed to this process: %v", err)
	}
}

func TestCustomToIgnoresAReplyFromAnotherProcess(t *testing.T) {
	ctx := context.Background()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	topic := a2a.ControlAppCustomTopic(testNamespace, "awe", "post", "instances")
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, topic); err != nil {
		t.Fatal(err)
	}
	go func() {
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			return
		}
		_ = brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: []byte(`{"result":{}}`), CorrelationID: msg.CorrelationID})
	}()

	client := NewClient(brk, testNamespace, WithTimeout(500*time.Millisecond))
	if _, err := client.CustomTo(ctx, "new-awe", "awe", "post", "instances", nil); err == nil {
		t.Fatal("a reply from a process that does not name itself was taken as the target's")
	}
	if _, err := NewClient(refusingReplyBroker{brk}, testNamespace).CustomTo(ctx, "new-awe", "awe", "post", "instances", nil); err == nil {
		t.Fatal("an addressed request with no reply subscription reported success")
	}
}

type refusingReplyBroker struct{ broker.Broker }

func (refusingReplyBroker) CreateDirectSubscription() (broker.Queue, error) {
	return nil, errors.New("subscriptions refused")
}
