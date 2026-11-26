// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
)

type connectorDescriber func(d *ToolDescriber, ctx context.Context, ref DescribeToolRef, conn *store.Connector, cc ConnectorConfig) ([]DescribedTool, error)

var connectorDescribers = map[string]connectorDescriber{
	"api":            (*ToolDescriber).describeAPIConnector,
	"mcp":            (*ToolDescriber).describeMCPConnector,
	"sql":            (*ToolDescriber).describeSTRConnector,
	"email":          (*ToolDescriber).describeSTRConnector,
	"knowledge_base": (*ToolDescriber).describeSTRConnector,
	"slack":          (*ToolDescriber).describeSTRConnector,
	"search":         (*ToolDescriber).describeSTRConnector,
	"graph_db":       (*ToolDescriber).describeSTRConnector,
	"event_mesh":     (*ToolDescriber).describeSTRConnector,
	"document_db":    (*ToolDescriber).describeSTRConnector,
	"web_search":     (*ToolDescriber).describeAgentOnlyConnector,
}

func (d *ToolDescriber) describeConnector(ctx context.Context, ref DescribeToolRef) ([]DescribedTool, error) {
	if d.connectors == nil {
		return nil, fmt.Errorf("platform services not initialized for resource %q", "connectors")
	}
	conn, err := resolveByName(d.connectors)(ctx, ref.Connector)
	switch {
	case errors.Is(err, errConnectorAmbiguous):
		return []DescribedTool{unavailableTool(ref, describeKindConnector, reasonConnectorAmbiguous)}, nil
	case errors.Is(err, errConnectorNotFound):
		return d.connectorNotFound(ctx, ref)
	case err != nil:
		return nil, fmt.Errorf("describe tools: %w", err)
	}
	if ConnectorTypeHidden(d.flags, conn.Type) {
		return []DescribedTool{unavailableConnectorTool(ref, conn, reasonTypeDisabled)}, nil
	}
	cc, err := connectorConfigFromRow(conn)
	if err != nil {
		d.logger.Warn("describe tools: stored connector values are not valid JSON", "connectorID", conn.ID)
		return []DescribedTool{unavailableConnectorTool(ref, conn, reasonOperationConfigBad)}, nil
	}
	describe, supported := connectorDescribers[conn.Type]
	if !supported {
		return []DescribedTool{unavailableConnectorTool(ref, conn, reasonUnsupportedSubtype)}, nil
	}
	described, err := describe(d, ctx, ref, conn, cc)
	if err != nil || ref.Tool != "" || len(described) <= describeMaxToolsPerConnector {
		return described, err
	}
	described = described[:describeMaxToolsPerConnector]
	last := &described[len(described)-1]
	last.Notes = append(last.Notes, reasonToolListTruncated)
	return described, nil
}

func (d *ToolDescriber) connectorNotFound(ctx context.Context, ref DescribeToolRef) ([]DescribedTool, error) {
	similar, err := d.connectors.GetByNormalizedName(ctx, ref.Connector)
	if err != nil {
		return nil, fmt.Errorf("describe tools: %w", err)
	}
	if similar != nil && !ConnectorTypeHidden(d.flags, similar.Type) {
		return []DescribedTool{notFoundTool(ref, reasonConnectorNotFound, []string{similar.Name})}, nil
	}
	suggestions, err := d.connectorNameSuggestions(ctx, ref.Connector)
	if err != nil {
		return nil, err
	}
	return []DescribedTool{notFoundTool(ref, reasonConnectorNotFound, suggestions)}, nil
}

func connectorTool(ref DescribeToolRef, conn *store.Connector) DescribedTool {
	return DescribedTool{
		Ref:           ref,
		Status:        describeStatusOK,
		Kind:          describeKindConnector,
		ConnectorName: conn.Name,
		ConnectorType: conn.Type,
		ToolNode:      ToolNodeVerdict{Usable: true},
	}
}

