// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

type stubServer struct {
	srv               *httptest.Server
	gotBody           []byte
	gotAuth           string
	gotPath           string
	gotAccept         string
	pollWriter        func(w http.ResponseWriter)
	submitReply       []byte
	submitCode        int
	submitContentType string
	pollContentType   string
}

func newStubServer(t *testing.T) *stubServer {
	t.Helper()
	s := &stubServer{submitCode: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/message:send", func(w http.ResponseWriter, r *http.Request) {
		s.gotPath = r.URL.Path
		s.gotAuth = r.Header.Get("Authorization")
		s.gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set(contentTypeHeader, cmp.Or(s.submitContentType, "application/json"))
		w.WriteHeader(s.submitCode)
		_, _ = w.Write(s.submitReply)
	})
	mux.HandleFunc("/api/v1/sse/subscribe/", func(w http.ResponseWriter, r *http.Request) {
		s.gotAccept = r.Header.Get("Accept")
		w.Header().Set(contentTypeHeader, cmp.Or(s.pollContentType, "text/event-stream"))
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if s.pollWriter != nil {
			s.pollWriter(w)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func TestSubmitTask_Success(t *testing.T) {
	s := newStubServer(t)
	s.submitReply = []byte(`{"jsonrpc":"2.0","id":"x","result":{"id":"task-7"}}`)

	c := NewGatewayClient(discardLogger())
	taskID, err := c.SubmitTask(context.Background(), s.srv.URL, "AgentX", "hello", "Bearer abc")
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "task-7" {
		t.Fatalf("taskID=%q", taskID)
	}
	if s.gotAuth != "Bearer abc" {
		t.Fatalf("auth=%q", s.gotAuth)
	}
	var body map[string]any
	if err := json.Unmarshal(s.gotBody, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["method"] != "message/send" {
		t.Fatalf("method=%v", body["method"])
	}
	params := body["params"].(map[string]any)
	msg := params["message"].(map[string]any)
	meta := msg["metadata"].(map[string]any)
	if meta["agent_name"] != "AgentX" {
		t.Fatalf("agent_name=%v", meta["agent_name"])
	}
	parts := msg["parts"].([]any)
	if parts[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("prompt: %v", parts[0])
	}
}

func TestSubmitTask_MissingResultID(t *testing.T) {
	s := newStubServer(t)
	s.submitReply = []byte(`{"jsonrpc":"2.0","id":"x","result":{}}`)
	c := NewGatewayClient(discardLogger())

	taskID, err := c.SubmitTask(context.Background(), s.srv.URL, "AgentX", "p", "")
	if err != nil {
		t.Fatal(err)
	}
	if taskID != "" {
		t.Fatalf("expected empty taskID on missing result.id, got %q", taskID)
	}
}

func TestSubmitTask_Non2xx(t *testing.T) {
	s := newStubServer(t)
	s.submitCode = http.StatusBadGateway
	s.submitReply = []byte(`{"detail":"upstream"}`)
	c := NewGatewayClient(discardLogger())

	_, err := c.SubmitTask(context.Background(), s.srv.URL, "AgentX", "p", "")
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected 502 error, got %v", err)
	}
	if strings.Contains(err.Error(), "upstream") {
		t.Errorf("error %q repeats the response body", err)
	}
}

func TestSubmitTask_EmptyURL(t *testing.T) {
	c := NewGatewayClient(discardLogger())
	_, err := c.SubmitTask(context.Background(), "", "AgentX", "p", "")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestSubmitTask_MalformedJSON(t *testing.T) {
	s := newStubServer(t)
	s.submitReply = []byte(`not-json`)
	c := NewGatewayClient(discardLogger())

	_, err := c.SubmitTask(context.Background(), s.srv.URL, "AgentX", "p", "")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestSubmitTask_HTMLSignInPageSurfacesActionableErrorWithoutMarkup(t *testing.T) {
	s := newStubServer(t)
	s.submitContentType = "text/html; charset=UTF-8"
	s.submitReply = []byte("<!-- Copyright (C) Microsoft Corporation. All rights reserved. -->\n" +
		"<!DOCTYPE html><html><head><title>Sign in to your account</title></head></html>")
	c := NewGatewayClient(discardLogger())

	_, err := c.SubmitTask(context.Background(), s.srv.URL, "AgentX", "p", "Bearer stale")
	if err == nil || !strings.Contains(err.Error(), "HTML page instead of JSON") {
		t.Fatalf("expected actionable HTML error, got %v", err)
	}
	if strings.Contains(err.Error(), "Sign in to your account") || strings.Contains(err.Error(), "<") {
		t.Fatalf("error must not leak raw HTML markup, got %v", err)
	}
}

func TestSubmitTask_HTMLBodyWithoutHTMLContentType(t *testing.T) {
	s := newStubServer(t)
	s.submitContentType = "text/plain"
	s.submitReply = []byte("  \n<!DOCTYPE html><html></html>")
	c := NewGatewayClient(discardLogger())

	_, err := c.SubmitTask(context.Background(), s.srv.URL, "AgentX", "p", "")
	if err == nil || !strings.Contains(err.Error(), "HTML page instead of JSON") {
		t.Fatalf("expected HTML detection via leading '<', got %v", err)
	}
}

func TestLooksLikeHTMLResponse(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		want        bool
	}{
		{"json object", "application/json", `{"result":{}}`, false},
		{"json array", "application/json", `[1,2,3]`, false},
		{"plain not-json", "application/json", "not-json", false},
		{"empty body", "application/json", "", false},
		{"html content type", "text/html; charset=UTF-8", `{"result":{}}`, true},
		{"leading angle bracket", "text/plain", "<!DOCTYPE html>", true},
		{"leading whitespace then bracket", "", "  \n\t<html>", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeHTMLResponse(tc.contentType, []byte(tc.body)); got != tc.want {
				t.Fatalf("looksLikeHTMLResponse(%q, %q)=%v, want %v", tc.contentType, tc.body, got, tc.want)
			}
		})
	}
}

func TestSubmitTask_TrimsTrailingSlash(t *testing.T) {
	s := newStubServer(t)
	s.submitReply = []byte(`{"result":{"id":"t"}}`)
	c := NewGatewayClient(discardLogger())

	if _, err := c.SubmitTask(context.Background(), s.srv.URL+"/", "A", "p", ""); err != nil {
		t.Fatal(err)
	}
	if s.gotPath != "/api/v1/message:send" {
		t.Fatalf("path=%q", s.gotPath)
	}
}

func sseLines(blocks ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, strings.Join(blocks, "\n\n")+"\n\n")
	}
}

func TestPollTask_FinalTaskEvent(t *testing.T) {
	s := newStubServer(t)
	s.pollWriter = sseLines(
		`event: msg
data: {"jsonrpc":"2.0","id":"x","result":{"kind":"task","status":{"message":{"parts":[{"kind":"text","text":"done"}]}}}}`,
	)
	c := NewGatewayClient(discardLogger())

	res, err := c.PollTask(context.Background(), s.srv.URL, "task-1", "Bearer abc", 2*time.Second)
	if err != nil || res == nil {
		t.Fatalf("err=%v res=%v", err, res)
	}
	if got := ExtractResponseText(res); got != "done" {
		t.Fatalf("text=%q", got)
	}
	if s.gotAccept != "text/event-stream" {
		t.Fatalf("accept=%q", s.gotAccept)
	}
}

func TestPollTask_StatusUpdateAccumulationThenFinal(t *testing.T) {
	s := newStubServer(t)
	s.pollWriter = sseLines(
		`data: {"result":{"kind":"status-update","final":false,"status":{"message":{"parts":[{"kind":"text","text":"chunk1"}]}}}}`,
		`data: {"result":{"kind":"status-update","final":false,"status":{"message":{"parts":[{"kind":"text","text":"chunk2"}]}}}}`,
		`data: {"result":{"kind":"status-update","final":true,"status":{"message":{"parts":[]}}}}`,
	)
	c := NewGatewayClient(discardLogger())

	res, err := c.PollTask(context.Background(), s.srv.URL, "t", "", 2*time.Second)
	if err != nil || res == nil {
		t.Fatalf("err=%v res=%v", err, res)
	}
	if got := ExtractResponseText(res); got != "chunk1\nchunk2" {
		t.Fatalf("accumulated text=%q", got)
	}
}

func TestPollTask_GatewayError(t *testing.T) {
	s := newStubServer(t)
	s.pollWriter = sseLines(`data: {"error":{"code":-32603,"message":"internal"}}`)
	c := NewGatewayClient(discardLogger())

	res, err := c.PollTask(context.Background(), s.srv.URL, "t", "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatalf("expected nil result on gateway error, got %v", res)
	}
}

func TestPollTask_HTMLSignInPage(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s := newStubServer(t)
	s.pollContentType = "text/html; charset=UTF-8"
	s.pollWriter = func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><head><title>Sign in to your account</title></head></html>")
	}
	c := NewGatewayClient(logger)

	res, err := c.PollTask(context.Background(), s.srv.URL, "task-1", "Bearer stale", 2*time.Second)
	if err != nil || res != nil {
		t.Fatalf("expected soft failure (nil, nil), got res=%v err=%v", res, err)
	}
	if !strings.Contains(logs.String(), "returned HTML instead of events") {
		t.Fatalf("expected HTML-redirect WARN, got logs: %q", logs.String())
	}
}

