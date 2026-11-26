// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const contentTypeHeader = "Content-Type"

type GatewayClient struct {
	httpClient *http.Client
	logger     *slog.Logger
}

type gatewayClientConfig struct {
	submitTimeout      time.Duration
	connectTimeout     time.Duration
	chunkReadTimeout   time.Duration
	disableHTTPTimeout bool
}

func defaultGatewayClientConfig() gatewayClientConfig {
	return gatewayClientConfig{
		submitTimeout:    30 * time.Second,
		connectTimeout:   30 * time.Second,
		chunkReadTimeout: 60 * time.Second,
	}
}

func NewGatewayClient(logger *slog.Logger) *GatewayClient {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &GatewayClient{
		httpClient: &http.Client{},
		logger:     logger,
	}
}

func (c *GatewayClient) SubmitTask(
	ctx context.Context,
	gatewayURL, agentName, prompt, authHeader string,
) (string, error) {
	return c.submitTask(ctx, gatewayURL, agentName, prompt, authHeader, defaultGatewayClientConfig())
}

func (c *GatewayClient) submitTask(
	ctx context.Context,
	gatewayURL, agentName, prompt, authHeader string,
	cfg gatewayClientConfig,
) (string, error) {
	if gatewayURL == "" {
		return "", errors.New("entrypoint URL is empty")
	}
	url := strings.TrimRight(gatewayURL, "/") + "/api/v1/message:send"

	body := buildSendMessageRequest(agentName, prompt)
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal submit body: %w", err)
	}

	reqCtx := ctx
	if !cfg.disableHTTPTimeout && cfg.submitTimeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, cfg.submitTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build submit request: %w", err)
	}
	req.Header.Set(contentTypeHeader, "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	c.logger.Debug("eval: submitting task",
		"url", url, "agentName", agentName, "promptLength", len(prompt))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("submit task http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if looksLikeHTMLResponse(resp.Header.Get(contentTypeHeader), respBody) {
		return "", fmt.Errorf(
			"entrypoint returned an HTML page instead of JSON (status %d): the request was likely redirected to a sign-in page — the session may have expired or the entrypoint is behind an authenticating proxy",
			resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("entrypoint returned status %d with a %d-byte body", resp.StatusCode, len(respBody))
	}
	var parsed struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("decode submit response of %d bytes: %w", len(respBody), err)
	}
	taskID, _ := parsed.Result["id"].(string)
	if taskID == "" {
		c.logger.Warn("eval: gateway response missing result.id")
		return "", nil
	}
	c.logger.Debug("eval: task submitted", "taskID", taskID)
	return taskID, nil
}

func (c *GatewayClient) PollTask(
	ctx context.Context,
	gatewayURL, taskID, authHeader string,
	waitTime time.Duration,
) (map[string]any, error) {
	return c.pollTask(ctx, gatewayURL, taskID, authHeader, waitTime, defaultGatewayClientConfig())
}

func (c *GatewayClient) pollTask(
	ctx context.Context,
	gatewayURL, taskID, authHeader string,
	waitTime time.Duration,
	cfg gatewayClientConfig,
) (map[string]any, error) {
	if gatewayURL == "" {
		return nil, errors.New("entrypoint URL is empty")
	}
	if taskID == "" {
		return nil, errors.New("taskID is empty")
	}
	url := strings.TrimRight(gatewayURL, "/") + "/api/v1/sse/subscribe/" + taskID

	deadlineCtx, cancel := context.WithTimeout(ctx, waitTime)
	defer cancel()

	req, err := http.NewRequestWithContext(deadlineCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build sse request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Warn("eval: sse stream error", "taskID", taskID, "error", err)
		return nil, nil //nolint:nilnil // Python returns None on stream error; callers treat as soft failure.
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		c.logger.Warn("eval: sse stream non-2xx", "taskID", taskID, "httpStatus", resp.StatusCode)
		return nil, nil //nolint:nilnil
	}

	if looksLikeHTMLResponse(resp.Header.Get(contentTypeHeader), nil) {
		c.logger.Warn("eval: sse stream returned HTML instead of events; likely redirected to a sign-in page", "taskID", taskID)
		return nil, nil //nolint:nilnil
	}

	result, accumulated, err := readSSEStream(deadlineCtx, resp.Body, cfg.chunkReadTimeout, c.logger, taskID)
	if err != nil {
		c.logger.Warn("eval: sse stream read error", "taskID", taskID, "error", err)
		return nil, nil //nolint:nilnil
	}
	if result != nil {
		return result, nil
	}
	c.logger.Warn("eval: sse stream closed before final event", "taskID", taskID)
	if len(accumulated) > 0 {
		return synthesizeResult(accumulated), nil
	}
	return nil, nil //nolint:nilnil
}

func ExtractResponseText(taskResult map[string]any) string {
	if taskResult == nil {
		return ""
	}
	status, _ := taskResult["status"].(map[string]any)
	if status == nil {
		return ""
	}
	message, _ := status["message"].(map[string]any)
	if message == nil {
		return ""
	}
	parts, _ := message["parts"].([]any)
	var out []string
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		root := pm
		if r, ok := pm["root"].(map[string]any); ok {
			root = r
		}
		if !isTextPart(root) {
			continue
		}
		if t, ok := root["text"].(string); ok && t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n")
}

