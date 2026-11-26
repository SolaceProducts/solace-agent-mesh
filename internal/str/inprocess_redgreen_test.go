// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	sqltool "github.com/SolaceProducts/solace-agent-mesh/tools/go/sql/sqltool"
)

func TestInProcessSQLConnector_RedGreen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dtp_demo.db")
	seedSQLiteFixture(t, dbPath)
	connStr := "sqlite:///" + dbPath

	const connToolName = "dtp_demo_sql_query_019eb14a"
	toolConfig := map[string]any{
		"_str_binary":        "execute_sql_query",
		"connection_string":  connStr,
		"tool_name":          connToolName,
		"auto_detect_schema": false,
	}

	t.Run("RED_without_inprocess_registration", func(t *testing.T) {
		brk := newTestBroker(t)
		w := NewWorker(&testServices{brk: brk}, WorkerConfig{
			WorkerID: "red", Namespace: "test-ns", Sandbox: SandboxConfig{Mode: "direct"},
		})
		if err := w.Init(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		if w.manifest.GetTool("execute_sql_query") != nil {
			t.Fatal("RED precondition broken: execute_sql_query should not be in the manifest without registration")
		}
		if reply := requestInitDirect(t, w, brk, connToolName, toolConfig); reply != nil {
			t.Fatalf("RED: expected init_request to be silently dropped, got reply: %s", reply)
		}
	})

	t.Run("GREEN_with_inprocess_registration", func(t *testing.T) {
		brk := newTestBroker(t)
		w := NewWorker(&testServices{brk: brk}, WorkerConfig{
			WorkerID: "green", Namespace: "test-ns", Sandbox: SandboxConfig{Mode: "direct"},
			InProcessTools: sqltool.Tools(),
		})
		if err := w.Init(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		if w.manifest.GetTool("execute_sql_query") == nil {
			t.Fatal("GREEN: execute_sql_query missing from manifest after in-process registration")
		}

		reply := requestInitDirect(t, w, brk, connToolName, toolConfig)
		if reply == nil {
			t.Fatal("GREEN: expected an init_request reply, got none")
		}
		var initResp struct {
			Result remotetool.InitParams `json:"result"`
		}
		if err := json.Unmarshal(reply, &initResp); err != nil {
			t.Fatalf("unmarshal init reply: %v", err)
		}
		if initResp.Result.ToolName != connToolName {
			t.Fatalf("init reply ToolName = %q, want %q", initResp.Result.ToolName, connToolName)
		}

		rows := invokeSQLDirect(t, w, brk, connToolName, toolConfig, "SELECT id, name FROM widgets ORDER BY id")
		if len(rows) != 2 {
			t.Fatalf("query returned %d rows, want 2: %+v", len(rows), rows)
		}
		if got := fmt.Sprintf("%v", rows[0]["name"]); got != "alpha" {
			t.Fatalf("row[0].name = %q, want alpha", got)
		}
		if got := fmt.Sprintf("%v", rows[1]["name"]); got != "beta" {
			t.Fatalf("row[1].name = %q, want beta", got)
		}
	})
}

func TestInProcessTool_ArtifactRoundTrip(t *testing.T) {
	const toolName = "make_report"
	const outName = "report.txt"
	want := []byte("hello from in-process tool")

	td := samtoolsdk.NewTool(toolName, "Emit a file artifact.",
		func(_ context.Context, _ struct {
			N string `json:"n"`
		}, _ *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			return samtoolsdk.OK("done", samtoolsdk.WithDataObjects(samtoolsdk.DataObject{
				Name:        outName,
				MIMEType:    "text/plain",
				Disposition: samtoolsdk.DispositionArtifact,
				Content:     want,
			})), nil
		})

	brk := newTestBroker(t)
	store := memory.New()
	artSvc := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-ns"}
	w := NewWorker(&testServices{brk: brk, artifacts: artSvc}, WorkerConfig{
		WorkerID: "art", Namespace: "test-ns", Sandbox: SandboxConfig{Mode: "direct"},
		InProcessTools: []*samtoolsdk.ToolDef{td},
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	entry := w.manifest.GetTool(toolName)
	if entry == nil {
		t.Fatalf("tool %q not registered", toolName)
	}

	result, invokeErr := w.executeInProcessTool(t.Context(),
		&remotetool.InvokeParams{
			ToolName:  toolName,
			TaskID:    "task-art",
			AppName:   "agent",
			UserID:    "user",
			SessionID: "sess",
			Args:      map[string]any{"n": "x"},
		}, entry, artSvc, func(string) {})
	if invokeErr != nil {
		t.Fatalf("invoke error: %s %s", invokeErr.Code, invokeErr.Message)
	}
	if len(result.CreatedArtifacts) == 0 {
		t.Fatalf("no CreatedArtifacts — output artifact was dropped: %+v", result.ToolResult)
	}
	found := false
	for _, ca := range result.CreatedArtifacts {
		if ca.Filename == outName {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected artifact %q in CreatedArtifacts, got %+v", outName, result.CreatedArtifacts)
	}

	loc := artSvc.Resolve("agent", "user", "sess", outName)
	got, _, err := artSvc.Inner.Load(t.Context(), loc, nil)
	if err != nil {
		t.Fatalf("load saved artifact: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("stored artifact content = %q, want %q", got, want)
	}
}

func TestInProcessTool_ForwardsUserProfile(t *testing.T) {
	const toolName = "whoami"
	td := samtoolsdk.NewTool(toolName, "Report the caller's country.",
		func(_ context.Context, _ struct{}, tc *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			country, _ := tc.UserProfile()["country"].(string)
			return samtoolsdk.OK("ok", samtoolsdk.WithData(map[string]any{"country": country})), nil
		})

	brk := newTestBroker(t)
	artSvc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "test-ns"}
	w := NewWorker(&testServices{brk: brk, artifacts: artSvc}, WorkerConfig{
		WorkerID: "profile", Namespace: "test-ns", Sandbox: SandboxConfig{Mode: "direct"},
		InProcessTools: []*samtoolsdk.ToolDef{td},
	})
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	entry := w.manifest.GetTool(toolName)
	if entry == nil {
		t.Fatalf("tool %q not registered", toolName)
	}

	result, invokeErr := w.executeInProcessTool(t.Context(),
		&remotetool.InvokeParams{
			ToolName:      toolName,
			TaskID:        "task-profile",
			AppName:       "agent",
			UserID:        "user",
			SessionID:     "sess",
			Args:          map[string]any{},
			A2AUserConfig: `{"user_profile":{"id":"user","country":"Netherlands"}}`,
		}, entry, artSvc, func(string) {})
	if invokeErr != nil {
		t.Fatalf("invoke error: %s %s", invokeErr.Code, invokeErr.Message)
	}

	raw, err := json.Marshal(result.ToolResult)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if got := wire.Data["country"]; got != "Netherlands" {
		t.Fatalf("country = %v, want Netherlands (profile not forwarded to the in-process tool): %s", got, raw)
	}
}

func seedSQLiteFixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	stmts := []string{
		`CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO widgets (id, name) VALUES (1, 'alpha'), (2, 'beta')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed sqlite (%s): %v", s, err)
		}
	}
}

func requestInitDirect(t *testing.T, w *Worker, brk broker.Broker, toolName string, toolConfig map[string]any) json.RawMessage {
	t.Helper()
	replyTopic := fmt.Sprintf("test-ns/reply/init/%s/%d", toolName, time.Now().UnixNano())
	replyQ, err := brk.CreateQueue(replyTopic, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(replyQ, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	req := a2a.NewRequest(fmt.Sprintf("init-%d", time.Now().UnixNano()),
		remotetool.MethodInitRequest,
		remotetool.InitRequestParams{ToolName: toolName, ToolConfig: toolConfig})
	payload, _ := json.Marshal(req)
	msg := broker.NewDeliveredMessage("init-msg", remotetool.InitRequestTopic("test-ns", toolName), payload, func() {}, nil)
	msg.UserProperties = map[string]string{
		remotetool.PropReplyTo:  replyTopic,
		remotetool.PropClientID: "test-agent",
	}

	w.handleInitRequest(t.Context(), msg)

	recvCtx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()
	resp, _ := brk.Receive(recvCtx, replyQ)
	if resp == nil {
		return nil
	}
	resp.Ack()
	return resp.Payload
}

func invokeSQLDirect(t *testing.T, w *Worker, brk broker.Broker, toolName string, toolConfig map[string]any, query string) []map[string]any {
	t.Helper()
	entry := w.manifest.GetTool(toolName)
	if entry == nil {
		t.Fatalf("tool %q not resolvable after init_request", toolName)
	}

	replyTopic := fmt.Sprintf("test-ns/reply/invoke/%s/%d", toolName, time.Now().UnixNano())
	replyQ, err := brk.CreateQueue(replyTopic, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(replyQ, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}

	reqID := fmt.Sprintf("invoke-%d", time.Now().UnixNano())
	params := remotetool.InvokeParams{
		ToolName:   toolName,
		TaskID:     "task-redgreen",
		SessionID:  "sess-redgreen",
		Args:       map[string]any{"query": query},
		ToolConfig: toolConfig,
	}
	req := a2a.NewRequest(reqID, remotetool.MethodInvoke, params)
	payload, _ := json.Marshal(req)
	msg := broker.NewDeliveredMessage("invoke-msg", remotetool.InvokeTopic("test-ns", toolName), payload, func() {}, nil)
	msg.UserProperties = map[string]string{
		remotetool.PropReplyTo:  replyTopic,
		remotetool.PropClientID: "test-agent",
		remotetool.PropUserID:   "test-user",
	}

	w.handleInvocation(t.Context(), msg, entry)

	recvCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	resp, recvErr := brk.Receive(recvCtx, replyQ)
	if recvErr != nil || resp == nil {
		t.Fatalf("no invoke response: %v", recvErr)
	}
	resp.Ack()

	var out struct {
		Result *remotetool.InvokeResult `json:"result"`
		Error  *remotetool.InvokeError  `json:"error"`
	}
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		t.Fatalf("unmarshal invoke response: %v", err)
	}
	if out.Error != nil {
		t.Fatalf("invoke returned error: %s %s", out.Error.Code, out.Error.Message)
	}
	if out.Result == nil {
		t.Fatal("invoke returned no result")
	}

	dataObjs, _ := out.Result.ToolResult["data_objects"].([]any)
	if len(dataObjs) == 0 {
		return nil
	}
	obj, _ := dataObjs[0].(map[string]any)
	content, _ := obj["content"].(string)
	if content == "" {
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(content), &rows); err != nil {
		t.Fatalf("unmarshal data_object content: %v", err)
	}
	return rows
}