func TestPollTask_StreamClosesWithoutFinal(t *testing.T) {
	s := newStubServer(t)
	s.pollWriter = sseLines(
		`data: {"result":{"kind":"status-update","status":{"message":{"parts":[{"kind":"text","text":"only"}]}}}}`,
	)
	c := NewGatewayClient(discardLogger())

	res, err := c.PollTask(context.Background(), s.srv.URL, "t", "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("expected synthesized result")
	}
	if got := ExtractResponseText(res); got != "only" {
		t.Fatalf("text=%q", got)
	}
}

func TestPollTask_StreamClosesWithoutAnyData(t *testing.T) {
	s := newStubServer(t)
	s.pollWriter = sseLines()
	c := NewGatewayClient(discardLogger())

	res, err := c.PollTask(context.Background(), s.srv.URL, "t", "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatalf("expected nil result on empty stream, got %v", res)
	}
}

func TestPollTask_NonJSONDataSkipped(t *testing.T) {
	s := newStubServer(t)
	s.pollWriter = sseLines(
		`: heartbeat`,
		`data: not-json`,
		`data: {"result":{"kind":"task","status":{"message":{"parts":[{"kind":"text","text":"ok"}]}}}}`,
	)
	c := NewGatewayClient(discardLogger())

	res, err := c.PollTask(context.Background(), s.srv.URL, "t", "", 2*time.Second)
	if err != nil || res == nil {
		t.Fatalf("err=%v res=%v", err, res)
	}
}

