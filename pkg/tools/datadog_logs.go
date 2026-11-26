// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const datadogMaxLimit = 100

type datadogConfig struct {
	apiKey    string
	appKey    string
	apiHost   string
	cluster   string
	namespace string

	httpClient *http.Client
}

func parseDatadogConfig(m map[string]any) datadogConfig {
	cfg := datadogConfig{
		apiKey:     cfgString(m, "api_key"),
		appKey:     cfgString(m, "app_key"),
		cluster:    cfgString(m, "cluster_name"),
		namespace:  cfgString(m, "kube_namespace"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	if host := cfgString(m, "api_host"); host != "" {
		cfg.apiHost = strings.TrimRight(host, "/")
	} else {
		site := cfgString(m, "site")
		if site == "" {
			site = "datadoghq.com"
		}
		cfg.apiHost = "https://api." + site
	}
	return cfg
}

func cfgString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func (c datadogConfig) scopedQuery(query string) string {
	var parts []string
	if c.cluster != "" {
		parts = append(parts, "cluster_name:"+c.cluster)
	}
	if c.namespace != "" {
		parts = append(parts, "kube_namespace:"+c.namespace)
	}
	if q := strings.TrimSpace(query); q != "" {
		parts = append(parts, "("+q+")")
	}
	return strings.Join(parts, " ")
}

type logEvent struct {
	Timestamp string `json:"timestamp"`
	Service   string `json:"service"`
	Status    string `json:"status"`
	Host      string `json:"host"`
	Message   string `json:"message"`
}

func (c datadogConfig) search(ctx context.Context, query, from, to string, limit int) ([]logEvent, error) {
	if c.apiKey == "" || c.appKey == "" {
		return nil, fmt.Errorf("datadog is not configured (api_key/app_key missing in tool_config)")
	}
	if limit <= 0 || limit > datadogMaxLimit {
		limit = datadogMaxLimit
	}
	if from == "" {
		from = "now-1h"
	}
	if to == "" {
		to = "now"
	}

	reqBody := map[string]any{
		"filter": map[string]any{
			"query": c.scopedQuery(query),
			"from":  from,
			"to":    to,
		},
		"sort": "-timestamp",
		"page": map[string]any{"limit": limit},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	url := c.apiHost + "/api/v2/logs/events/search"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DD-API-KEY", c.apiKey)
	req.Header.Set("DD-APPLICATION-KEY", c.appKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("datadog request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("datadog returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if readErr != nil {
		return nil, fmt.Errorf("read datadog response body: %w", readErr)
	}

	var parsed struct {
		Data []struct {
			Attributes struct {
				Timestamp string `json:"timestamp"`
				Service   string `json:"service"`
				Status    string `json:"status"`
				Host      string `json:"host"`
				Message   string `json:"message"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parse datadog response: %w", err)
	}
	events := make([]logEvent, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		events = append(events, logEvent{
			Timestamp: d.Attributes.Timestamp,
			Service:   d.Attributes.Service,
			Status:    d.Attributes.Status,
			Host:      d.Attributes.Host,
			Message:   d.Attributes.Message,
		})
	}
	return events, nil
}

func toolTraceID(toolCtx *tool.Context) string {
	if toolCtx == nil {
		return ""
	}
	return toolCtx.TraceID
}

type datadogToolBase struct {
	artifactToolBase
	cfg datadogConfig
}

func (datadogToolBase) RequiredScopes() []string { return []string{rbac.ScopeToolDatadogLogs} }

func (b datadogToolBase) run(ctx context.Context, traceID, name, query, from, to string, limit int) (*tool.Result, error) {
	events, err := b.cfg.search(ctx, query, from, to, limit)
	if err != nil {
		slog.Default().With("logger_name", "datadog_logs").Warn("datadog query failed", "tool", name, "query", b.cfg.scopedQuery(query),
			"from", from, "to", to, "traceID", traceID, "error", err)
		return tool.Error(err.Error()), nil
	}
	data := map[string]any{"query": b.cfg.scopedQuery(query), "from": from, "to": to, "count": len(events), "events": events}
	content, mErr := json.MarshalIndent(data, "", "  ")
	if mErr != nil {
		return tool.Error(fmt.Sprintf("marshal %s results: %s", name, mErr)), nil
	}
	return tool.OK(fmt.Sprintf("Found %d log event(s)", len(events)),
		tool.WithDataObjects(tool.DataObject{
			Content:     content,
			MIMEType:    "application/json",
			Disposition: tool.DispositionAuto,
			Description: fmt.Sprintf("%s results", name),
		}),
	), nil
}

func timeRangeParams() map[string]any {
	return map[string]any{
		"from":  map[string]any{"type": "string", "description": "Start of the search window: ISO 8601 or Datadog relative time (e.g. 'now-1h', 'now-24h'). Default 'now-1h'."},
		"to":    map[string]any{"type": "string", "description": "End of the search window: ISO 8601 or 'now'. Default 'now'."},
		"limit": map[string]any{"type": "integer", "description": fmt.Sprintf("Max events to return (1-%d). Default %d.", datadogMaxLimit, datadogMaxLimit)},
	}
}

type DatadogQueryLogsTool struct {
	datadogToolBase
	tool.ReadOnlyTool
}

func NewDatadogQueryLogsTool(m map[string]any) *DatadogQueryLogsTool {
	return &DatadogQueryLogsTool{datadogToolBase: datadogToolBase{cfg: parseDatadogConfig(m)}}
}

func (t *DatadogQueryLogsTool) Name() string { return "datadog_query_logs" }

func (t *DatadogQueryLogsTool) Description() string {
	return "Searches Datadog logs for this SAM deployment. Pass a Datadog log query string; the deployment's " +
		"cluster_name and kube_namespace are added automatically. Returns matching log events (timestamp, " +
		"service, status, host, message). Always bound the search with a time window."
}

func (t *DatadogQueryLogsTool) Parameters() *tool.JSONSchema {
	props := map[string]any{
		"query": map[string]any{"type": "string", "description": "Datadog log query, e.g. 'status:error service:gateway'. Empty matches all (within the deployment scope)."},
	}
	for k, v := range timeRangeParams() {
		props[k] = v
	}
	return &tool.JSONSchema{Type: "object", Properties: props}
}

func (t *DatadogQueryLogsTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

func (t *DatadogQueryLogsTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	limit, _ := sentinelIntArg(args, "limit")
	return t.run(ctx, toolTraceID(toolCtx), t.Name(), sentinelStrArg(args, "query"), sentinelStrArg(args, "from"), sentinelStrArg(args, "to"), limit)
}

type DatadogGetErrorsTool struct {
	datadogToolBase
	tool.ReadOnlyTool
}

func NewDatadogGetErrorsTool(m map[string]any) *DatadogGetErrorsTool {
	return &DatadogGetErrorsTool{datadogToolBase: datadogToolBase{cfg: parseDatadogConfig(m)}}
}

func (t *DatadogGetErrorsTool) Name() string { return "datadog_get_errors" }

func (t *DatadogGetErrorsTool) Description() string {
	return "Fetches error-level Datadog logs for this SAM deployment, optionally filtered by service. " +
		"Shorthand for a status:error query. Returns matching error events. Bound the search with a time window."
}

func (t *DatadogGetErrorsTool) Parameters() *tool.JSONSchema {
	props := map[string]any{
		"service": map[string]any{"type": "string", "description": "Optional service filter, e.g. 'gateway' or 'awe'."},
	}
	for k, v := range timeRangeParams() {
		props[k] = v
	}
	return &tool.JSONSchema{Type: "object", Properties: props}
}

func (t *DatadogGetErrorsTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

func (t *DatadogGetErrorsTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	query := "status:error"
	if svc := sentinelStrArg(args, "service"); svc != "" {
		query += " service:" + svc
	}
	limit, _ := sentinelIntArg(args, "limit")
	return t.run(ctx, toolTraceID(toolCtx), t.Name(), query, sentinelStrArg(args, "from"), sentinelStrArg(args, "to"), limit)
}

type CorrelateTaskWithLogsTool struct {
	datadogToolBase
	tool.ReadOnlyTool
}

func NewCorrelateTaskWithLogsTool(m map[string]any) *CorrelateTaskWithLogsTool {
	return &CorrelateTaskWithLogsTool{datadogToolBase: datadogToolBase{cfg: parseDatadogConfig(m)}}
}

func (t *CorrelateTaskWithLogsTool) Name() string { return "correlate_task_with_logs" }

func (t *CorrelateTaskWithLogsTool) Description() string {
	return "Retrieves the Datadog log lines for a single task by its trace ID (the @traceID that threads " +
		"through every hop of a task). Use this to see exactly what was logged while a specific task ran."
}

func (t *CorrelateTaskWithLogsTool) Parameters() *tool.JSONSchema {
	props := map[string]any{
		"trace_id": map[string]any{"type": "string", "description": "The task's traceID (UUID). Required."},
	}
	for k, v := range timeRangeParams() {
		props[k] = v
	}
	return &tool.JSONSchema{Type: "object", Properties: props, Required: []string{"trace_id"}}
}

func (t *CorrelateTaskWithLogsTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

func (t *CorrelateTaskWithLogsTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	traceID := sentinelStrArg(args, "trace_id")
	if traceID == "" && toolCtx != nil {
		traceID = toolCtx.TraceID
	}
	if traceID == "" {
		return tool.Error("trace_id is required to correlate logs with a task"), nil
	}
	limit, _ := sentinelIntArg(args, "limit")
	from := sentinelStrArg(args, "from")
	if from == "" {
		from = "now-7d"
	}
	return t.run(ctx, toolTraceID(toolCtx), t.Name(), "@traceID:"+traceID, from, sentinelStrArg(args, "to"), limit)
}
