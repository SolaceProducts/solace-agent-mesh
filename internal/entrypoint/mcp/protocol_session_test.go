// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var legacyClient = []mcpclient.ClientOption{mcpclient.WithProtocolVersion(mcplib.LATEST_LEGACY_PROTOCOL_VERSION)}

const sessionIDPrefix = sessionIDArg + ": "

type conversationGateway struct {
	a         *Adapter
	brk       *broker.DevBroker
	queue     broker.Queue
	url       string
	artifacts *artifact.ScopedService
	db        *store.DB
	logged    *lockedBuffer
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) takeLines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(b.buf.String()), "\n")
	b.buf.Reset()
	return lines
}

func newConversationGateway(t *testing.T) *conversationGateway {
	t.Helper()
	artifacts := &artifact.ScopedService{Inner: artmem.New()}
	a, brk := newDispatchingToolRegistryAdapter(t, runtime.WithArtifacts(artifacts))
	logged := &lockedBuffer{}
	a.logger = slog.New(slog.NewTextHandler(logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	db := store.TestDB(t)
	a.sessKeys = store.NewGatewaySessionKeyRepo(db)
	a.sessions = store.NewSessionRepo(db)
	a.links = store.NewMCPArtifactLinkRepo(db)
	a.buildMCPServer()
	card := cardWithDisplayName(alphaCardName, "Support Desk", "Search")
	a.registerAgentTools(card)
	q, err := brk.CreateQueue("mcp-session-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic(dispatchTestNamespace, card.Name)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	a.httpServer = server.NewStreamableHTTPServer(a.mcpServer)
	srv := httptest.NewServer(a.buildHTTPHandler())
	t.Cleanup(srv.Close)
	return &conversationGateway{a: a, brk: brk, queue: q, url: srv.URL, artifacts: artifacts, db: db, logged: logged}
}

const searchTool = alphaPrefix + "_search"

func TestEachProtocolEraKeepsAConversationAcrossToolCalls(t *testing.T) {
	for name, tc := range map[string]struct {
		opts        []mcpclient.ClientOption
		wantVersion string
		passHandle  bool
		wantSame    bool
	}{
		"legacy client":                        {opts: legacyClient, wantVersion: mcplib.LATEST_LEGACY_PROTOCOL_VERSION, wantSame: true},
		"modern client passing the handle":     {wantVersion: mcplib.LATEST_PROTOCOL_VERSION, passHandle: true, wantSame: true},
		"modern client leaving the handle out": {wantVersion: mcplib.LATEST_PROTOCOL_VERSION, wantSame: false},
	} {
		t.Run(name, func(t *testing.T) {
			gw := newConversationGateway(t)
			c := startMCPClient(t, gw.url, tc.opts...)
			if got := c.ProtocolVersion(); got != tc.wantVersion {
				t.Fatalf("client negotiated protocol %q, want %q", got, tc.wantVersion)
			}

			first, result := callAndAnswer(t, gw, c, searchTool, nil)
			args := map[string]any{}
			if tc.passHandle {
				args[sessionIDArg] = handleIn(t, result)
			}
			second, _ := callAndAnswer(t, gw, c, searchTool, args)
			if (first == second) != tc.wantSame {
				t.Errorf("calls ran in SAM sessions %q and %q, want same=%v", first, second, tc.wantSame)
			}
		})
	}
}

func TestOnlyModernClientsSeeTheConversationHandle(t *testing.T) {
	for name, tc := range map[string]struct {
		opts       []mcpclient.ClientOption
		wantHandle bool
	}{
		"legacy client": {opts: legacyClient, wantHandle: false},
		"modern client": {wantHandle: true},
	} {
		t.Run(name, func(t *testing.T) {
			gw := newConversationGateway(t)
			c := startMCPClient(t, gw.url, tc.opts...)

			listed, err := c.ListTools(t.Context(), mcplib.ListToolsRequest{})
			if err != nil {
				t.Fatalf("tools/list: %v", err)
			}
			if len(listed.Tools) != 1 {
				t.Fatalf("listed %d tools, want 1", len(listed.Tools))
			}
			if _, offered := listed.Tools[0].InputSchema.Properties[sessionIDArg]; offered != tc.wantHandle {
				t.Errorf("tool offers %s: %v, want %v", sessionIDArg, offered, tc.wantHandle)
			}

			sessionID, result := callAndAnswer(t, gw, c, searchTool, nil)
			if !tc.wantHandle {
				if len(result.Content) != 1 || mcplib.GetTextFromContent(result.Content[0]) != "found it" {
					t.Errorf("result content = %+v, want only the agent's answer", result.Content)
				}
				return
			}
			handle := handleIn(t, result)
			mapped, err := gw.a.sessKeys.GetByGatewayAndExternalKeys(t.Context(), store.GatewayTypeMCP, []string{handle})
			if err != nil {
				t.Fatalf("look up handle: %v", err)
			}
			if mapped[handle] != sessionID {
				t.Errorf("handle %q names SAM session %q, want the call's session %q", handle, mapped[handle], sessionID)
			}
		})
	}
}

func TestModernCallsRefuseAHandleThatIsNotTheCallersGatewayConversation(t *testing.T) {
	gw := newConversationGateway(t)
	caller := gw.a.defaultIdentity().UserID()
	mcpHandleOf := func(user string) string {
		t.Helper()
		handle := idgen.New()
		if _, err := gw.a.sessKeys.GetOrCreateByExternalKey(t.Context(), store.GetOrCreateByExternalKeyArgs{
			GatewayType: store.GatewayTypeMCP, ExternalKey: handle, UserID: user,
		}); err != nil {
			t.Fatalf("create session: %v", err)
		}
		return handle
	}
	slackSessionOf := func(user string) string {
		t.Helper()
		id, err := gw.a.sessKeys.GetOrCreateByExternalKey(t.Context(), store.GetOrCreateByExternalKeyArgs{
			GatewayType: store.GatewayTypeSlack, ExternalKey: "C1:T1", UserID: user,
		})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		return id
	}
	deletedHandle := mcpHandleOf(caller)
	deleted, err := gw.a.sessKeys.GetByGatewayAndExternalKeys(t.Context(), store.GatewayTypeMCP, []string{deletedHandle})
	if err != nil {
		t.Fatalf("look up handle: %v", err)
	}
	if _, err := gw.a.sessions.SoftDelete(t.Context(), deleted[deletedHandle], caller); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	inDeletedProjectHandle := inDeletedProject(t, gw, caller, mcpHandleOf(caller))

	for name, tc := range map[string]struct {
		handle string
		level  string
		reason string
	}{
		"another user's MCP conversation":                {handle: mcpHandleOf("someone-else"), level: "WARN", reason: "not_owner"},
		"the caller's deleted conversation":              {handle: deletedHandle, level: "DEBUG", reason: "not_found"},
		"the caller's conversation in a deleted project": {handle: inDeletedProjectHandle, level: "DEBUG", reason: "deleted"},
		"the caller's Slack session id":                  {handle: slackSessionOf(caller), level: "DEBUG", reason: "not_found"},
		"a handle this gateway never handed":             {handle: "0199f0a0-0000-7000-8000-000000000001", level: "DEBUG", reason: "not_found"},
		"not a handle":                                   {handle: "../../etc/passwd", level: "DEBUG", reason: "malformed"},
	} {
		t.Run(name, func(t *testing.T) {
			c := startMCPClient(t, gw.url)
			gw.logged.takeLines()
			result, err := c.CallTool(t.Context(), mcplib.CallToolRequest{Params: mcplib.CallToolParams{
				Name:      searchTool,
				Arguments: map[string]any{"message": "find the runbook", sessionIDArg: tc.handle},
			}})
			if err != nil {
				t.Fatalf("tools/call: %v", err)
			}
			if !result.IsError || len(result.Content) != 1 || mcplib.GetTextFromContent(result.Content[0]) != unknownSessionIDMessage {
				t.Fatalf("result = %+v, want the unknown-sessionId refusal", result)
			}
			line := refusalLine(t, gw.logged.takeLines())
			if got := logFieldValue(t, line, "level"); got != tc.level {
				t.Errorf("refusal logged at %s, want %s", got, tc.level)
			}
			if got := logFieldValue(t, line, "reason"); got != tc.reason {
				t.Errorf("refusal reason %q, want %q", got, tc.reason)
			}
			gw.a.tasksMu.Lock()
			defer gw.a.tasksMu.Unlock()
			if len(gw.a.tasks) != 0 {
				t.Errorf("a task was started for a refused handle")
			}
		})
	}
}

func inDeletedProject(t *testing.T, gw *conversationGateway, user, handle string) string {
	t.Helper()
	mapped, err := gw.a.sessKeys.GetByGatewayAndExternalKeys(t.Context(), store.GatewayTypeMCP, []string{handle})
	if err != nil {
		t.Fatalf("look up handle: %v", err)
	}
	projects := store.NewProjectRepo(gw.db)
	project := &store.Project{Name: "runbooks", UserID: user}
	if err := projects.Create(t.Context(), project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := gw.a.sessions.AssignProject(t.Context(), mapped[handle], project.ID); err != nil {
		t.Fatalf("assign project: %v", err)
	}
	if err := projects.SoftDeleteSessionsByProject(t.Context(), project.ID, user); err != nil {
		t.Fatalf("delete project sessions: %v", err)
	}
	return handle
}

func refusalLine(t *testing.T, lines []string) string {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, logSessionIDRefused) {
			return line
		}
	}
	t.Fatalf("no %q line was logged; log was:\n%s", logSessionIDRefused, strings.Join(lines, "\n"))
	return ""
}

func TestEachProtocolEraReadsTheArtifactLinkItsToolCallReturns(t *testing.T) {
	for name, tc := range map[string]struct {
		opts         []mcpclient.ClientOption
		filename     string
		wantPrefix   string
		noStore      bool
		wantEmbedded bool
	}{
		"legacy client":                        {opts: legacyClient, filename: "report.pdf"},
		"legacy client, name with spaces":      {opts: legacyClient, filename: "q3 report (final).pdf"},
		"modern client":                        {filename: "report.pdf", wantPrefix: "artifact://"},
		"modern client, name with spaces":      {filename: "q3 report (final).pdf", wantPrefix: "artifact://"},
		"modern client, name in CJK":           {filename: "季度报告.pdf", wantPrefix: "artifact://"},
		"modern client, no conversation store": {filename: "report.pdf", noStore: true, wantEmbedded: true},
	} {
		t.Run(name, func(t *testing.T) {
			gw := newConversationGateway(t)
			gw.a.cfg.InlineBinaryMaxBytes = 4
			if tc.noStore {
				gw.a.sessKeys, gw.a.sessions = nil, nil
			}
			c := startMCPClient(t, gw.url, tc.opts...)
			content := []byte("%PDF-1.7 quarterly numbers")

			_, result := callAndAnswerWith(t, gw, c, searchTool, nil, func(taskID, sessionID string) {
				saveArtifact(t, gw.artifacts, alphaCardName, gw.a.defaultIdentity().UserID(), sessionID, tc.filename, content, "application/pdf")
				gw.a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(tc.filename, "application/pdf", 0), nil)
			})

			if tc.wantEmbedded {
				if decoded := embeddedBlobIn(t, result); !bytes.Equal(decoded, content) {
					t.Errorf("embedded artifact = %q, want %q", decoded, content)
				}
				return
			}
			link := resourceLinkIn(t, result)
			if tc.wantPrefix != "" {
				handle := handleIn(t, result)
				if want := tc.wantPrefix + handle + "/"; !strings.HasPrefix(link, want) {
					t.Errorf("modern link %q, want the template link starting %q", link, want)
				}
			}
			got, err := c.ReadResource(t.Context(), mcplib.ReadResourceRequest{Params: mcplib.ReadResourceParams{URI: link}})
			if err != nil {
				t.Fatalf("read %q: %v", link, err)
			}
			blob, ok := got.Contents[0].(mcplib.BlobResourceContents)
			if !ok {
				t.Fatalf("read %q as %+v, want blob contents", link, got.Contents)
			}
			if decoded, _ := base64.StdEncoding.DecodeString(blob.Blob); !bytes.Equal(decoded, content) {
				t.Errorf("read %q = %q, want %q", link, decoded, content)
			}
		})
	}
}

func TestTheArtifactTemplateReadsOnlyTheCallersConversation(t *testing.T) {
	gw := newConversationGateway(t)
	caller := gw.a.defaultIdentity().UserID()
	modern := startMCPClient(t, gw.url)
	var firstVersion int
	_, result := callAndAnswerWith(t, gw, modern, searchTool, nil, func(_, sessionID string) {
		loc := gw.artifacts.Resolve(alphaCardName, caller, sessionID, "report.pdf")
		var err error
		if firstVersion, err = gw.artifacts.Inner.Save(t.Context(), loc, []byte("%PDF first"), "application/pdf"); err != nil {
			t.Fatalf("save artifact: %v", err)
		}
		saveArtifact(t, gw.artifacts, alphaCardName, caller, sessionID, "report.pdf", []byte("%PDF latest"), "application/pdf")
		saveArtifact(t, gw.artifacts, alphaCardName, caller, sessionID, "user:notes.txt", []byte("persistent notes"), "text/plain")
		saveArtifact(t, gw.artifacts, alphaCardName, caller, sessionID, "empty.bin", []byte{}, "application/octet-stream")
	})
	handle := handleIn(t, result)
	link := func(sessionID, filename, query string) string {
		return "artifact://" + sessionID + "/" + filename + query
	}
	others := idgen.New()
	if _, err := gw.a.sessKeys.GetOrCreateByExternalKey(t.Context(), store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeMCP, ExternalKey: others, UserID: "someone-else",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	agent := "?agent=" + alphaCardName

	read := func(c *mcpclient.Client, uri string) (string, error) {
		got, err := c.ReadResource(t.Context(), mcplib.ReadResourceRequest{Params: mcplib.ReadResourceParams{URI: uri}})
		if err != nil {
			return "", err
		}
		blob, ok := got.Contents[0].(mcplib.BlobResourceContents)
		if !ok {
			t.Fatalf("read %q as %+v, want blob contents", uri, got.Contents)
		}
		decoded, _ := base64.StdEncoding.DecodeString(blob.Blob)
		return string(decoded), nil
	}
	for name, tc := range map[string]struct{ uri, want string }{
		"the latest version": {uri: link(handle, "report.pdf", agent), want: "%PDF latest"},
		"an earlier version": {uri: link(handle, "report.pdf", agent+"&version="+strconv.Itoa(firstVersion)), want: "%PDF first"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := read(modern, tc.uri)
			if err != nil {
				t.Fatalf("read %q: %v", tc.uri, err)
			}
			if got != tc.want {
				t.Errorf("read %q = %q, want %q", tc.uri, got, tc.want)
			}
		})
	}
	for name, uri := range map[string]string{
		"another user's conversation": link(others, "report.pdf", agent),
		"a user-scoped artifact":      link(handle, "user%3Anotes.txt", agent),
		"an empty artifact":           link(handle, "empty.bin", agent),
		"no agent":                    link(handle, "report.pdf", ""),
		"a version that is no number": link(handle, "report.pdf", agent+"&version=latest"),
		"a malformed handle":          link("not-a-handle", "report.pdf", agent),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := read(modern, uri); !errors.Is(err, mcplib.ErrInvalidParams) {
				t.Errorf("read %q answered %v, want the missing-resource refusal", uri, err)
			}
		})
	}
	t.Run("legacy client", func(t *testing.T) {
		if _, err := read(startMCPClient(t, gw.url, legacyClient...), link(handle, "report.pdf", agent)); !errors.Is(err, mcplib.ErrResourceNotFound) {
			t.Errorf("legacy client read a modern artifact link: %v, want it refused as before", err)
		}
	})
	t.Run("after the agent leaves discovery", func(t *testing.T) {
		gw.a.deregisterAgentTools(alphaCardName)
		if _, err := read(modern, link(handle, "report.pdf", agent)); err != nil {
			t.Errorf("read after the agent left: %v", err)
		}
	})
}