func TestPollTask_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "denied")
	}))
	defer srv.Close()
	c := NewGatewayClient(discardLogger())
	res, err := c.PollTask(context.Background(), srv.URL, "t", "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatalf("expected nil on 403, got %v", res)
	}
}

func TestPollTask_EmptyURLOrTaskID(t *testing.T) {
	c := NewGatewayClient(discardLogger())
	if _, err := c.PollTask(context.Background(), "", "t", "", time.Second); err == nil {
		t.Fatal("expected error on empty URL")
	}
	if _, err := c.PollTask(context.Background(), "http://x", "", "", time.Second); err == nil {
		t.Fatal("expected error on empty taskID")
	}
}

func TestPollTask_OverallTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Header().Set(contentTypeHeader, "text/event-stream")
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	c := NewGatewayClient(discardLogger())
	start := time.Now()
	res, err := c.PollTask(context.Background(), srv.URL, "t", "", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatalf("expected nil on timeout, got %v", res)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("PollTask took %s; expected ~200ms", elapsed)
	}
}

func TestPollTask_TransportError(t *testing.T) {
	c := NewGatewayClient(discardLogger())
	res, err := c.PollTask(context.Background(), "http://127.0.0.1:1", "t", "", 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatal("expected nil on transport error")
	}
}

func TestSplitSSEBlocks_NoSeparator(t *testing.T) {
	tail, blocks := splitSSEBlocks("partial without sep")
	if tail != "partial without sep" || len(blocks) != 0 {
		t.Fatalf("tail=%q blocks=%v", tail, blocks)
	}
}

