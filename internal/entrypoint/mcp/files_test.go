// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const fileInputTool = "target_greet"

func newFileInputAdapter(t *testing.T, raw map[string]any) (*Adapter, *artifact.ScopedService, *broker.DevBroker, broker.Queue) {
	t.Helper()
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	a, brk, q := newFileInputAdapterWithStore(t, raw, scoped)
	return a, scoped, brk, q
}

func newFileInputAdapterWithStore(t *testing.T, raw map[string]any, scoped *artifact.ScopedService) (*Adapter, *broker.DevBroker, broker.Queue) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var opts []runtime.Option
	if scoped != nil {
		opts = append(opts, runtime.WithArtifacts(scoped))
	}
	core := entrypoint.NewGatewayCore(stubGatewayAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "test-mcp-gw"})
	core.SetServices(runtime.NewRuntime(brk, nil, opts...).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("core start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	q, err := brk.CreateQueue("file-input", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	a := &Adapter{
		core:       core,
		cfg:        mustParseConfig(t, raw),
		toolMap:    map[string]toolMapping{fileInputTool: {agentName: "target-agent"}},
		agentTools: map[string][]string{},
		tasks:      map[string]*taskState{},
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	a.cfg.TaskTimeoutSeconds = 2
	return a, brk, q
}

func fileCall(message string, files ...map[string]any) mcplib.CallToolRequest {
	req := toolCall(fileInputTool, message)
	args := req.Params.Arguments.(map[string]any)
	list := make([]any, 0, len(files))
	for _, f := range files {
		list = append(list, f)
	}
	args[filesArg] = list
	return req
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func submitted(t *testing.T, a *Adapter, brk *broker.DevBroker, q broker.Queue, req mcplib.CallToolRequest) a2a.Message {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = a.callTool(ctx, fileInputTool, req)
	}()
	recvCtx, recvCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer recvCancel()
	received, err := brk.Receive(recvCtx, q)
	cancel()
	<-done
	if err != nil || received == nil {
		t.Fatalf("no task published: %v", err)
	}
	var request struct {
		Params struct {
			Message a2a.Message `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(received.Payload, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return request.Params.Message
}

func TestCallTool_FilesAreStoredAndReachTheAgent(t *testing.T) {
	a, scoped, brk, q := newFileInputAdapter(t, map[string]any{"enable_file_input": true})
	const csv = "id,name\n1,widget\n"

	msg := submitted(t, a, brk, q, fileCall("Summarize the attached sheet",
		map[string]any{"name": " orders.csv ", "mimeType": "text/csv", "data": b64(csv)}))

	if len(msg.Parts) != 2 || msg.Parts[0].Text != "Summarize the attached sheet" {
		t.Fatalf("submitted parts = %+v, want the message then one file", msg.Parts)
	}
	file := msg.Parts[1].File
	if msg.Parts[1].Kind != "file" || file == nil || file.Bytes != "" || !strings.HasPrefix(file.URI, "artifact://") {
		t.Fatalf("file part = %+v, want a reference to a stored artifact, not inline bytes", msg.Parts[1])
	}
	if file.Name != "orders.csv" {
		t.Errorf("file part name = %q, want the name the file was stored under", file.Name)
	}

	loc := scoped.Resolve("", mcpNoIdentityUserID, msg.ContextID, "orders.csv")
	content, _, err := scoped.Inner.Load(t.Context(), loc, nil)
	if err != nil {
		t.Fatalf("the file is not stored in the conversation under its trimmed name: %v", err)
	}
	if string(content) != csv {
		t.Errorf("stored content = %q, want %q", content, csv)
	}
	tags, _ := artifact.LoadCompanionMetadataAtLocator(t.Context(), scoped, loc, nil)["tags"].([]any)
	if !slices.Contains(tags, any(a2a.ArtifactTagUserUploaded)) {
		t.Errorf("stored tags = %v, want %s", tags, a2a.ArtifactTagUserUploaded)
	}
}

func TestCallTool_StoredFileKeepsOrDerivesItsMediaType(t *testing.T) {
	cases := map[string]struct {
		sent, wantStored, wantPart string
	}{
		"sent":          {sent: "text/csv", wantStored: "text/csv", wantPart: "text/csv"},
		"custom":        {sent: "application/vnd.example+json", wantStored: "application/vnd.example+json", wantPart: "application/vnd.example+json"},
		"none, derived": {sent: "", wantStored: "text/csv", wantPart: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			a, scoped, brk, q := newFileInputAdapter(t, map[string]any{"enable_file_input": true})
			file := map[string]any{"name": "orders.csv", "data": b64("id,name\n1,widget\n")}
			if tc.sent != "" {
				file["mimeType"] = tc.sent
			}

			msg := submitted(t, a, brk, q, fileCall("Summarize the attached sheet", file))

			if got := msg.Parts[1].File.MIMEType; got != tc.wantPart {
				t.Errorf("file part media type = %q, want %q", got, tc.wantPart)
			}
			_, stored, err := scoped.Inner.Load(t.Context(), scoped.Resolve("", mcpNoIdentityUserID, msg.ContextID, "orders.csv"), nil)
			if err != nil {
				t.Fatalf("load stored file: %v", err)
			}
			if stored != tc.wantStored {
				t.Errorf("stored media type = %q, want %q", stored, tc.wantStored)
			}
		})
	}
}

func TestCallTool_FileAtTheSizeLimitIsAccepted(t *testing.T) {
	a, _, brk, q := newFileInputAdapter(t, map[string]any{"enable_file_input": true, "max_input_file_bytes": 8})

	msg := submitted(t, a, brk, q, fileCall("hello", map[string]any{"name": "a.txt", "data": b64("12345678")}))

	if len(msg.Parts) != 2 || msg.Parts[1].Kind != "file" {
		t.Errorf("a file of exactly max_input_file_bytes was not sent to the agent: parts %+v", msg.Parts)
	}
}

func TestCallTool_FileRefusals(t *testing.T) {
	small := map[string]any{"enable_file_input": true, "max_input_files": 2, "max_input_file_bytes": 8}
	good := map[string]any{"name": "a.txt", "data": b64("a")}
	cases := map[string]struct {
		cfg   map[string]any
		files []map[string]any
		want  string
	}{
		"file input off":      {cfg: nil, files: []map[string]any{{"name": "a.txt", "data": b64("hi")}}, want: "does not accept files"},
		"too many files":      {cfg: small, files: []map[string]any{good, good, good}, want: "too many files"},
		"too large":           {cfg: small, files: []map[string]any{{"name": "a.txt", "data": b64("123456789")}}, want: "larger than 8 bytes"},
		"not base64":          {cfg: small, files: []map[string]any{{"name": "a.txt", "data": "%%%"}}, want: "not valid base64"},
		"no name":             {cfg: small, files: []map[string]any{{"name": " ", "data": b64("a")}}, want: "has no name"},
		"empty":               {cfg: small, files: []map[string]any{{"name": "a.txt", "data": ""}}, want: "is empty"},
		"not a file array":    {cfg: small, files: []map[string]any{{"name": 7, "data": b64("a")}}, want: "must be an array"},
		"path in second name": {cfg: small, files: []map[string]any{good, {"name": "docs/b.txt", "data": b64("b")}}, want: "not an allowed name"},
		"parent directory":    {cfg: small, files: []map[string]any{{"name": "a..b.txt", "data": b64("a")}}, want: "not an allowed name"},
		"internal name":       {cfg: small, files: []map[string]any{{"name": "a.txt.metadata.json", "data": b64("a")}}, want: "not an allowed name"},
		"scope prefix":        {cfg: small, files: []map[string]any{good, {"name": "user:b.txt", "data": b64("b")}}, want: "not an allowed name"},
		"scope prefix only":   {cfg: small, files: []map[string]any{{"name": "user:", "data": b64("a")}}, want: "not an allowed name"},
		"scope prefix, dot":   {cfg: small, files: []map[string]any{{"name": "user:.", "data": b64("a")}}, want: "not an allowed name"},
		"scope prefix, index": {cfg: small, files: []map[string]any{{"name": "user:project_bm25_index.zip", "data": b64("a")}}, want: "not an allowed name"},
		"control character":   {cfg: small, files: []map[string]any{{"name": "a\nb.txt", "data": b64("a")}}, want: "no control characters"},
		"name too long":       {cfg: small, files: []map[string]any{{"name": strings.Repeat("n", 252) + ".txt", "data": b64("a")}}, want: "at most 255 bytes"},
		"invalid media type":  {cfg: small, files: []map[string]any{{"name": "a.txt", "mimeType": "not a type", "data": b64("a")}}, want: "not a valid media type"},
		"media type too long": {cfg: small, files: []map[string]any{{"name": "a.txt", "mimeType": "text/" + strings.Repeat("x", 251), "data": b64("a")}}, want: "mimeType must be at most 255 bytes"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			a, _, brk, q := newFileInputAdapter(t, tc.cfg)

			res, reason, err := a.callTool(t.Context(), fileInputTool, fileCall("hello", tc.files...))

			if err != nil || res == nil || !res.IsError {
				t.Fatalf("callTool = (%+v, %v), want a tool error", res, err)
			}
			if text := res.Content[0].(mcplib.TextContent).Text; !strings.Contains(text, tc.want) {
				t.Errorf("refusal = %q, want it to say %q", text, tc.want)
			}
			if reason != taxonomy.RejectMalformed {
				t.Errorf("refusal reason = %q, want %q: the client's input was wrong, nothing broke", reason, taxonomy.RejectMalformed)
			}
			assertNoTaskPublished(t, brk, q)
		})
	}
}

func TestCallTool_FileThatCannotBeStoredRefusesTheCall(t *testing.T) {
	a, brk, q := newFileInputAdapterWithStore(t, map[string]any{"enable_file_input": true}, nil)

	res, reason, err := a.callTool(t.Context(), fileInputTool, fileCall("hello", map[string]any{"name": "a.txt", "data": b64("a")}))

	if err != nil || res == nil || !res.IsError {
		t.Fatalf("callTool = (%+v, %v), want a tool error", res, err)
	}
	if text := res.Content[0].(mcplib.TextContent).Text; !strings.Contains(text, `"a.txt" could not be stored`) {
		t.Errorf("refusal = %q, want it to name the file that could not be stored", text)
	}
	if reason != taxonomy.RejectInternal {
		t.Errorf("refusal reason = %q, want %q", reason, taxonomy.RejectInternal)
	}
	assertNoTaskPublished(t, brk, q)
}

func TestCallTool_FilesNeverLandInAConversationTheCallerDoesNotOwn(t *testing.T) {
	gw := newConversationGateway(t)
	gw.a.cfg.EnableFileInput = true
	const owner, filename = "someone-else", "orders.csv"
	handle := idgen.New()
	sessionID, err := gw.a.sessKeys.GetOrCreateByExternalKey(t.Context(), store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeMCP, ExternalKey: handle, UserID: owner,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	result, err := startMCPClient(t, gw.url).CallTool(t.Context(), mcplib.CallToolRequest{Params: mcplib.CallToolParams{
		Name: searchTool,
		Arguments: map[string]any{
			"message": "summarize the attached file", sessionIDArg: handle,
			filesArg: []any{map[string]any{"name": filename, "data": b64("id,qty\n1,3\n")}},
		},
	}})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}

	if !result.IsError || mcplib.GetTextFromContent(result.Content[0]) != unknownSessionIDMessage {
		t.Fatalf("result = %+v, want the unknown-sessionId refusal", result)
	}
	for _, user := range []string{gw.a.defaultIdentity().UserID(), owner} {
		stored, _, _ := gw.artifacts.Inner.Load(t.Context(), gw.artifacts.Resolve("", user, sessionID, filename), nil)
		if len(stored) != 0 {
			t.Errorf("the file was written into the other user's conversation under user %q", user)
		}
	}
}

func TestCallTool_StoreFailureKeepsTheConversationHandle(t *testing.T) {
	gw := newConversationGateway(t)
	gw.a.cfg.EnableFileInput = true
	gw.artifacts.Inner = failingSaves{Service: gw.artifacts.Inner}

	result, err := startMCPClient(t, gw.url).CallTool(t.Context(), mcplib.CallToolRequest{Params: mcplib.CallToolParams{
		Name: searchTool,
		Arguments: map[string]any{
			"message": "summarize the attached file",
			filesArg:  []any{map[string]any{"name": "orders.csv", "data": b64("id,qty\n1,3\n")}},
		},
	}})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}

	if !result.IsError || !strings.Contains(mcplib.GetTextFromContent(result.Content[0]), `"orders.csv" could not be stored`) {
		t.Fatalf("result = %+v, want the could-not-be-stored refusal", result)
	}
	if handleIn(t, result) == "" {
		t.Error("the refusal carries no conversation handle")
	}
}

func TestCallTool_ContinuedConversationStoresItsFilesThere(t *testing.T) {
	gw := newConversationGateway(t)
	gw.a.cfg.EnableFileInput = true
	c := startMCPClient(t, gw.url)
	const filename, content = "orders.csv", "id,qty\n1,3\n"

	firstSession, first := callAndAnswer(t, gw, c, searchTool, nil)
	secondSession, _ := callAndAnswer(t, gw, c, searchTool, map[string]any{
		sessionIDArg: handleIn(t, first),
		filesArg:     []any{map[string]any{"name": filename, "data": b64(content)}},
	})

	if secondSession != firstSession {
		t.Fatalf("the continued call ran in session %q, want %q", secondSession, firstSession)
	}
	loc := gw.artifacts.Resolve("", gw.a.defaultIdentity().UserID(), firstSession, filename)
	if stored, _, _ := gw.artifacts.Inner.Load(t.Context(), loc, nil); string(stored) != content {
		t.Errorf("stored in the continued conversation: %q, want %q", stored, content)
	}
}

type failingSaves struct{ artifact.Service }

func (failingSaves) Save(context.Context, artifact.Locator, []byte, string) (int, error) {
	return 0, errors.New("store unavailable")
}

func (failingSaves) SaveVersion(context.Context, artifact.Locator, int, []byte, string) error {
	return errors.New("store unavailable")
}

func assertNoTaskPublished(t *testing.T, brk *broker.DevBroker, q broker.Queue) {
	t.Helper()
	recvCtx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if received, _ := brk.Receive(recvCtx, q); received != nil {
		t.Error("a refused call still published a task")
	}
}

func TestRegisteredTools_OfferFilesOnlyWhenEnabled(t *testing.T) {
	card := &a2a.AgentCard{Name: "test-agent", Skills: []a2a.AgentSkill{{ID: "s1", Name: "greet", Description: "Say hello"}}}
	for _, enabled := range []bool{false, true} {
		a := newToolRegistryAdapter(t)
		a.cfg.EnableFileInput = enabled

		a.registerAgentTools(card)

		schema, offered := a.mcpServer.GetTool("test_agent_greet").Tool.InputSchema.Properties[filesArg].(map[string]any)
		if offered != enabled {
			t.Fatalf("enable_file_input=%v: files offered = %v", enabled, offered)
		}
		if !enabled {
			continue
		}
		items := schema["items"].(map[string]any)
		if req := items["required"].([]string); !slices.Equal(req, []string{"name", "data"}) {
			t.Errorf("file item required = %v, want [name data]", req)
		}
		if schema["maxItems"] != defaultMaxInputFiles {
			t.Errorf("maxItems = %v, want %d", schema["maxItems"], defaultMaxInputFiles)
		}
	}
}

func TestConfig_FileInputDefaultsAndValidation(t *testing.T) {
	cfg := mustParseConfig(t, nil)
	if cfg.EnableFileInput || cfg.MaxInputFiles != 5 || cfg.MaxInputFileBytes != 10<<20 {
		t.Errorf("defaults = (%v, %d, %d), want file input off with 5 files of 10 MiB, the documented defaults",
			cfg.EnableFileInput, cfg.MaxInputFiles, cfg.MaxInputFileBytes)
	}
	if ceilingMaxInputFiles != 20 || ceilingInputFileTotalSize != 128<<20 {
		t.Errorf("ceilings = (%d files, %d bytes), want the documented 20 files and 128 MiB", ceilingMaxInputFiles, ceilingInputFileTotalSize)
	}
	for name, raw := range map[string]map[string]any{
		"negative file count":     {"max_input_files": -1},
		"negative file size":      {"max_input_file_bytes": -1},
		"file count over ceiling": {"max_input_files": ceilingMaxInputFiles + 1, "max_input_file_bytes": 1},
		"total size over ceiling": {"max_input_files": 4, "max_input_file_bytes": ceilingInputFileTotalSize/4 + 1},
		"product wraps negative":  {"max_input_files": 2, "max_input_file_bytes": 1 << 62},
		"product wraps to zero":   {"max_input_files": 4, "max_input_file_bytes": 1 << 62},
		"largest size":            {"max_input_files": ceilingMaxInputFiles, "max_input_file_bytes": math.MaxInt},
	} {
		if _, err := parseConfig(raw); err == nil {
			t.Errorf("%s: %v accepted", name, raw)
		}
	}
	for _, raw := range []map[string]any{
		{"max_input_files": ceilingMaxInputFiles, "max_input_file_bytes": ceilingInputFileTotalSize / ceilingMaxInputFiles},
		{"max_input_files": 4, "max_input_file_bytes": ceilingInputFileTotalSize / 4},
	} {
		if _, err := parseConfig(raw); err != nil {
			t.Errorf("limits at the ceiling %v refused: %v", raw, err)
		}
	}
}

func TestHTTPHandler_CapsBodiesOnlyWithFileInput(t *testing.T) {
	small := map[string]any{"enable_file_input": true, "max_input_files": 1, "max_input_file_bytes": 1 << 20}
	cases := map[string]struct {
		raw      map[string]any
		withAuth bool
		capped   bool
	}{
		"file input off":                    {raw: nil, capped: false},
		"file input on, authentication off": {raw: small, capped: true},
		"file input on, authentication on":  {raw: small, withAuth: true, capped: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := mustParseConfig(t, tc.raw)
			a := &Adapter{cfg: cfg, logger: noopLogger()}
			if tc.withAuth {
				a.mcpauthHandler = mcpauth.New(mcpauth.Config{
					EnableAuth: true, Issuer: "https://mcp.example.com", Host: "127.0.0.1", Port: 8090,
					IDPClient: fakeMcpauthIDPClient{}, SAMTokenMinter: fakeMcpauthMinter{}, AuthzResolver: fakeMcpauthResolver{},
					Logger: noopLogger(),
				})
			}
			h := a.assembleHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			}))

			limit := cfg.fileInputBodyLimit()
			over := httptest.NewRecorder()
			h.ServeHTTP(over, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(make([]byte, limit+1))))
			if refused := over.Code == http.StatusRequestEntityTooLarge; refused != tc.capped {
				t.Errorf("body of %d bytes got %d, want refused with 413 = %v", limit+1, over.Code, tc.capped)
			}
			if !tc.capped {
				return
			}
			within := httptest.NewRecorder()
			h.ServeHTTP(within, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(make([]byte, limit))))
			if within.Code == http.StatusRequestEntityTooLarge {
				t.Errorf("body of exactly %d bytes was refused", limit)
			}
		})
	}
}

func TestFileInputBodyLimit_FitsTheLargestAllowedCall(t *testing.T) {
	cfg := mustParseConfig(t, map[string]any{"enable_file_input": true, "max_input_files": 3, "max_input_file_bytes": 8 << 20})
	files := make([]any, 0, cfg.MaxInputFiles)
	for i := range cfg.MaxInputFiles {
		files = append(files, map[string]any{
			"name": strings.Repeat("n", 250) + strconv.Itoa(i), "mimeType": "application/octet-stream",
			"data": base64.StdEncoding.EncodeToString(make([]byte, cfg.MaxInputFileBytes)),
		})
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": fileInputTool, "arguments": map[string]any{
			"message": strings.Repeat("m", 1<<20), filesArg: files,
		}},
	})
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	if limit := cfg.fileInputBodyLimit(); int64(len(body)) > limit {
		t.Errorf("a call with %d files of %d bytes and a 1 MiB message is %d bytes, over the %d-byte cap",
			cfg.MaxInputFiles, cfg.MaxInputFileBytes, len(body), limit)
	}
	if slack := cfg.fileInputBodyLimit() - int64(len(body)); slack > baseRequestBodyBytes {
		t.Errorf("cap leaves %d bytes beyond the largest allowed call, more than the %d-byte base allowance", slack, baseRequestBodyBytes)
	}
}
