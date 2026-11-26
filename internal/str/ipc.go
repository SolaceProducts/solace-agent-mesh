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
)

type LLMCallFunc func(ctx context.Context, componentName, systemPrompt, userPrompt string, temperature float64) (string, error)

type IPCServer struct {
	socketPath    string
	listener      net.Listener
	logger        *slog.Logger
	llmCall       LLMCallFunc
	llmChat       LLMChatFunc
	componentName string
}

func NewIPCServer(socketPath string, llmCall LLMCallFunc, componentName string, logger *slog.Logger) (*IPCServer, error) {
	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	return &IPCServer{
		socketPath:    socketPath,
		listener:      listener,
		logger:        logger,
		llmCall:       llmCall,
		componentName: componentName,
	}, nil
}

func (s *IPCServer) Serve(ctx context.Context) {
	defer func() { _ = s.Close() }()

	connCh := make(chan net.Conn, 1)
	go func() {
		conn, err := s.listener.Accept()
		if err != nil {
			s.logger.Debug("IPC accept error", "error", err)
			close(connCh)
			return
		}
		connCh <- conn
	}()

	var conn net.Conn
	select {
	case <-ctx.Done():
		return
	case c, ok := <-connCh:
		if !ok {
			return
		}
		conn = c
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}

	reader := bufio.NewReader(conn)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		resp := s.handleRequest(ctx, line)
		respData, _ := json.Marshal(resp)
		respData = append(respData, '\n')
		if _, writeErr := conn.Write(respData); writeErr != nil {
			s.logger.Debug("IPC write error", "error", writeErr)
			return
		}
	}
}

func (s *IPCServer) Close() error {
	err := s.listener.Close()
	_ = os.Remove(s.socketPath)
	return err
}

type ipcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type ipcResponse struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      string  `json:"id"`
	Result  any     `json:"result,omitempty"`
	Error   *ipcErr `json:"error,omitempty"`
}

type ipcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type llmCallParams struct {
	SystemPrompt string  `json:"system_prompt"`
	UserPrompt   string  `json:"user_prompt"`
	Temperature  float64 `json:"temperature"`
}

type llmCallResult struct {
	Text string `json:"text"`
}

func (s *IPCServer) handleRequest(ctx context.Context, data []byte) *ipcResponse {
	var req ipcRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return &ipcResponse{
			JSONRPC: "2.0",
			Error:   &ipcErr{Code: -32700, Message: "parse error"},
		}
	}

	switch req.Method {
	case "llm/call":
		return s.handleLLMCall(ctx, req)
	case "llm/chat":
		return s.handleLLMChat(ctx, req)
	default:
		return &ipcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &ipcErr{Code: -32601, Message: fmt.Sprintf("method %q not found", req.Method)},
		}
	}
}

func (s *IPCServer) handleLLMCall(ctx context.Context, req ipcRequest) *ipcResponse {
	if s.llmCall == nil {
		return &ipcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &ipcErr{Code: -32000, Message: "LLM service not available"},
		}
	}

	var params llmCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &ipcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &ipcErr{Code: -32602, Message: fmt.Sprintf("invalid params: %v", err)},
		}
	}

	text, err := s.llmCall(ctx, s.componentName, params.SystemPrompt, params.UserPrompt, params.Temperature)
	if err != nil {
		return &ipcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &ipcErr{Code: -32000, Message: fmt.Sprintf("LLM call failed: %v", err)},
		}
	}

	return &ipcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  llmCallResult{Text: text},
	}
}
