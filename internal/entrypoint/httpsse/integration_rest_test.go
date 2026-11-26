// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type testGateway struct {
	server     *httptest.Server
	client     *http.Client
	adapter    *HTTPSSEAdapter
	sessRepo   *store.SessionRepo
	chatRepo   *store.ChatTaskRepo
	sseRepo    *store.SSEEventRepo
	taskRepo   *store.TaskRepo
	groupRepo  *store.PromptGroupRepo
	promptRepo *store.PromptRepo
	db         *store.DB
	artSvc     *artifact.ScopedService
}

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	return buildTestGateway(t, testGatewayOptions{flags: communityFlags(t)})
}

func communityFlags(t *testing.T) *features.Registry {
	t.Helper()
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		t.Fatalf("load community flags: %v", err)
	}
	return reg
}

type testGatewayOptions struct {
	appConfig map[string]any
	flags     *features.Registry
	volumes   volume.Service
	llm       llm.Client
}

func buildTestGateway(t *testing.T, opts testGatewayOptions) *testGateway {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()

	appConfig := map[string]any{
		"namespace":          "test-ns",
		"session_secret_key": "test-secret",
		"gateway_id":         "test-gw",
		"fastapi_host":       "127.0.0.1",
		"fastapi_port":       0,
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": testDBURL(t, "integ"),
		},
	}
	for k, v := range opts.appConfig {
		appConfig[k] = v
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	artSvc := &artifact.ScopedService{
		Inner:     memory.New(),
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	rtOpts := []runtime.Option{runtime.WithLogger(slog.Default()), runtime.WithArtifacts(artSvc)}
	if opts.flags != nil {
		rtOpts = append(rtOpts, runtime.WithFeatureRegistry(opts.flags))
	}
	if opts.volumes != nil {
		rtOpts = append(rtOpts, runtime.WithVolumes(opts.volumes))
	}
	if opts.llm != nil {
		rtOpts = append(rtOpts, runtime.WithDefaultLLM(opts.llm))
	}
	rt := runtime.NewRuntime(brk, nil, rtOpts...)
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	db := core.Store()
	if db == nil {
		t.Fatal("expected DB to be initialized")
	}
	t.Cleanup(func() { _ = db.Close() })

	ts := httptest.NewServer(adapter.Handler())
	t.Cleanup(ts.Close)

	return &testGateway{
		server:     ts,
		client:     ts.Client(),
		adapter:    adapter,
		sessRepo:   store.NewSessionRepo(db),
		chatRepo:   store.NewChatTaskRepo(db),
		sseRepo:    store.NewSSEEventRepo(db),
		taskRepo:   store.NewTaskRepo(db),
		groupRepo:  store.NewPromptGroupRepo(db),
		promptRepo: store.NewPromptRepo(db),
		db:         db,
		artSvc:     artSvc,
	}
}

func (g *testGateway) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := g.client.Get(g.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (g *testGateway) seedChatTask(t *testing.T, sessionID string) {
	t.Helper()
	um := "hello"
	if err := g.chatRepo.Create(context.Background(), &store.ChatTask{
		ID:             idgen.New(),
		SessionID:      sessionID,
		UserID:         "sam_dev_user",
		UserMessage:    &um,
		MessageBubbles: `[]`,
		CreatedTime:    time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}
}

func (g *testGateway) delete(t *testing.T, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("DELETE", g.server.URL+path, nil)
	resp, err := g.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (g *testGateway) postJSON(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := g.client.Post(g.server.URL+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (g *testGateway) patchJSON(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("PATCH", g.server.URL+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (g *testGateway) doMultipart(t *testing.T, path, contentType string, body io.Reader) *http.Response {
	t.Helper()
	resp, err := g.client.Post(g.server.URL+path, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (g *testGateway) uploadProjectArtifact(t *testing.T, projID, filename, content string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("uploadFile", filename)
	_, _ = part.Write([]byte(content))
	_ = w.WriteField("filename", filename)
	_ = w.Close()

	resp := g.doMultipart(t, fmt.Sprintf("/api/v1/projects/%s/artifacts", projID), w.FormDataContentType(), &buf)
	if resp.StatusCode != 202 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload project artifact: expected 202, got %d; body: %s", resp.StatusCode, body)
	}
	if loc := resp.Header.Get("Sse-Location"); loc == "" {
		t.Error("upload project artifact: expected Sse-Location header")
	}
	_ = resp.Body.Close()
}

func decodeRespJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func assertErrorBody(t *testing.T, resp *http.Response, wantMessage string) {
	t.Helper()
	var body ErrorResponse
	decodeRespJSON(t, resp, &body)
	if body.Message != wantMessage {
		t.Errorf("error message = %q, want %q", body.Message, wantMessage)
	}
	if body.ErrorID == "" {
		t.Error("error response missing errorId")
	}
}

func TestIntegration_InfoEndpoints(t *testing.T) {
	gw := newTestGateway(t)
	body := func(path string) map[string]any {
		t.Helper()
		resp := gw.get(t, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200; body: %s", path, resp.StatusCode, bodyString(t, resp))
		}
		var v map[string]any
		decodeRespJSON(t, resp, &v)
		return v
	}

	if got := body("/health")["status"]; got == "" || got == nil {
		t.Errorf("/health status = %v, want non-empty", got)
	}
	if _, ok := body("/api/v1/config")["frontendFeatureEnablement"]; ok {
		t.Error("/api/v1/config still carries frontendFeatureEnablement")
	}
	if products, _ := body("/api/v1/version")["products"].([]any); len(products) == 0 {
		t.Error("/api/v1/version lists no products")
	}
	if user := body("/api/v1/user"); user["userId"] == "" && user["username"] == "" {
		t.Errorf("/api/v1/user = %v, want a user id", user)
	}
	if got := body("/api/v1/speech/config")["sttExternal"]; got != false {
		t.Errorf("/api/v1/speech/config sttExternal = %v, want false when speech is not configured", got)
	}
	for _, path := range []string{"/api/v1/user/capabilities", "/api/v1/agentCards", "/api/v1/people/search?query=test"} {
		body(path)
	}
}

func voiceFlag(t *testing.T, on bool) *features.Registry {
	t.Helper()
	reg := communityFlags(t)
	reg.Register(features.Definition{Key: featureKeyVoiceInteraction, Name: "Voice Interaction", Default: on})
	return reg
}

func voiceEnabledRegistry(t *testing.T) *features.Registry {
	t.Helper()
	return voiceFlag(t, true)
}

func validSpeechBlock() map[string]any {
	return map[string]any{
		"stt": map[string]any{"provider": "azure", "azure": map[string]any{"api_key": "fake-key", "region": "eastus"}},
		"tts": map[string]any{"provider": "gemini", "gemini": map[string]any{"api_key": "fake-key"}},
	}
}

func TestIntegration_ConfigFrontendSettings(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		voice  bool
		want   string
	}{
		{
			name: "app name and gemini TTS from config",
			config: map[string]any{
				"model":                     map[string]any{"model": "gpt-4"},
				"speech":                    map[string]any{"tts": map[string]any{"provider": "gemini", "gemini": map[string]any{"api_key": "fake-key"}}},
				"task_logging":              map[string]any{"enabled": true},
				"frontend_welcome_message":  "Hello!",
				"frontend_app_name":         "TestBot",
				"frontend_collect_feedback": true,
			},
			voice: true,
			want:  `{"frontendAppName":"TestBot","frontendLogoUrl":null,"engineTTS":"external","ttsProvider":"gemini","textToSpeech":true}`,
		},
		{
			name: "azure TTS provider is reported",
			config: map[string]any{"speech": map[string]any{"tts": map[string]any{
				"provider": "azure", "gemini": map[string]any{"api_key": "fake-key"}, "azure": map[string]any{"api_key": "fake-key", "region": "eastus"},
			}}},
			voice: true,
			want:  `{"frontendAppName":"Solace Agent Mesh","frontendLogoUrl":null,"engineTTS":"external","ttsProvider":"azure","textToSpeech":true}`,
		},
		{
			name:   "deprecated frontend_bot_name still sets the app name",
			config: map[string]any{"model": map[string]any{"model": "gpt-4"}, "frontend_bot_name": "LegacyBot"},
			voice:  false,
			want:   `{"frontendAppName":"LegacyBot","frontendLogoUrl":null,"engineTTS":"browser","ttsProvider":"gemini","textToSpeech":false}`,
		},
		{
			name:   "explicitly emptied logo is an empty string, not null",
			config: map[string]any{"model": map[string]any{"model": "gpt-4"}, "frontend_logo_url": ""},
			voice:  false,
			want:   `{"frontendAppName":"Solace Agent Mesh","frontendLogoUrl":"","engineTTS":"browser","ttsProvider":"gemini","textToSpeech":false}`,
		},
		{
			name:   "configured logo is the url",
			config: map[string]any{"model": map[string]any{"model": "gpt-4"}, "frontend_logo_url": "https://cdn.example.com/logo.svg"},
			voice:  false,
			want:   `{"frontendAppName":"Solace Agent Mesh","frontendLogoUrl":"https://cdn.example.com/logo.svg","engineTTS":"browser","ttsProvider":"gemini","textToSpeech":false}`,
		},
		{
			name:   "speech configured with voice_interaction off stays disabled",
			config: map[string]any{"speech": validSpeechBlock()},
			voice:  false,
			want:   `{"frontendAppName":"Solace Agent Mesh","frontendLogoUrl":null,"engineTTS":"browser","ttsProvider":"gemini","textToSpeech":false}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGatewayWithFlags(t, tc.config, voiceFlag(t, tc.voice))
			resp := gw.get(t, "/api/v1/config")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var cfg map[string]any
			decodeRespJSON(t, resp, &cfg)
			tts, _ := cfg["ttsSettings"].(map[string]any)
			got := map[string]any{"engineTTS": tts["engineTTS"], "ttsProvider": tts["ttsProvider"], "textToSpeech": tts["textToSpeech"]}
			for _, key := range []string{"frontendAppName", "frontendLogoUrl"} {
				if v, present := cfg[key]; present {
					got[key] = v
				}
			}
			if diff := cmp.Diff(decodeJSONText(t, tc.want), any(got)); diff != "" {
				t.Errorf("config (-want +got):\n%s", diff)
			}
		})
	}
}

const speechExposureFields = "textToSpeech, speechToText, sttExternal, ttsExternal, speech routes registered"

func speechExposure(t *testing.T, gw *testGateway) []any {
	t.Helper()
	var cfg struct {
		TTS map[string]any `json:"ttsSettings"`
		STT map[string]any `json:"sttSettings"`
	}
	decodeRespJSON(t, gw.get(t, "/api/v1/config"), &cfg)
	var speech map[string]any
	decodeRespJSON(t, gw.get(t, "/api/v1/speech/config"), &speech)
	routes := false
	for _, r := range gw.adapter.Routes() {
		routes = routes || (strings.HasPrefix(r.Path, "/api/v1/speech/") && r.Path != pathSpeechConfig)
	}
	return []any{cfg.TTS["textToSpeech"], cfg.STT["speechToText"], speech["sttExternal"], speech["ttsExternal"], routes}
}

func TestIntegration_VoiceShippedDefaultIgnoresConfiguredSpeech(t *testing.T) {
	gw := newTestGatewayWithFlags(t, map[string]any{"speech": validSpeechBlock()}, communityFlags(t))
	if diff := cmp.Diff([]any{false, false, false, false, false}, speechExposure(t, gw)); diff != "" {
		t.Errorf("%s (-want +got):\n%s", speechExposureFields, diff)
	}
}

func TestIntegration_VoiceFlagGatesConfiguredSpeech(t *testing.T) {
	tests := []struct {
		name         string
		voice        bool
		wantExternal bool
		wantRoutes   bool
	}{
		{name: "flag off ignores the speech block", voice: false, wantExternal: false, wantRoutes: false},
		{name: "flag on serves the speech block", voice: true, wantExternal: true, wantRoutes: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGatewayWithFlags(t, map[string]any{"speech": validSpeechBlock()}, voiceFlag(t, tc.voice))
			want := []any{tc.wantExternal, tc.wantExternal, tc.wantExternal, tc.wantExternal, tc.wantRoutes}
			if diff := cmp.Diff(want, speechExposure(t, gw)); diff != "" {
				t.Errorf("%s (-want +got):\n%s", speechExposureFields, diff)
			}
		})
	}
}

func TestIntegration_GenerateTitleStatus(t *testing.T) {
	tests := []struct {
		name, session, body string
		wantStatus          int
	}{
		{name: "messages given, no LLM, falls back synchronously", session: "{bare}", body: `{"userMessage":"What is Go?","agentResponse":"Go is a programming language.","force":false}`, wantStatus: 200},
		{name: "force regenerates an existing title", session: "{named}", body: `{"userMessage":"New question","agentResponse":"New answer","force":true}`, wantStatus: 200},
		{name: "existing title is kept", session: "{named}", body: `{}`, wantStatus: 200},
		{name: "untitled session with history falls back synchronously", session: "{chat}", body: `{}`, wantStatus: 200},
		{name: "malformed JSON", session: "{bare}", body: `"not json"`, wantStatus: 400},
		{name: "unknown session", session: "{new}", body: `{"userMessage":"hi"}`, wantStatus: 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			bare := gw.seedSession(t, store.Session{})
			named := gw.seedSession(t, store.Session{Name: strPtr("Existing Title")})
			chat := gw.seedSession(t, store.Session{})
			if err := gw.taskRepo.Create(context.Background(), &store.Task{ID: idgen.New(), UserID: "sam_dev_user", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground", SessionID: &chat}); err != nil {
				t.Fatal(err)
			}
			if err := gw.chatRepo.Create(context.Background(), &store.ChatTask{
				ID: idgen.New(), SessionID: chat, UserID: "sam_dev_user", UserMessage: strPtr("What is the meaning of life?"),
				MessageBubbles: `[{"role":"agent","parts":[{"text":"42"}]}]`, CreatedTime: time.Now().UnixMilli(),
			}); err != nil {
				t.Fatal(err)
			}

			r := strings.NewReplacer("{bare}", bare, "{named}", named, "{chat}", chat, "{new}", idgen.New())
			resp := gw.send(t, http.MethodPost, "/api/v1/sessions/"+r.Replace(tc.session)+"/generateTitle", tc.body)
			if body := bodyString(t, resp); resp.StatusCode != tc.wantStatus {
				t.Errorf("status %d, want %d; body: %s", resp.StatusCode, tc.wantStatus, body)
			}
		})
	}
}

func TestIntegration_GenerateTitleFromServerData(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	taskID := idgen.New()
	if err := gw.taskRepo.Create(context.Background(), &store.Task{ID: taskID, UserID: "sam_dev_user", StartTime: time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sid, InitialRequestText: strPtr("Tell me about Go interfaces")}); err != nil {
		t.Fatal(err)
	}
	gw.seedSSEEvent(t, store.SSEEventBufferRow{TaskID: taskID, SessionID: sid, EventSequence: 1, EventType: "final_response",
		EventData: `{"jsonrpc":"2.0","result":{"kind":"task","task":{"artifacts":[{"parts":[{"kind":"text","text":"Go interfaces are implicit contracts."}]}]}}}`})

	if resp := gw.send(t, http.MethodPost, "/api/v1/sessions/"+sid+"/generateTitle", `{}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	session, err := gw.sessRepo.GetByID(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	if session.Name == nil || *session.Name != "Tell me about Go interfaces" {
		t.Errorf("session name = %v, want the recovered user message", session.Name)
	}
}

func TestIntegration_GenerateTitleWithLLM(t *testing.T) {
	gw := newTestGatewayWithLLM(t, &mockLLMClient{response: "Weather in London"})
	sid := gw.seedSession(t, store.Session{})
	gw.seedTask(t, store.Task{SessionID: &sid})
	if err := gw.chatRepo.Create(context.Background(), &store.ChatTask{
		ID: idgen.New(), SessionID: sid, UserID: "sam_dev_user", UserMessage: strPtr("What is the weather in London?"),
		MessageBubbles: `[{"role":"agent","parts":[{"text":"It is sunny and 22°C in London today."}]}]`, CreatedTime: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	resp := gw.send(t, http.MethodPost, "/api/v1/sessions/"+sid+"/generateTitle",
		`{"userMessage":"What is the weather in London?","agentResponse":"It is sunny and 22°C in London today."}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d, want 202; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	_ = resp.Body.Close()

	time.Sleep(300 * time.Millisecond)

	var list PaginatedResponse[SessionResponse]
	decodeRespJSON(t, gw.get(t, "/api/v1/sessions"), &list)
	for _, s := range list.Data {
		if s.ID == sid {
			if s.Name == nil || *s.Name != "Weather in London" {
				t.Errorf("session title = %v, want %q", s.Name, "Weather in London")
			}
			return
		}
	}
	t.Errorf("session %s not listed", sid)
}

func (g *testGateway) seedTask(t *testing.T, task store.Task) string {
	t.Helper()
	if task.ID == "" {
		task.ID = idgen.New()
	}
	if task.UserID == "" {
		task.UserID = "sam_dev_user"
	}
	if task.Status == nil {
		task.Status = strPtr("completed")
	}
	if task.StartTime == 0 {
		task.StartTime = time.Now().UnixMilli()
	}
	if task.ExecutionMode == "" {
		task.ExecutionMode = "foreground"
	}
	if err := g.taskRepo.Create(context.Background(), &task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return task.ID
}

func (g *testGateway) seedSession(t *testing.T, s store.Session) string {
	t.Helper()
	if s.ID == "" {
		s.ID = idgen.New()
	}
	if s.UserID == "" {
		s.UserID = "sam_dev_user"
	}
	if err := g.sessRepo.Create(context.Background(), &s); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return s.ID
}

func (g *testGateway) seedSSEEvent(t *testing.T, row store.SSEEventBufferRow) {
	t.Helper()
	if row.UserID == "" {
		row.UserID = "sam_dev_user"
	}
	if err := g.sseRepo.Append(context.Background(), &row); err != nil {
		t.Fatalf("seed SSE event: %v", err)
	}
}

func decodeDataArray(t *testing.T, resp *http.Response) []map[string]any {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	decodeRespJSON(t, resp, &body)
	return body.Data
}

func TestIntegration_SessionAndTaskRouteStatus(t *testing.T) {
	tests := []struct {
		name, method, path, body string
		wantStatus               int
	}{
		{name: "get session", method: http.MethodGet, path: "/api/v1/sessions/{session}", wantStatus: 200},
		{name: "rename session", method: http.MethodPatch, path: "/api/v1/sessions/{session}", body: `{"name":"New Name"}`, wantStatus: 200},
		{name: "delete session", method: http.MethodDelete, path: "/api/v1/sessions/{session}", wantStatus: 204},
		{name: "assign session to project", method: http.MethodPatch, path: "/api/v1/sessions/{session}/project", body: `{"projectId":"{project}"}`, wantStatus: 200},
		{name: "unassign project with empty id", method: http.MethodPatch, path: "/api/v1/sessions/{session}/project", body: `{"projectId":""}`, wantStatus: 200},
		{name: "unassign project with null id", method: http.MethodPatch, path: "/api/v1/sessions/{session}/project", body: `{"projectId":null}`, wantStatus: 200},
		{name: "list sessions in a project", method: http.MethodGet, path: "/api/v1/sessions?projectId={project}", wantStatus: 200},
		{name: "search sessions", method: http.MethodGet, path: "/api/v1/sessions/search?query=Alpha", wantStatus: 200},
		{name: "unconsumed events without events", method: http.MethodGet, path: "/api/v1/sessions/{session}/events/unconsumed", wantStatus: 200},
		{name: "session messages", method: http.MethodGet, path: "/api/v1/sessions/{session}/messages", wantStatus: 200},
		{name: "save chat task", method: http.MethodPost, path: "/api/v1/sessions/{session}/chatTasks", body: `{"id":"{new}","userMessage":"Hello agent","messageBubbles":"[{\"role\":\"user\",\"parts\":[{\"text\":\"Hello agent\"}]}]"}`, wantStatus: 200},
		{name: "list tasks in a session", method: http.MethodGet, path: "/api/v1/tasks?sessionId={session}", wantStatus: 200},
		{name: "task status", method: http.MethodGet, path: "/api/v1/tasks/{task}/status", wantStatus: 200},
		{name: "task events", method: http.MethodGet, path: "/api/v1/tasks/{task}/events", wantStatus: 200},
		{name: "task buffered events", method: http.MethodGet, path: "/api/v1/tasks/{task}/events/buffered", wantStatus: 200},
		{name: "delete task buffered events", method: http.MethodDelete, path: "/api/v1/tasks/{task}/events/buffered", wantStatus: 204},
		{name: "task title data", method: http.MethodGet, path: "/api/v1/tasks/{task}/titleData", wantStatus: 200},
		{name: "active background tasks", method: http.MethodGet, path: "/api/v1/tasks/background/active", wantStatus: 200},
		{name: "task export", method: http.MethodGet, path: "/api/v1/tasks/{task}", wantStatus: 200},
		{name: "cancel task with a running child", method: http.MethodPost, path: "/api/v1/tasks/{task}:cancel", wantStatus: 202},
		{name: "cancel unknown task", method: http.MethodPost, path: "/api/v1/tasks/{new}:cancel", wantStatus: 202},
		{name: "events of unknown task", method: http.MethodGet, path: "/api/v1/tasks/{new}/events", wantStatus: 200},
		{name: "delete buffered events of unknown task", method: http.MethodDelete, path: "/api/v1/tasks/{new}/events/buffered", wantStatus: 204},
		{name: "get unknown session", method: http.MethodGet, path: "/api/v1/sessions/{new}", wantStatus: 404},
		{name: "rename unknown session", method: http.MethodPatch, path: "/api/v1/sessions/{new}", body: `{"name":"New"}`, wantStatus: 404},
		{name: "delete unknown session", method: http.MethodDelete, path: "/api/v1/sessions/{new}", wantStatus: 404},
		{name: "assign project to unknown session", method: http.MethodPatch, path: "/api/v1/sessions/{new}/project", body: `{"projectId":"{project}"}`, wantStatus: 404},
		{name: "messages of unknown session", method: http.MethodGet, path: "/api/v1/sessions/{new}/messages", wantStatus: 404},
		{name: "POST to the rename route", method: http.MethodPost, path: "/api/v1/sessions/{session}", body: `"not json"`, wantStatus: 405},
		{name: "POST to the project route", method: http.MethodPost, path: "/api/v1/sessions/{session}/project", body: `"not json"`, wantStatus: 405},
		{name: "save chat task with malformed JSON", method: http.MethodPost, path: "/api/v1/sessions/{session}/chatTasks", body: `"not json"`, wantStatus: 400},
		{name: "save chat task without id", method: http.MethodPost, path: "/api/v1/sessions/{session}/chatTasks", body: `{"messageBubbles":"[]"}`, wantStatus: 400},
		{name: "get unknown task", method: http.MethodGet, path: "/api/v1/tasks/{new}", wantStatus: 404},
		{name: "status of unknown task", method: http.MethodGet, path: "/api/v1/tasks/{new}/status", wantStatus: 404},
		{name: "title data of unknown task", method: http.MethodGet, path: "/api/v1/tasks/{new}/titleData", wantStatus: 404},
		{name: "POST to a task without an action", method: http.MethodPost, path: "/api/v1/tasks/{new}", wantStatus: 404},
		{name: "stream message with malformed JSON", method: http.MethodPost, path: "/api/v1/message:stream", body: `"not json"`, wantStatus: 400},
		{name: "stream message with JSON-RPC 1.0", method: http.MethodPost, path: "/api/v1/message:stream", body: `{"jsonrpc":"1.0","method":"message/send","id":"1"}`, wantStatus: 400},
		{name: "stream message without an agent", method: http.MethodPost, path: "/api/v1/message:stream", body: `{"jsonrpc":"2.0","method":"message/send","id":"1","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hello"}],"metadata":{}}}}`, wantStatus: 400},
		{name: "stream message to an unknown agent", method: http.MethodPost, path: "/api/v1/message:stream", body: `{"jsonrpc":"2.0","method":"message/send","id":"1","params":{"message":{"role":"user","parts":[{"kind":"text","text":"hello"}],"metadata":{"agent_name":"nonexistent-agent"}}}}`, wantStatus: 404},
		{name: "send message with malformed JSON", method: http.MethodPost, path: "/api/v1/message:send", body: `"bad body"`, wantStatus: 400},
		{name: "subscribe without a task id", method: http.MethodGet, path: "/api/v1/sse/subscribe", wantStatus: 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			sessionID := gw.seedSession(t, store.Session{Name: strPtr("Alpha Session")})
			gw.seedChatTask(t, sessionID)
			taskID := gw.seedTask(t, store.Task{SessionID: &sessionID, EventsBuffered: true})
			gw.seedTask(t, store.Task{Status: strPtr("running"), ParentTaskID: &taskID})
			projectID := createProject(t, gw, `{"name":"Assign Target"}`)

			r := strings.NewReplacer("{session}", sessionID, "{task}", taskID, "{project}", projectID, "{new}", idgen.New())
			resp := gw.send(t, tc.method, r.Replace(tc.path), r.Replace(tc.body))
			if body := bodyString(t, resp); resp.StatusCode != tc.wantStatus {
				t.Errorf("%s %s: status %d, want %d; body: %s", tc.method, tc.path, resp.StatusCode, tc.wantStatus, body)
			}
		})
	}
}

func TestIntegration_SessionListEnriched(t *testing.T) {
	gw := newTestGateway(t)
	projectID := createProject(t, gw, `{"name":"My Project"}`)
	inProject := gw.seedSession(t, store.Session{ProjectID: &projectID})
	gw.seedTask(t, store.Task{Status: strPtr("running"), ExecutionMode: "background", SessionID: &inProject})
	plain := gw.seedSession(t, store.Session{})
	gw.seedChatTask(t, plain)

	resp := gw.get(t, "/api/v1/sessions?pageNumber=1&pageSize=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	var list PaginatedResponse[SessionResponse]
	decodeRespJSON(t, resp, &list)
	got := map[string]string{}
	for _, s := range list.Data {
		project := "<none>"
		if s.Project != nil && s.Project.Name != nil {
			project = *s.Project.Name
		}
		got[s.ID] = fmt.Sprintf("project=%s running=%v", project, s.HasRunningBackgroundTask)
	}
	want := map[string]string{inProject: "project=My Project running=true", plain: "project=<none> running=false"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("listed sessions (-want +got):\n%s", diff)
	}
}

func TestIntegration_SessionSearch(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{Name: strPtr("Searchable Session")})
	gw.seedChatTask(t, sid)

	found := decodeDataArray(t, gw.get(t, "/api/v1/sessions/search?query=Searchable"))
	if len(found) == 0 {
		t.Error("search returned no sessions, want the Searchable Session")
	}
}

func TestIntegration_SessionUnconsumedEvents(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	gw.seedSSEEvent(t, store.SSEEventBufferRow{
		TaskID: idgen.New(), SessionID: sid, EventSequence: 1, EventType: "status_update",
		EventData: `{"jsonrpc":"2.0","result":{"kind":"status-update"}}`,
	})

	resp := gw.get(t, "/api/v1/sessions/"+sid+"/events/unconsumed")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	var body UnconsumedEventsResponse
	decodeRespJSON(t, resp, &body)
	if !body.HasEvents {
		t.Error("hasEvents = false, want true")
	}
}

func TestIntegration_SessionMessages(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	if err := gw.chatRepo.Create(context.Background(), &store.ChatTask{
		ID:             idgen.New(),
		SessionID:      sid,
		UserMessage:    strPtr("Hello"),
		MessageBubbles: `[{"type":"user","text":"Hello"},{"type":"agent","text":"Hi there!"}]`,
		TaskMetadata:   strPtr(`{"agentName":"test-agent"}`),
		CreatedTime:    time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	empty := gw.seedSession(t, store.Session{})

	messages := decodeDataArray(t, gw.get(t, "/api/v1/sessions/"+sid+"/messages"))
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	got := []any{messages[0]["senderType"], messages[1]["senderType"], messages[1]["senderName"]}
	if diff := cmp.Diff([]any{"user", "agent", "test-agent"}, got); diff != "" {
		t.Errorf("senderType, senderType, senderName (-want +got):\n%s", diff)
	}
	if got := decodeDataArray(t, gw.get(t, "/api/v1/sessions/"+empty+"/messages")); len(got) != 0 {
		t.Errorf("session without chat history: got %d messages, want 0", len(got))
	}
}

func TestIntegration_ChatTaskSaveIsAnUpsert(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	taskID := idgen.New()
	for _, body := range []string{
		`{"id":"` + taskID + `","userMessage":"Hello","messageBubbles":"[{\"role\":\"user\",\"parts\":[{\"text\":\"Hello\"}]}]"}`,
		`{"id":"` + taskID + `","userMessage":"Hello updated","messageBubbles":"[{\"role\":\"user\",\"parts\":[{\"text\":\"Hello updated\"}]},{\"role\":\"agent\",\"parts\":[{\"text\":\"Hi!\"}]}]"}`,
	} {
		if resp := gw.send(t, http.MethodPost, "/api/v1/sessions/"+sid+"/chatTasks", body); resp.StatusCode != http.StatusOK {
			t.Fatalf("save: status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
		}
	}
	if got := decodeDataArray(t, gw.get(t, "/api/v1/sessions/"+sid+"/chatTasks")); len(got) != 1 {
		t.Errorf("got %d chat tasks, want 1 after saving the same id twice", len(got))
	}
}

func TestIntegration_ChatTaskListReturnsEveryTask(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	for i := range 3 {
		if err := gw.chatRepo.Create(context.Background(), &store.ChatTask{
			ID: idgen.New(), SessionID: sid, UserID: "sam_dev_user", UserMessage: strPtr(fmt.Sprintf("msg %d", i)),
			MessageBubbles: "[]", CreatedTime: time.Now().UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := decodeDataArray(t, gw.get(t, "/api/v1/sessions/"+sid+"/chatTasks")); len(got) != 3 {
		t.Errorf("got %d chat tasks, want 3", len(got))
	}
}

func TestIntegration_TaskBufferedEvents(t *testing.T) {
	gw := newTestGateway(t)
	taskID := gw.seedTask(t, store.Task{Status: strPtr("running")})
	gw.seedSSEEvent(t, store.SSEEventBufferRow{
		TaskID: taskID, SessionID: idgen.New(), EventSequence: 1, EventType: "status_update",
		EventData: `{"jsonrpc":"2.0","result":{"kind":"status-update"}}`,
	})

	if got := decodeDataArray(t, gw.get(t, "/api/v1/tasks/"+taskID+"/events/buffered")); len(got) != 1 {
		t.Errorf("got %d buffered events, want 1", len(got))
	}
	if resp := gw.delete(t, "/api/v1/tasks/"+taskID+"/events/buffered"); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: status %d, want 204", resp.StatusCode)
	}
	if got := decodeDataArray(t, gw.get(t, "/api/v1/tasks/"+taskID+"/events/buffered")); len(got) != 0 {
		t.Errorf("got %d buffered events after delete, want 0", len(got))
	}
}

func taskTitleData(t *testing.T, gw *testGateway, taskID string) map[string]any {
	t.Helper()
	resp := gw.get(t, "/api/v1/tasks/"+taskID+"/titleData")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("title data: status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	var got map[string]any
	decodeRespJSON(t, resp, &got)
	return got
}

func TestIntegration_TaskTitleDataFromRequestText(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	taskID := gw.seedTask(t, store.Task{SessionID: &sid, InitialRequestText: strPtr("What is Go?")})

	got := taskTitleData(t, gw, taskID)
	if got["userMessage"] != "What is Go?" || got["sessionId"] != sid {
		t.Errorf("userMessage %v, sessionId %v; want %q, %q", got["userMessage"], got["sessionId"], "What is Go?", sid)
	}
}

func TestIntegration_TaskTitleDataFromChatBubbles(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	taskID := gw.seedTask(t, store.Task{SessionID: &sid})
	if err := gw.chatRepo.Create(context.Background(), &store.ChatTask{
		ID: taskID, SessionID: sid, UserMessage: strPtr("What is Go?"), CreatedTime: time.Now().UnixMilli(),
		MessageBubbles: `[{"role":"user","parts":[{"text":"What is Go?"}]},{"role":"agent","parts":[{"text":"Go is a programming language."}]}]`,
	}); err != nil {
		t.Fatal(err)
	}

	got := taskTitleData(t, gw, taskID)
	if got["userMessage"] != "What is Go?" || got["agentResponse"] != "Go is a programming language." {
		t.Errorf("userMessage %v, agentResponse %v", got["userMessage"], got["agentResponse"])
	}
}

func TestIntegration_TaskTitleDataFallsBackToFinalSSEResponse(t *testing.T) {
	gw := newTestGateway(t)
	taskID := gw.seedTask(t, store.Task{InitialRequestText: strPtr("Explain Go")})
	gw.seedSSEEvent(t, store.SSEEventBufferRow{TaskID: taskID, SessionID: idgen.New(), EventSequence: 1, EventType: "final_response",
		EventData: `{"jsonrpc":"2.0","result":{"task":{"artifacts":[{"parts":[{"kind":"text","text":"Go is great!"}]}]}}}`})

	got := taskTitleData(t, gw, taskID)
	if got["userMessage"] != "Explain Go" || got["agentResponse"] != "Go is great!" {
		t.Errorf("userMessage %v, agentResponse %v", got["userMessage"], got["agentResponse"])
	}
}

func TestIntegration_TaskTitleDataJoinsStreamedStatusUpdates(t *testing.T) {
	gw := newTestGateway(t)
	taskID := gw.seedTask(t, store.Task{})
	sid := idgen.New()
	for i, chunk := range []string{"full ", "response"} {
		gw.seedSSEEvent(t, store.SSEEventBufferRow{TaskID: taskID, SessionID: sid, EventSequence: i + 1, EventType: "status_update",
			EventData: `{"jsonrpc":"2.0","result":{"status":{"message":{"parts":[{"text":"` + chunk + `"}]}}}}`})
	}

	if got := taskTitleData(t, gw, taskID)["agentResponse"]; got != "full response" {
		t.Errorf("agentResponse = %v, want %q", got, "full response")
	}
}

func TestIntegration_TaskEvents(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	taskID := gw.seedTask(t, store.Task{SessionID: &sid, InitialRequestText: strPtr("What is Go?")})
	eventRepo := store.NewTaskEventRepo(gw.db)
	for _, e := range []store.TaskEvent{
		{Direction: "request", Topic: "req/topic", Payload: `{"method":"message/send","params":{"message":{"metadata":{"agent_name":"my-agent"}}}}`},
		{Direction: "response", Topic: "resp/topic", Payload: `{"jsonrpc":"2.0","result":{"metadata":{"agent_name":"my-agent"},"task":{"metadata":{"agent_name":"my-agent"}}}}`},
		{Direction: "status", Topic: "status/topic", Payload: `{"jsonrpc":"2.0","result":{"status":{"message":{"metadata":{"agent_name":"status-agent"}}}}}`},
		{Direction: "error", Topic: "err/topic", Payload: `{"jsonrpc":"2.0","result":{"metadata":{"agent_name":"error-agent"}}}`},
	} {
		e.ID, e.TaskID, e.CreatedTime = idgen.New(), taskID, time.Now().UnixMilli()
		if err := eventRepo.Create(context.Background(), &e); err != nil {
			t.Fatal(err)
		}
	}

	var entry map[string]any
	for _, e := range decodeDataArray(t, gw.get(t, "/api/v1/tasks/"+taskID+"/events")) {
		if e["taskId"] == taskID {
			entry = e
		}
	}
	if entry == nil {
		t.Fatal("no events entry for the task")
	}
	if entry["sessionId"] != sid || entry["initialRequestText"] != "What is Go?" {
		t.Errorf("events entry sessionId = %v, initialRequestText = %v, want %q and %q", entry["sessionId"], entry["initialRequestText"], sid, "What is Go?")
	}
	if events, _ := entry["events"].([]any); len(events) < 4 {
		t.Errorf("got %d events, want at least 4 (one per direction)", len(events))
	}
}

func TestIntegration_TaskStatus(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	parentID := idgen.New()
	taskID := gw.seedTask(t, store.Task{
		ExecutionMode: "background", SessionID: &sid, ParentTaskID: &parentID, InitialRequestText: strPtr("test query"),
		EndTime: new(time.Now().UnixMilli()), TotalInputTokens: new(100), TotalOutputTokens: new(200),
	})

	resp := gw.get(t, "/api/v1/tasks/"+taskID+"/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	decodeRespJSON(t, resp, &body)
	if _, ok := body["isRunning"]; !ok {
		t.Error("response has no isRunning")
	}
	task, _ := body["task"].(map[string]any)
	if task["parentTaskId"] != parentID || task["initialRequestText"] != "test query" || task["endTime"] == nil || task["totalInputTokens"] == nil {
		t.Errorf("task = %v, want parentTaskId, initialRequestText, endTime and totalInputTokens set", task)
	}
}

func TestIntegration_ActiveBackgroundTasks(t *testing.T) {
	gw := newTestGateway(t)
	gw.seedTask(t, store.Task{Status: strPtr("running"), ExecutionMode: "background"})
	if got := decodeDataArray(t, gw.get(t, "/api/v1/tasks/background/active")); len(got) != 1 {
		t.Errorf("got %d active background tasks, want 1", len(got))
	}
}

func TestIntegration_TaskListSessionFilter(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})
	gw.seedTask(t, store.Task{SessionID: &sid})
	gw.seedTask(t, store.Task{})

	got := decodeDataArray(t, gw.get(t, "/api/v1/tasks?pageNumber=1&pageSize=10&sessionId="+sid))
	if len(got) == 0 {
		t.Fatal("got no tasks, want the session's task")
	}
	for _, task := range got {
		if task["sessionId"] != nil && task["sessionId"] != sid {
			t.Errorf("task from session %v listed, want only session %s", task["sessionId"], sid)
		}
	}
}

func TestIntegration_TaskListDateRange(t *testing.T) {
	gw := newTestGateway(t)
	now := time.Now().UnixMilli()
	gw.seedTask(t, store.Task{StartTime: now - 5000})
	gw.seedTask(t, store.Task{StartTime: now})
	gw.seedTask(t, store.Task{StartTime: now - 60000})

	start := time.UnixMilli(now - 10000).UTC().Format(time.RFC3339)
	end := time.UnixMilli(now + 10000).UTC().Format(time.RFC3339)
	if got := decodeDataArray(t, gw.get(t, "/api/v1/tasks?startDate="+start+"&endDate="+end)); len(got) != 2 {
		t.Errorf("got %d tasks in range, want the 2 started inside it", len(got))
	}
}

func TestIntegration_TaskStimExport(t *testing.T) {
	gw := newTestGateway(t)
	taskID := gw.seedTask(t, store.Task{InitialRequestText: strPtr("Hello agent")})
	if err := store.NewTaskEventRepo(gw.db).Create(context.Background(), &store.TaskEvent{
		ID: idgen.New(), TaskID: taskID, Direction: "request", Topic: "sam/request", CreatedTime: time.Now().UnixMilli(),
		Payload: `{"method":"message/send","params":{"message":{"metadata":{"agent_name":"test-agent"}}}}`,
	}); err != nil {
		t.Fatal(err)
	}

	resp := gw.get(t, "/api/v1/tasks/"+taskID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}
	body := bodyString(t, resp)
	for _, want := range []string{"invocation_details", "invocation_flow", taskID} {
		if !strings.Contains(body, want) {
			t.Errorf("STIM YAML does not contain %q", want)
		}
	}
}

func TestIntegration_ArtifactAndFeedbackRouteStatus(t *testing.T) {
	tests := []struct {
		name, method, path, contentType, body string
		wantStatus                            int
	}{
		{name: "get unknown artifact", method: http.MethodGet, path: "/api/v1/artifacts/{new}/nonexistent.txt", wantStatus: 404},
		{name: "versions of unknown artifact", method: http.MethodGet, path: "/api/v1/artifacts/{new}/nonexistent.txt/versions", wantStatus: 200},
		{name: "delete unknown artifact", method: http.MethodDelete, path: "/api/v1/artifacts/{session}/nonexistent.txt", wantStatus: 204},
		{name: "by URI without uri", method: http.MethodGet, path: "/api/v1/artifacts/byUri", wantStatus: 400},
		{name: "by URI for unknown artifact", method: http.MethodGet, path: "/api/v1/artifacts/byUri?uri=artifact://{new}/test.txt", wantStatus: 400},
		{name: "upload without a file", method: http.MethodPost, path: "/api/v1/artifacts/upload", contentType: mpContentType,
			body: "--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"sessionId\"\r\n\r\n{session}" + mpEnd, wantStatus: 400},
		{name: "submit feedback", method: http.MethodPost, path: "/api/v1/feedback", contentType: jsonType,
			body: `{"taskId":"{task}","sessionId":"{session}","feedbackType":"up","feedbackText":"Great job!"}`, wantStatus: 200},
		{name: "feedback without session and type", method: http.MethodPost, path: "/api/v1/feedback", contentType: jsonType, body: `{"taskId":"{new}"}`, wantStatus: 400},
		{name: "feedback with unknown type", method: http.MethodPost, path: "/api/v1/feedback", contentType: jsonType,
			body: `{"taskId":"{new}","sessionId":"{new}","feedbackType":"invalid"}`, wantStatus: 400},
		{name: "feedback with malformed JSON", method: http.MethodPost, path: "/api/v1/feedback", contentType: jsonType, body: `"not json"`, wantStatus: 400},
		{name: "list feedback", method: http.MethodGet, path: "/api/v1/feedback", wantStatus: 200},
		{name: "list feedback with bad startDate", method: http.MethodGet, path: "/api/v1/feedback?startDate=not-a-date", wantStatus: 400},
		{name: "list feedback with bad endDate", method: http.MethodGet, path: "/api/v1/feedback?endDate=not-a-date", wantStatus: 400},
		{name: "list feedback of another user's task", method: http.MethodGet, path: "/api/v1/feedback?taskId={other}", wantStatus: 404},
		{name: "list feedback of unknown task", method: http.MethodGet, path: "/api/v1/feedback?taskId={new}", wantStatus: 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			sessionID := gw.seedSession(t, store.Session{})
			taskID := gw.seedTask(t, orchestratorTask(sessionID))
			otherID := gw.seedTask(t, store.Task{UserID: "other_user"})

			r := strings.NewReplacer("{session}", sessionID, "{task}", taskID, "{other}", otherID, "{new}", idgen.New())
			resp := gw.sendTyped(t, tc.method, r.Replace(tc.path), tc.contentType, r.Replace(tc.body))
			if body := bodyString(t, resp); resp.StatusCode != tc.wantStatus {
				t.Errorf("%s %s: status %d, want %d; body: %s", tc.method, tc.path, resp.StatusCode, tc.wantStatus, body)
			}
		})
	}
}

func orchestratorTask(sessionID string) store.Task {
	return store.Task{
		AgentName: strPtr("OrchestratorAgent"), HandlerName: strPtr("OrchestratorAgent"),
		HandlerType: strPtr(store.TaskHandlerTypeAgent), SessionID: &sessionID,
	}
}

func (g *testGateway) uploadSessionArtifact(t *testing.T, sessionID, filename, content string) ArtifactUploadResponse {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("uploadFile", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(content))
	_ = mw.WriteField("sessionId", sessionID)
	_ = mw.WriteField("filename", filename)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	resp := g.doMultipart(t, "/api/v1/artifacts/upload", mw.FormDataContentType(), &buf)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: status %d, want 201; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	var uploaded ArtifactUploadResponse
	decodeRespJSON(t, resp, &uploaded)
	return uploaded
}

func TestIntegration_ArtifactLifecycle(t *testing.T) {
	gw := newTestGateway(t)
	sid := gw.seedSession(t, store.Session{})

	uploaded := gw.uploadSessionArtifact(t, sid, "test.txt", "hello world")
	if uploaded.Filename != "test.txt" {
		t.Errorf("uploaded filename = %q, want test.txt", uploaded.Filename)
	}
	if got := decodeDataArray(t, gw.get(t, "/api/v1/artifacts/"+sid)); len(got) != 1 {
		t.Errorf("listed %d artifacts, want 1", len(got))
	}
	for _, path := range []string{"/api/v1/artifacts/" + sid + "/test.txt", "/api/v1/artifacts/byUri?uri=" + uploaded.URI} {
		resp := gw.get(t, path)
		if body := bodyString(t, resp); resp.StatusCode != http.StatusOK || body != "hello world" {
			t.Errorf("GET %s: status %d body %q, want 200 and the uploaded content", path, resp.StatusCode, body)
		}
	}
	var versions PaginatedResponse[int]
	decodeRespJSON(t, gw.get(t, "/api/v1/artifacts/"+sid+"/test.txt/versions"), &versions)
	if len(versions.Data) != 1 {
		t.Errorf("listed %d versions, want 1", len(versions.Data))
	}

	if resp := gw.delete(t, "/api/v1/artifacts/"+sid+"/test.txt"); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: status %d, want 204", resp.StatusCode)
	}
	if resp := gw.get(t, "/api/v1/artifacts/"+sid+"/test.txt"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete: status %d, want 404", resp.StatusCode)
	}
}

func TestIntegration_FeedbackList(t *testing.T) {
	tests := []struct {
		name, query string
		wantCount   int
	}{
		{name: "first page", query: "?pageNumber=1&pageSize=10", wantCount: 2},
		{name: "date range around now", query: "?startDate={start}&endDate={end}", wantCount: 2},
		{name: "filtered by task", query: "?taskId={task}", wantCount: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			sessionID := gw.seedSession(t, store.Session{})
			taskID := gw.seedTask(t, orchestratorTask(sessionID))
			for _, id := range []string{taskID, gw.seedTask(t, orchestratorTask(sessionID))} {
				submit := gw.send(t, http.MethodPost, "/api/v1/feedback",
					`{"taskId":"`+id+`","sessionId":"`+sessionID+`","feedbackType":"down","feedbackText":"not helpful"}`)
				if submit.StatusCode != http.StatusOK {
					t.Fatalf("submit: status %d, want 200; body: %s", submit.StatusCode, bodyString(t, submit))
				}
			}

			now := time.Now().UTC()
			r := strings.NewReplacer("{task}", taskID, "{start}", now.Add(-time.Hour).Format(time.RFC3339), "{end}", now.Add(time.Hour).Format(time.RFC3339))
			if got := decodeDataArray(t, gw.get(t, "/api/v1/feedback"+r.Replace(tc.query))); len(got) != tc.wantCount {
				t.Errorf("listed %d feedback entries, want %d", len(got), tc.wantCount)
			}
		})
	}
}

const (
	groupOwnedByDevUser  = `"userId":"sam_dev_user","createdBy":"sam_dev_user","authorName":null,"isShared":false,"isPinned":false`
	promptOwnedByDevUser = `"userId":"sam_dev_user","createdBy":"sam_dev_user"`
	targetPromptGroup    = `{"name":"Target","initialPrompt":"v1 text","description":"old desc","category":"old-cat","command":"old-cmd"}`
	targetGroupFields    = `{"name":"Target","description":"old desc","category":"old-cat","command":"old-cmd",` + groupOwnedByDevUser
	targetV1Production   = `,"productionPrompt":{"promptText":"v1 text","version":1,"name":"Target","description":"old desc","category":"old-cat","command":"old-cmd",` + promptOwnedByDevUser + `}}`
	unchangedPromptGroup = targetGroupFields + targetV1Production
	freshImportData      = `{"version":"1.0","prompt":{"name":"Imported","promptText":"p","command":"fresh","category":"cat"}}`
)

func createPromptGroup(t *testing.T, gw *testGateway, body string) (groupID, promptID string) {
	t.Helper()
	resp := gw.send(t, http.MethodPost, "/api/v1/prompts/groups", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create prompt group %s: status %d; body: %s", body, resp.StatusCode, bodyString(t, resp))
	}
	var g struct {
		ID                 string `json:"id"`
		ProductionPromptID string `json:"productionPromptId"`
	}
	decodeRespJSON(t, resp, &g)
	return g.ID, g.ProductionPromptID
}

func createPromptVersion(t *testing.T, gw *testGateway, groupID, text string) string {
	t.Helper()
	resp := gw.send(t, http.MethodPost, "/api/v1/prompts/groups/"+groupID+"/prompts", `{"promptText":"`+text+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create prompt version: status %d; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	var p struct {
		ID string `json:"id"`
	}
	decodeRespJSON(t, resp, &p)
	return p.ID
}

func normalizePrompt(t *testing.T, p map[string]any, groupID string) {
	t.Helper()
	if id, _ := p["id"].(string); id == "" {
		t.Errorf("prompt id = %v, want a non-empty string", p["id"])
	}
	if p["groupId"] != groupID {
		t.Errorf("prompt groupId = %v, want %q", p["groupId"], groupID)
	}
	for _, k := range []string{"createdAt", "updatedAt"} {
		if s, _ := p[k].(string); s == "" {
			t.Errorf("prompt %s = %v, want a timestamp", k, p[k])
		}
	}
	for _, k := range []string{"id", "groupId", "createdAt", "updatedAt"} {
		delete(p, k)
	}
}

func decodePromptGroup(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var g map[string]any
	decodeRespJSON(t, resp, &g)
	id, _ := g["id"].(string)
	if id == "" {
		t.Errorf("group id = %v, want a non-empty string", g["id"])
	}
	for _, k := range []string{"createdAt", "updatedAt"} {
		if s, _ := g[k].(string); s == "" {
			t.Errorf("group %s = %v, want a timestamp", k, g[k])
		}
	}
	if prod, ok := g["productionPrompt"].(map[string]any); ok {
		if g["productionPromptId"] != prod["id"] {
			t.Errorf("productionPromptId = %v, want the production prompt's id %v", g["productionPromptId"], prod["id"])
		}
		normalizePrompt(t, prod, id)
	}
	for _, k := range []string{"id", "createdAt", "updatedAt", "productionPromptId"} {
		delete(g, k)
	}
	return g
}

func getPromptGroup(t *testing.T, gw *testGateway, id string) map[string]any {
	t.Helper()
	resp := gw.get(t, "/api/v1/prompts/groups/"+id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get prompt group: status %d; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	return decodePromptGroup(t, resp)
}

func listedPromptGroupNames(t *testing.T, gw *testGateway) []string {
	t.Helper()
	var list struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	decodeRespJSON(t, gw.get(t, "/api/v1/prompts/groups/all"), &list)
	names := make([]string, 0, len(list.Data))
	for _, g := range list.Data {
		names = append(names, g.Name)
	}
	slices.Sort(names)
	return names
}

func listedPromptVersions(t *testing.T, gw *testGateway, groupID string) []string {
	t.Helper()
	resp := gw.get(t, "/api/v1/prompts/groups/"+groupID+"/prompts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list versions of %s: status %d; body: %s", groupID, resp.StatusCode, bodyString(t, resp))
	}
	var list struct {
		Data []struct {
			Version    int    `json:"version"`
			PromptText string `json:"promptText"`
		} `json:"data"`
	}
	decodeRespJSON(t, resp, &list)
	versions := make([]string, 0, len(list.Data))
	for _, p := range list.Data {
		versions = append(versions, fmt.Sprintf("v%d:%s", p.Version, p.PromptText))
	}
	slices.Sort(versions)
	return versions
}

func storedPromptVersions(t *testing.T, gw *testGateway, groupID string) []string {
	t.Helper()
	rows, err := gw.db.QueryContext(t.Context(), `SELECT version, prompt_text FROM prompts WHERE group_id = ? ORDER BY version`, groupID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	versions := []string{}
	for rows.Next() {
		var v int
		var text string
		if err := rows.Scan(&v, &text); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, fmt.Sprintf("v%d:%s", v, text))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return versions
}

func TestIntegration_PromptGroupCreateAccepted(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "every field is stored and the initial prompt is production version 1",
			body: `{"name":"My Prompt","initialPrompt":"You are helpful.","description":"d","category":"general","command":"mycmd"}`,
			want: `{"name":"My Prompt","description":"d","category":"general","command":"mycmd",` + groupOwnedByDevUser +
				`,"productionPrompt":{"promptText":"You are helpful.","version":1,"name":"My Prompt","description":"d","category":"general","command":"mycmd",` + promptOwnedByDevUser + `}}`,
		},
		{
			name: "omitted optional fields are null",
			body: `{"name":"Bare","initialPrompt":"p"}`,
			want: `{"name":"Bare","description":null,"category":null,"command":null,` + groupOwnedByDevUser +
				`,"productionPrompt":{"promptText":"p","version":1,"name":"Bare","description":null,"category":null,"command":null,` + promptOwnedByDevUser + `}}`,
		},
		{
			name: "name of 255 bytes and initial prompt of 10000 bytes fit",
			body: `{"name":"` + strings.Repeat("n", 255) + `","initialPrompt":"` + strings.Repeat("p", 10000) + `"}`,
			want: `{"name":"` + strings.Repeat("n", 255) + `","description":null,"category":null,"command":null,` + groupOwnedByDevUser +
				`,"productionPrompt":{"promptText":"` + strings.Repeat("p", 10000) + `","version":1,"name":"` + strings.Repeat("n", 255) + `","description":null,"category":null,"command":null,` + promptOwnedByDevUser + `}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			resp := gw.send(t, http.MethodPost, "/api/v1/prompts/groups", tc.body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status %d, want 201; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			body := bodyString(t, resp)
			var ref struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(body), &ref); err != nil {
				t.Fatal(err)
			}
			got := decodePromptGroup(t, &http.Response{Body: io.NopCloser(strings.NewReader(body))})
			if diff := cmp.Diff(decodeJSONText(t, tc.want), any(got)); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(got, getPromptGroup(t, gw, ref.ID)); diff != "" {
				t.Errorf("stored group differs from the response (-response +stored):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptGroupCreateRejected(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{name: "empty name", body: `{"name":"","initialPrompt":"p"}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "name of 256 bytes", body: `{"name":"` + strings.Repeat("n", 256) + `","initialPrompt":"p"}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "empty initial prompt", body: `{"name":"New","initialPrompt":""}`, wantStatus: 400, wantMessage: "initialPrompt is required (1-10000 characters)"},
		{name: "initial prompt of 10001 bytes", body: `{"name":"New","initialPrompt":"` + strings.Repeat("p", 10001) + `"}`, wantStatus: 400, wantMessage: "initialPrompt is required (1-10000 characters)"},
		{name: "command another group uses", body: `{"name":"New","initialPrompt":"p","command":"taken"}`, wantStatus: 409, wantMessage: "command already exists for this user"},
		{name: "malformed JSON", body: `"not json"`, wantStatus: 400, wantMessage: "invalid request body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			createPromptGroup(t, gw, `{"name":"Existing","initialPrompt":"p","command":"taken"}`)
			resp := gw.send(t, http.MethodPost, "/api/v1/prompts/groups", tc.body)
			assertErrorMessage(t, resp, tc.wantStatus, tc.wantMessage)
			if diff := cmp.Diff([]string{"Existing"}, listedPromptGroupNames(t, gw)); diff != "" {
				t.Errorf("stored groups (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptGroupUpdateAccepted(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStored string
	}{
		{
			name: "name, description, category and command are replaced",
			body: `{"name":"Renamed","description":"new desc","category":"new-cat","command":"new-cmd"}`,
			wantStored: `{"name":"Renamed","description":"new desc","category":"new-cat","command":"new-cmd",` + groupOwnedByDevUser +
				targetV1Production,
		},
		{
			name:       "omitted fields are kept",
			body:       `{}`,
			wantStored: unchangedPromptGroup,
		},
		{
			name:       "keeping its own command is not a conflict",
			body:       `{"command":"old-cmd"}`,
			wantStored: unchangedPromptGroup,
		},
		{
			name: "initial prompt adds the next version as production",
			body: `{"initialPrompt":"v2 text"}`,
			wantStored: targetGroupFields +
				`,"productionPrompt":{"promptText":"v2 text","version":2,"name":"Target","description":"old desc","category":"old-cat","command":"old-cmd",` + promptOwnedByDevUser + `}}`,
		},
		{
			name:       "empty initial prompt adds no version",
			body:       `{"initialPrompt":""}`,
			wantStored: unchangedPromptGroup,
		},
		{
			name: "description and initial prompt together: the new version carries the new description",
			body: `{"description":"new description","initialPrompt":"new production text"}`,
			wantStored: `{"name":"Target","description":"new description","category":"old-cat","command":"old-cmd",` + groupOwnedByDevUser +
				`,"productionPrompt":{"promptText":"new production text","version":2,"name":"Target","description":"new description","category":"old-cat","command":"old-cmd",` + promptOwnedByDevUser + `}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			createPromptGroup(t, gw, `{"name":"Other","initialPrompt":"p","command":"taken"}`)
			id, _ := createPromptGroup(t, gw, targetPromptGroup)
			resp := gw.send(t, http.MethodPatch, "/api/v1/prompts/groups/"+id, tc.body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			got := decodePromptGroup(t, resp)
			stored := getPromptGroup(t, gw, id)
			if diff := cmp.Diff(decodeJSONText(t, tc.wantStored), any(stored)); diff != "" {
				t.Errorf("stored group (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(stored, got); diff != "" {
				t.Errorf("response differs from the stored group (-stored +response):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptGroupUpdateRejected(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{name: "empty name", body: `{"name":""}`, wantStatus: 400, wantMessage: "name must be 1-255 characters"},
		{name: "name of 256 bytes", body: `{"name":"` + strings.Repeat("n", 256) + `"}`, wantStatus: 400, wantMessage: "name must be 1-255 characters"},
		{name: "command another group uses", body: `{"command":"taken"}`, wantStatus: 409, wantMessage: "command already exists for this user"},
		{name: "malformed JSON", body: `"not json"`, wantStatus: 400, wantMessage: "invalid request body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			createPromptGroup(t, gw, `{"name":"Other","initialPrompt":"p","command":"taken"}`)
			id, _ := createPromptGroup(t, gw, targetPromptGroup)
			resp := gw.send(t, http.MethodPatch, "/api/v1/prompts/groups/"+id, tc.body)
			assertErrorMessage(t, resp, tc.wantStatus, tc.wantMessage)
			if diff := cmp.Diff(decodeJSONText(t, unchangedPromptGroup), any(getPromptGroup(t, gw, id))); diff != "" {
				t.Errorf("stored group (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptGroupPinToggles(t *testing.T) {
	gw := newTestGateway(t)
	id, _ := createPromptGroup(t, gw, `{"name":"Pin","initialPrompt":"p"}`)
	for _, want := range []bool{true, false} {
		resp := gw.send(t, http.MethodPatch, "/api/v1/prompts/groups/"+id+"/pin", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("pin: status %d; body: %s", resp.StatusCode, bodyString(t, resp))
		}
		got := decodePromptGroup(t, resp)
		if got["isPinned"] != want {
			t.Errorf("pin response isPinned = %v, want %v", got["isPinned"], want)
		}
		if diff := cmp.Diff(got, getPromptGroup(t, gw, id)); diff != "" {
			t.Errorf("stored group differs from the pin response (-response +stored):\n%s", diff)
		}
	}
}

func TestIntegration_PromptUnknownIDIsNotFound(t *testing.T) {
	tests := []struct {
		name, method, path, body, wantMessage string
	}{
		{name: "get group", method: http.MethodGet, path: "/api/v1/prompts/groups/{id}", wantMessage: "prompt group not found"},
		{name: "update group", method: http.MethodPatch, path: "/api/v1/prompts/groups/{id}", body: `{"name":"x"}`, wantMessage: "prompt group not found"},
		{name: "delete group", method: http.MethodDelete, path: "/api/v1/prompts/groups/{id}", wantMessage: "prompt group not found"},
		{name: "pin group", method: http.MethodPatch, path: "/api/v1/prompts/groups/{id}/pin", wantMessage: "prompt group not found"},
		{name: "export group", method: http.MethodGet, path: "/api/v1/prompts/groups/{id}/export", wantMessage: "prompt group not found"},
		{name: "list versions", method: http.MethodGet, path: "/api/v1/prompts/groups/{id}/prompts", wantMessage: "prompt group not found"},
		{name: "create version", method: http.MethodPost, path: "/api/v1/prompts/groups/{id}/prompts", body: `{"promptText":"orphan"}`, wantMessage: "prompt group not found"},
		{name: "update prompt", method: http.MethodPatch, path: "/api/v1/prompts/{id}", body: `{"promptText":"x"}`, wantMessage: "prompt not found"},
		{name: "make production", method: http.MethodPatch, path: "/api/v1/prompts/{id}/makeProduction", wantMessage: "prompt not found"},
		{name: "delete prompt", method: http.MethodDelete, path: "/api/v1/prompts/{id}", wantMessage: "prompt not found"},
		{name: "unknown prompt action", method: http.MethodPatch, path: "/api/v1/prompts/{id}/unknown-action", wantMessage: "not found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			resp := gw.send(t, tc.method, strings.ReplaceAll(tc.path, "{id}", idgen.New()), tc.body)
			assertErrorMessage(t, resp, http.StatusNotFound, tc.wantMessage)
		})
	}
}

func TestIntegration_PromptVersionCreate(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantText string
	}{
		{name: "version 2 copies the group fields", text: "v2 text", wantText: "v2 text"},
		{name: "text of 10000 bytes fits", text: strings.Repeat("t", 10000), wantText: strings.Repeat("t", 10000)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			id, _ := createPromptGroup(t, gw, targetPromptGroup)
			resp := gw.send(t, http.MethodPost, "/api/v1/prompts/groups/"+id+"/prompts", `{"promptText":"`+tc.text+`"}`)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status %d, want 201; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var got map[string]any
			decodeRespJSON(t, resp, &got)
			normalizePrompt(t, got, id)
			want := `{"promptText":"` + tc.wantText + `","version":2,"name":"Target","description":"old desc","category":"old-cat","command":"old-cmd",` + promptOwnedByDevUser + `}`
			if diff := cmp.Diff(decodeJSONText(t, want), any(got)); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{"v1:v1 text", "v2:" + tc.wantText}, storedPromptVersions(t, gw, id)); diff != "" {
				t.Errorf("stored versions (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{"v1:v1 text", "v2:" + tc.wantText}, listedPromptVersions(t, gw, id)); diff != "" {
				t.Errorf("listed versions (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(decodeJSONText(t, unchangedPromptGroup), any(getPromptGroup(t, gw, id))); diff != "" {
				t.Errorf("group after adding a version (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptVersionUpdate(t *testing.T) {
	gw := newTestGateway(t)
	id, v1 := createPromptGroup(t, gw, targetPromptGroup)
	resp := gw.send(t, http.MethodPatch, "/api/v1/prompts/"+v1, `{"promptText":"edited"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	var got map[string]any
	decodeRespJSON(t, resp, &got)
	normalizePrompt(t, got, id)
	want := `{"promptText":"edited","version":1,"name":"Target","description":"old desc","category":"old-cat","command":"old-cmd",` + promptOwnedByDevUser + `}`
	if diff := cmp.Diff(decodeJSONText(t, want), any(got)); diff != "" {
		t.Errorf("response (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"v1:edited"}, storedPromptVersions(t, gw, id)); diff != "" {
		t.Errorf("stored versions (-want +got):\n%s", diff)
	}
}

func TestIntegration_PromptTextRejected(t *testing.T) {
	tests := []struct {
		name, method, path, body, wantMessage string
	}{
		{name: "new version with empty text", method: http.MethodPost, path: "/api/v1/prompts/groups/{group}/prompts", body: `{"promptText":""}`, wantMessage: "promptText is required (1-10000 characters)"},
		{name: "new version with 10001 bytes", method: http.MethodPost, path: "/api/v1/prompts/groups/{group}/prompts", body: `{"promptText":"` + strings.Repeat("t", 10001) + `"}`, wantMessage: "promptText is required (1-10000 characters)"},
		{name: "new version with malformed JSON", method: http.MethodPost, path: "/api/v1/prompts/groups/{group}/prompts", body: `"not json"`, wantMessage: "invalid request body"},
		{name: "edit with empty text", method: http.MethodPatch, path: "/api/v1/prompts/{prompt}", body: `{"promptText":""}`, wantMessage: "promptText is required (1-10000 characters)"},
		{name: "edit with 10001 bytes", method: http.MethodPatch, path: "/api/v1/prompts/{prompt}", body: `{"promptText":"` + strings.Repeat("t", 10001) + `"}`, wantMessage: "promptText is required (1-10000 characters)"},
		{name: "edit with malformed JSON", method: http.MethodPatch, path: "/api/v1/prompts/{prompt}", body: `"not json"`, wantMessage: "invalid request body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			id, v1 := createPromptGroup(t, gw, targetPromptGroup)
			path := strings.NewReplacer("{group}", id, "{prompt}", v1).Replace(tc.path)
			assertErrorMessage(t, gw.send(t, tc.method, path, tc.body), http.StatusBadRequest, tc.wantMessage)
			if diff := cmp.Diff([]string{"v1:v1 text"}, storedPromptVersions(t, gw, id)); diff != "" {
				t.Errorf("stored versions (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptMakeProduction(t *testing.T) {
	gw := newTestGateway(t)
	id, _ := createPromptGroup(t, gw, targetPromptGroup)
	v2 := createPromptVersion(t, gw, id, "v2 text")

	resp := gw.send(t, http.MethodPatch, "/api/v1/prompts/"+v2+"/makeProduction", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	var got map[string]any
	decodeRespJSON(t, resp, &got)
	if got["id"] != v2 {
		t.Errorf("response prompt id = %v, want %q", got["id"], v2)
	}
	prod, _ := getPromptGroup(t, gw, id)["productionPrompt"].(map[string]any)
	if prod["version"] != float64(2) || prod["promptText"] != "v2 text" {
		t.Errorf("production prompt = %v, want version 2 with v2 text", prod)
	}
}

func TestIntegration_PromptDelete(t *testing.T) {
	tests := []struct {
		name            string
		extraVersions   []string
		makeProduction  int
		deleteVersion   int
		wantGroupStatus int
		wantProduction  any
		wantStored      []string
	}{
		{name: "deleting the only version deletes the group", makeProduction: 1, deleteVersion: 1, wantGroupStatus: http.StatusNotFound, wantProduction: nil, wantStored: []string{}},
		{name: "deleting a non-production version keeps production", extraVersions: []string{"v2 text"}, makeProduction: 1, deleteVersion: 2, wantGroupStatus: http.StatusOK, wantProduction: "v1 text", wantStored: []string{"v1:v1 text"}},
		{name: "deleting the production version promotes the other", extraVersions: []string{"v2 text"}, makeProduction: 2, deleteVersion: 2, wantGroupStatus: http.StatusOK, wantProduction: "v1 text", wantStored: []string{"v1:v1 text"}},
		{name: "deleting the original production version promotes the newer", extraVersions: []string{"v2 text"}, makeProduction: 1, deleteVersion: 1, wantGroupStatus: http.StatusOK, wantProduction: "v2 text", wantStored: []string{"v2:v2 text"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			id, v1 := createPromptGroup(t, gw, targetPromptGroup)
			versionIDs := []string{v1}
			for _, text := range tc.extraVersions {
				versionIDs = append(versionIDs, createPromptVersion(t, gw, id, text))
			}
			if r := gw.send(t, http.MethodPatch, "/api/v1/prompts/"+versionIDs[tc.makeProduction-1]+"/makeProduction", ""); r.StatusCode != http.StatusOK {
				t.Fatalf("make production: status %d; body: %s", r.StatusCode, bodyString(t, r))
			}

			resp := gw.delete(t, "/api/v1/prompts/"+versionIDs[tc.deleteVersion-1])
			if body := bodyString(t, resp); resp.StatusCode != http.StatusNoContent || body != "" {
				t.Fatalf("delete: status %d body %q, want 204 and no body", resp.StatusCode, body)
			}

			groupResp := gw.get(t, "/api/v1/prompts/groups/"+id)
			var group struct {
				ProductionPrompt *struct {
					PromptText string `json:"promptText"`
				} `json:"productionPrompt"`
			}
			decodeRespJSON(t, groupResp, &group)
			var production any
			if group.ProductionPrompt != nil {
				production = group.ProductionPrompt.PromptText
			}
			if groupResp.StatusCode != tc.wantGroupStatus || production != tc.wantProduction {
				t.Errorf("GET group after delete = status %d with production %v, want status %d with production %v", groupResp.StatusCode, production, tc.wantGroupStatus, tc.wantProduction)
			}
			if diff := cmp.Diff(tc.wantStored, storedPromptVersions(t, gw, id)); diff != "" {
				t.Errorf("stored versions (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptDeleteNonProductionKeepsProductionWhenANewerVersionExists(t *testing.T) {
	gw := newTestGateway(t)
	id, _ := createPromptGroup(t, gw, targetPromptGroup)
	v2 := createPromptVersion(t, gw, id, "v2 text")
	newest := &store.Prompt{ID: idgen.New(), GroupID: id, PromptText: "v3 text", UserID: "sam_dev_user", Version: 3, CreatedAt: time.Now().Add(time.Hour).UnixMilli()}
	if err := gw.promptRepo.Create(t.Context(), newest); err != nil {
		t.Fatal(err)
	}

	resp := gw.delete(t, "/api/v1/prompts/"+v2)
	if body := bodyString(t, resp); resp.StatusCode != http.StatusNoContent || body != "" {
		t.Fatalf("delete: status %d body %q, want 204 and no body", resp.StatusCode, body)
	}
	prod, _ := getPromptGroup(t, gw, id)["productionPrompt"].(map[string]any)
	if prod["promptText"] != "v1 text" {
		t.Errorf("production prompt = %v, want v1 text kept", prod["promptText"])
	}
	if diff := cmp.Diff([]string{"v1:v1 text", "v3:v3 text"}, storedPromptVersions(t, gw, id)); diff != "" {
		t.Errorf("stored versions (-want +got):\n%s", diff)
	}
}

func TestIntegration_PromptGroupDelete(t *testing.T) {
	gw := newTestGateway(t)
	id, _ := createPromptGroup(t, gw, targetPromptGroup)
	createPromptVersion(t, gw, id, "v2 text")

	resp := gw.delete(t, "/api/v1/prompts/groups/"+id)
	if body := bodyString(t, resp); resp.StatusCode != http.StatusNoContent || body != "" {
		t.Fatalf("delete: status %d body %q, want 204 and no body", resp.StatusCode, body)
	}
	assertErrorMessage(t, gw.get(t, "/api/v1/prompts/groups/"+id), http.StatusNotFound, "prompt group not found")
	if diff := cmp.Diff([]string{}, storedPromptVersions(t, gw, id)); diff != "" {
		t.Errorf("stored versions after group delete (-want +got):\n%s", diff)
	}
}

func TestIntegration_PromptGroupList(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantNames []string
		wantCount int
	}{
		{name: "paged list returns every group", path: "/api/v1/prompts/groups", wantNames: []string{"Alpha:alpha text", "Beta:beta text"}, wantCount: 2},
		{name: "unpaged list returns every group", path: "/api/v1/prompts/groups/all", wantNames: []string{"Alpha:alpha text", "Beta:beta text"}, wantCount: 2},
		{name: "category keeps matching groups", path: "/api/v1/prompts/groups?category=work", wantNames: []string{"Alpha:alpha text"}, wantCount: 1},
		{name: "search matches name or description ignoring case", path: "/api/v1/prompts/groups?search=ALPHA", wantNames: []string{"Alpha:alpha text", "Beta:beta text"}, wantCount: 2},
		{name: "search without a match lists none", path: "/api/v1/prompts/groups?search=zzz", wantNames: []string{}, wantCount: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			createPromptGroup(t, gw, `{"name":"Alpha","initialPrompt":"alpha text","category":"work"}`)
			createPromptGroup(t, gw, `{"name":"Beta","initialPrompt":"beta text","category":"home","description":"mentions alpha"}`)

			resp := gw.get(t, tc.path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var list struct {
				Data []struct {
					Name             string `json:"name"`
					ProductionPrompt *struct {
						PromptText string `json:"promptText"`
					} `json:"productionPrompt"`
				} `json:"data"`
				Meta struct {
					Pagination struct {
						Count int `json:"count"`
					} `json:"pagination"`
				} `json:"meta"`
			}
			decodeRespJSON(t, resp, &list)
			if list.Data == nil {
				t.Fatal("data is null, want an array")
			}
			if list.Meta.Pagination.Count != tc.wantCount {
				t.Errorf("GET %s meta.pagination.count = %d, want %d", tc.path, list.Meta.Pagination.Count, tc.wantCount)
			}
			got := make([]string, 0, len(list.Data))
			for _, g := range list.Data {
				text := "<no production prompt>"
				if g.ProductionPrompt != nil {
					text = g.ProductionPrompt.PromptText
				}
				got = append(got, g.Name+":"+text)
			}
			slices.Sort(got)
			if diff := cmp.Diff(tc.wantNames, got); diff != "" {
				t.Errorf("listed groups (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptGroupExportImportRoundTrip(t *testing.T) {
	gw := newTestGateway(t)
	id, _ := createPromptGroup(t, gw, targetPromptGroup)

	resp := gw.get(t, "/api/v1/prompts/groups/"+id+"/export")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: status %d; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	exported := bodyString(t, resp)
	var export map[string]any
	if err := json.Unmarshal([]byte(exported), &export); err != nil {
		t.Fatal(err)
	}
	if s, _ := export["exportedAt"].(string); s == "" {
		t.Errorf("exportedAt = %v, want a timestamp", export["exportedAt"])
	}
	prompt, _ := export["prompt"].(map[string]any)
	metadata, _ := prompt["metadata"].(map[string]any)
	if _, ok := metadata["originalCreatedAt"].(float64); !ok {
		t.Errorf("metadata.originalCreatedAt = %v, want epoch milliseconds", metadata["originalCreatedAt"])
	}
	delete(export, "exportedAt")
	delete(metadata, "originalCreatedAt")
	wantExport := `{"version":"1.0","prompt":{"name":"Target","description":"old desc","category":"old-cat","command":"old-cmd","promptText":"v1 text","metadata":{"originalVersion":1}}}`
	if diff := cmp.Diff(decodeJSONText(t, wantExport), any(export)); diff != "" {
		t.Errorf("export (-want +got):\n%s", diff)
	}

	importResp := gw.send(t, http.MethodPost, "/api/v1/prompts/import", `{"promptData":`+exported+`,"options":{}}`)
	if importResp.StatusCode != http.StatusOK {
		t.Fatalf("import: status %d; body: %s", importResp.StatusCode, bodyString(t, importResp))
	}
	var imported map[string]any
	decodeRespJSON(t, importResp, &imported)
	copyID, _ := imported["promptGroupId"].(string)
	delete(imported, "promptGroupId")
	if diff := cmp.Diff(decodeJSONText(t, `{"success":true,"warnings":["command renamed to 'old-cmd-2' to avoid conflict"]}`), any(imported)); diff != "" {
		t.Errorf("import response (-want +got):\n%s", diff)
	}
	wantCopy := `{"name":"Target","description":"old desc","category":"old-cat","command":"old-cmd-2",` + groupOwnedByDevUser +
		`,"productionPrompt":{"promptText":"v1 text","version":1,"name":"Target","description":"old desc","category":"old-cat","command":"old-cmd-2",` + promptOwnedByDevUser + `}}`
	if diff := cmp.Diff(decodeJSONText(t, wantCopy), any(getPromptGroup(t, gw, copyID))); diff != "" {
		t.Errorf("imported group (-want +got):\n%s", diff)
	}
}

func TestIntegration_PromptGroupExportWithoutProductionPrompt(t *testing.T) {
	gw := newTestGateway(t)
	ctx := context.Background()
	group := &store.PromptGroup{ID: idgen.New(), UserID: "sam_dev_user", Name: "No Prod Group"}
	if err := gw.groupRepo.Create(ctx, group); err != nil {
		t.Fatal(err)
	}
	if err := gw.promptRepo.Create(ctx, &store.Prompt{ID: idgen.New(), GroupID: group.ID, PromptText: "text", UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	assertErrorMessage(t, gw.get(t, "/api/v1/prompts/groups/"+group.ID+"/export"), http.StatusNotFound, "no production prompt set")
}

func TestIntegration_PromptImportAccepted(t *testing.T) {
	tests := []struct {
		name         string
		promptData   string
		options      string
		wantWarnings string
		wantGroup    string
	}{
		{
			name:         "export with an RFC3339 timestamp",
			promptData:   `{"version":"1.0","exportedAt":"2026-01-01T00:00:00Z","prompt":{"name":"Imported","description":"from export","promptText":"You are imported."}}`,
			options:      `{}`,
			wantWarnings: `[]`,
			wantGroup: `{"name":"Imported","description":"from export","category":null,"command":null,` + groupOwnedByDevUser +
				`,"productionPrompt":{"promptText":"You are imported.","version":1,"name":"Imported","description":"from export","category":null,"command":null,` + promptOwnedByDevUser + `}}`,
		},
		{
			name:         "python export with epoch-ms timestamps keeps the author",
			promptData:   `{"version":"1.0","exportedAt":1783111183670,"prompt":{"name":"From Python","promptText":"p","metadata":{"originalVersion":3,"originalCreatedAt":1783111183502,"authorName":"Ada Lovelace"}}}`,
			options:      `{}`,
			wantWarnings: `[]`,
			wantGroup: `{"name":"From Python","description":null,"category":null,"command":null,"authorName":"Ada Lovelace","userId":"sam_dev_user","createdBy":"sam_dev_user","isShared":false,"isPinned":false` +
				`,"productionPrompt":{"promptText":"p","version":1,"name":"From Python","description":null,"category":null,"command":null,` + promptOwnedByDevUser + `}}`,
		},
		{
			name:         "taken command gets a numeric suffix",
			promptData:   `{"version":"1.0","prompt":{"name":"Imported","promptText":"p","command":"taken","category":"cat"}}`,
			options:      `{}`,
			wantWarnings: `["command renamed to 'taken-2' to avoid conflict"]`,
			wantGroup: `{"name":"Imported","description":null,"category":"cat","command":"taken-2",` + groupOwnedByDevUser +
				`,"productionPrompt":{"promptText":"p","version":1,"name":"Imported","description":null,"category":"cat","command":"taken-2",` + promptOwnedByDevUser + `}}`,
		},
		{
			name:         "preserveCommand false drops the command",
			promptData:   freshImportData,
			options:      `{"preserveCommand":false}`,
			wantWarnings: `[]`,
			wantGroup: `{"name":"Imported","description":null,"category":"cat","command":null,` + groupOwnedByDevUser +
				`,"productionPrompt":{"promptText":"p","version":1,"name":"Imported","description":null,"category":"cat","command":null,` + promptOwnedByDevUser + `}}`,
		},
		{
			name:         "preserveCategory false drops the category",
			promptData:   freshImportData,
			options:      `{"preserveCategory":false}`,
			wantWarnings: `[]`,
			wantGroup: `{"name":"Imported","description":null,"category":null,"command":"fresh",` + groupOwnedByDevUser +
				`,"productionPrompt":{"promptText":"p","version":1,"name":"Imported","description":null,"category":null,"command":"fresh",` + promptOwnedByDevUser + `}}`,
		},
		{
			name:         "over-long name and text are truncated with warnings",
			promptData:   `{"version":"1.0","prompt":{"name":"` + strings.Repeat("n", 256) + `","promptText":"` + strings.Repeat("p", 10001) + `"}}`,
			options:      `{}`,
			wantWarnings: `["name was truncated to 255 characters","promptText was truncated to 10000 characters"]`,
			wantGroup: `{"name":"` + strings.Repeat("n", 255) + `","description":null,"category":null,"command":null,` + groupOwnedByDevUser +
				`,"productionPrompt":{"promptText":"` + strings.Repeat("p", 10000) + `","version":1,"name":"` + strings.Repeat("n", 255) + `","description":null,"category":null,"command":null,` + promptOwnedByDevUser + `}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			createPromptGroup(t, gw, `{"name":"Existing","initialPrompt":"p","command":"taken"}`)
			resp := gw.send(t, http.MethodPost, "/api/v1/prompts/import", `{"promptData":`+tc.promptData+`,"options":`+tc.options+`}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var got map[string]any
			decodeRespJSON(t, resp, &got)
			id, _ := got["promptGroupId"].(string)
			delete(got, "promptGroupId")
			if diff := cmp.Diff(decodeJSONText(t, `{"success":true,"warnings":`+tc.wantWarnings+`}`), any(got)); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(decodeJSONText(t, tc.wantGroup), any(getPromptGroup(t, gw, id))); diff != "" {
				t.Errorf("imported group (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptImportRejected(t *testing.T) {
	tests := []struct {
		name, body, wantMessage string
	}{
		{name: "unsupported version", body: `{"promptData":{"version":"2.0","prompt":{"name":"X","promptText":"Y"}}}`, wantMessage: "unsupported import version: 2.0"},
		{name: "malformed JSON", body: `"not json"`, wantMessage: "invalid request body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			assertErrorMessage(t, gw.send(t, http.MethodPost, "/api/v1/prompts/import", tc.body), http.StatusBadRequest, tc.wantMessage)
			if diff := cmp.Diff([]string{}, listedPromptGroupNames(t, gw)); diff != "" {
				t.Errorf("stored groups (-want +got):\n%s", diff)
			}
		})
	}
}

const (
	projectDescriptionLimit  = 10
	projectInstructionsLimit = 12

	ownedByDevUser = `"userId":"sam_dev_user","createdBy":"sam_dev_user","isPinned":false`

	mpBoundary     = "TESTBOUNDARY"
	mpContentType  = "multipart/form-data; boundary=" + mpBoundary
	mpNameField    = "--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\n"
	mpDescField    = "\r\n--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"description\"\r\n\r\n"
	mpInstrField   = "\r\n--" + mpBoundary + "\r\nContent-Disposition: form-data; name=\"systemPrompt\"\r\n\r\n"
	mpEnd          = "\r\n--" + mpBoundary + "--\r\n"
	jsonType       = "application/json"
	existingName   = "Existing Project"
	targetProject  = `{"name":"Target","description":"old desc","systemPrompt":"old instr","defaultAgentId":"old-agent"}`
	unchangedStore = `{"name":"Target","description":"old desc","systemPrompt":"old instr","defaultAgentId":"old-agent","updatedAt":null,` + ownedByDevUser + `}`
)

func newProjectTestGateway(t *testing.T) *testGateway {
	t.Helper()
	gw := newTestGatewayWithConfig(t, map[string]any{
		"gateway_max_project_description_chars":  projectDescriptionLimit,
		"gateway_max_project_instructions_chars": projectInstructionsLimit,
	})
	createProject(t, gw, `{"name":"`+existingName+`"}`)
	return gw
}

func createProject(t *testing.T, gw *testGateway, body string) string {
	t.Helper()
	resp := gw.send(t, http.MethodPost, "/api/v1/projects", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project %s: status %d; body: %s", body, resp.StatusCode, bodyString(t, resp))
	}
	var created struct {
		ID string `json:"id"`
	}
	decodeRespJSON(t, resp, &created)
	return created.ID
}

func (g *testGateway) sendTyped(t *testing.T, method, path, contentType, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, g.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := g.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeProject(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var p map[string]any
	decodeRespJSON(t, resp, &p)
	if id, _ := p["id"].(string); id == "" {
		t.Errorf("id = %v, want a non-empty string", p["id"])
	}
	for _, k := range []string{"createdAt", "updatedAt"} {
		if s, ok := p[k].(string); ok {
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				t.Errorf("%s = %q: %v", k, s, err)
			}
			delete(p, k)
		}
	}
	delete(p, "id")
	delete(p, "artifactCount")
	return p
}

func getProject(t *testing.T, gw *testGateway, id string) map[string]any {
	t.Helper()
	resp := gw.get(t, "/api/v1/projects/"+id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get project: status %d; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	return decodeProject(t, resp)
}

func listedProjectNames(t *testing.T, gw *testGateway) []string {
	t.Helper()
	var list struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	resp := gw.get(t, "/api/v1/projects")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list projects: status %d; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	decodeRespJSON(t, resp, &list)
	names := make([]string, 0, len(list.Data))
	for _, p := range list.Data {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	return names
}

func assertErrorMessage(t *testing.T, resp *http.Response, wantStatus int, wantMessage string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status %d, want %d; body: %s", resp.StatusCode, wantStatus, bodyString(t, resp))
	}
	assertErrorBody(t, resp, wantMessage)
}

func TestIntegration_ProjectCreateAccepted(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{
			name:        "every field is stored",
			contentType: jsonType,
			body:        `{"name":"New","description":"d","systemPrompt":"s","defaultAgentId":"agent-x"}`,
			want:        `{"name":"New","description":"d","systemPrompt":"s","defaultAgentId":"agent-x","updatedAt":null,` + ownedByDevUser + `}`,
		},
		{
			name:        "surrounding whitespace is trimmed from the name",
			contentType: jsonType,
			body:        `{"name":"  Padded  "}`,
			want:        `{"name":"Padded","description":null,"systemPrompt":null,"defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`,
		},
		{
			name:        "name of 255 multibyte characters fits",
			contentType: jsonType,
			body:        `{"name":"` + strings.Repeat("項", 255) + `"}`,
			want:        `{"name":"` + strings.Repeat("項", 255) + `","description":null,"systemPrompt":null,"defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`,
		},
		{
			name:        "description at the limit in multibyte characters fits",
			contentType: jsonType,
			body:        `{"name":"New","description":"` + strings.Repeat("—", 10) + `"}`,
			want:        `{"name":"New","description":"` + strings.Repeat("—", 10) + `","systemPrompt":null,"defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`,
		},
		{
			name:        "description newlines count once each",
			contentType: jsonType,
			body:        `{"name":"New","description":"a\nb\nc\nd\ne"}`,
			want:        `{"name":"New","description":"a\nb\nc\nd\ne","systemPrompt":null,"defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`,
		},
		{
			name:        "instructions at the limit fit",
			contentType: jsonType,
			body:        `{"name":"New","systemPrompt":"` + strings.Repeat("i", 12) + `"}`,
			want:        `{"name":"New","description":null,"systemPrompt":"` + strings.Repeat("i", 12) + `","defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`,
		},
		{
			name:        "multipart CRLF newlines count as the browser typed them",
			contentType: mpContentType,
			body:        mpNameField + "New" + mpDescField + "a\r\nb\r\nc\r\nd\r\ne" + mpEnd,
			want:        `{"name":"New","description":"a\r\nb\r\nc\r\nd\r\ne","systemPrompt":null,"defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newProjectTestGateway(t)
			resp := gw.sendTyped(t, http.MethodPost, "/api/v1/projects", tc.contentType, tc.body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status %d, want 201; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			body := bodyString(t, resp)
			var ref struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(body), &ref); err != nil {
				t.Fatal(err)
			}
			got := decodeProject(t, &http.Response{Body: io.NopCloser(strings.NewReader(body))})
			if diff := cmp.Diff(decodeJSONText(t, tc.want), any(got)); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(got, getProject(t, gw, ref.ID)); diff != "" {
				t.Errorf("stored project differs from the response (-response +stored):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectCreateRejected(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{name: "missing name", contentType: jsonType, body: `{}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "whitespace-only name", contentType: jsonType, body: `{"name":"   "}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "name of 256 characters", contentType: jsonType, body: `{"name":"` + strings.Repeat("a", 256) + `"}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "description one over the limit", contentType: jsonType, body: `{"name":"New","description":"` + strings.Repeat("d", 11) + `"}`, wantStatus: 400, wantMessage: "description must be 10 characters or fewer (got 11)"},
		{name: "instructions one over the limit", contentType: jsonType, body: `{"name":"New","systemPrompt":"` + strings.Repeat("i", 13) + `"}`, wantStatus: 400, wantMessage: "instructions must be 12 characters or fewer (got 13)"},
		{name: "name error wins over description error", contentType: jsonType, body: `{"name":"   ","description":"` + strings.Repeat("d", 11) + `"}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "description error wins over instructions error", contentType: jsonType, body: `{"name":"New","description":"` + strings.Repeat("d", 11) + `","systemPrompt":"` + strings.Repeat("i", 13) + `"}`, wantStatus: 400, wantMessage: "description must be 10 characters or fewer (got 11)"},
		{name: "multipart description one over the limit", contentType: mpContentType, body: mpNameField + "New" + mpDescField + strings.Repeat("d", 11) + mpEnd, wantStatus: 400, wantMessage: "description must be 10 characters or fewer (got 11)"},
		{name: "multipart instructions one over the limit", contentType: mpContentType, body: mpNameField + "New" + mpInstrField + strings.Repeat("i", 13) + mpEnd, wantStatus: 400, wantMessage: "instructions must be 12 characters or fewer (got 13)"},
		{name: "same name as an existing project", contentType: jsonType, body: `{"name":"Existing Project"}`, wantStatus: 409, wantMessage: "a project with this name already exists"},
		{name: "existing name in another case", contentType: jsonType, body: `{"name":"existing project"}`, wantStatus: 409, wantMessage: "a project with this name already exists"},
		{name: "existing name with surrounding whitespace", contentType: jsonType, body: `{"name":"  Existing Project  "}`, wantStatus: 409, wantMessage: "a project with this name already exists"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newProjectTestGateway(t)
			resp := gw.sendTyped(t, http.MethodPost, "/api/v1/projects", tc.contentType, tc.body)
			assertErrorMessage(t, resp, tc.wantStatus, tc.wantMessage)
			if diff := cmp.Diff([]string{existingName}, listedProjectNames(t, gw)); diff != "" {
				t.Errorf("stored projects (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectUpdateAccepted(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStored string
	}{
		{
			name:       "every field is replaced",
			body:       `{"name":"Renamed","description":"new desc","systemPrompt":"new instr","defaultAgentId":"new-agent"}`,
			wantStored: `{"name":"Renamed","description":"new desc","systemPrompt":"new instr","defaultAgentId":"new-agent",` + ownedByDevUser + `}`,
		},
		{
			name:       "omitted fields are kept",
			body:       `{"name":"Renamed"}`,
			wantStored: `{"name":"Renamed","description":"old desc","systemPrompt":"old instr","defaultAgentId":"old-agent",` + ownedByDevUser + `}`,
		},
		{
			name:       "surrounding whitespace is trimmed from the name",
			body:       `{"name":"  Padded  "}`,
			wantStored: `{"name":"Padded","description":"old desc","systemPrompt":"old instr","defaultAgentId":"old-agent",` + ownedByDevUser + `}`,
		},
		{
			name:       "keeping its own name is not a conflict",
			body:       `{"name":"target"}`,
			wantStored: `{"name":"target","description":"old desc","systemPrompt":"old instr","defaultAgentId":"old-agent",` + ownedByDevUser + `}`,
		},
		{
			name:       "name of 255 multibyte characters fits",
			body:       `{"name":"` + strings.Repeat("目", 255) + `"}`,
			wantStored: `{"name":"` + strings.Repeat("目", 255) + `","description":"old desc","systemPrompt":"old instr","defaultAgentId":"old-agent",` + ownedByDevUser + `}`,
		},
		{
			name:       "description at the limit in multibyte characters fits",
			body:       `{"description":"` + strings.Repeat("—", 10) + `"}`,
			wantStored: `{"name":"Target","description":"` + strings.Repeat("—", 10) + `","systemPrompt":"old instr","defaultAgentId":"old-agent",` + ownedByDevUser + `}`,
		},
		{
			name:       "instructions at the limit fit",
			body:       `{"systemPrompt":"` + strings.Repeat("i", 12) + `"}`,
			wantStored: `{"name":"Target","description":"old desc","systemPrompt":"` + strings.Repeat("i", 12) + `","defaultAgentId":"old-agent",` + ownedByDevUser + `}`,
		},
		{
			name:       "instructions at the limit in multibyte characters fit",
			body:       `{"systemPrompt":"` + strings.Repeat("—", 12) + `"}`,
			wantStored: `{"name":"Target","description":"old desc","systemPrompt":"` + strings.Repeat("—", 12) + `","defaultAgentId":"old-agent",` + ownedByDevUser + `}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newProjectTestGateway(t)
			id := createProject(t, gw, targetProject)
			resp := gw.send(t, http.MethodPut, "/api/v1/projects/"+id, tc.body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			got := decodeProject(t, resp)
			stored := getProject(t, gw, id)
			if diff := cmp.Diff(decodeJSONText(t, tc.wantStored), any(stored)); diff != "" {
				t.Errorf("stored project (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(stored, got); diff != "" {
				t.Errorf("response differs from the stored project (-stored +response):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectUpdateRejected(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{name: "whitespace-only name", body: `{"name":"   "}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "name of 256 multibyte characters", body: `{"name":"` + strings.Repeat("目", 256) + `"}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "description one over the limit", body: `{"description":"` + strings.Repeat("d", 11) + `"}`, wantStatus: 400, wantMessage: "description must be 10 characters or fewer (got 11)"},
		{name: "instructions one over the limit", body: `{"systemPrompt":"` + strings.Repeat("i", 13) + `"}`, wantStatus: 400, wantMessage: "instructions must be 12 characters or fewer (got 13)"},
		{name: "name error wins over description error", body: `{"name":"   ","description":"` + strings.Repeat("d", 11) + `"}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "name error wins over instructions error", body: `{"name":"   ","systemPrompt":"` + strings.Repeat("i", 13) + `"}`, wantStatus: 400, wantMessage: "name is required (1-255 characters)"},
		{name: "description error wins over instructions error", body: `{"description":"` + strings.Repeat("d", 11) + `","systemPrompt":"` + strings.Repeat("i", 13) + `"}`, wantStatus: 400, wantMessage: "description must be 10 characters or fewer (got 11)"},
		{name: "another project's name in another case", body: `{"name":"existing project"}`, wantStatus: 409, wantMessage: "a project with this name already exists"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newProjectTestGateway(t)
			id := createProject(t, gw, targetProject)
			resp := gw.send(t, http.MethodPut, "/api/v1/projects/"+id, tc.body)
			assertErrorMessage(t, resp, tc.wantStatus, tc.wantMessage)
			if diff := cmp.Diff(decodeJSONText(t, unchangedStore), any(getProject(t, gw, id))); diff != "" {
				t.Errorf("stored project (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectWrongBodyTypeIsRejected(t *testing.T) {
	tests := []struct {
		name, method, path string
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/projects"},
		{name: "update", method: http.MethodPut, path: "/api/v1/projects/{id}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newProjectTestGateway(t)
			id := createProject(t, gw, targetProject)
			resp := gw.send(t, tc.method, strings.ReplaceAll(tc.path, "{id}", id), `"not json"`)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var body ErrorResponse
			decodeRespJSON(t, resp, &body)
			if body.Message != "validation error" || body.ErrorID == "" {
				t.Errorf("error body = %+v, want message %q and an errorId", body, "validation error")
			}
			if diff := cmp.Diff(decodeJSONText(t, unchangedStore), any(getProject(t, gw, id))); diff != "" {
				t.Errorf("stored project (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{existingName, "Target"}, listedProjectNames(t, gw)); diff != "" {
				t.Errorf("stored projects (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectLimitsAdvertisedInConfig(t *testing.T) {
	gw := newProjectTestGateway(t)
	var cfg struct {
		ValidationLimits struct {
			ProjectDescriptionMax  int `json:"projectDescriptionMax"`
			ProjectInstructionsMax int `json:"projectInstructionsMax"`
		} `json:"validationLimits"`
	}
	decodeRespJSON(t, gw.get(t, "/api/v1/config"), &cfg)
	if got := cfg.ValidationLimits; got.ProjectDescriptionMax != 10 || got.ProjectInstructionsMax != 12 {
		t.Errorf("/config validationLimits = %+v, want the enforced limits 10 and 12", got)
	}
}

func TestIntegration_ProjectUnknownIDIsNotFound(t *testing.T) {
	tests := []struct {
		name, method, suffix, body string
	}{
		{name: "get", method: http.MethodGet},
		{name: "update", method: http.MethodPut, body: `{"name":"updated"}`},
		{name: "delete", method: http.MethodDelete},
		{name: "export", method: http.MethodGet, suffix: "/export"},
		{name: "list artifacts", method: http.MethodGet, suffix: "/artifacts"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			resp := gw.send(t, tc.method, "/api/v1/projects/"+idgen.New()+tc.suffix, tc.body)
			assertErrorMessage(t, resp, http.StatusNotFound, "project not found")
		})
	}
}

func TestIntegration_ProjectArtifactCount(t *testing.T) {
	tests := []struct {
		name          string
		artifacts     []string
		wantGet       float64
		wantListPlain any
		wantExpanded  float64
	}{
		{name: "project without artifacts", artifacts: nil, wantGet: 0, wantListPlain: nil, wantExpanded: 0},
		{name: "project with two artifacts", artifacts: []string{"a.txt", "b.txt"}, wantGet: 2, wantListPlain: nil, wantExpanded: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			id := createProject(t, gw, `{"name":"Counted"}`)
			for _, name := range tc.artifacts {
				gw.uploadProjectArtifact(t, id, name, "content of "+name)
			}

			var got map[string]any
			decodeRespJSON(t, gw.get(t, "/api/v1/projects/"+id), &got)
			var plain, expanded struct {
				Data []map[string]any `json:"data"`
				Meta struct {
					Pagination struct {
						Count int `json:"count"`
					} `json:"pagination"`
				} `json:"meta"`
			}
			for _, l := range []struct {
				path string
				into any
			}{{"/api/v1/projects", &plain}, {"/api/v1/projects?expand=artifactCount", &expanded}} {
				resp := gw.get(t, l.path)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("GET %s: status %d; body: %s", l.path, resp.StatusCode, bodyString(t, resp))
				}
				decodeRespJSON(t, resp, l.into)
			}
			if len(plain.Data) != 1 || len(expanded.Data) != 1 {
				t.Fatalf("listed %d and %d projects, want 1 each", len(plain.Data), len(expanded.Data))
			}
			if plain.Meta.Pagination.Count != 1 || expanded.Meta.Pagination.Count != 1 {
				t.Errorf("meta.pagination.count = %d and %d, want 1 each", plain.Meta.Pagination.Count, expanded.Meta.Pagination.Count)
			}
			gotCounts := []any{got["artifactCount"], plain.Data[0]["artifactCount"], expanded.Data[0]["artifactCount"]}
			wantCounts := []any{tc.wantGet, tc.wantListPlain, tc.wantExpanded}
			if diff := cmp.Diff(wantCounts, gotCounts); diff != "" {
				t.Errorf("artifactCount on get, list, list?expand=artifactCount (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectDelete(t *testing.T) {
	gw := newTestGateway(t)
	ctx := context.Background()
	id := createProject(t, gw, `{"name":"Doomed"}`)
	linked := []string{idgen.New(), idgen.New()}
	for _, sid := range linked {
		if err := gw.sessRepo.Create(ctx, &store.Session{ID: sid, UserID: "sam_dev_user", ProjectID: &id}); err != nil {
			t.Fatal(err)
		}
	}
	unlinked := idgen.New()
	if err := gw.sessRepo.Create(ctx, &store.Session{ID: unlinked, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	gw.uploadProjectArtifact(t, id, "keepme.txt", "some content")

	resp := gw.delete(t, "/api/v1/projects/"+id)
	if body := bodyString(t, resp); resp.StatusCode != http.StatusNoContent || body != "" {
		t.Fatalf("delete: status %d body %q, want 204 and no body", resp.StatusCode, body)
	}

	assertErrorMessage(t, gw.get(t, "/api/v1/projects/"+id), http.StatusNotFound, "project not found")
	for _, sid := range linked {
		if got := gw.get(t, "/api/v1/sessions/"+sid); got.StatusCode != http.StatusNotFound {
			t.Errorf("linked session %s after project delete: status %d, want 404", sid, got.StatusCode)
		}
	}
	if got := gw.get(t, "/api/v1/sessions/"+unlinked); got.StatusCode != http.StatusOK {
		t.Errorf("unlinked session after project delete: status %d, want 200", got.StatusCode)
	}

	keys, err := gw.artSvc.Inner.ListKeys(ctx, artifact.KeyScope{
		AppName:   gw.artSvc.Namespace,
		UserID:    "sam_dev_user",
		SessionID: "project-" + id,
	})
	if err != nil {
		t.Fatalf("list artifact keys after delete: %v", err)
	}
	if !slices.Contains(keys, "keepme.txt") {
		t.Errorf("artifact keys after project delete = %v, want keepme.txt kept", keys)
	}
}

type uploadFile struct{ field, filename, content string }

func (g *testGateway) uploadProjectFiles(t *testing.T, projectID, fileMetadata string, files ...uploadFile) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range files {
		part, err := mw.CreateFormFile(f.field, f.filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(f.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.WriteField("fileMetadata", fileMetadata); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return g.doMultipart(t, "/api/v1/projects/"+projectID+"/artifacts", mw.FormDataContentType(), &buf)
}

func listedProjectArtifacts(t *testing.T, gw *testGateway, projectID string) []map[string]any {
	t.Helper()
	var list struct {
		Data []map[string]any `json:"data"`
	}
	resp := gw.get(t, "/api/v1/projects/"+projectID+"/artifacts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list artifacts of project %s: status %d; body: %s", projectID, resp.StatusCode, bodyString(t, resp))
	}
	decodeRespJSON(t, resp, &list)
	if list.Data == nil {
		t.Fatal("artifact list data is null, want an array")
	}
	out := make([]map[string]any, 0, len(list.Data))
	for _, a := range list.Data {
		out = append(out, map[string]any{"filename": a["filename"], "mimeType": a["mimeType"], "size": a["size"], "description": a["description"]})
	}
	slices.SortFunc(out, func(a, b map[string]any) int { return strings.Compare(a["filename"].(string), b["filename"].(string)) })
	return out
}

func decodeWithoutErrorID(t *testing.T, body string) any {
	t.Helper()
	if body == "" {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	delete(v, "errorId")
	return v
}

func TestIntegration_ProjectArtifactUpload(t *testing.T) {
	tests := []struct {
		name         string
		files        []uploadFile
		fileMetadata string
		wantStatus   int
		wantIndexing bool
		wantBody     string
		wantListed   string
	}{
		{
			name:         "text file is stored and indexed",
			files:        []uploadFile{{"file1", "readme.txt", "# Hello"}},
			wantStatus:   http.StatusAccepted,
			wantIndexing: true,
			wantBody:     `{"uploaded":[{"filename":"readme.txt","size":7,"mimeType":"text/plain"}]}`,
			wantListed:   `[{"filename":"readme.txt","mimeType":"text/plain","size":7,"description":null}]`,
		},
		{
			name:         "flat file metadata sets the description",
			files:        []uploadFile{{"file1", "notes.txt", "some notes"}},
			fileMetadata: `{"notes.txt":"Description for the test file"}`,
			wantStatus:   http.StatusAccepted,
			wantIndexing: true,
			wantBody:     `{"uploaded":[{"filename":"notes.txt","size":10,"mimeType":"text/plain"}]}`,
			wantListed:   `[{"filename":"notes.txt","mimeType":"text/plain","size":10,"description":"Description for the test file"}]`,
		},
		{
			name:         "nested file metadata sets the description",
			files:        []uploadFile{{"file1", "notes.txt", "some notes"}},
			fileMetadata: `{"notes.txt":{"description":"Nested description"}}`,
			wantStatus:   http.StatusAccepted,
			wantIndexing: true,
			wantBody:     `{"uploaded":[{"filename":"notes.txt","size":10,"mimeType":"text/plain"}]}`,
			wantListed:   `[{"filename":"notes.txt","mimeType":"text/plain","size":10,"description":"Nested description"}]`,
		},
		{
			name:         "image only is stored without indexing",
			files:        []uploadFile{{"file1", "logo.png", "\x89PNG\r\n\x1a\nbinary-image-bytes"}},
			wantStatus:   http.StatusCreated,
			wantIndexing: false,
			wantBody:     `{"uploaded":[{"filename":"logo.png","size":26,"mimeType":"image/png"}]}`,
			wantListed:   `[{"filename":"logo.png","mimeType":"image/png","size":26,"description":null}]`,
		},
		{
			name:         "no files uploads nothing",
			wantStatus:   http.StatusCreated,
			wantIndexing: false,
			wantBody:     `{"uploaded":[]}`,
			wantListed:   `[]`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			id := createProject(t, gw, `{"name":"Artifacts"}`)
			resp := gw.uploadProjectFiles(t, id, tc.fileMetadata, tc.files...)
			indexing := resp.Header.Get("Sse-Location") != ""
			body := bodyString(t, resp)
			if resp.StatusCode != tc.wantStatus || indexing != tc.wantIndexing {
				t.Errorf("status %d, indexing stream advertised %v; want %d, %v", resp.StatusCode, indexing, tc.wantStatus, tc.wantIndexing)
			}
			if diff := cmp.Diff(decodeJSONText(t, tc.wantBody), decodeJSONText(t, body)); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			got, _ := json.Marshal(listedProjectArtifacts(t, gw, id))
			if diff := cmp.Diff(decodeJSONText(t, tc.wantListed), decodeJSONText(t, string(got))); diff != "" {
				t.Errorf("listed artifacts (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectArtifactUploadRequiresMultipart(t *testing.T) {
	gw := newTestGateway(t)
	id := createProject(t, gw, `{"name":"Artifacts"}`)
	resp := gw.send(t, http.MethodPost, "/api/v1/projects/"+id+"/artifacts", `{}`)
	assertErrorMessage(t, resp, http.StatusBadRequest, "invalid multipart form or file too large")
}

func TestIntegration_ProjectArtifactUpdateMetadata(t *testing.T) {
	tests := []struct {
		name            string
		filename        string
		body            string
		wantStatus      int
		wantBody        string
		wantDescription any
	}{
		{
			name:            "description is replaced",
			filename:        "notes.txt",
			body:            `{"description":"A readme file"}`,
			wantStatus:      http.StatusOK,
			wantBody:        `{"message":"Artifact metadata updated successfully"}`,
			wantDescription: "A readme file",
		},
		{
			name:            "unknown file is not found",
			filename:        "no-such-file.txt",
			body:            `{"description":"nope"}`,
			wantStatus:      http.StatusNotFound,
			wantBody:        `{"message":"artifact not found"}`,
			wantDescription: "original",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			id := createProject(t, gw, `{"name":"Artifacts"}`)
			_ = bodyString(t, gw.uploadProjectFiles(t, id, `{"notes.txt":"original"}`, uploadFile{"file1", "notes.txt", "some notes"}))

			resp := gw.send(t, http.MethodPatch, "/api/v1/projects/"+id+"/artifacts/"+tc.filename, tc.body)
			body := bodyString(t, resp)
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status %d, want %d; body: %s", resp.StatusCode, tc.wantStatus, body)
			}
			if diff := cmp.Diff(decodeWithoutErrorID(t, tc.wantBody), decodeWithoutErrorID(t, body)); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			listed := listedProjectArtifacts(t, gw, id)
			if len(listed) != 1 || listed[0]["description"] != tc.wantDescription {
				t.Errorf("listed artifacts = %v, want notes.txt with description %v", listed, tc.wantDescription)
			}
		})
	}
}

func TestIntegration_ProjectArtifactUpdateMetadataRequiresJSON(t *testing.T) {
	gw := newTestGateway(t)
	id := createProject(t, gw, `{"name":"Artifacts"}`)
	_ = bodyString(t, gw.uploadProjectFiles(t, id, `{"notes.txt":"original"}`, uploadFile{"file1", "notes.txt", "some notes"}))

	resp := gw.sendTyped(t, http.MethodPatch, "/api/v1/projects/"+id+"/artifacts/notes.txt", mpContentType, mpDescField[2:]+"an update"+mpEnd)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d, want 415; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	var body struct {
		Message string `json:"message"`
	}
	decodeRespJSON(t, resp, &body)
	if body.Message != "unsupported media type: Content-Type must be application/json" {
		t.Errorf("message = %q", body.Message)
	}
	if listed := listedProjectArtifacts(t, gw, id); len(listed) != 1 || listed[0]["description"] != "original" {
		t.Errorf("listed artifacts = %v, want notes.txt with description original", listed)
	}
}

func TestIntegration_ProjectArtifactDelete(t *testing.T) {
	tests := []struct {
		name         string
		filename     string
		wantStatus   int
		wantIndexing bool
		wantBody     string
		wantListed   []string
	}{
		{
			name:         "existing file is removed and the index rebuilt",
			filename:     "kept.txt",
			wantStatus:   http.StatusAccepted,
			wantIndexing: true,
			wantBody:     `{"deleted":true,"filename":"kept.txt"}`,
			wantListed:   []string{},
		},
		{
			name:         "missing file is a no-op",
			filename:     "missing.txt",
			wantStatus:   http.StatusNoContent,
			wantIndexing: false,
			wantListed:   []string{"kept.txt"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			id := createProject(t, gw, `{"name":"Artifacts"}`)
			gw.uploadProjectArtifact(t, id, "kept.txt", "content")

			resp := gw.delete(t, "/api/v1/projects/"+id+"/artifacts/"+tc.filename)
			indexing := resp.Header.Get("Sse-Location") != ""
			body := bodyString(t, resp)
			if resp.StatusCode != tc.wantStatus || indexing != tc.wantIndexing {
				t.Errorf("status %d, indexing stream advertised %v; want %d, %v", resp.StatusCode, indexing, tc.wantStatus, tc.wantIndexing)
			}
			if diff := cmp.Diff(decodeWithoutErrorID(t, tc.wantBody), decodeWithoutErrorID(t, body)); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantListed, projectArtifactNames(t, gw, id)); diff != "" {
				t.Errorf("listed artifacts (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_PromptGroup_NoDependencyOnLegacyTable(t *testing.T) {
	gw := newTestGateway(t)

	createResp := gw.postJSON(t, "/api/v1/prompts/groups", map[string]any{
		"name":          "Legacy Cleanup",
		"initialPrompt": "hi",
	})
	if createResp.StatusCode != 201 {
		t.Fatalf("create: expected 201, got %d; body: %s", createResp.StatusCode, bodyString(t, createResp))
	}
	var group PromptGroupResponse
	decodeRespJSON(t, createResp, &group)

	if _, err := gw.db.ExecContext(context.Background(), `DROP TABLE IF EXISTS prompt_group_users`); err != nil {
		t.Fatalf("drop legacy table: %v", err)
	}

	if r := gw.get(t, "/api/v1/prompts/groups"); r.StatusCode != 200 {
		t.Errorf("list: expected 200, got %d; body: %s", r.StatusCode, bodyString(t, r))
	}
	if r := gw.get(t, "/api/v1/prompts/groups/all"); r.StatusCode != 200 {
		t.Errorf("listAll: expected 200, got %d; body: %s", r.StatusCode, bodyString(t, r))
	}
	if r := gw.get(t, "/api/v1/prompts/groups/"+group.ID); r.StatusCode != 200 {
		t.Errorf("get: expected 200, got %d; body: %s", r.StatusCode, bodyString(t, r))
	}
	if r := gw.patchJSON(t, "/api/v1/prompts/groups/"+group.ID, map[string]any{"name": "renamed"}); r.StatusCode != 200 {
		t.Errorf("patch: expected 200, got %d; body: %s", r.StatusCode, bodyString(t, r))
	}
}

type zipEntry struct{ name, content string }

func zipOf(entries ...zipEntry) string {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			panic(err)
		}
		if _, err := w.Write([]byte(e.content)); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.String()
}

func projectJSON(project string) zipEntry {
	return zipEntry{name: "project.json", content: `{"version":"1.0","exportedAt":1735689600000,"project":` + project + `,"artifacts":[]}`}
}

func (g *testGateway) importProject(t *testing.T, fileField, file, options string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile(fileField, "project_export.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(file)); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("options", options); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return g.doMultipart(t, "/api/v1/projects/import", mw.FormDataContentType(), &buf)
}

func projectArtifactNames(t *testing.T, gw *testGateway, projectID string) []string {
	t.Helper()
	var list struct {
		Data []struct {
			Filename string `json:"filename"`
		} `json:"data"`
	}
	resp := gw.get(t, "/api/v1/projects/"+projectID+"/artifacts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list artifacts of project %s: status %d; body: %s", projectID, resp.StatusCode, bodyString(t, resp))
	}
	decodeRespJSON(t, resp, &list)
	names := make([]string, 0, len(list.Data))
	for _, a := range list.Data {
		names = append(names, a.Filename)
	}
	slices.Sort(names)
	return names
}

const bareImportedProject = `{"name":"Imported","description":null,"systemPrompt":null,"defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`

func TestIntegration_ProjectImportAccepted(t *testing.T) {
	tests := []struct {
		name          string
		file          string
		options       string
		wantStatus    int
		wantResponse  string
		wantStored    string
		wantArtifacts []string
	}{
		{
			name: "metadata and artifacts are restored and indexed",
			file: zipOf(
				zipEntry{"project.json", `{"version":"1.0","exportedAt":1735689600000,"project":{"name":"Imported","description":"from zip","systemPrompt":"be brief","defaultAgentId":"agent-x"},"artifacts":[{"filename":"notes.txt","mimeType":"text/plain","size":10,"metadata":{"source":"project"}}]}`},
				zipEntry{"artifacts/notes.txt", "some notes"},
			),
			wantStatus:    http.StatusAccepted,
			wantResponse:  `{"name":"Imported","artifactsImported":1}`,
			wantStored:    `{"name":"Imported","description":"from zip","systemPrompt":"be brief","defaultAgentId":"agent-x","updatedAt":null,` + ownedByDevUser + `}`,
			wantArtifacts: []string{"notes.txt"},
		},
		{
			name:          "without artifacts there is nothing to index",
			file:          zipOf(projectJSON(`{"name":"Imported"}`)),
			wantStatus:    http.StatusCreated,
			wantResponse:  `{"name":"Imported","artifactsImported":0}`,
			wantStored:    bareImportedProject,
			wantArtifacts: []string{},
		},
		{
			name:          "custom name replaces the exported name",
			file:          zipOf(projectJSON(`{"name":"Original Name"}`)),
			options:       `{"preserveName":false,"customName":"My Renamed Project"}`,
			wantStatus:    http.StatusCreated,
			wantResponse:  `{"name":"My Renamed Project","artifactsImported":0}`,
			wantStored:    `{"name":"My Renamed Project","description":null,"systemPrompt":null,"defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`,
			wantArtifacts: []string{},
		},
		{
			name:          "blank custom name keeps the exported name",
			file:          zipOf(projectJSON(`{"name":"Original Name"}`)),
			options:       `{"preserveName":false,"customName":"   "}`,
			wantStatus:    http.StatusCreated,
			wantResponse:  `{"name":"Original Name","artifactsImported":0}`,
			wantStored:    `{"name":"Original Name","description":null,"systemPrompt":null,"defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`,
			wantArtifacts: []string{},
		},
		{
			name:          "surrounding whitespace is trimmed from the name",
			file:          zipOf(projectJSON(`{"name":"  Padded Import  "}`)),
			wantStatus:    http.StatusCreated,
			wantResponse:  `{"name":"Padded Import","artifactsImported":0}`,
			wantStored:    `{"name":"Padded Import","description":null,"systemPrompt":null,"defaultAgentId":null,"updatedAt":null,` + ownedByDevUser + `}`,
			wantArtifacts: []string{},
		},
		{
			name: "legacy sessions array is ignored",
			file: zipOf(
				zipEntry{"project.json", `{"version":"1.0","exportedAt":1735689600000,"project":{"name":"Imported"},"artifacts":[],"sessions":[{"name":"Session 1","chatTasks":[{"userMessage":"hello","messageBubbles":"[]"}]}]}`},
			),
			wantStatus:    http.StatusCreated,
			wantResponse:  `{"name":"Imported","artifactsImported":0}`,
			wantStored:    bareImportedProject,
			wantArtifacts: []string{},
		},
		{
			name: "internal and unsafe artifact names are skipped",
			file: zipOf(
				projectJSON(`{"name":"Imported"}`),
				zipEntry{"artifacts/report.metadata.json", `{"poisoned":"yes"}`},
				zipEntry{"artifacts/data.converted.txt", `{"poisoned":"yes"}`},
				zipEntry{"artifacts/project_bm25_index.zip", `{"poisoned":"yes"}`},
				zipEntry{"artifacts/../escape.txt", "escaped"},
				zipEntry{"artifacts/clean.txt", "ok"},
			),
			wantStatus:    http.StatusAccepted,
			wantResponse:  `{"name":"Imported","artifactsImported":1}`,
			wantStored:    bareImportedProject,
			wantArtifacts: []string{"clean.txt"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGateway(t)
			resp := gw.importProject(t, "file", tc.file, tc.options)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status %d, want %d; body: %s", resp.StatusCode, tc.wantStatus, bodyString(t, resp))
			}
			var got map[string]any
			decodeRespJSON(t, resp, &got)
			id, _ := got["projectId"].(string)
			if id == "" {
				t.Fatalf("projectId = %v, want a non-empty string", got["projectId"])
			}
			delete(got, "projectId")
			if diff := cmp.Diff(decodeJSONText(t, tc.wantResponse), any(got)); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(decodeJSONText(t, tc.wantStored), any(getProject(t, gw, id))); diff != "" {
				t.Errorf("stored project (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantArtifacts, projectArtifactNames(t, gw, id)); diff != "" {
				t.Errorf("stored artifacts (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectImportRejected(t *testing.T) {
	tests := []struct {
		name        string
		fileField   string
		file        string
		wantMessage string
	}{
		{name: "file field missing", fileField: "upload", file: zipOf(projectJSON(`{"name":"Imported"}`)), wantMessage: "file is required"},
		{name: "file is not a ZIP", fileField: "file", file: "not a zip file", wantMessage: "invalid ZIP file"},
		{name: "ZIP without project.json", fileField: "file", file: zipOf(zipEntry{"other.txt", "not project.json"}), wantMessage: "ZIP must contain project.json"},
		{name: "project.json is not JSON", fileField: "file", file: zipOf(zipEntry{"project.json", "{not json"}), wantMessage: "invalid project.json format"},
		{name: "unsupported export version", fileField: "file", file: zipOf(zipEntry{"project.json", `{"version":"99.0","project":{"name":"Bad"}}`}), wantMessage: "unsupported export version: 99.0"},
		{name: "empty exported name", fileField: "file", file: zipOf(projectJSON(`{"name":""}`)), wantMessage: "name is required (1-255 characters)"},
		{name: "whitespace-only exported name", fileField: "file", file: zipOf(projectJSON(`{"name":"   "}`)), wantMessage: "name is required (1-255 characters)"},
		{name: "exported name of 256 characters", fileField: "file", file: zipOf(projectJSON(`{"name":"` + strings.Repeat("n", 256) + `"}`)), wantMessage: "name is required (1-255 characters)"},
		{name: "description one over the limit", fileField: "file", file: zipOf(projectJSON(`{"name":"Imported","description":"` + strings.Repeat("d", 11) + `"}`)), wantMessage: "description must be 10 characters or fewer (got 11)"},
		{name: "instructions one over the limit", fileField: "file", file: zipOf(projectJSON(`{"name":"Imported","systemPrompt":"` + strings.Repeat("i", 13) + `"}`)), wantMessage: "instructions must be 12 characters or fewer (got 13)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newProjectTestGateway(t)
			resp := gw.importProject(t, tc.fileField, tc.file, "")
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var body ErrorResponse
			decodeRespJSON(t, resp, &body)
			if body.Message != tc.wantMessage || body.ErrorID == "" {
				t.Errorf("error body = %+v, want message %q and an errorId", body, tc.wantMessage)
			}
			if diff := cmp.Diff([]string{existingName}, listedProjectNames(t, gw)); diff != "" {
				t.Errorf("stored projects (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectImportRejectsInvalidOptions(t *testing.T) {
	tests := []struct{ name, options string }{
		{name: "malformed options", options: `{not json`},
		{name: "unknown options field", options: `{"customName":"X","bogusField":true}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw := newProjectTestGateway(t)
			resp := gw.importProject(t, "file", zipOf(projectJSON(`{"name":"Imported"}`)), tc.options)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var body ErrorResponse
			decodeRespJSON(t, resp, &body)
			if !strings.HasPrefix(body.Message, "invalid options: ") || body.ErrorID == "" {
				t.Errorf("error body = %+v, want message starting %q and an errorId", body, "invalid options: ")
			}
			if diff := cmp.Diff([]string{existingName}, listedProjectNames(t, gw)); diff != "" {
				t.Errorf("stored projects (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_ProjectExportImportRoundTrip(t *testing.T) {
	gw := newTestGateway(t)
	id := createProject(t, gw, `{"name":"Round Trip","description":"desc","systemPrompt":"be brief","defaultAgentId":"agent-x"}`)
	gw.uploadProjectArtifact(t, id, "notes.txt", "project notes")

	resp := gw.get(t, "/api/v1/projects/"+id+"/export")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	gotHeaders := [2]string{resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition")}
	if want := [2]string{"application/zip", `attachment; filename="project_Round_Trip.zip"`}; gotHeaders != want {
		t.Errorf("export headers = %q, want %q", gotHeaders, want)
	}
	exported := bodyString(t, resp)
	zr, err := zip.NewReader(strings.NewReader(exported), int64(len(exported)))
	if err != nil {
		t.Fatalf("export is not a ZIP: %v", err)
	}
	entries := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		entries[f.Name] = string(b)
	}
	if got := entries["artifacts/notes.txt"]; got != "project notes" {
		t.Errorf("exported artifacts/notes.txt = %q, want %q", got, "project notes")
	}

	importResp := gw.importProject(t, "file", exported, `{"customName":"Round Trip Copy"}`)
	if importResp.StatusCode != http.StatusAccepted {
		t.Fatalf("import: status %d, want 202; body: %s", importResp.StatusCode, bodyString(t, importResp))
	}
	var imported struct {
		ProjectID string `json:"projectId"`
	}
	decodeRespJSON(t, importResp, &imported)

	original := getProject(t, gw, id)
	copied := getProject(t, gw, imported.ProjectID)
	original["name"] = "Round Trip Copy"
	if diff := cmp.Diff(original, copied); diff != "" {
		t.Errorf("imported project differs from the exported one (-exported +imported):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"notes.txt"}, projectArtifactNames(t, gw, imported.ProjectID)); diff != "" {
		t.Errorf("imported artifacts (-want +got):\n%s", diff)
	}
}

func TestIntegration_ProjectExportWithoutArtifacts(t *testing.T) {
	gw := newTestGateway(t)
	ctx := context.Background()
	projectID, sessionID := idgen.New(), idgen.New()
	desc := "export test project"
	if err := store.NewProjectRepo(gw.db).Create(ctx, &store.Project{ID: projectID, Name: "Export Test", UserID: "sam_dev_user", Description: &desc}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	sessName := "Test Session"
	if err := gw.sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: "sam_dev_user", Name: &sessName, ProjectID: &projectID}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	msg := "Hello"
	if err := gw.chatRepo.Create(ctx, &store.ChatTask{
		ID: idgen.New(), SessionID: sessionID, UserMessage: &msg,
		MessageBubbles: `[{"role":"user","parts":[{"text":"Hello"}]}]`,
		CreatedTime:    time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}

	resp := gw.get(t, "/api/v1/projects/"+projectID+"/export")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	gotHeaders := [2]string{resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition")}
	if want := [2]string{"application/zip", `attachment; filename="project_Export_Test.zip"`}; gotHeaders != want {
		t.Errorf("export headers = %q, want %q", gotHeaders, want)
	}
}

func newTestGatewayWithConfig(t *testing.T, extra map[string]any) *testGateway {
	t.Helper()
	return newTestGatewayWithFlags(t, extra, nil)
}

func newTestGatewayWithFlags(t *testing.T, extra map[string]any, reg *features.Registry) *testGateway {
	t.Helper()
	return buildTestGateway(t, testGatewayOptions{appConfig: extra, flags: reg})
}

func newTestGatewayWithVolumes(t *testing.T) (*testGateway, volume.Service) {
	t.Helper()
	return newTestGatewayWithVolumesFlag(t, true)
}

func newTestGatewayWithVolumesFlag(t *testing.T, volumesEnabled bool) (*testGateway, volume.Service) {
	t.Helper()
	reg := communityFlags(t)
	reg.Register(features.Definition{Key: "volumes", Name: "Volumes", Default: volumesEnabled})
	volSvc := volume.NewMemoryStore()
	return buildTestGateway(t, testGatewayOptions{flags: reg, volumes: volSvc}), volSvc
}

func (g *testGateway) send(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, g.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func seedVolume(t *testing.T, svc volume.Service, opts volume.CreateOptions, sessionIDs ...string) string {
	t.Helper()
	ctx := context.Background()
	id, err := svc.Create(ctx, opts)
	if err != nil {
		t.Fatalf("seed volume %q: %v", opts.Name, err)
	}
	for _, sid := range sessionIDs {
		if err := svc.AssociateSession(ctx, id, sid); err != nil {
			t.Fatalf("associate volume %q with session %q: %v", opts.Name, sid, err)
		}
	}
	return id
}

func decodeJSONText(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("decode %s: %v", text, err)
	}
	return v
}

func stripVolumeClockFields(t *testing.T, v map[string]any) time.Duration {
	t.Helper()
	if id, _ := v["id"].(string); id == "" {
		t.Errorf("id = %v, want a non-empty string", v["id"])
	}
	created, _ := v["createdAt"].(string)
	createdAt, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		t.Errorf("createdAt = %q: %v", created, err)
	}
	if age := time.Since(createdAt); age < -time.Minute || age > time.Minute {
		t.Errorf("createdAt = %q, want within a minute of now", created)
	}
	if v["updatedAt"] != created || v["lastAccess"] != created {
		t.Errorf("updatedAt = %v, lastAccess = %v, want both equal to createdAt %q", v["updatedAt"], v["lastAccess"], created)
	}
	var expiry time.Duration
	if s, ok := v["expiresAt"].(string); ok {
		expiresAt, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Errorf("expiresAt = %q: %v", s, err)
		}
		expiry = expiresAt.Sub(createdAt)
	}
	for _, k := range []string{"id", "createdAt", "updatedAt", "lastAccess", "expiresAt"} {
		delete(v, k)
	}
	return expiry
}

func TestIntegration_VolumeRoutesGatedOffByDefault(t *testing.T) {
	tests := []struct {
		name, method, path, body string
	}{
		{name: "list is not routed", method: http.MethodGet, path: "/api/v1/volumes"},
		{name: "create is not routed", method: http.MethodPost, path: "/api/v1/volumes", body: `{"name":"should-not-exist"}`},
		{name: "usage is not routed", method: http.MethodGet, path: "/api/v1/user/volumes/usage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, _ := newTestGatewayWithVolumesFlag(t, false)
			resp := gw.send(t, tc.method, tc.path, tc.body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s with the volumes flag off: status %d, want 404", tc.method, tc.path, resp.StatusCode)
			}
		})
	}
}

const managedVolumeOfDevUser = `"userId":"sam_dev_user","createdBy":"sam_dev_user","updatedBy":"sam_dev_user",` +
	`"creatorType":"human","creatorName":"sam_dev_user","sizeBytes":0,"writeLock":null,"kind":"managed"`

func TestIntegration_VolumeCreate(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		want       string
		wantExpiry time.Duration
	}{
		{
			name:       "ttl sets an expiry that long after creation",
			body:       `{"name":"my-vol","description":"integration test volume","ttl":"24h"}`,
			want:       `{"name":"my-vol","description":"integration test volume","ttl":"24h0m0s",` + managedVolumeOfDevUser + `}`,
			wantExpiry: 24 * time.Hour,
		},
		{
			name: "without ttl the volume never expires",
			body: `{"name":"my-vol"}`,
			want: `{"name":"my-vol","description":"",` + managedVolumeOfDevUser + `}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, _ := newTestGatewayWithVolumes(t)
			resp := gw.send(t, http.MethodPost, "/api/v1/volumes", tc.body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status %d, want 201; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var got map[string]any
			decodeRespJSON(t, resp, &got)
			if expiry := stripVolumeClockFields(t, got); expiry != tc.wantExpiry {
				t.Errorf("expiresAt - createdAt = %v, want %v", expiry, tc.wantExpiry)
			}
			if diff := cmp.Diff(decodeJSONText(t, tc.want), any(got)); diff != "" {
				t.Errorf("response (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_VolumeCreateRejected(t *testing.T) {
	tests := []struct {
		name              string
		body              string
		wantMessagePrefix string
	}{
		{name: "missing name", body: `{"description":"no name"}`, wantMessagePrefix: "name is required"},
		{name: "unparseable ttl", body: `{"name":"bad-ttl","ttl":"not-a-duration"}`, wantMessagePrefix: "invalid ttl: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, volSvc := newTestGatewayWithVolumes(t)
			resp := gw.send(t, http.MethodPost, "/api/v1/volumes", tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d, want 400; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var body ErrorResponse
			decodeRespJSON(t, resp, &body)
			if !strings.HasPrefix(body.Message, tc.wantMessagePrefix) || body.ErrorID == "" {
				t.Errorf("error body = %+v, want message starting %q and an errorId", body, tc.wantMessagePrefix)
			}
			stored, err := volSvc.List(context.Background(), "sam_dev_user", volume.ListFilter{})
			if err != nil || len(stored) != 0 {
				t.Errorf("stored volumes = %d (err %v), want none", len(stored), err)
			}
		})
	}
}

func TestIntegration_VolumeCreatedIsListedAndReadable(t *testing.T) {
	gw, _ := newTestGatewayWithVolumes(t)
	createResp := gw.send(t, http.MethodPost, "/api/v1/volumes", `{"name":"get-me","ttl":"1h"}`)
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d, want 201; body: %s", createResp.StatusCode, bodyString(t, createResp))
	}
	created := bodyString(t, createResp)
	var ref struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &ref); err != nil {
		t.Fatal(err)
	}

	listResp := gw.get(t, "/api/v1/volumes")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d, want 200; body: %s", listResp.StatusCode, bodyString(t, listResp))
	}
	var listed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decodeRespJSON(t, listResp, &listed)
	if len(listed.Data) != 1 || listed.Data[0].ID != ref.ID {
		t.Errorf("GET /api/v1/volumes = %+v, want only the created volume %s", listed.Data, ref.ID)
	}

	resp := gw.get(t, "/api/v1/volumes/"+ref.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	if got := bodyString(t, resp); got != created {
		t.Errorf("get body = %s, want the create body %s", got, created)
	}
}

func TestIntegration_VolumeList(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantNames []string
	}{
		{name: "no filter lists only the caller's volumes", query: "", wantNames: []string{"own-agent", "own-agent-in-a", "own-human-in-a"}},
		{name: "creatorType=agent keeps agent-created volumes", query: "?creatorType=agent", wantNames: []string{"own-agent", "own-agent-in-a"}},
		{name: "creatorType=human keeps human-created volumes", query: "?creatorType=human", wantNames: []string{"own-human-in-a"}},
		{name: "sessionId keeps the caller's volumes in that session", query: "?sessionId=sess-a", wantNames: []string{"own-agent-in-a", "own-human-in-a"}},
		{name: "creatorType and sessionId both apply", query: "?creatorType=agent&sessionId=sess-a", wantNames: []string{"own-agent-in-a"}},
		{name: "session without volumes lists none", query: "?sessionId=sess-none", wantNames: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, volSvc := newTestGatewayWithVolumes(t)
			seedVolume(t, volSvc, volume.CreateOptions{Name: "own-human-in-a", UserID: "sam_dev_user", CreatorType: volume.CallerHuman}, "sess-a")
			seedVolume(t, volSvc, volume.CreateOptions{Name: "own-agent", UserID: "sam_dev_user", CreatorType: volume.CallerAgent})
			seedVolume(t, volSvc, volume.CreateOptions{Name: "own-agent-in-a", UserID: "sam_dev_user", CreatorType: volume.CallerAgent}, "sess-a")
			seedVolume(t, volSvc, volume.CreateOptions{Name: "other-in-a", UserID: "other_user", CreatorType: volume.CallerHuman}, "sess-a")

			resp := gw.get(t, "/api/v1/volumes"+tc.query)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var result struct {
				Data []struct {
					Name string `json:"name"`
				} `json:"data"`
			}
			decodeRespJSON(t, resp, &result)
			if result.Data == nil {
				t.Fatal("data is null, want an array")
			}
			gotNames := make([]string, 0, len(result.Data))
			for _, v := range result.Data {
				gotNames = append(gotNames, v.Name)
			}
			slices.Sort(gotNames)
			if diff := cmp.Diff(tc.wantNames, gotNames); diff != "" {
				t.Errorf("volume names (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_VolumeListPagination(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantLen  int
		wantMeta string
	}{
		{
			name:     "first page is full and links to the next",
			query:    "?pageSize=2&pageNumber=1",
			wantLen:  2,
			wantMeta: `{"pagination":{"pageNumber":1,"pageSize":2,"totalPages":2,"count":3,"nextPage":2}}`,
		},
		{
			name:     "last page holds the remainder and has no next",
			query:    "?pageSize=2&pageNumber=2",
			wantLen:  1,
			wantMeta: `{"pagination":{"pageNumber":2,"pageSize":2,"totalPages":2,"count":3,"nextPage":null}}`,
		},
		{
			name:     "page past the end is empty",
			query:    "?pageSize=2&pageNumber=3",
			wantLen:  0,
			wantMeta: `{"pagination":{"pageNumber":3,"pageSize":2,"totalPages":2,"count":3,"nextPage":null}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, volSvc := newTestGatewayWithVolumes(t)
			for _, name := range []string{"v1", "v2", "v3"} {
				seedVolume(t, volSvc, volume.CreateOptions{Name: name, UserID: "sam_dev_user", CreatorType: volume.CallerHuman})
			}

			resp := gw.get(t, "/api/v1/volumes"+tc.query)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			var result struct {
				Data []json.RawMessage `json:"data"`
				Meta any               `json:"meta"`
			}
			decodeRespJSON(t, resp, &result)
			if result.Data == nil || len(result.Data) != tc.wantLen {
				t.Errorf("data = %v, want an array of %d", result.Data, tc.wantLen)
			}
			if diff := cmp.Diff(decodeJSONText(t, tc.wantMeta), result.Meta); diff != "" {
				t.Errorf("meta (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIntegration_VolumeOfAnotherUserIsNotFound(t *testing.T) {
	tests := []struct {
		name, method string
	}{
		{name: "get", method: http.MethodGet},
		{name: "delete", method: http.MethodDelete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, volSvc := newTestGatewayWithVolumes(t)
			id := seedVolume(t, volSvc, volume.CreateOptions{Name: "private", UserID: "other_user", CreatorType: volume.CallerHuman})

			resp := gw.send(t, tc.method, "/api/v1/volumes/"+id, "")
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status %d, want 404; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			assertErrorBody(t, resp, "volume not found")
			if _, err := volSvc.Get(context.Background(), id); err != nil {
				t.Errorf("other user's volume after %s: %v, want it kept", tc.method, err)
			}
		})
	}
}

func TestIntegration_VolumeUnknownIDIsNotFound(t *testing.T) {
	tests := []struct {
		name, method string
	}{
		{name: "get", method: http.MethodGet},
		{name: "delete", method: http.MethodDelete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, _ := newTestGatewayWithVolumes(t)
			resp := gw.send(t, tc.method, "/api/v1/volumes/"+idgen.New(), "")
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status %d, want 404; body: %s", resp.StatusCode, bodyString(t, resp))
			}
			assertErrorBody(t, resp, "volume not found")
		})
	}
}

func TestIntegration_VolumeDelete(t *testing.T) {
	gw, volSvc := newTestGatewayWithVolumes(t)
	id := seedVolume(t, volSvc, volume.CreateOptions{Name: "delete-me", UserID: "sam_dev_user", CreatorType: volume.CallerHuman})

	resp := gw.delete(t, "/api/v1/volumes/"+id)
	if body := bodyString(t, resp); resp.StatusCode != http.StatusNoContent || body != "" {
		t.Fatalf("delete: status %d body %q, want 204 and no body", resp.StatusCode, body)
	}
	resp = gw.get(t, "/api/v1/volumes/"+id)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: status %d, want 404", resp.StatusCode)
	}
	assertErrorBody(t, resp, "volume not found")
}

func TestIntegration_VolumeDeleteLocked(t *testing.T) {
	gw, volSvc := newTestGatewayWithVolumes(t)
	id := seedVolume(t, volSvc, volume.CreateOptions{Name: "locked-vol", UserID: "sam_dev_user", CreatorType: volume.CallerHuman})
	if err := volSvc.AcquireWriteLock(context.Background(), id, "agent-a", 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	resp := gw.delete(t, "/api/v1/volumes/"+id)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409; body: %s", resp.StatusCode, bodyString(t, resp))
	}
	var body ErrorResponse
	decodeRespJSON(t, resp, &body)
	if !strings.HasPrefix(body.Message, "volume is locked: ") || body.ErrorID == "" {
		t.Errorf("error body = %+v, want message starting %q and an errorId", body, "volume is locked: ")
	}
	if _, err := volSvc.Get(context.Background(), id); err != nil {
		t.Errorf("locked volume after delete: %v, want it kept", err)
	}
}

func newTestGatewayWithLLM(t *testing.T, llmClient llm.Client) *testGateway {
	t.Helper()
	return buildTestGateway(t, testGatewayOptions{llm: llmClient})
}

func strPtr(s string) *string {
	return &s
}