func unavailableConnectorTool(ref DescribeToolRef, conn *store.Connector, reason string) DescribedTool {
	described := unavailableTool(ref, describeKindConnector, reason)
	described.ConnectorName = conn.Name
	described.ConnectorType = conn.Type
	return described
}

func connectorToolNotFound(ref DescribeToolRef, conn *store.Connector, names []string) DescribedTool {
	described := notFoundTool(ref, reasonToolNotFound, names[:min(len(names), describeMaxListedTools)])
	described.ConnectorName = conn.Name
	described.ConnectorType = conn.Type
	return described
}

func (d *ToolDescriber) logUnreadable(ctx context.Context, conn *store.Connector, err error) {
	class := failureClass(err)
	level := slog.LevelWarn
	if isExpectedFailure(class) {
		level = slog.LevelDebug
	}
	d.logger.Log(ctx, level, "describe tools: could not read a connector's stored specification", "connectorID", conn.ID, "failureClass", class)
}

const describeSpecTimeout = 8 * time.Second

type apiSpecView struct {
	known              bool
	readFailed         bool
	refused            bool
	names              []string
	mediaTypesDisagree map[string]bool
	described          map[string]openapi.OperationDescription
}

func (v apiSpecView) unavailableReason(name string) string {
	_, described := v.described[name]
	switch {
	case v.readFailed || v.refused:
		return reasonOperationConfigBad
	case !v.known || described:
		return ""
	case v.mediaTypesDisagree[name]:
		return reasonOperationAmbiguous
	default:
		return reasonOperationConfigBad
	}
}

func (d *ToolDescriber) readAPISpec(ctx context.Context, conn *store.Connector, cc ConnectorConfig) apiSpecView {
	if view, cached := d.apiSpecs[conn.ID]; cached {
		return view
	}
	view := d.loadAPISpec(ctx, conn, cc)
	if d.apiSpecs == nil {
		d.apiSpecs = make(map[string]apiSpecView)
	}
	d.apiSpecs[conn.ID] = view
	return view
}

func (d *ToolDescriber) loadAPISpec(ctx context.Context, conn *store.Connector, cc ConnectorConfig) apiSpecView {
	if conn.ResourceKey == nil || *conn.ResourceKey == "" {
		return apiSpecView{}
	}
	ctx, cancel := context.WithTimeout(ctx, describeSpecTimeout)
	defer cancel()
	data, err := readStoredSpec(ctx, conn, *conn.ResourceKey, d.specs)
	if err != nil {
		d.logUnreadable(ctx, conn, err)
		return apiSpecView{readFailed: true}
	}
	survey, err := openapi.SurveyOperations(ctx, data, allowListOperations(cc.Values), nil)
	if err != nil {
		d.logUnreadable(ctx, conn, err)
		return apiSpecView{readFailed: true}
	}
	view := apiSpecView{
		known:              true,
		names:              survey.ToolNames,
		mediaTypesDisagree: make(map[string]bool, len(survey.MediaTypesDisagree)),
		described:          make(map[string]openapi.OperationDescription, len(survey.Operations)),
		refused:            openapi.ValidateOperationIDUniqueness(data, allowListOperations(cc.Values), nil) != nil,
	}
	for _, name := range survey.MediaTypesDisagree {
		view.mediaTypesDisagree[name] = true
	}
	for _, operation := range survey.Operations {
		view.described[operation.ToolName] = operation
	}
	return view
}

func allowListOperations(values map[string]any) []string {
	allowList, _ := values["allow_list"].(string)
	return parseOperationList(allowList)
}

func apiToolNames(cc ConnectorConfig, view apiSpecView) ([]string, bool) {
	if names, listed := apiAllowListToolNames(cc.Values); listed {
		return names, len(names) > 0
	}
	return view.names, len(view.names) > 0
}

