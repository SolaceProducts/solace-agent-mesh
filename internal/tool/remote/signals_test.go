// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const leakedSourceTitle = "CANARY-SOURCE-TITLE-7f3a"

func webSource(citationID string) map[string]any {
	return map[string]any{
		"citationId":     citationID,
		"title":          leakedSourceTitle,
		"sourceUrl":      "https://example.com/page",
		"url":            "https://example.com/page",
		"contentPreview": "snippet",
		"relevanceScore": 1.0,
		"sourceType":     "web",
		"metadata": map[string]any{
			"title":   leakedSourceTitle,
			"link":    "https://example.com/page",
			"type":    "web_search",
			"favicon": "https://www.google.com/s2/favicons?domain=https://example.com/page&sz=32",
		},
	}
}

func ragSignal(turn int, sources ...map[string]any) map[string]any {
	return map[string]any{
		"type": "rag_info_update",
		"data": map[string]any{
			"query":       "golang",
			"search_type": "web_search",
			"search_turn": turn,
			"is_complete": true,
			"sources":     sources,
		},
	}
}

func schemaResult(signals ...any) map[string]any {
	return map[string]any{
		"_schema": "ToolResult", "_schema_version": "1.0",
		"status": "success", "message": "searched",
		"data":    map[string]any{"formatted_results": "results"},
		"signals": signals,
	}
}

type invokeCapture struct {
	mu     sync.Mutex
	params []remotetool.InvokeParams
}

func (c *invokeCapture) all() []remotetool.InvokeParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]remotetool.InvokeParams(nil), c.params...)
}

func stubSTR(t *testing.T, brk *broker.DevBroker, toolName string, reply func(p remotetool.InvokeParams, call int) map[string]any) *invokeCapture {
	t.Helper()
	q, err := brk.CreateQueue("signals-str-"+toolName, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, remotetool.InvokeTopic(testNamespace, toolName)); err != nil {
		t.Fatal(err)
	}
	capture := &invokeCapture{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			msg, recvErr := brk.Receive(ctx, q)
			if recvErr != nil {
				return
			}
			msg.Ack()
			var req struct {
				ID     string                  `json:"id"`
				Params remotetool.InvokeParams `json:"params"`
			}
			_ = json.Unmarshal(msg.Payload, &req)
			capture.mu.Lock()
			capture.params = append(capture.params, req.Params)
			call := len(capture.params) - 1
			capture.mu.Unlock()
			replyTo := msg.UserProperties[remotetool.PropReplyTo]
			result := reply(req.Params, call)
			if result == nil {
				simulateSTRError(brk, req.ID, replyTo, remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "restarting"})
				continue
			}
			payload, _ := json.Marshal(a2a.NewResponse(req.ID, remotetool.InvokeResult{ToolResult: result}))
			_ = brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload})
		}
	}()
	return capture
}

func newSignalTool(t *testing.T, cfg *ToolConfig, logs *bytes.Buffer) (*Tool, *broker.DevBroker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	exe := NewExecutor(brk, testNamespace, testAgent, WithInitTimeout(2*time.Second),
		WithInvokeBuffer(2*time.Second), WithLogger(logger))
	startExecutor(t, exe)
	if cfg.TimeoutSeconds == 0 {
		cfg.TimeoutSeconds = 5
	}
	return NewTool(cfg, exe), brk
}

type signalSink struct {
	mu      sync.Mutex
	signals []any
}

func (s *signalSink) send(signal any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signals = append(s.signals, signal)
	return nil
}

func (s *signalSink) ragUpdates() []a2a.RAGInfoUpdateData {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []a2a.RAGInfoUpdateData
	for _, sig := range s.signals {
		if update, ok := sig.(a2a.RAGInfoUpdateData); ok {
			out = append(out, update)
		}
	}
	return out
}

func searchContext(sink *signalSink) *tool.Context {
	return &tool.Context{AgentName: testAgent, SearchTurns: &tool.SearchTurns{}, SendSignal: sink.send}
}

func turnsOf(t *testing.T, params []remotetool.InvokeParams) []int {
	t.Helper()
	turns := make([]int, len(params))
	for i, p := range params {
		if p.SearchTurn == nil {
			t.Fatalf("invoke %d carries no search_turn", i)
		}
		turns[i] = *p.SearchTurn
	}
	return turns
}