func TestParseSSEBlock_NoDataLines(t *testing.T) {
	if got := parseSSEBlock(": heartbeat\nevent: foo", discardLogger()); got != nil {
		t.Fatalf("expected nil for non-data block, got %v", got)
	}
}

func TestParseSSEBlock_MultilineData(t *testing.T) {
	got := parseSSEBlock("data: {\n\"a\":1\ndata: ,\"b\":2}", discardLogger())
	if got == nil {
		return
	}
	t.Fatalf("expected nil for malformed multiline, got %v", got)
}

func TestCollectTextParts_DropsNonText(t *testing.T) {
	parts := []any{
		map[string]any{"kind": "text", "text": "yes"},
		map[string]any{"kind": "data", "text": "no"},
		map[string]any{"text": "kind missing"},
		"not-an-object",
		map[string]any{"kind": "text", "text": ""},
	}
	got := collectTextParts(parts)
	if len(got) != 1 || got[0] != "yes" {
		t.Fatalf("got %v", got)
	}
}

func TestSynthesizeResult_RoundTrip(t *testing.T) {
	res := synthesizeResult([]string{"a", "b"})
	if got := ExtractResponseText(res); got != "a\nb" {
		t.Fatalf("text=%q", got)
	}
}

func TestBuildSendMessageRequest_HasUniqueIDs(t *testing.T) {
	r1 := buildSendMessageRequest("A", "p")
	r2 := buildSendMessageRequest("A", "p")
	if r1["id"] == r2["id"] {
		t.Fatal("expected unique JSON-RPC ids")
	}
	msg1 := r1["params"].(map[string]any)["message"].(map[string]any)
	msg2 := r2["params"].(map[string]any)["message"].(map[string]any)
	if msg1["messageId"] == msg2["messageId"] {
		t.Fatal("expected unique messageIds")
	}
}

func TestBuildSendMessageRequest_StampsEvalSource(t *testing.T) {
	msg := buildSendMessageRequest("A", "p")["params"].(map[string]any)["message"].(map[string]any)
	meta := msg["metadata"].(map[string]any)
	if meta["session_source"] != "eval" {
		t.Errorf("session_source = %v, want eval (keeps eval sessions out of chat history)", meta["session_source"])
	}
}

func TestTruncateForLog(t *testing.T) {
	if got := truncateForLog("short", 10); got != "short" {
		t.Fatalf("unexpected truncate: %q", got)
	}
	if got := truncateForLog("0123456789", 4); got != "0123…" {
		t.Fatalf("got %q", got)
	}
}

func TestReadSSEStream_CtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, _ := io.Pipe()
	res, _, err := readSSEStream(ctx, pr, 100*time.Millisecond, discardLogger(), "t")
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatalf("expected nil result on ctx-cancel, got %v", res)
	}
}

