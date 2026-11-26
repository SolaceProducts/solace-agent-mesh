// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

import "time"

type AgentCard struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Capabilities AgentCapabilities `json:"capabilities,omitempty"`
}

type AgentCapabilities struct {
	Extensions []AgentExtension `json:"extensions,omitempty"`
}

type AgentExtension struct {
	URI    string         `json:"uri"`
	Params map[string]any `json:"params,omitempty"`
}

const displayNameExtensionURI = "https://solace.com/a2a/extensions/display-name"

func (c AgentCard) DisplayName() string {
	for _, ext := range c.Capabilities.Extensions {
		if ext.URI != displayNameExtensionURI {
			continue
		}
		if v, ok := ext.Params["display_name"].(string); ok && v != "" {
			return v
		}
	}
	return c.Name
}

type TaskRequest struct {
	JSONRPC string     `json:"jsonrpc"`
	ID      string     `json:"id"`
	Method  string     `json:"method"`
	Params  TaskParams `json:"params"`
}

type TaskParams struct {
	Message TaskMessage `json:"message"`
}

type TaskMessage struct {
	Role      string         `json:"role"`
	Parts     []MessagePart  `json:"parts"`
	MessageID string         `json:"messageId"`
	Kind      string         `json:"kind"`
	ContextID string         `json:"contextId"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type MessagePart map[string]any

type TaskResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      string         `json:"id"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *RPCError      `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type SSEEvent struct {
	EventType string
	Data      map[string]any
	RawData   string
	Timestamp time.Time
	Sequence  int64
}

type AssembledMessage struct {
	TextParts    []string
	DataParts    []any
	Artifacts    []map[string]any
	IsComplete   bool
	IsError      bool
	ErrorMessage string
	TaskID       string
	ContextID    string
	StatusText   string
	HILRequest   *HILRequest
	AuthRequest  *AuthRequest
	LastSequence int64
}

type AuthRequest struct {
	AuthURI           string `json:"auth_uri"`
	FunctionCallID    string `json:"function_call_id"`
	CredentialKey     string `json:"credential_key"`
	TargetAgent       string `json:"target_agent"`
	TargetAgentTaskID string `json:"target_agent_task_id"`
}

type HILRequest struct {
	RequestID    string `json:"request_id"`
	Source       string `json:"source"`
	ExpiresAt    string `json:"expires_at"`
	Surface      any    `json:"surface"`
	TaskID       string `json:"task_id"`
	AgentName    string `json:"agent_name"`
	SessionID    string `json:"session_id"`
	LastSequence int64  `json:"last_sequence"`
	GatewayURL   string `json:"gateway_url"`
}

type HILRespondRequest struct {
	TaskID    string         `json:"taskId"`
	AgentName string         `json:"agentName"`
	RequestID string         `json:"requestId"`
	Status    string         `json:"status"`
	Data      map[string]any `json:"data"`
}

func (m *AssembledMessage) Text() string {
	var total int
	for _, p := range m.TextParts {
		total += len(p)
	}
	buf := make([]byte, 0, total)
	for _, p := range m.TextParts {
		buf = append(buf, p...)
	}
	return string(buf)
}

type ArtifactInfo struct {
	Filename  string
	Size      int
	MimeType  string
	LocalPath string
}
