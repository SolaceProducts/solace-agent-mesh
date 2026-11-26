// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/connector"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

const connectorStopTimeout = 5 * time.Second

type workflowToolinitHost struct {
	w *WorkflowInstance
}

func (h workflowToolinitHost) Name() string { return h.w.cfg.Name }

func (h workflowToolinitHost) Namespace() string { return h.w.cfg.Namespace }

func (h workflowToolinitHost) Tools() *tool.Set { return h.w.tools }

func (h workflowToolinitHost) AddSubComponent(sc toolinit.Startable) { h.w.addSubComponent(sc) }

func (h workflowToolinitHost) RegisterAuthHandler(handler tool.AuthResponseHandler) {
	h.w.toolAuth.register(handler)
}

func (h workflowToolinitHost) DeregisterAuthHandler(credentialKey string) {
	h.w.toolAuth.deregister(credentialKey)
}

func (h workflowToolinitHost) RegisterMCPAuthHandler(handler tool.AuthResponseHandler) {
	h.w.toolAuth.register(handler)
}

func (w *WorkflowInstance) addSubComponent(sc toolinit.Startable) {
	w.subComponents = append(w.subComponents, sc)
}

func (w *WorkflowInstance) initToolsAndConnectors(ctx context.Context, svc runtime.Services) error {
	w.tools = tool.NewSet()
	if svc != nil {
		w.tools.SetAuthorizer(svc.ToolAuthorizer())
	}

	logger := w.logger.With("logger_name", "workflow-tools")
	w.tools.SetLogger(logger)

	if len(w.cfg.Toolsets) > 0 {
		logger.Warn("workflow config carries an unexpanded toolsets list — the engine does not resolve toolsets; deploy through the platform (which expands them into tools:) or declare tools: directly",
			"toolsetCount", len(w.cfg.Toolsets))
	}

	if len(w.cfg.supersededBodyTools) > 0 {
		logger.Warn("workflow body declares tools already declared at the app_config level; the app_config entries win and the body ones are ignored",
			"toolNames", w.cfg.supersededBodyTools)
	}

	if err := w.registerConnectors(ctx, svc, logger); err != nil {
		return err
	}

	if len(w.cfg.Tools) > 0 {
		if err := detectToolNameCollisions(w.tools, w.cfg.Tools); err != nil {
			return err
		}

		host := workflowToolinitHost{w: w}
		opts := w.toolInitOpts
		if opts.ConnectorSpecStore == nil && svc != nil {
			if s := svc.ConnectorSpecStore(); s != nil {
				opts.ConnectorSpecStore = s
			}
		}
		remoteExec, entries, err := toolinit.RegisterTools(host, w.cfg.Tools, svc, opts)
		if err != nil {
			return fmt.Errorf("register tools: %w", err)
		}
		w.remoteExec = remoteExec
		w.toolConfigs = buildToolConfigIndex(entries, logger)
		for _, e := range entries {
			for _, name := range e.ToolNames {
				w.recordToolProvenance(name, toolinit.ProvenanceOf(e.ToolMap, name))
			}
		}

		hilConfigs, hilErr := parseHILConfigs(logger, entries)
		if hilErr != nil {
			return hilErr
		}
		w.hilToolConfigs = hilConfigs
		w.tools.SetHILConfigs(hilConfigs)

		effectOverrides, effectErr := tool.ParseEffectOverrides(logger, sharedHILEntries(entries))
		if effectErr != nil {
			return fmt.Errorf("parse tool effect config: %w", effectErr)
		}
		w.tools.SetEffectOverrides(effectOverrides)
	}

	return nil
}

func parseHILConfigs(logger *slog.Logger, entries []toolinit.EntryRecord) (map[string]tool.HILToolConfig, error) {
	configs, err := tool.ParseHILConfigs(logger, sharedHILEntries(entries))
	if err != nil {
		return nil, fmt.Errorf("parse tool hil config: %w", err)
	}
	return configs, nil
}

func sharedHILEntries(entries []toolinit.EntryRecord) []tool.HILEntry {
	shared := make([]tool.HILEntry, 0, len(entries))
	for _, e := range entries {
		shared = append(shared, tool.HILEntry{ToolMap: e.ToolMap, ToolNames: e.ToolNames, Degraded: e.Degraded()})
	}
	return shared
}

func (w *WorkflowInstance) warnOnUngatableTools(logger *slog.Logger) {
	if len(w.hilToolConfigs) == 0 || logger == nil {
		return
	}
	names := slices.Sorted(maps.Keys(w.hilToolConfigs))

	if !w.toolApprovalEnforced() {
		logger.Warn("tools declare an approval gate but workflow approval enforcement is disabled; nodes calling them dispatch WITHOUT asking anyone. Set SAM_FEATURE_WORKFLOW_TOOL_APPROVAL_ENFORCEMENT=true to enforce",
			"toolNames", names)
		return
	}
	if !w.workflowHILEnabled() {
		logger.Warn("tools require human approval but this workflow cannot prompt; nodes calling them will fail. Enable workflow_hil_pause_resume and configure a session database to allow approvals",
			"toolNames", names)
	}
}

func (w *WorkflowInstance) recordToolProvenance(toolName string, p toolinit.Provenance) {
	if w.toolProvenance == nil {
		w.toolProvenance = make(map[string]toolinit.Provenance)
	}
	w.toolProvenance[toolName] = p
}