func embeddedBlobIn(t *testing.T, result *mcplib.CallToolResult) []byte {
	t.Helper()
	for _, c := range result.Content {
		if _, isLink := c.(mcplib.ResourceLink); isLink {
			t.Fatalf("result content %+v carries a resource link, want the artifact embedded", result.Content)
		}
		if embedded, ok := c.(mcplib.EmbeddedResource); ok {
			if blob, ok := embedded.Resource.(mcplib.BlobResourceContents); ok {
				decoded, _ := base64.StdEncoding.DecodeString(blob.Blob)
				return decoded
			}
		}
	}
	t.Fatalf("result content %+v embeds no blob", result.Content)
	return nil
}

func TestGatewayLogsTheProtocolVersionARequestAsksFor(t *testing.T) {
	for header, want := range map[string]string{
		mcplib.LATEST_PROTOCOL_VERSION:        mcplib.LATEST_PROTOCOL_VERSION,
		mcplib.LATEST_LEGACY_PROTOCOL_VERSION: mcplib.LATEST_LEGACY_PROTOCOL_VERSION,
		"2026-07-28\nforged=1":                "unknown",
	} {
		t.Run(want, func(t *testing.T) {
			logged := &bytes.Buffer{}
			a := &Adapter{
				cfg:    mustParseConfig(t, map[string]any{"transport": "sse"}),
				logger: slog.New(slog.NewTextHandler(logged, &slog.HandlerOptions{Level: slog.LevelDebug})),
			}
			h := a.assembleHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set(mcplib.HeaderProtocolVersion, header)
			h.ServeHTTP(httptest.NewRecorder(), req)

			line := logLineWith(t, logged, logRequestProtocolVersion)
			if got := logFieldValue(t, line, "requestedProtocolVersion"); got != want {
				t.Errorf("logged requestedProtocolVersion %q, want %q", got, want)
			}
		})
	}
}

