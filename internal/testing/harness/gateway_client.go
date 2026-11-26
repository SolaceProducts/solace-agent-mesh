// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type GatewayClient struct {
	baseURL   string
	namespace string
	client    *http.Client

	mu     sync.Mutex
	events []CollectedEvent
	done   chan struct{}
}

var _ Gateway = (*GatewayClient)(nil)

func NewGatewayClient(baseURL, namespace string) *GatewayClient {
	return &GatewayClient{
		baseURL:   baseURL,
		namespace: namespace,
		client:    &http.Client{Timeout: 60 * time.Second},
		done:      make(chan struct{}),
	}
}

func (c *GatewayClient) Setup(_ context.Context) error {
	return nil
}

func (c *GatewayClient) SendRequest(ctx context.Context, agentName string, input *GatewayInput) (string, error) {
	taskID := idgen.New()

	var parts []map[string]any
	for _, p := range input.GetParts() {
		switch p.Type {
		case "file":
			parts = append(parts, map[string]any{
				"kind": "file",
				"file": map[string]any{
					"uri":      p.URI,
					"mimeType": p.ContentBase64,
				},
			})
		case "data":
			if p.Data != nil {
				parts = append(parts, map[string]any{
					"kind": "data",
					"data": p.Data,
				})
			}
		default:
			if t := p.GetText(); t != "" {
				parts = append(parts, map[string]any{
					"kind": "text",
					"text": t,
				})
			}
		}
	}
	if len(parts) == 0 {
		parts = []map[string]any{{"kind": "text", "text": ""}}
	}

	contextID := idgen.New()
	if input.ExternalContext != nil && input.ExternalContext.A2ASessionID != "" {
		contextID = input.ExternalContext.A2ASessionID
	}

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      taskID,
		"method":  pythonStreamingMethod,
		"params": map[string]any{
			"message": map[string]any{
				"kind":      "message",
				"messageId": "msg-" + taskID,
				"role":      "user",
				"parts":     parts,
				"contextId": contextID,
				"metadata": map[string]any{
					"agent_name": agentName,
				},
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/api/v1/message:stream"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceWebUI)

	userID := "test-user"
	if input.UserIdentity != "" {
		userID = input.UserIdentity
	}
	httpReq.Header.Set("X-SAM-User-ID", userID)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("POST message:stream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST message:stream: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp struct {
		Result struct {
			ID        string `json:"id"`
			ContextID string `json:"contextId"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(rpcResp.Error) > 0 && string(rpcResp.Error) != "null" {
		return "", fmt.Errorf("gateway error: %s", string(rpcResp.Error))
	}

	serverTaskID := rpcResp.Result.ID
	if serverTaskID == "" {
		serverTaskID = taskID
	}

	go c.collectSSEEvents(ctx, serverTaskID)

	return serverTaskID, nil
}

func (c *GatewayClient) CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error) {
	collectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	gotResponse := false
	select {
	case <-c.done:
		gotResponse = true
		time.Sleep(100 * time.Millisecond)
	case <-collectCtx.Done():
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	events := make([]CollectedEvent, len(c.events))
	copy(events, c.events)

	if gotResponse {
		return events, nil
	}
	return events, collectCtx.Err()
}

func (c *GatewayClient) SendCancel(ctx context.Context, agentName string, taskID string) error {
	url := fmt.Sprintf("%s/api/v1/tasks/%s:cancel", c.baseURL, taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create cancel request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("send cancel: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

func (c *GatewayClient) Cleanup() {}

func (c *GatewayClient) collectSSEEvents(ctx context.Context, taskID string) {
	sseURL := fmt.Sprintf("%s/api/v1/sse/subscribe/%s", c.baseURL, taskID)
	sseClient := NewSSEClient(sseURL, &http.Client{Timeout: 0}, nil)

	events := make(chan SSEEvent, 100)
	go func() {
		if err := sseClient.Subscribe(ctx, events); err != nil {
			_ = ctx.Err()
		}
		close(events)
	}()

	for evt := range events {
		collected := c.sseEventToCollected(taskID, evt)
		c.mu.Lock()
		c.events = append(c.events, collected)
		c.mu.Unlock()

		if evt.Event == "final_response" || evt.Event == "error" {
			select {
			case <-c.done:
			default:
				close(c.done)
			}
		}
	}
}

func (c *GatewayClient) sseEventToCollected(taskID string, evt SSEEvent) CollectedEvent {
	eventType := "status"
	if evt.Event == "final_response" || evt.Event == "error" {
		eventType = "response"
	}
	return CollectedEvent{
		Type:      eventType,
		Topic:     fmt.Sprintf("%s/gateway/response/%s", c.namespace, taskID),
		TaskID:    taskID,
		Payload:   evt.Data,
		Timestamp: time.Now(),
	}
}
