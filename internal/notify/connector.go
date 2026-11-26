// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

const connectorAppName = "notify"

type ConnectorInvoker interface {
	Invoke(ctx context.Context, toolName, strBinary string, args map[string]any, userID, traceID string) (map[string]any, error)
}

type executorInvoker struct {
	exe *remote.Executor
}

func newExecutorInvoker(brk broker.Broker, namespace string) *executorInvoker {
	return &executorInvoker{
		exe: remote.NewExecutor(brk, namespace, connectorAppName),
	}
}

func (i *executorInvoker) Start(ctx context.Context) error { return i.exe.Start(ctx) }

func (i *executorInvoker) Stop() { i.exe.Stop() }

func (i *executorInvoker) Invoke(ctx context.Context, toolName, strBinary string, args map[string]any, userID, traceID string) (map[string]any, error) {
	corrID := idgen.New()
	resp, err := i.exe.Invoke(ctx, corrID, &remote.InvokeRequest{
		ToolName:       toolName,
		Args:           args,
		ToolConfig:     map[string]any{"_str_binary": strBinary},
		AppName:        connectorAppName,
		UserID:         userID,
		SessionID:      corrID,
		TimeoutSeconds: 30,
		TraceID:        traceID,
	}, nil)
	if err != nil {
		return nil, err
	}
	result := resp.Result.ToolResult
	return result, interpretConnectorResult(result)
}

func interpretConnectorResult(result map[string]any) error {
	if result == nil {
		return nil
	}
	if ok, present := result["success"].(bool); present && !ok {
		return fmt.Errorf("connector tool reported failure: %v", result["error"])
	}
	if msg, hasErr := result["error"].(string); hasErr && msg != "" {
		return fmt.Errorf("connector tool error: %s", msg)
	}
	return nil
}

type connectorChannelConfig struct {
	toolName  string
	strBinary string
	args      map[string]any
	textArg   string
}

func parseConnectorChannel(cfg map[string]any) (connectorChannelConfig, error) {
	c := connectorChannelConfig{textArg: "text", args: map[string]any{}}
	c.toolName, _ = cfg["tool_name"].(string)
	if c.toolName == "" {
		return c, fmt.Errorf("connector channel requires tool_name")
	}
	c.strBinary, _ = cfg["str_binary"].(string)
	if c.strBinary == "" {
		return c, fmt.Errorf("connector channel requires str_binary")
	}
	if ta, ok := cfg["text_arg"].(string); ok && ta != "" {
		c.textArg = ta
	}
	if a, ok := cfg["args"].(map[string]any); ok {
		for k, v := range a {
			c.args[k] = v
		}
	}
	return c, nil
}