func TestReadSSEStream_ReaderError(t *testing.T) {
	ctx := context.Background()
	r := errReader{err: errors.New("boom")}
	res, _, err := readSSEStream(ctx, r, time.Second, discardLogger(), "t")
	if err == nil {
		t.Fatal("expected wrapped reader error")
	}
	if res != nil {
		t.Fatal("expected nil result on reader error")
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestParseDBSkills_TypedRows(t *testing.T) {
	raw := `[{"id":"1","name":"S1","description":"d1"},{"id":"2","name":"S2","description":""}]`
	got := parseDBSkills(raw)
	if len(got) != 2 || got[0] != "S1 (d1)" || got[1] != "S2" {
		t.Fatalf("got %v", got)
	}
}

func TestParseDBSkills_GenericFallback(t *testing.T) {
	raw := `[{"name":"OnlyName"},{"description":"orphan"},{"name":"Both","description":"d"}]`
	got := parseDBSkills(raw)
	if !contains(got, "OnlyName") || !contains(got, "Both (d)") {
		t.Fatalf("got %v", got)
	}
}

func TestParseDBSkills_EmptyAndMalformed(t *testing.T) {
	for _, in := range []string{"", "not json"} {
		if got := parseDBSkills(in); got != nil {
			t.Fatalf("input=%q: expected nil, got %v", in, got)
		}
	}
}

func TestPtrToStr(t *testing.T) {
	if got := ptrToStr(nil); got != "" {
		t.Fatalf("got %q", got)
	}
	v := "x"
	if got := ptrToStr(&v); got != "x" {
		t.Fatalf("got %q", got)
	}
}

func TestCapitalise(t *testing.T) {
	if got := capitalise(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if got := capitalise("hello"); got != "Hello" {
		t.Fatalf("got %q", got)
	}
}

func TestRunOrchestratorRoundTrip_PollErrorWrapsCode(t *testing.T) {
	fake := &fakeGatewayClient{taskID: "t", pollErr: fmt.Errorf("net: down")}
	ctx, set := newDatasetSvcWithFake(t, fake)
	_, err := set.dataset.GenerateDataset(ctx, GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 1}, "alice", "http://gw", "")
	brv := asBusinessRuleErr(t, err)
	if brv.Code != "DATASET_GEN_FAILED" {
		t.Fatalf("code=%q msg=%q", brv.Code, brv.Message)
	}
	if !strings.Contains(brv.Message, "Dataset-generation task failed") {
		t.Fatalf("msg=%q", brv.Message)
	}
}

func TestRunOrchestratorRoundTrip_SubmitErrorWrapsCode(t *testing.T) {
	fake := &fakeGatewayClient{submitErr: errors.New("dial fail")}
	ctx, set := newDatasetSvcWithFake(t, fake)
	_, err := set.dataset.GenerateDataset(ctx, GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 1}, "alice", "http://gw", "")
	brv := asBusinessRuleErr(t, err)
	if !strings.Contains(brv.Message, "Failed to submit dataset-generation task") {
		t.Fatalf("msg=%q", brv.Message)
	}
}

func TestRunOrchestratorRoundTrip_EmptyResponseTextBRV(t *testing.T) {
	fake := &fakeGatewayClient{taskID: "t", result: map[string]any{
		"status": map[string]any{
			"message": map[string]any{
				"parts": []any{map[string]any{"kind": "data"}},
			},
		},
	}}
	ctx, set := newDatasetSvcWithFake(t, fake)
	_, err := set.dataset.GenerateDataset(ctx, GenerateDatasetRequest{TargetAgent: "AgentX", NumExamples: 1}, "alice", "http://gw", "")
	brv := asBusinessRuleErr(t, err)
	if !strings.Contains(brv.Message, "empty response") {
		t.Fatalf("msg=%q", brv.Message)
	}
}

func TestWithAIGen_SetsDefaultsAndEnables(t *testing.T) {
	ctx, set := newEvalSvcs(t)
	_ = ctx
	fake := &fakeGatewayClient{}
	svc := set.dataset.WithAIGen(AIGenConfig{Client: fake})
	if !svc.IsAIGenEnabled() {
		t.Fatal("expected enabled")
	}
	if svc.aiGen.Orchestrator != DefaultDatasetGenAgent {
		t.Fatalf("orchestrator default not applied: %q", svc.aiGen.Orchestrator)
	}
	if svc.aiGen.Timeout != DefaultDatasetGenTimeout {
		t.Fatalf("timeout default not applied: %v", svc.aiGen.Timeout)
	}
}

func TestWithAIGen_NilClientStaysDisabled(t *testing.T) {
	_, set := newEvalSvcs(t)
	svc := set.dataset.WithAIGen(AIGenConfig{Client: nil})
	if svc.IsAIGenEnabled() {
		t.Fatal("nil client should leave AI gen disabled")
	}
}

func contains(s []string, target string) bool { return slices.Contains(s, target) }