type sseReadResult struct {
	n   int
	err error
}

func readSSEStream(
	ctx context.Context,
	r io.Reader,
	chunkTimeout time.Duration,
	logger *slog.Logger,
	taskID string,
) (map[string]any, []string, error) {
	var (
		buffer      strings.Builder
		accumulated []string
		readBuf     = make([]byte, 8192)
	)

	for {
		if err := ctx.Err(); err != nil {
			return nil, accumulated, nil
		}

		rr, timedOut := awaitChunk(ctx, r, readBuf, chunkTimeout)
		if timedOut {
			logger.Warn("eval: sse chunk read timed out", "taskID", taskID, "chunkTimeout", chunkTimeout)
			return nil, accumulated, nil
		}

		if rr.n > 0 {
			chunk := strings.ReplaceAll(string(readBuf[:rr.n]), "\r\n", "\n")
			buffer.WriteString(chunk)
			done, blocks := splitSSEBlocks(buffer.String())
			buffer.Reset()
			buffer.WriteString(done)
			result, done2, more := walkSSEBlocks(blocks, &accumulated, logger, taskID)
			if done2 {
				return result, accumulated, nil
			}
			if !more {
				return nil, accumulated, nil
			}
		}

		if rr.err != nil {
			if errors.Is(rr.err, io.EOF) {
				return nil, accumulated, nil
			}
			return nil, accumulated, rr.err
		}
	}
}

func awaitChunk(ctx context.Context, r io.Reader, buf []byte, chunkTimeout time.Duration) (sseReadResult, bool) {
	ch := make(chan sseReadResult, 1)
	go func() {
		n, err := r.Read(buf)
		ch <- sseReadResult{n: n, err: err}
	}()
	select {
	case <-ctx.Done():
		return sseReadResult{}, false
	case <-time.After(chunkTimeout):
		return sseReadResult{}, true
	case rr := <-ch:
		return rr, false
	}
}

func walkSSEBlocks(
	blocks []string,
	accumulated *[]string,
	logger *slog.Logger,
	taskID string,
) (result map[string]any, done bool, more bool) {
	for _, block := range blocks {
		data := parseSSEBlock(block, logger)
		if data == nil {
			continue
		}
		if errVal, ok := data["error"]; ok && errVal != nil {
			logger.Warn("eval: gateway error in sse stream", "taskID", taskID, "error", errVal)
			return nil, false, false
		}
		event, ok := data["result"].(map[string]any)
		if !ok {
			continue
		}
		if final, term := dispatchSSEEvent(event, accumulated); term {
			return final, true, true
		}
	}
	return nil, false, true
}

func dispatchSSEEvent(event map[string]any, accumulated *[]string) (final map[string]any, terminating bool) {
	switch kind, _ := event["kind"].(string); kind {
	case "task":
		return event, true
	case "status-update":
		appendStatusText(event, accumulated)
		if isFinal, _ := event["final"].(bool); isFinal {
			return synthesizeResult(*accumulated), true
		}
	}
	return nil, false
}

func appendStatusText(event map[string]any, acc *[]string) {
	status, ok := event["status"].(map[string]any)
	if !ok {
		return
	}
	msg, ok := status["message"].(map[string]any)
	if !ok {
		return
	}
	parts, ok := msg["parts"].([]any)
	if !ok {
		return
	}
	*acc = append(*acc, collectTextParts(parts)...)
}

func splitSSEBlocks(buf string) (tail string, blocks []string) {
	for {
		idx := strings.Index(buf, "\n\n")
		if idx < 0 {
			return buf, blocks
		}
		blocks = append(blocks, buf[:idx])
		buf = buf[idx+2:]
	}
}

func parseSSEBlock(block string, logger *slog.Logger) map[string]any {
	var dataLines []string
	for line := range strings.SplitSeq(block, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimLeft(line[len("data:"):], " ")
			dataLines = append(dataLines, payload)
		}
	}
	if len(dataLines) == 0 {
		return nil
	}
	raw := strings.Join(dataLines, "\n")
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		logger.Debug("eval: skipping non-JSON sse block", "preview", truncateForLog(raw, 120))
		return nil
	}
	return out
}

func collectTextParts(parts []any) []string {
	var out []string
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if !isTextPart(pm) {
			continue
		}
		if t, ok := pm["text"].(string); ok && t != "" {
			out = append(out, t)
		}
	}
	return out
}

func isTextPart(part map[string]any) bool {
	if k, ok := part["kind"].(string); ok && k == "text" {
		return true
	}
	if k, ok := part["type"].(string); ok && k == "text" {
		return true
	}
	return false
}

func synthesizeResult(textParts []string) map[string]any {
	return map[string]any{
		"status": map[string]any{
			"message": map[string]any{
				"parts": []any{
					map[string]any{"kind": "text", "text": strings.Join(textParts, "\n")},
				},
			},
		},
	}
}

func buildSendMessageRequest(agentName, prompt string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      idgen.New(),
		"method":  "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"kind":      "message",
				"role":      "user",
				"messageId": idgen.New(),
				"parts": []any{
					map[string]any{"kind": "text", "text": prompt},
				},
				"metadata": map[string]any{"agent_name": agentName, "session_source": "eval"},
			},
		},
	}
}

func looksLikeHTMLResponse(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return true
	}
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '<'
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
