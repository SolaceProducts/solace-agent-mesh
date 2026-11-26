// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIPCServer_LLMCall(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	var gotComponent string
	llmCall := func(_ context.Context, componentName, systemPrompt, userPrompt string, temperature float64) (string, error) {
		gotComponent = componentName
		return fmt.Sprintf("response to: %s (temp=%.1f)", userPrompt, temperature), nil
	}

	server, err := NewIPCServer(socketPath, llmCall, "pptx_create_presentation", slog.Default())
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go server.Serve(ctx)

	time.Sleep(50 * time.Millisecond)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "llm/call",
		"params": map[string]any{
			"system_prompt": "You are helpful",
			"user_prompt":   "Hello",
			"temperature":   0.7,
		},
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, writeErr := conn.Write(data); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	reader := bufio.NewReader(conn)
	line, readErr := reader.ReadBytes('\n')
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}

	var resp ipcResponse
	if unmarshalErr := json.Unmarshal(line, &resp); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.ID != "1" {
		t.Errorf("id: got %q, want '1'", resp.ID)
	}

	resultData, _ := json.Marshal(resp.Result)
	var result llmCallResult
	if umErr := json.Unmarshal(resultData, &result); umErr != nil {
		t.Fatalf("unmarshal result: %v", umErr)
	}

	expected := "response to: Hello (temp=0.7)"
	if result.Text != expected {
		t.Errorf("text: got %q, want %q", result.Text, expected)
	}

	if gotComponent != "pptx_create_presentation" {
		t.Errorf("componentName forwarded to llmCall = %q, want pptx_create_presentation", gotComponent)
	}
}

func TestIPCServer_LLMError(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	llmCall := func(_ context.Context, _, _, _ string, _ float64) (string, error) {
		return "", fmt.Errorf("model unavailable")
	}

	server, err := NewIPCServer(socketPath, llmCall, "test_tool", slog.Default())
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go server.Serve(ctx)
	time.Sleep(50 * time.Millisecond)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "2",
		"method":  "llm/call",
		"params": map[string]any{
			"system_prompt": "",
			"user_prompt":   "test",
			"temperature":   0.0,
		},
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}

	reader := bufio.NewReader(conn)
	line, _ := reader.ReadBytes('\n')

	var resp ipcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != -32000 {
		t.Errorf("error code: got %d, want -32000", resp.Error.Code)
	}
}

func TestIPCServer_UnknownMethod(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	server, err := NewIPCServer(socketPath, nil, "", slog.Default())
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go server.Serve(ctx)
	time.Sleep(50 * time.Millisecond)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "3",
		"method":  "unknown/method",
		"params":  map[string]any{},
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}

	reader := bufio.NewReader(conn)
	line, _ := reader.ReadBytes('\n')

	var resp ipcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code: got %d, want -32601", resp.Error.Code)
	}
}

func TestIPCServer_SocketCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "ipc.sock")

	server, err := NewIPCServer(socketPath, nil, "", slog.Default())
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket file should exist: %v", err)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("socket file should be removed after Close")
	}
}