func startMCPClient(t *testing.T, url string, opts ...mcpclient.ClientOption) *mcpclient.Client {
	t.Helper()
	tr, err := transport.NewStreamableHTTP(url)
	if err != nil {
		t.Fatalf("new transport: %v", err)
	}
	c := mcpclient.NewClient(tr, opts...)
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("start client: %v", err)
	}
	if _, err := c.Initialize(t.Context(), mcplib.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return c
}

func handleIn(t *testing.T, result *mcplib.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	text := mcplib.GetTextFromContent(result.Content[len(result.Content)-1])
	handle, ok := strings.CutPrefix(text, sessionIDPrefix)
	if !ok {
		t.Fatalf("last content block %q carries no %s", text, sessionIDArg)
	}
	return handle
}

func resourceLinkIn(t *testing.T, result *mcplib.CallToolResult) string {
	t.Helper()
	for _, c := range result.Content {
		if link, ok := c.(mcplib.ResourceLink); ok {
			return link.URI
		}
	}
	t.Fatalf("result content %+v carries no resource link", result.Content)
	return ""
}

func callAndAnswer(t *testing.T, gw *conversationGateway, c *mcpclient.Client, tool string, extra map[string]any) (string, *mcplib.CallToolResult) {
	t.Helper()
	return callAndAnswerWith(t, gw, c, tool, extra, nil)
}