func (d *ToolDescriber) describeAPIConnector(ctx context.Context, ref DescribeToolRef, conn *store.Connector, cc ConnectorConfig) ([]DescribedTool, error) {
	view := d.readAPISpec(ctx, conn, cc)
	names, named := apiToolNames(cc, view)
	if !named {
		reason := reasonNeedsAllowList
		if view.readFailed || view.known {
			reason = reasonOperationConfigBad
		}
		return []DescribedTool{unavailableConnectorTool(ref, conn, reason)}, nil
	}
	if ref.Tool != "" {
		selected, err := selectAPIConnectorToolName(conn, names, ref.Tool)
		if err != nil {
			return []DescribedTool{connectorToolNotFound(ref, conn, names)}, nil
		}
		names = []string{selected}
	}

	out := make([]DescribedTool, 0, len(names))
	for _, name := range names {
		if reason := view.unavailableReason(name); reason != "" {
			unavailable := unavailableConnectorTool(ref, conn, reason)
			unavailable.Name = name
			out = append(out, unavailable)
			continue
		}
		operation := view.described[name]
		tool := connectorTool(ref, conn)
		tool.Name, tool.ConcreteName = name, name
		if !view.known {
			tool.Notes = append(tool.Notes, reasonNoStoredSpec)
		} else {
			tool.Description = truncateDescription(operation.Summary)
			tool.Parameters = operation.Parameters
		}
		tool.Notes = append(tool.Notes, reasonResultUndeclared)
		out = append(out, tool)
	}
	return out, nil
}

func (d *ToolDescriber) describeMCPConnector(_ context.Context, ref DescribeToolRef, conn *store.Connector, cc ConnectorConfig) ([]DescribedTool, error) {
	entry := buildConnectorTools(cc)[0]
	cfg, parseErr := parseMCPEntry(entry)

	registered, err := mcpConnectorTools(conn, cc.Values, entry)
	if err != nil {
		reason := reasonManifestMissing
		if parseErr != nil || len(cfg.Manifest) > 0 {
			reason = reasonOperationConfigBad
		}
		return []DescribedTool{unavailableConnectorTool(ref, conn, reason)}, nil
	}
	clashing := duplicateRegisteredNames(registered)
	manifest := make(map[string]mcp.ManifestTool, len(cfg.Manifest))
	for _, tool := range cfg.Manifest {
		manifest[tool.Name] = tool
	}

	if ref.Tool != "" {
		rows := map[string]*store.Connector{ref.Connector: conn}
		values := map[string]map[string]any{ref.Connector: cc.Values}
		entries := map[string][]map[string]any{ref.Connector: {entry}}
		if _, err := newMCPBindings(rows, values, entries).bind(conn, ref.Connector, "describe", ref.Tool); err != nil {
			servers := make([]string, len(registered))
			for i, tool := range registered {
				servers[i] = tool.server
			}
			return []DescribedTool{connectorToolNotFound(ref, conn, servers)}, nil
		}
		registered = slices.DeleteFunc(registered, func(tool mcpConnectorTool) bool { return tool.server != ref.Tool })
	}

	out := make([]DescribedTool, 0, len(registered))
	for _, tool := range registered {
		described := connectorTool(ref, conn)
		described.Name, described.ConcreteName = tool.server, tool.registered
		if clashing[tool.registered] {
			out = append(out, markUnavailable(described, reasonToolNameClash))
			continue
		}
		if schema, ok := manifest[tool.server]; ok {
			described.Description = truncateDescription(schema.Description)
			described.Parameters = schemaMap(schema.InputSchema)
			described.Result = schemaMap(schema.OutputSchema)
		} else {
			described.Notes = append(described.Notes, reasonPinnedNoManifest)
		}
		if described.Result == nil {
			described.Notes = append(described.Notes, reasonResultUndeclared)
		}
		out = append(out, described)
	}
	return out, nil
}

func parseMCPEntry(entry map[string]any) (*mcp.ToolsetConfig, error) {
	generic, err := asGenericJSON(entry)
	if err != nil {
		return nil, err
	}
	return mcp.ParseToolsetConfig(generic)
}

func schemaMap(schema map[string]any) json.RawMessage {
	if len(schema) == 0 {
		return nil
	}
	return marshalSchema(schema)
}