func TestExecute_PublishesASearchToolsCitationsAndHidesThemFromTheModel(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	rt, brk := newSignalTool(t, &ToolConfig{ToolName: "web_search_1a2b3c4d"}, &logs)
	capture := stubSTR(t, brk, "web_search_1a2b3c4d", func(p remotetool.InvokeParams, _ int) map[string]any {
		turn := *p.SearchTurn
		return schemaResult(ragSignal(turn, webSource("s"+strconv.Itoa(turn)+"r0")))
	})

	sink := &signalSink{}
	toolCtx := searchContext(sink)
	for range 2 {
		result, err := rt.Execute(context.Background(), map[string]any{"query": "golang"}, toolCtx)
		if err != nil || result.Status != tool.StatusSuccess {
			t.Fatalf("Execute = %+v, %v", result, err)
		}
		if result.Data["formatted_results"] != "results" || len(result.Data) != 1 {
			t.Fatalf("result data = %v, want only the tool's data", result.Data)
		}
	}

	if turns := turnsOf(t, capture.all()); len(turns) != 2 || turns[0] != 0 || turns[1] != 1 {
		t.Fatalf("search turns = %v, want [0 1]", turns)
	}
	updates := sink.ragUpdates()
	if len(updates) != 2 {
		t.Fatalf("published %d signals, want 2", len(updates))
	}
	for i, u := range updates {
		if u.Type != string(a2a.SignalRAGInfoUpdate) || u.SearchTurn != i || u.Sources[0].CitationID != "s"+strconv.Itoa(i)+"r0" {
			t.Errorf("signal %d = %+v", i, u)
		}
		if _, kept := u.Sources[0].Metadata["favicon"]; kept {
			t.Errorf("signal %d kept the tool-supplied favicon", i)
		}
	}
	if strings.Contains(logs.String(), leakedSourceTitle) {
		t.Error("a source title from an untrusted tool result reached the logs")
	}
}

func TestExecute_LeavesSignalsInALegacyPlainResult(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	rt, brk := newSignalTool(t, &ToolConfig{ToolName: "web_search_legacy"}, &logs)
	stubSTR(t, brk, "web_search_legacy", func(p remotetool.InvokeParams, _ int) map[string]any {
		return map[string]any{"message": "ok", "signals": []any{ragSignal(*p.SearchTurn, webSource("s0r0"))}}
	})

	sink := &signalSink{}
	result, err := rt.Execute(context.Background(), map[string]any{}, searchContext(sink))
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := result.Data[resultSignalsKey]; !kept {
		t.Fatalf("result data = %v, want the legacy result's signals key kept", result.Data)
	}
	if n := len(sink.ragUpdates()); n != 0 {
		t.Fatalf("published %d signals from a legacy result, want 0", n)
	}
}

func TestExecute_SendsNoSearchTurnForAToolThatIsNotASearch(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	rt, brk := newSignalTool(t, &ToolConfig{ToolName: "query_sql"}, &logs)
	capture := stubSTR(t, brk, "query_sql", func(remotetool.InvokeParams, int) map[string]any {
		return schemaResult(ragSignal(0, webSource("s0r0")))
	})

	sink := &signalSink{}
	if _, err := rt.Execute(context.Background(), map[string]any{}, searchContext(sink)); err != nil {
		t.Fatal(err)
	}
	if p := capture.all(); len(p) != 1 || p[0].SearchTurn != nil {
		t.Fatalf("invoke params = %+v, want one without a search_turn", p)
	}
	if n := len(sink.ragUpdates()); n != 0 {
		t.Fatalf("published %d signals for a call with no search turn, want 0", n)
	}
	if !strings.Contains(logs.String(), errSignalNoTurn.Error()) {
		t.Errorf("logs = %s, want the no-turn drop reason", logs.String())
	}
}

func TestExecute_RetryReusesTheSearchTurn(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	rt, brk := newSignalTool(t, &ToolConfig{ToolName: "web_search_retry"}, &logs)
	capture := stubSTR(t, brk, "web_search_retry", func(p remotetool.InvokeParams, call int) map[string]any {
		if call == 0 {
			return nil
		}
		return schemaResult(ragSignal(*p.SearchTurn, webSource("s"+strconv.Itoa(*p.SearchTurn)+"r0")))
	})

	sink := &signalSink{}
	toolCtx := searchContext(sink)
	toolCtx.SearchTurns.Next("web_search")
	if _, err := rt.Execute(context.Background(), map[string]any{}, toolCtx); err != nil {
		t.Fatal(err)
	}
	if turns := turnsOf(t, capture.all()); len(turns) != 2 || turns[0] != 1 || turns[1] != 1 {
		t.Fatalf("search turns = %v, want the retry to reuse turn 1", turns)
	}
	if updates := sink.ragUpdates(); len(updates) != 1 || updates[0].SearchTurn != 1 {
		t.Fatalf("published %+v, want one signal for turn 1", updates)
	}
	if next := toolCtx.SearchTurns.Next("web_search"); next != 2 {
		t.Fatalf("next turn = %d, want 2: the retry must not draw a turn", next)
	}
}