func callAndAnswerWith(t *testing.T, gw *conversationGateway, c *mcpclient.Client, tool string, extra map[string]any, produce func(taskID, sessionID string)) (string, *mcplib.CallToolResult) {
	t.Helper()
	args := map[string]any{"message": "find the runbook"}
	for k, v := range extra {
		args[k] = v
	}
	type outcome struct {
		result *mcplib.CallToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := c.CallTool(t.Context(), mcplib.CallToolRequest{Params: mcplib.CallToolParams{Name: tool, Arguments: args}})
		done <- outcome{result, err}
	}()

	recvCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if received, _ := gw.brk.Receive(recvCtx, gw.queue); received == nil {
		t.Fatalf("tools/call %q never reached the agent", tool)
	}
	taskID, sessionID := inFlightSession(t, gw.a)
	if produce != nil {
		produce(taskID, sessionID)
	}
	gw.a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{
			State:   "completed",
			Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("found it")}},
		},
	}, nil)

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("tools/call: %v", got.err)
		}
		return sessionID, got.result
	case <-time.After(5 * time.Second):
		t.Fatal("tools/call never returned after the final response")
	}
	return "", nil
}

func inFlightSession(t *testing.T, a *Adapter) (taskID, sessionID string) {
	t.Helper()
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	if len(a.tasks) != 1 {
		t.Fatalf("in-flight tool calls = %d, want exactly 1", len(a.tasks))
	}
	for id, ts := range a.tasks {
		return id, ts.sessionID
	}
	return "", ""
}
