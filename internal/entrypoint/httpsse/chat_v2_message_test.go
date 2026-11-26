// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

func sendBody(agent, contextID string) string {
	meta := `{"agent_name":"` + agent + `"}`
	ctx := ""
	if contextID != "" {
		ctx = `"contextId":"` + contextID + `",`
	}
	return `{"jsonrpc":"2.0","id":"req-1","params":{"message":{"kind":"message","role":"user",` + ctx +
		`"parts":[{"kind":"text","text":"hello"}],"metadata":` + meta + `}}}`
}

func putChat(a *HTTPSSEAdapter, path, body, caller string) *httptest.ResponseRecorder {
	req := withIdentity(httptest.NewRequest(http.MethodPut, chatv2.Prefix+path, strings.NewReader(body)), caller)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	return rec
}

func postV1Send(a *HTTPSSEAdapter, body, caller string) *httptest.ResponseRecorder {
	req := withIdentity(httptest.NewRequest(http.MethodPost, "/api/v1/message:send", strings.NewReader(body)), caller)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	return rec
}

func TestChatV2SendStartsATaskTheCallerCanRead(t *testing.T) {
	a := newCoverageAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent})

	for _, path := range []string{"/message/send", "/message/stream"} {
		rec := putChat(a, path, sendBody(sweepAgent, ""), sweepOwner)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", path, rec.Code, rec.Body)
		}
		var resp chatv2.SendResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: decode %s: %v", path, rec.Body, err)
		}
		if resp.JSONRPC != "2.0" || resp.ID != "req-1" || resp.Result.Kind != "task" || resp.Result.ID == "" || resp.Result.ContextID == "" {
			t.Fatalf("%s: response = %+v, want a JSON-RPC task result", path, resp)
		}
		if code, task := getChatTask(t, a, resp.Result.ID, sweepOwner); code != http.StatusOK || task.Status.State != chatv2.TaskStateWorking {
			t.Errorf("%s: reading the new task gave %d %+v, want 200 working", path, code, task)
		}
		if code, _ := getChatTask(t, a, resp.Result.ID, sweepStranger); code != http.StatusNotFound {
			t.Errorf("%s: another user reading the new task got %d, want 404", path, code)
		}
	}
}

var traceIDField = regexp.MustCompile(`"traceId":"[^"]*"`)

func withoutTrace(body string) string { return traceIDField.ReplaceAllString(body, `"traceId":""`) }

func TestChatV2SendRefusesAsV1Does(t *testing.T) {
	a := newCoverageAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent})

	cases := map[string]struct {
		body   string
		status int
	}{
		"unknown agent":    {sendBody("no-such-agent", ""), http.StatusNotFound},
		"no agent":         {`{"jsonrpc":"2.0","id":"req-1","params":{"message":{"role":"user","parts":[],"metadata":{}}}}`, http.StatusBadRequest},
		"bad context":      {sendBody(sweepAgent, "not-a-uuid"), http.StatusBadRequest},
		"wrong version":    {`{"jsonrpc":"1.0","id":"req-1","params":{}}`, http.StatusBadRequest},
		"not json":         {`{`, http.StatusBadRequest},
		"control response": {`{"jsonrpc":"2.0","id":"req-1","params":{"message":{"role":"user","parts":[{"kind":"data","data":{"type":"user_input_response"}}],"metadata":{"agent_name":"` + sweepAgent + `"}}}}`, http.StatusBadRequest},
	}
	for name, c := range cases {
		v1 := postV1Send(a, c.body, sweepOwner)
		v2 := putChat(a, "/message/send", c.body, sweepOwner)

		if v1.Code != c.status || v2.Code != c.status {
			t.Errorf("%s: v1 answered %d, v2 %d, want %d from both", name, v1.Code, v2.Code, c.status)
		}
		if withoutTrace(v1.Body.String()) != withoutTrace(v2.Body.String()) {
			t.Errorf("%s: v1 body %s, v2 body %s, want the same JSON-RPC error", name, v1.Body, v2.Body)
		}
	}
}

func TestChatV2SendIntoSomeoneElsesSessionIsRefused(t *testing.T) {
	a := newCoverageAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent})
	first := putChat(a, "/message/send", sendBody(sweepAgent, ""), sweepOwner)
	var sent chatv2.SendResponse
	if err := json.Unmarshal(first.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}

	rec := putChat(a, "/message/send", sendBody(sweepAgent, sent.Result.ContextID), sweepStranger)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestChatV2SendRefusesACallerWhoMayNotUseTheAgent(t *testing.T) {
	a := newCoverageAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent})
	body := sendBody(sweepAgent, "")
	scoped := func(path string, req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.Mux().ServeHTTP(rec, req)
		return rec
	}
	newReq := func(method, path string) *http.Request {
		req := withIdentity(httptest.NewRequest(method, path, strings.NewReader(body)), sweepOwner, "agent:other-agent:invoke")
		req.Header.Set("Content-Type", "application/json")
		return req
	}

	v1 := scoped("", newReq(http.MethodPost, "/api/v1/message:send"))
	v2 := scoped("", newReq(http.MethodPut, chatv2.Prefix+"/message/send"))

	if v1.Code != http.StatusForbidden || v2.Code != http.StatusForbidden {
		t.Fatalf("v1 answered %d, v2 %d, want 403 from both", v1.Code, v2.Code)
	}
	if withoutTrace(v1.Body.String()) != withoutTrace(v2.Body.String()) {
		t.Errorf("v1 body %s, v2 body %s, want the same JSON-RPC error", v1.Body, v2.Body)
	}
}

func TestChatV2SendRejectsANonJSONContentType(t *testing.T) {
	a := newCoverageAdapter(t)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent})
	req := withIdentity(httptest.NewRequest(http.MethodPut, chatv2.Prefix+"/message/send", strings.NewReader(sendBody(sweepAgent, ""))), sweepOwner)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	a.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", rec.Code)
	}
}