func (d *ToolDescriber) describeSTRConnector(_ context.Context, ref DescribeToolRef, conn *store.Connector, cc ConnectorConfig) ([]DescribedTool, error) {
	entries := slices.DeleteFunc(buildConnectorTools(cc), func(entry map[string]any) bool { return entry == nil })
	if len(entries) == 0 {
		return []DescribedTool{unavailableConnectorTool(ref, conn, reasonUnsupportedSubtype)}, nil
	}
	suffix := connectorSuffix(conn.ID)
	if ref.Tool != "" {
		selected, err := selectConnectorToolName(conn, entries, ref.Tool)
		if err != nil {
			return []DescribedTool{connectorToolNotFound(ref, conn, unsuffixedToolNames(entryToolNames(entries), suffix))}, nil
		}
		entries = slices.DeleteFunc(entries, func(entry map[string]any) bool { return entryToolName(entry) != selected })
	}

	out := make([]DescribedTool, 0, len(entries))
	for _, entry := range entries {
		out = append(out, d.describeSTREntry(ref, conn, entry, suffix))
	}
	return out, nil
}

func (d *ToolDescriber) describeAgentOnlyConnector(_ context.Context, ref DescribeToolRef, conn *store.Connector, cc ConnectorConfig) ([]DescribedTool, error) {
	entries := slices.DeleteFunc(buildConnectorTools(cc), func(entry map[string]any) bool { return entry == nil })
	if len(entries) == 0 {
		return []DescribedTool{unavailableConnectorTool(ref, conn, reasonUnsupportedSubtype)}, nil
	}
	suffix := toolNameSuffix(conn.Type, conn.ID)
	out := make([]DescribedTool, 0, len(entries))
	for _, entry := range entries {
		concrete := entryToolName(entry)
		if ref.Tool != "" && ref.Tool != concrete && ref.Tool+"_"+suffix != concrete {
			continue
		}
		toolConfig, _ := entry["tool_config"].(map[string]any)
		described := connectorTool(ref, conn)
		described.Name = strings.TrimSuffix(concrete, "_"+suffix)
		described.ConcreteName = concrete
		described.Description = truncateDescription(entryDescription(toolConfig))
		out = append(out, markUnavailable(described, reasonAgentNodeOnly))
	}
	if len(out) == 0 {
		return []DescribedTool{connectorToolNotFound(ref, conn, unsuffixedToolNames(entryToolNames(entries), suffix))}, nil
	}
	return out, nil
}

func (d *ToolDescriber) describeSTREntry(ref DescribeToolRef, conn *store.Connector, entry map[string]any, suffix string) DescribedTool {
	concrete := entryToolName(entry)
	toolConfig, _ := entry["tool_config"].(map[string]any)
	binary, _ := toolConfig["_str_binary"].(string)

	described := connectorTool(ref, conn)
	described.Name = strings.TrimSuffix(concrete, "_"+suffix)
	described.ConcreteName = concrete
	described.Description = truncateDescription(entryDescription(toolConfig))

	params, overrideApplied, registered := d.strSchema(binary, toolConfig)
	switch {
	case !registered:
		return markUnavailable(described, reasonUnsupportedInBuild)
	case !overrideApplied:
		return markUnavailable(described, reasonOperationConfigBad)
	}
	described.Parameters = params
	described.Notes = []string{reasonResultUndeclared}
	return described
}

func markUnavailable(described DescribedTool, reason string) DescribedTool {
	described.Status = describeStatusUnavailable
	described.Reason = reason
	described.ToolNode = ToolNodeVerdict{Reason: reason}
	return described
}

func entryToolName(entry map[string]any) string {
	name, _ := entry["tool_name"].(string)
	return name
}

func entryToolNames(entries []map[string]any) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entryToolName(entry)
	}
	return names
}

func entryDescription(toolConfig map[string]any) string {
	for _, key := range []string{"tool_description", "description"} {
		if description, _ := toolConfig[key].(string); description != "" {
			return description
		}
	}
	return ""
}
