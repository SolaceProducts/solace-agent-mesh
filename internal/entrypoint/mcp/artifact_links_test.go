// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestLegacyArtifactLinkIsListedAndReadOnAnotherInstance(t *testing.T) {
	gw := newConversationGateway(t)
	gw.a.cfg.InlineBinaryMaxBytes = 4
	otherURL := newOtherInstance(t, gw)

	tr, err := transport.NewStreamableHTTP(gw.url)
	if err != nil {
		t.Fatalf("new transport: %v", err)
	}
	c := mcpclient.NewClient(tr, legacyClient...)
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Start(t.Context()); err != nil {
		t.Fatalf("start client: %v", err)
	}
	if _, err := c.Initialize(t.Context(), mcplib.InitializeRequest{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	const filename = "q3 report (final).pdf"
	content := []byte("%PDF-1.7 quarterly numbers")
	_, result := callAndAnswerWith(t, gw, c, searchTool, nil, func(taskID, sessionID string) {
		saveArtifact(t, gw.artifacts, alphaCardName, gw.a.defaultIdentity().UserID(), sessionID, filename, content, "application/pdf")
		gw.a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "application/pdf", 0), nil)
	})
	link := resourceLinkIn(t, result)

	var listed mcplib.ListResourcesResult
	postOnSession(t, otherURL, tr.GetSessionId(), "resources/list", map[string]any{}, &listed)
	if len(listed.Resources) != 1 || listed.Resources[0].URI != link {
		t.Fatalf("other instance listed %+v, want only %q", listed.Resources, link)
	}

	var read struct {
		Contents []struct {
			URI  string `json:"uri"`
			Blob string `json:"blob"`
		} `json:"contents"`
	}
	postOnSession(t, otherURL, tr.GetSessionId(), "resources/read", map[string]any{"uri": link}, &read)
	if len(read.Contents) != 1 {
		t.Fatalf("other instance read %+v, want one blob", read.Contents)
	}
	if decoded, _ := base64.StdEncoding.DecodeString(read.Contents[0].Blob); !bytes.Equal(decoded, content) {
		t.Errorf("other instance read %q, want %q", decoded, content)
	}

	var otherSession mcplib.ListResourcesResult
	postOnSession(t, otherURL, (&server.StatelessGeneratingSessionIdManager{}).Generate(), "resources/list", map[string]any{}, &otherSession)
	if len(otherSession.Resources) != 0 {
		t.Errorf("another MCP session listed %+v, want none", otherSession.Resources)
	}
}

func TestLegacyArtifactIsEmbeddedWhenNoLinkCanBeRecorded(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	a.cfg.InlineBinaryMaxBytes = 4
	a.mcpServer = server.NewMCPServer("t", "1.0.0", server.WithResourceCapabilities(true, true))
	saveArtifact(t, scoped, "Ag", "u", "s-reg", "big.bin", []byte("larger than four"), "application/octet-stream")
	ts := newTrackedTask(t, a, "task-reg", "Ag", "u", "s-reg")
	ts.mcpSessionID = "mcp-session-xyz"

	a.OnStatusUpdate(t.Context(), "task-reg", artifactCompletedEvent("big.bin", "application/octet-stream", 0), nil)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.contents) != 1 {
		t.Fatalf("contents %+v, want one", ts.contents)
	}
	if _, ok := ts.contents[0].(mcplib.EmbeddedResource); !ok {
		t.Fatalf("expected the artifact embedded, got %#v", ts.contents[0])
	}
}

func TestHoldsTaskOnlyWhileACallWaits(t *testing.T) {
	a, _ := newArtifactAdapter(t)
	newTrackedTask(t, a, "task-held", "Ag", "u", "s")
	if !a.HoldsTask("task-held") {
		t.Fatal("a waiting call's task is not held")
	}
	a.OnError(t.Context(), "task-held", &a2a.Error{Message: "failed"}, nil)
	if a.HoldsTask("task-held") {
		t.Fatal("the task is still held after its call ended")
	}
}

func TestMCPArtifactLinksGoWithTheirSessionAndByAge(t *testing.T) {
	db := store.TestDB(t)
	links := store.NewMCPArtifactLinkRepo(db)
	put := func(mcpSession, uri, samSession string) {
		t.Helper()
		if err := links.Put(t.Context(), store.MCPArtifactLink{
			MCPSessionID: mcpSession, URI: uri, AgentName: "Ag", UserID: "u", SessionID: samSession, Filename: "f",
		}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	sam1, sam2 := idgen.New(), idgen.New()
	sessions := store.NewSessionRepo(db)
	if err := sessions.Create(t.Context(), &store.Session{ID: sam1, UserID: "u"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	put("mcp-a", "artifact://"+sam1+"/a.pdf", sam1)
	put("mcp-a", "artifact://"+sam2+"/b.pdf", sam2)

	if _, err := sessions.SoftDelete(t.Context(), sam1, "u"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	left, err := links.ListBySession(t.Context(), "mcp-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(left) != 1 || left[0].SessionID != sam2 {
		t.Fatalf("after deleting the first session the MCP session lists %+v, want only the second session's link", left)
	}

	deleted, err := links.DeleteOlderThan(t.Context(), time.Now().Add(time.Minute), 1)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteOlderThan = %d, %v; want 1 link deleted", deleted, err)
	}
}

func newOtherInstance(t *testing.T, gw *conversationGateway) string {
	t.Helper()
	a, _ := newDispatchingToolRegistryAdapter(t, runtime.WithArtifacts(gw.artifacts))
	a.sessKeys = store.NewGatewaySessionKeyRepo(gw.db)
	a.sessions = store.NewSessionRepo(gw.db)
	a.links = store.NewMCPArtifactLinkRepo(gw.db)
	a.buildMCPServer()
	a.httpServer = server.NewStreamableHTTPServer(a.mcpServer)
	srv := httptest.NewServer(a.buildHTTPHandler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func postOnSession(t *testing.T, url, sessionID, method string, params map[string]any, out any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(server.HeaderKeySessionID, sessionID)
	req.Header.Set(server.HeaderKeyProtocolVersion, mcplib.LATEST_LEGACY_PROTOCOL_VERSION)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw := readRPCBody(t, resp)
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("%s answered %q: %v", method, raw, err)
	}
	if envelope.Error != nil {
		t.Fatalf("%s failed: %s", method, envelope.Error.Message)
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		t.Fatalf("%s result %q: %v", method, envelope.Result, err)
	}
}

func readRPCBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
		return buf.Bytes()
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
			return []byte(data)
		}
	}
	t.Fatalf("event stream carried no data: %v", scanner.Err())
	return nil
}