func buildToolConfigIndex(entries []toolinit.EntryRecord, logger *slog.Logger) map[string]map[string]any {
	warn := func(msg string, entry toolinit.EntryRecord, args ...any) {
		if logger == nil {
			return
		}
		toolType, _ := entry.ToolMap["tool_type"].(string)
		args = append(args, "toolType", toolType)
		if name, _ := entry.ToolMap["tool_name"].(string); name != "" {
			args = append(args, "toolName", name)
		}
		if group, _ := entry.ToolMap["group_name"].(string); group != "" {
			args = append(args, "groupName", group)
		}
		if entry.Error != "" {
			args = append(args, "registrationError", entry.Error)
		}
		logger.Warn(msg, args...)
	}

	out := make(map[string]map[string]any)
	for _, groupPass := range []bool{true, false} {
		writtenThisPass := make(map[string]bool, len(entries))
		for _, entry := range entries {
			toolType, _ := entry.ToolMap["tool_type"].(string)
			if (toolType == toolinit.ToolTypeBuiltinGroup) != groupPass {
				continue
			}
			raw, declared := entry.ToolMap["tool_config"]
			tc, ok := raw.(map[string]any)
			if !ok || tc == nil {
				if declared {
					warn("tool_config ignored; not a map", entry, "gotType", fmt.Sprintf("%T", raw))
				}
				continue
			}
			indexed := 0
			for _, name := range entry.ToolNames {
				if name == "" {
					continue
				}
				indexed++
				if writtenThisPass[name] {
					warn("tool_config collision; keeping the first entry's config", entry, "toolName", name)
					continue
				}
				out[name] = tc
				writtenThisPass[name] = true
			}
			if indexed == 0 {
				warn("tool_config ignored; entry registered no tool names", entry)
			}
		}
	}
	return out
}

func detectToolNameCollisions(set *tool.Set, toolsCfg []any) error {
	for i, item := range toolsCfg {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["tool_type"].(string); t != "builtin" {
			continue
		}
		name, _ := m["tool_name"].(string)
		if name == "" {
			continue
		}
		if set.GetTool(name) != nil {
			return fmt.Errorf("tools[%d]: name %q already registered (likely by a connector); tools and connectors share a single namespace", i, name)
		}
	}
	return nil
}

func (w *WorkflowInstance) registerConnectors(ctx context.Context, svc runtime.Services, logger *slog.Logger) error {
	if len(w.cfg.Connectors) == 0 {
		return nil
	}
	for i, item := range w.cfg.Connectors {
		entryMap, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("connectors[%d]: entry must be a map, got %T", i, item)
		}
		cfg, err := connector.ParseEntry(entryMap)
		if err != nil {
			return fmt.Errorf("connectors[%d]: %w", i, err)
		}

		conn, err := connector.DefaultRegistry().Build(ctx, cfg, svc)
		if err != nil {
			return fmt.Errorf("connectors[%d]: %w", i, err)
		}

		if err := w.tools.Register(conn.Tool()); err != nil {
			return fmt.Errorf("connectors[%d] (%s): register tool %q: %w",
				i, cfg.Name, conn.Tool().Name(), err)
		}

		w.recordToolProvenance(conn.Tool().Name(), toolinit.Provenance{
			Category:          taxonomy.ToolCategoryConnector,
			ConnectorCategory: cfg.Type,
		})

		w.connectors = append(w.connectors, conn)
		w.addSubComponent(connectorStartable{conn: conn, logger: logger.With("connector", cfg.Name)})

		logger.Info("registered connector",
			"name", cfg.Name,
			"type", cfg.Type,
			"tool", conn.Tool().Name(),
		)
	}
	return nil
}

type connectorStartable struct {
	conn   connector.Connector
	logger *slog.Logger
}

func (c connectorStartable) Start(ctx context.Context) error {
	return c.conn.Start(ctx)
}

func (c connectorStartable) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), connectorStopTimeout)
	defer cancel()
	if err := c.conn.Stop(ctx); err != nil {
		c.logger.Warn("connector Stop returned error", "error", err)
	}
}

func (w *WorkflowInstance) startSubComponents(ctx context.Context) error {
	for i, sc := range w.subComponents {
		if err := sc.Start(ctx); err != nil {
			for j := i - 1; j >= 0; j-- {
				w.subComponents[j].Stop()
			}
			return fmt.Errorf("sub-component %d start failed: %w", i, err)
		}
	}
	return nil
}

func (w *WorkflowInstance) stopSubComponents() {
	for i := len(w.subComponents) - 1; i >= 0; i-- {
		w.subComponents[i].Stop()
	}
}

func (w *WorkflowInstance) toolNodeSchemas(node *NodeConfig) (inputSchema, outputSchema map[string]any) {
	inputSchema = node.InputSchemaOverride
	outputSchema = node.OutputSchemaOverride
	if inputSchema != nil && outputSchema != nil {
		return
	}
	if w.tools == nil {
		return
	}
	t := w.tools.GetTool(node.ToolName)
	if t == nil {
		return
	}
	if inputSchema == nil {
		inputSchema = jsonSchemaToMap(t.Parameters())
	}
	if outputSchema == nil {
		outputSchema = jsonSchemaToMap(tool.ResultSchemaOf(t))
	}
	return
}

func jsonSchemaToMap(schema *tool.JSONSchema) map[string]any {
	if schema == nil {
		return nil
	}
	if schema.Raw != nil {
		return schema.Raw
	}
	return map[string]any{
		"type":       schema.Type,
		"properties": schema.Properties,
		"required":   schema.Required,
	}
}