func TestExecute_WithoutASignalChannelStillReturnsTheResult(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	rt, brk := newSignalTool(t, &ToolConfig{ToolName: "web_search_nochan"}, &logs)
	stubSTR(t, brk, "web_search_nochan", func(p remotetool.InvokeParams, _ int) map[string]any {
		return schemaResult(ragSignal(*p.SearchTurn, webSource("s0r0")))
	})
	toolCtx := &tool.Context{AgentName: testAgent, SearchTurns: &tool.SearchTurns{}}
	result, err := rt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("Execute = %+v, %v", result, err)
	}
}

func TestDecodeResultSignal_RefusesWhatTheSandboxMustNotSend(t *testing.T) {
	t.Parallel()
	three, one, ten := 3, 1, 10
	withData := func(mutate func(data map[string]any)) map[string]any {
		s := ragSignal(3, webSource("s3r0"))
		mutate(s["data"].(map[string]any))
		return s
	}
	withSource := func(mutate func(source map[string]any)) map[string]any {
		source := webSource("s3r0")
		mutate(source)
		return ragSignal(3, source)
	}
	withMetadata := func(key string, value any) map[string]any {
		return withSource(func(s map[string]any) { s["metadata"].(map[string]any)[key] = value })
	}
	sources := func(n int) []map[string]any {
		out := make([]map[string]any, n)
		for i := range out {
			out[i] = webSource("s3r" + strconv.Itoa(i))
		}
		return out
	}

	cases := []struct {
		name   string
		signal any
		turn   *int
		want   error
	}{
		{"not an object", "rag_info_update", &three, errSignalShape},
		{"unknown envelope field", map[string]any{"type": "rag_info_update", "data": map[string]any{}, "extra": 1}, &three, errSignalShape},
		{"type not allowlisted", map[string]any{"type": "authentication_required", "data": map[string]any{}}, &three, errSignalType},
		{"unknown data field", withData(func(d map[string]any) { d["script"] = "x" }), &three, errSignalData},
		{"data type disagrees", withData(func(d map[string]any) { d["type"] = "deep_research_report" }), &three, errSignalData},
		{"no turn assigned", ragSignal(3, webSource("s3r0")), nil, errSignalNoTurn},
		{"another turn", ragSignal(2, webSource("s2r0")), &three, errSignalTurn},
		{"deep research search type", withData(func(d map[string]any) { d["search_type"] = "deep_research" }), &three, errSignalSearchType},
		{"empty search type", withData(func(d map[string]any) { d["search_type"] = "" }), &three, errSignalSearchType},
		{"51 sources", ragSignal(3, sources(51)...), &three, errSignalSourceCount},
		{"citation of another turn", ragSignal(3, webSource("s2r0")), &three, errSignalCitationID},
		{"turn 10 citation for turn 1", ragSignal(1, webSource("s10r0")), &one, errSignalCitationID},
		{"turn 11 citation for turn 1", ragSignal(1, webSource("s11r0")), &one, errSignalCitationID},
		{"turn 10 image for turn 1", ragSignal(1, webSource("img10r0")), &one, errSignalCitationID},
		{"turn 1 citation for turn 10", ragSignal(10, webSource("s1r0")), &ten, errSignalCitationID},
		{"prefixed citation", ragSignal(3, webSource("xs3r0")), &three, errSignalCitationID},
		{"suffixed citation", ragSignal(3, webSource("s3r0x")), &three, errSignalCitationID},
		{"citation without a result index", ragSignal(3, webSource("s3r")), &three, errSignalCitationID},
		{"citation without r", ragSignal(3, webSource("s3")), &three, errSignalCitationID},
		{"javascript source URL", withSource(func(s map[string]any) { s["sourceUrl"] = "javascript:alert(1)" }), &three, errSignalSourceScheme},
		{"javascript URL with a host", withSource(func(s map[string]any) { s["sourceUrl"] = "javascript://example.com/%0Aalert(1)" }), &three, errSignalSourceScheme},
		{"ftp source URL", withSource(func(s map[string]any) { s["sourceUrl"] = "ftp://example.com/f" }), &three, errSignalSourceScheme},
		{"https URL without a host", withSource(func(s map[string]any) { s["sourceUrl"] = "https:///x" }), &three, errSignalSourceScheme},
		{"relative source URL", withSource(func(s map[string]any) { s["sourceUrl"] = "/rel" }), &three, errSignalSourceScheme},
		{"bad url field", withSource(func(s map[string]any) { s["url"] = "javascript:alert(1)" }), &three, errSignalSourceScheme},
		{"data URL image", withMetadata("imageUrl", "data:image/svg+xml;base64,AAAA"), &three, errSignalSourceScheme},
		{"bad metadata link", withMetadata("link", "javascript:alert(1)"), &three, errSignalSourceScheme},
		{"non-string link", withMetadata("link", 7), &three, errSignalData},
		{"object title", withMetadata("title", map[string]any{"x": 1}), &three, errSignalData},
		{"number title", withMetadata("title", 7), &three, errSignalData},
		{"unknown metadata key", withMetadata("source_type", "web"), &three, errSignalMetadataKey},
		{"over the size limit", withSource(func(s map[string]any) {
			s["contentPreview"] = strings.Repeat("x", 256<<10)
		}), &three, errSignalTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeResultSignal(tc.signal, tc.turn); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecodeResultSignal_AcceptsWhatAWebSearchSends(t *testing.T) {
	t.Parallel()
	fifty := make([]map[string]any, 50)
	for i := range fifty {
		fifty[i] = webSource("s12r" + strconv.Itoa(i))
	}
	image := webSource("img12r0")
	image["metadata"].(map[string]any)["imageUrl"] = "https://example.com/a.png"
	plainHTTP := webSource("s12r1")
	plainHTTP["sourceUrl"], plainHTTP["url"] = "http://example.com/p", ""
	upperScheme := webSource("s12r2")
	upperScheme["sourceUrl"] = "HTTPS://example.com/p"
	noURL := webSource("s12r3")
	noURL["sourceUrl"], noURL["url"] = "", ""

	turn := 12
	for name, signal := range map[string]any{
		"page and image": ragSignal(turn, webSource("s12r0"), image),
		"http and empty": ragSignal(turn, plainHTTP, noURL),
		"upper scheme":   ragSignal(turn, upperScheme),
		"50 sources":     ragSignal(turn, fifty...),
	} {
		got, err := decodeResultSignal(signal, &turn)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		update := got.(a2a.RAGInfoUpdateData)
		if update.Type != string(a2a.SignalRAGInfoUpdate) {
			t.Fatalf("%s: type = %q", name, update.Type)
		}
		for _, s := range update.Sources {
			if _, kept := s.Metadata["favicon"]; kept {
				t.Fatalf("%s: favicon kept on %s", name, s.CitationID)
			}
		}
	}
}

func TestPublishResultSignals_CapsTheSignalCount(t *testing.T) {
	t.Parallel()
	turn := 0
	for _, tc := range []struct{ sent, published int }{{4, 4}, {6, 4}} {
		var logs bytes.Buffer
		rt := NewTool(&ToolConfig{ToolName: "web_search_x"}, nil)
		rt.logger = slog.New(slog.NewTextHandler(&logs, nil))
		signals := make([]any, tc.sent)
		for i := range signals {
			signals[i] = ragSignal(turn, webSource("s0r0"))
		}
		sink := &signalSink{}
		rt.publishResultSignals(&tool.Context{SendSignal: sink.send}, signals, &turn)
		if n := len(sink.ragUpdates()); n != tc.published {
			t.Fatalf("sent %d: published %d, want %d", tc.sent, n, tc.published)
		}
		if warned := strings.Contains(logs.String(), "over the count limit"); warned != (tc.sent > tc.published) {
			t.Errorf("sent %d: count-limit warning = %v; logs = %s", tc.sent, warned, logs.String())
		}
	}
}

func TestTheSDKSignalTypeIsTheOneTheAgentAccepts(t *testing.T) {
	t.Parallel()
	if samtoolsdk.SignalRAGInfoUpdate != string(a2a.SignalRAGInfoUpdate) {
		t.Fatalf("samtoolsdk.SignalRAGInfoUpdate = %q, a2a.SignalRAGInfoUpdate = %q", samtoolsdk.SignalRAGInfoUpdate, a2a.SignalRAGInfoUpdate)
	}
}

func TestPublishResultSignals_RefusesSignalsThatAreNotAnArray(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	rt := NewTool(&ToolConfig{ToolName: "web_search_x"}, nil)
	rt.logger = slog.New(slog.NewTextHandler(&logs, nil))
	turn := 0
	sink := &signalSink{}
	rt.publishResultSignals(&tool.Context{SendSignal: sink.send}, ragSignal(turn, webSource("s0r0")), &turn)
	if len(sink.ragUpdates()) != 0 || !strings.Contains(logs.String(), errSignalsNotArray.Error()) {
		t.Fatalf("published %d; logs = %s", len(sink.ragUpdates()), logs.String())
	}
}
