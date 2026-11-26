// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

type MockMCPAuthServer struct {
	listener      net.Listener
	server        *http.Server
	expectedToken string
	sessionID     string
	authProvider  *MockOAuthProvider

	mu                    sync.Mutex
	toolCalls             int
	authHeaders           []string
	toolCallArgs          []map[string]any
	oauthDiscoveryEnabled bool
	clientIDCounter       int
}

func NewMockMCPAuthServer(expectedToken string) (*MockMCPAuthServer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	s := &MockMCPAuthServer{
		listener:              l,
		expectedToken:         expectedToken,
		sessionID:             "mock-mcp-session-1",
		oauthDiscoveryEnabled: true,
	}

	s.installHandlers()
	go s.server.Serve(s.listener) //nolint:errcheck

	return s, nil
}

func NewMockMCPAuthServerWithProvider(provider *MockOAuthProvider) (*MockMCPAuthServer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	s := &MockMCPAuthServer{
		listener:              l,
		sessionID:             "mock-mcp-session-1",
		authProvider:          provider,
		oauthDiscoveryEnabled: true,
	}

	s.installHandlers()
	go s.server.Serve(s.listener) //nolint:errcheck

	return s, nil
}

func (s *MockMCPAuthServer) installHandlers() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", s.handleMCP)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.handleProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleAuthorizationServerMetadata)
	mux.HandleFunc("POST /register", s.handleRegister)
	s.server = &http.Server{Handler: mux}
}

func (s *MockMCPAuthServer) URL() string {
	return fmt.Sprintf("http://%s", s.listener.Addr().String())
}

func (s *MockMCPAuthServer) MCPURL() string {
	return s.URL() + "/mcp"
}

func (s *MockMCPAuthServer) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *MockMCPAuthServer) Close() {
	_ = s.server.Close()
}

func (s *MockMCPAuthServer) ToolCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolCalls
}

func (s *MockMCPAuthServer) ClientIDCounter() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientIDCounter
}

func (s *MockMCPAuthServer) AuthHeaders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]string, len(s.authHeaders))
	copy(cp, s.authHeaders)
	return cp
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *MockMCPAuthServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "parse json-rpc", http.StatusBadRequest)
		return
	}

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, req)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		s.handleToolsList(w, req)
	case "tools/call":
		s.handleToolsCall(w, r, req)
	default:
		s.writeRPCError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *MockMCPAuthServer) handleInitialize(w http.ResponseWriter, req jsonRPCRequest) {
	result := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "MockMCPAuthServer",
			"version": "1.0.0",
		},
	}
	w.Header().Set("mcp-session-id", s.sessionID)
	s.writeRPCResult(w, req.ID, result)
}

func (s *MockMCPAuthServer) handleToolsList(w http.ResponseWriter, req jsonRPCRequest) {
	result := map[string]any{
		"tools": []map[string]any{
			{
				"name":        "auth_protected_tool",
				"description": "A tool that requires OAuth2 authentication",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "The query to search for",
						},
					},
					"required": []string{"query"},
				},
			},
		},
	}
	s.writeRPCResult(w, req.ID, result)
}

func (s *MockMCPAuthServer) handleToolsCall(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	authHeader := r.Header.Get("Authorization")

	s.mu.Lock()
	s.toolCalls++
	s.authHeaders = append(s.authHeaders, authHeader)

	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if req.Params != nil {
		_ = json.Unmarshal(req.Params, &params)
	}
	s.toolCallArgs = append(s.toolCallArgs, params.Arguments)
	s.mu.Unlock()

	if s.authProvider != nil {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if authHeader == "" || token == authHeader || !s.authProvider.IsAccessTokenValid(token) {
			result := map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": "Error: 401 Unauthorized - Invalid or missing authentication token",
					},
				},
				"isError": true,
			}
			s.writeRPCResult(w, req.ID, result)
			return
		}
	} else if s.expectedToken != "" {
		expected := "Bearer " + s.expectedToken
		if authHeader != expected {
			result := map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": "Error: 401 Unauthorized - Invalid or missing authentication token",
					},
				},
				"isError": true,
			}
			s.writeRPCResult(w, req.ID, result)
			return
		}
	}

	query := ""
	if params.Arguments != nil {
		if q, ok := params.Arguments["query"].(string); ok {
			query = q
		}
	}

	result := map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": fmt.Sprintf("Authenticated result for query: %s", query),
			},
		},
		"isError": false,
	}
	s.writeRPCResult(w, req.ID, result)
}

func (s *MockMCPAuthServer) writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resultJSON, _ := json.Marshal(result)
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  resultJSON,
	}
	w.Header().Set("Content-Type", "application/json")
	respBytes, _ := json.Marshal(resp)
	_, _ = w.Write(respBytes)
}

func (s *MockMCPAuthServer) writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *MockMCPAuthServer) EnableOAuthDiscovery(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauthDiscoveryEnabled = enabled
}

func (s *MockMCPAuthServer) handleProtectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	enabled := s.oauthDiscoveryEnabled
	s.mu.Unlock()
	if !enabled {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":              s.URL(),
		"authorization_servers": []string{s.URL()},
	})
}

func (s *MockMCPAuthServer) handleAuthorizationServerMetadata(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	enabled := s.oauthDiscoveryEnabled
	s.mu.Unlock()
	if !enabled {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                 s.URL(),
		"authorization_endpoint": s.URL() + "/authorize",
		"token_endpoint":         s.URL() + "/token",
		"registration_endpoint":  s.URL() + "/register",
		"scopes_supported":       []string{"read", "write"},
		"token_endpoint_auth_methods_supported": []string{
			"client_secret_post",
			"client_secret_basic",
			"none",
		},
	})
}

func (s *MockMCPAuthServer) handleRegister(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	if !s.oauthDiscoveryEnabled {
		s.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		return
	}
	s.clientIDCounter++
	id := fmt.Sprintf("mock-client-%d", s.clientIDCounter)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_id":                  id,
		"client_secret":              id + "-secret",
		"token_endpoint_auth_method": "client_secret_post",
	})
}

func (s *MockMCPAuthServer) ConnectionParams() map[string]any {
	return map[string]any{
		"type": "streamable-http",
		"url":  s.MCPURL(),
	}
}

func (s *MockMCPAuthServer) LastAuthHeader() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.authHeaders) == 0 {
		return ""
	}
	return s.authHeaders[len(s.authHeaders)-1]
}

func (s *MockMCPAuthServer) HasValidAuthHeader() bool {
	last := s.LastAuthHeader()
	if s.expectedToken == "" {
		return true
	}
	return last == "Bearer "+s.expectedToken
}
