// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const testExtURI = "https://example.com/sam/ext/sessiontest/v1"
const testExtMethod = "session/echo"

func agentForExtensionTest(t *testing.T) (*Agent, *mockBroker) {
	t.Helper()
	a, _, _ := newTestAgent()
	svc, ok := a.svc.(*mockServices)
	if !ok {
		t.Fatalf("expected *mockServices, got %T", a.svc)
	}
	brk := newMockBroker()
	svc.brk = brk
	return a, brk
}

func readPublishedResponse(t *testing.T, brk *mockBroker, replyTo string) *a2a.Response {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range brk.getPublished() {
			if m.topic == replyTo {
				var resp a2a.Response
				if err := json.Unmarshal(m.payload, &resp); err != nil {
					t.Fatalf("unmarshal response: %v", err)
				}
				return &resp
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no response published to %q after timeout; published=%v", replyTo, brk.getPublished())
	return nil
}

func TestHandleExtensionRequest_DispatchesToHandler(t *testing.T) {
	a, brk := agentForExtensionTest(t)
	a.RegisterExtensionMethod(testExtURI, testExtMethod, func(_ context.Context, _ extensions.RequestInfo, params json.RawMessage) (any, *extensions.Error) {
		var p struct {
			S string `json:"s"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &extensions.Error{Code: extensions.CodeInternal, Message: err.Error()}
		}
		return map[string]string{"echo": p.S}, nil
	})

	replyTo := "test-ns/a2a/v1/gateway/response/gw-1/rpc-1"
	msg := &broker.Message{
		UserProperties: map[string]string{
			a2a.PropReplyTo:       replyTo,
			a2a.PropA2AExtensions: testExtURI,
		},
	}
	req := &a2a.Request{JSONRPC: "2.0", ID: "rpc-1", Method: testExtMethod, Params: map[string]any{"s": "hi"}}

	a.handleExtensionRequest(t.Context(), msg, req)

	resp := readPublishedResponse(t, brk, replyTo)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.ID != "rpc-1" {
		t.Fatalf("id = %q", resp.ID)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok || m["echo"] != "hi" {
		t.Fatalf("result = %#v", resp.Result)
	}
}

func TestHandleExtensionRequest_MissingActivationHeader(t *testing.T) {
	a, brk := agentForExtensionTest(t)
	a.RegisterExtensionMethod(testExtURI, testExtMethod, func(_ context.Context, _ extensions.RequestInfo, _ json.RawMessage) (any, *extensions.Error) {
		t.Fatal("handler must not run when activation header is missing")
		return nil, nil
	})

	replyTo := "test-ns/a2a/v1/gateway/response/gw-1/rpc-2"
	msg := &broker.Message{
		UserProperties: map[string]string{
			a2a.PropReplyTo: replyTo,
		},
	}
	req := &a2a.Request{JSONRPC: "2.0", ID: "rpc-2", Method: testExtMethod}

	a.handleExtensionRequest(t.Context(), msg, req)

	resp := readPublishedResponse(t, brk, replyTo)
	if resp.Error == nil || resp.Error.Code != extensions.CodeMethodNotFound {
		t.Fatalf("want method-not-found error, got %+v", resp.Error)
	}
}

func TestHandleExtensionRequest_UnknownMethod(t *testing.T) {
	a, brk := agentForExtensionTest(t)

	replyTo := "test-ns/a2a/v1/gateway/response/gw-1/rpc-3"
	msg := &broker.Message{
		UserProperties: map[string]string{
			a2a.PropReplyTo:       replyTo,
			a2a.PropA2AExtensions: "https://example.com/something/v1",
		},
	}
	req := &a2a.Request{JSONRPC: "2.0", ID: "rpc-3", Method: "session/totally-unknown"}

	a.handleExtensionRequest(t.Context(), msg, req)

	resp := readPublishedResponse(t, brk, replyTo)
	if resp.Error == nil || resp.Error.Code != extensions.CodeMethodNotFound {
		t.Fatalf("want method-not-found error, got %+v", resp.Error)
	}
}

func TestHandleExtensionRequest_NoReplyToDrops(t *testing.T) {
	a, brk := agentForExtensionTest(t)
	a.RegisterExtensionMethod(testExtURI, testExtMethod, func(_ context.Context, _ extensions.RequestInfo, _ json.RawMessage) (any, *extensions.Error) {
		t.Fatal("handler must not run without replyTo")
		return nil, nil
	})

	msg := &broker.Message{
		UserProperties: map[string]string{
			a2a.PropA2AExtensions: testExtURI,
		},
	}
	req := &a2a.Request{JSONRPC: "2.0", ID: "rpc-4", Method: testExtMethod}

	a.handleExtensionRequest(t.Context(), msg, req)

	if pubs := brk.getPublished(); len(pubs) != 0 {
		t.Fatalf("expected no publishes, got %v", pubs)
	}
}

func TestHandleExtensionRequest_HandlerErrorPropagates(t *testing.T) {
	a, brk := agentForExtensionTest(t)
	a.RegisterExtensionMethod(testExtURI, testExtMethod, func(_ context.Context, _ extensions.RequestInfo, _ json.RawMessage) (any, *extensions.Error) {
		return nil, &extensions.Error{Code: extensions.CodeNotFound, Message: "missing", Data: map[string]string{"id": "x"}}
	})

	replyTo := "test-ns/a2a/v1/gateway/response/gw-1/rpc-5"
	msg := &broker.Message{
		UserProperties: map[string]string{
			a2a.PropReplyTo:       replyTo,
			a2a.PropA2AExtensions: testExtURI,
		},
	}
	req := &a2a.Request{JSONRPC: "2.0", ID: "rpc-5", Method: testExtMethod}

	a.handleExtensionRequest(t.Context(), msg, req)

	resp := readPublishedResponse(t, brk, replyTo)
	if resp.Error == nil || resp.Error.Code != extensions.CodeNotFound {
		t.Fatalf("want NotFound error, got %+v", resp.Error)
	}
	if resp.Error.Message != "missing" {
		t.Fatalf("error message = %q", resp.Error.Message)
	}
}

func TestActivationListIncludes(t *testing.T) {
	cases := []struct {
		header string
		uri    string
		want   bool
	}{
		{"", "https://x", false},
		{"https://x", "https://x", true},
		{"https://y , https://x", "https://x", true},
		{"https://y, https://z", "https://x", false},
		{"  https://x  ,https://y", "https://x", true},
	}
	for _, c := range cases {
		if got := activationListIncludes(c.header, c.uri); got != c.want {
			t.Errorf("activationListIncludes(%q, %q) = %v, want %v", c.header, c.uri, got, c.want)
		}
	}
}

func TestHasExtensionMethod_OnlyRegisteredMethodsRoute(t *testing.T) {
	a, _ := agentForExtensionTest(t)

	if a.hasExtensionMethod("session/totally-unknown") {
		t.Fatal("unregistered method must not route")
	}
	if !a.hasExtensionMethod("session/compact") {
		t.Fatal("session/compact should be auto-registered when a session store is configured")
	}

	a.RegisterExtensionMethod(testExtURI, "session/echo-x", func(_ context.Context, _ extensions.RequestInfo, _ json.RawMessage) (any, *extensions.Error) {
		return nil, nil
	})

	if !a.hasExtensionMethod("session/echo-x") {
		t.Fatal("registered method must route")
	}
	if a.hasExtensionMethod("message/send") {
		t.Fatal("core methods must not be reported as extension methods")
	}
	if a.hasExtensionMethod("session/other") {
		t.Fatal("unrelated session/ method must not route")
	}
}

func TestRegisterExtensionMethod_ConcurrentSafe(t *testing.T) {
	a, _ := agentForExtensionTest(t)
	const N = 50
	done := make(chan struct{}, N*2)
	for i := range N {
		go func() {
			a.RegisterExtensionMethod(testExtURI, "session/m"+string(rune('a'+i%26)), func(_ context.Context, _ extensions.RequestInfo, _ json.RawMessage) (any, *extensions.Error) {
				return nil, nil
			})
			done <- struct{}{}
		}()
		go func() {
			_ = a.hasExtensionMethod("session/m" + string(rune('a'+i%26)))
			done <- struct{}{}
		}()
	}
	for range N * 2 {
		<-done
	}
}
