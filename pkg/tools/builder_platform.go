// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const connectorTypeComponent = "connector_type"

var resourceTypeMap = map[string]string{
	"agent":                "agents",
	"workflow":             "workflows",
	"gateway":              "gateways",
	"connector":            "connectors",
	"a2a_agent":            "a2a-agents",
	"skill":                "skills",
	"tool":                 "tools",
	"model":                "models",
	connectorTypeComponent: "connectors/types",
}

const decisionNodesEnabledKey = "decision_nodes_enabled"

var defaultListTypes = []string{"agent", "workflow", "gateway", "connector", "a2a_agent", "skill"}

var ListingFields = map[string][]string{
	"agent":     {"id", "name", "instanceName", "description", "deploymentStatus", "runtimeStatus", "toolsets", "skillRefs", "connectors", "modelProvider"},
	"workflow":  {"id", "name", "description", "deploymentStatus", "runtimeStatus"},
	"gateway":   {"id", "name", "description", "type", "publicUrl", "deploymentStatus", "runtimeStatus"},
	"connector": {"id", "name", "description", "type", "subtype", "agentCount", "hasResource"},
	"a2a_agent": {"id", "name", "instanceName", "description", "agentUrl", "deploymentStatus", "runtimeStatus"},
	"skill":     {"id", "name", "label", "description", "tags", "origin", "discoveryStatus", "hasTools", "toolsets"},
	"tool":      {"id", "name", "description", "origin", "status"},
}

const previewDescriptionMaxRunes = 160

type ListPlatformComponentsTool struct {
	tool.SideEffectingTool
	artifactToolBase
}

func (t *ListPlatformComponentsTool) Name() string { return "platform_list_components" }

func (t *ListPlatformComponentsTool) Description() string {
	return "Lists components registered in the SAM platform: agents, workflows, entrypoints, connectors, A2A agents and skills by default. " +
		"Queries the platform service over the broker control protocol. " +
		"Returns each component's id, name, description, type and status, plus the few fields that tell components apart; " +
		"the full configuration comes from platform_get_component_config. " +
		"Use this to discover what components exist in the system (including undeployed ones). " +
		"component_type \"tool\" lists the tools the Secure Tool Runtime has registered (a registry, not names an agent binds); describe_tools gives a tool's parameters. " +
		"component_type \"model\" lists the registered models by name and modelType (chat or decision), never their credentials, " +
		"and reports decision_nodes_enabled: a workflow decision node needs a decision model AND decision nodes enabled. " +
		"component_type \"connector_type\" lists the connector types enabled on this deployment with their subtypes; " +
		"never propose a connector type it does not list, because a type can be switched off."
}

func (t *ListPlatformComponentsTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"component_type": map[string]any{
				"type":        "string",
				"description": "Filter by component type; entrypoints are listed under \"gateway\". Default (or \"all\"): agent, workflow, gateway, connector, a2a_agent and skill. tool, model and connector_type must be requested by name.",
				"enum":        []string{"agent", "workflow", "gateway", "connector", "a2a_agent", "skill", "tool", "model", "connector_type", "all"},
			},
		},
	}
}

func (t *ListPlatformComponentsTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "total_count", typ: "integer", description: "Total components across every listed type, as the platform counts them. Present only when component_type was omitted or \"all\".", optional: true},
		resultField{name: "count", typ: "integer", description: "Number of components of the requested type, as the platform counts them. Present only when a single component_type was requested.", optional: true},
		resultField{name: "type", typ: "string", description: "The requested component type, echoed back. Single-type requests only.", optional: true},
		resultField{name: "decision_nodes_enabled", typ: "boolean", description: "Whether workflow decision nodes are enabled on this deployment. component_type \"model\" only.", optional: true},
	)
}

func (t *ListPlatformComponentsTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx.ControlQuerier == nil {
		return tool.Error("Platform service not available. This feature requires the platform service to be running."), nil
	}

	componentType, _ := args["component_type"].(string)
	if componentType == "" {
		componentType = "all"
	}

	if componentType != "all" {
		resource, ok := resourceTypeMap[componentType]
		if !ok {
			return tool.Error(fmt.Sprintf("unknown component type: %s", componentType)), nil
		}
		return queryPlatformList(ctx, toolCtx, componentType, resource)
	}
	return queryDefaultListing(ctx, toolCtx)
}

type componentListing struct {
	items                []map[string]any
	total                int
	decisionNodesEnabled *bool
}

func (l componentListing) truncated() bool { return l.total > len(l.items) }

func fetchListing(ctx context.Context, toolCtx *tool.Context, typeName, resource string) (componentListing, error) {
	start := time.Now()
	raw, err := toolCtx.ControlQuerier.Custom(ctx, "platform", "get", resource, nil)
	elapsed := time.Since(start)
	if err != nil {
		slog.Default().With("logger_name", "platform-tools").Warn("platform_list_components: query failed", "type", typeName, "traceID", toolCtx.TraceID, "error", err, "elapsed_ms", elapsed.Milliseconds())
		return componentListing{}, fmt.Errorf("failed to query %s: %w", componentDisplayWord(typeName), err)
	}
	var parsed struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Pagination struct {
				Count int `json:"count"`
			} `json:"pagination"`
		} `json:"meta"`
		DecisionNodesEnabled json.RawMessage `json:"decisionNodesEnabled"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		logUnparsableListing(toolCtx, typeName, raw, err)
		return componentListing{}, fmt.Errorf("failed to parse %s response: %w", componentDisplayWord(typeName), err)
	}
	listing := componentListing{items: projectListing(typeName, parsed.Data), total: max(parsed.Meta.Pagination.Count, len(parsed.Data))}
	if len(parsed.DecisionNodesEnabled) > 0 {
		var enabled bool
		if err := json.Unmarshal(parsed.DecisionNodesEnabled, &enabled); err != nil {
			logUnparsableListing(toolCtx, typeName, raw, err)
			return componentListing{}, fmt.Errorf("failed to parse %s decisionNodesEnabled: %w", componentDisplayWord(typeName), err)
		}
		listing.decisionNodesEnabled = &enabled
	}
	return listing, nil
}

func logUnparsableListing(toolCtx *tool.Context, typeName string, raw []byte, err error) {
	slog.Default().With("logger_name", "platform-tools").Warn("platform_list_components: response did not parse", "type", typeName, "traceID", toolCtx.TraceID, "error", err, "bytes", len(raw))
}

func projectListing(typeName string, items []map[string]any) []map[string]any {
	fields, slim := ListingFields[typeName]
	if !slim {
		return items
	}
	projected := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record := make(map[string]any, len(fields))
		for _, field := range fields {
			if v, ok := item[field]; ok && v != nil {
				record[field] = v
			}
		}
		projected = append(projected, record)
	}
	return projected
}

func queryDefaultListing(ctx context.Context, toolCtx *tool.Context) (*tool.Result, error) {
	listings := make(map[string]componentListing, len(defaultListTypes))
	failures := make(map[string]string)
	combined := make(map[string]any, len(defaultListTypes)+1)
	totalCount := 0
	for _, ct := range defaultListTypes {
		listing, err := fetchListing(ctx, toolCtx, ct, resourceTypeMap[ct])
		if err != nil {
			failures[ct] = err.Error()
			combined[ct+"s"] = map[string]any{"error": err.Error()}
			continue
		}
		listings[ct] = listing
		combined[ct+"s"] = listing.items
		totalCount += listing.total
	}
	combined["total_count"] = totalCount

	if len(failures) == len(defaultListTypes) {
		return tool.Error("Could not list any component type; " + failures[defaultListTypes[0]]), nil
	}

	msg := fmt.Sprintf("Found %d component(s) across all types", totalCount)
	if cut := cutNote(defaultListTypes, listings); cut != "" {
		msg += "; " + cut
	}
	if len(failures) > 0 {
		msg += "; could not list " + joinDisplayWords(defaultListTypes, failures)
	}

	content, err := json.MarshalIndent(combined, "", "  ")
	if err != nil {
		return tool.OK(msg, tool.WithData(combined)), nil
	}

	return tool.OK(msg,
		tool.WithData(map[string]any{"total_count": totalCount}),
		tool.WithDataObjects(tool.DataObject{
			Name:        "platform_components.json",
			Content:     content,
			MIMEType:    "application/json",
			Disposition: tool.DispositionArtifactWithPreview,
			Description: msg,
			Preview:     buildComponentListPreview(listings, failures, defaultListTypes, totalCount),
			Tags:        []string{"__working"},
		}),
	), nil
}

func cutNote(order []string, listings map[string]componentListing) string {
	parts := make([]string, 0, len(order))
	for _, ct := range order {
		if listing, ok := listings[ct]; ok && listing.truncated() {
			parts = append(parts, fmt.Sprintf("%d of %d %ss", len(listing.items), listing.total, componentDisplayWord(ct)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "the platform returned the first " + strings.Join(parts, ", ")
}

func joinDisplayWords(order []string, failures map[string]string) string {
	words := make([]string, 0, len(failures))
	for _, ct := range order {
		if _, failed := failures[ct]; failed {
			words = append(words, componentDisplayWord(ct)+"s")
		}
	}
	return strings.Join(words, ", ")
}

func componentDisplayWord(typeName string) string {
	switch typeName {
	case "gateway":
		return "entrypoint"
	case connectorTypeComponent:
		return "connector type"
	}
	return typeName
}

func queryPlatformList(ctx context.Context, toolCtx *tool.Context, typeName, resource string) (*tool.Result, error) {
	listing, err := fetchListing(ctx, toolCtx, typeName, resource)
	if err != nil {
		return tool.Error(capitalize(err.Error())), nil
	}

	data := map[string]any{
		"components": listing.items,
		"count":      listing.total,
		"type":       typeName,
	}
	inline := map[string]any{"type": typeName, "count": listing.total}
	msg := fmt.Sprintf("Found %d %s(s)", listing.total, componentDisplayWord(typeName))
	if listing.truncated() {
		msg += fmt.Sprintf("; the platform returned the first %d", len(listing.items))
	}
	if listing.decisionNodesEnabled != nil {
		data[decisionNodesEnabledKey] = *listing.decisionNodesEnabled
		inline[decisionNodesEnabledKey] = *listing.decisionNodesEnabled
		state := "disabled"
		if *listing.decisionNodesEnabled {
			state = "enabled"
		}
		msg += "; workflow decision nodes are " + state
	}

	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return tool.OK(msg, tool.WithData(data)), nil
	}

	return tool.OK(msg,
		tool.WithData(inline),
		tool.WithDataObjects(tool.DataObject{
			Name:        fmt.Sprintf("platform_%ss.json", typeName),
			Content:     content,
			MIMEType:    "application/json",
			Disposition: tool.DispositionArtifactWithPreview,
			Description: msg,
			Preview:     buildSingleTypePreview(typeName, listing),
			Tags:        []string{"__working"},
		}),
	), nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return strings.ToUpper(string(r)) + s[size:]
}

type GetComponentConfigTool struct {
	tool.SideEffectingTool
	artifactToolBase
}

func (t *GetComponentConfigTool) Name() string { return "platform_get_component_config" }

func (t *GetComponentConfigTool) Description() string {
	return "Retrieves detailed configuration for a specific platform component by type and ID. " +
		"Returns the component's full configuration including system prompt, skills, connectors, and deployment status; " +
		"a skill's references and bundled tools; a registered tool's parameters and instructions. " +
		"Use PlatformListComponents first to find available component IDs."
}

func (t *GetComponentConfigTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"component_type": map[string]any{
				"type":        "string",
				"description": "The type of component to retrieve.",
				"enum":        []string{"agent", "workflow", "gateway", "connector", "a2a_agent", "skill", "tool"},
			},
			"component_id": map[string]any{
				"type":        "string",
				"description": "The UUID id of the component to retrieve, exactly as returned in parentheses by platform_list_components (e.g. \"880e8400-...\"). Pass the id, not the component's display name.",
			},
		},
		Required: []string{"component_type", "component_id"},
	}
}

func (t *GetComponentConfigTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

func (t *GetComponentConfigTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx.ControlQuerier == nil {
		return tool.Error("Platform service not available. This feature requires the platform service to be running."), nil
	}

	componentType, _ := args["component_type"].(string)
	componentID, _ := args["component_id"].(string)

	if componentType == "" || componentID == "" {
		return tool.Error("Both component_type and component_id are required."), nil
	}
	if componentType == connectorTypeComponent {
		return tool.Error("connector_type can only be listed: use platform_list_components with component_type connector_type."), nil
	}

	resource, ok := resourceTypeMap[componentType]
	if !ok {
		return tool.Error(fmt.Sprintf("unknown component type: %s", componentType)), nil
	}

	customPath := fmt.Sprintf("%s/%s", resource, componentID)
	start := time.Now()
	raw, err := toolCtx.ControlQuerier.Custom(ctx, "platform", "get", customPath, nil)
	elapsed := time.Since(start)
	if err != nil {
		slog.Default().With("logger_name", "platform-tools").Warn("platform_get_component_config: query failed", "type", componentType, "componentID", componentID, "traceID", toolCtx.TraceID, "error", err, "elapsed_ms", elapsed.Milliseconds())
		return tool.Error(fmt.Sprintf("Failed to retrieve %s %s: %s", componentDisplayWord(componentType), componentID, err)), nil
	}

	var component map[string]any
	if unmarshalErr := json.Unmarshal(raw, &component); unmarshalErr != nil {
		return tool.Error(fmt.Sprintf("Failed to parse response: %s", unmarshalErr)), nil
	}

	name, _ := component["name"].(string)
	if name == "" {
		name = componentID
	}
	msg := fmt.Sprintf("Retrieved %s configuration: %s", componentDisplayWord(componentType), name)

	content, err := json.MarshalIndent(component, "", "  ")
	if err != nil {
		return tool.OK(msg, tool.WithData(component)), nil
	}

	preview := buildConfigPreview(componentType, component)

	return tool.OK(msg,
		tool.WithDataObjects(tool.DataObject{
			Name:        configArtifactName(componentType, name),
			Content:     content,
			MIMEType:    "application/json",
			Disposition: tool.DispositionArtifactWithPreview,
			Description: msg,
			Preview:     preview,
			Tags:        []string{"__working"},
		}),
	), nil
}

const configArtifactNameMaxBytes = 200

func configArtifactName(componentType, name string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_").Replace(name)
	safe = strings.ReplaceAll(safe, "..", "_")
	for len(safe) > configArtifactNameMaxBytes {
		_, size := utf8.DecodeLastRuneInString(safe)
		safe = safe[:len(safe)-size]
	}
	return fmt.Sprintf("%s_%s_config.json", componentType, safe)
}

func buildComponentListPreview(listings map[string]componentListing, failures map[string]string, types []string, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Platform Components (%d total)\n", total)
	for _, ct := range types {
		if reason, failed := failures[ct]; failed {
			fmt.Fprintf(&b, "\n%ss: could not be listed (%s)\n", titleWord(ct), reason)
			continue
		}
		listing, ok := listings[ct]
		if !ok || len(listing.items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s:\n", listingHeading(ct, listing))
		b.WriteString(registryNote(ct))
		for _, line := range summaryLines(listing.items, true) {
			fmt.Fprintf(&b, "  - %s\n", line)
		}
	}
	return b.String()
}

const toolRegistryNote = "  (Registered Secure Tool Runtime tools, not names an agent binds under `tools:` —\n" +
	"   a built-in group, a skill under `skills:` or a connector provides them.)\n"

func registryNote(componentType string) string {
	if componentType == "tool" {
		return toolRegistryNote
	}
	return ""
}

func titleWord(typeName string) string {
	return cases.Title(language.Und).String(componentDisplayWord(typeName))
}

func listingHeading(typeName string, listing componentListing) string {
	if listing.truncated() {
		return fmt.Sprintf("%ss (%d of %d shown)", titleWord(typeName), len(listing.items), listing.total)
	}
	return fmt.Sprintf("%ss (%d)", titleWord(typeName), listing.total)
}

func buildSingleTypePreview(typeName string, listing componentListing) string {
	if typeName == connectorTypeComponent {
		return buildConnectorTypePreview(listing.items)
	}
	var b strings.Builder
	b.WriteString(listingHeading(typeName, listing) + "\n")
	if listing.decisionNodesEnabled != nil {
		fmt.Fprintf(&b, "Workflow decision nodes enabled: %t\n", *listing.decisionNodesEnabled)
	}
	b.WriteString(registryNote(typeName))
	_, withDescription := ListingFields[typeName]
	for _, line := range summaryLines(listing.items, withDescription) {
		fmt.Fprintf(&b, "  - %s\n", line)
	}
	return b.String()
}

func buildConnectorTypePreview(types []map[string]any) string {
	names := make([]string, 0, len(types))
	for _, ct := range types {
		name, ok := ct["type"].(string)
		if !ok {
			return "Connector types: the platform response was unreadable, so the enabled types are unknown.\n"
		}
		names = append(names, name)
	}
	enabled := "none"
	if len(names) > 0 {
		enabled = strings.Join(names, ", ")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Connector Types (%d)\n", len(types))
	fmt.Fprintf(&b, "Enabled connector types on this deployment: %s\n", enabled)
	for _, ct := range types {
		subtypes := make([]string, 0)
		if raw, ok := ct["subtypes"].([]any); ok {
			for _, st := range raw {
				if m, ok := st.(map[string]any); ok {
					if name, _ := m["subtype"].(string); name != "" {
						subtypes = append(subtypes, name)
					}
				}
			}
		}
		fmt.Fprintf(&b, "  - %s: %s\n", ct["type"], strings.Join(subtypes, ", "))
	}
	return b.String()
}

func buildConfigPreview(typeName string, component map[string]any) string {
	var b strings.Builder
	name, _ := component["name"].(string)
	fmt.Fprintf(&b, "%s: %s\n", cases.Title(language.Und).String(componentDisplayWord(typeName)), name)

	if desc, ok := component["description"].(string); ok && desc != "" {
		fmt.Fprintf(&b, "Description: %s\n", desc)
	}
	if ts, ok := component["toolsets"].([]any); ok && len(ts) > 0 {
		names := make([]string, 0, len(ts))
		for _, t := range ts {
			if s, ok := t.(string); ok {
				names = append(names, s)
			}
		}
		fmt.Fprintf(&b, "Toolsets: %s\n", strings.Join(names, ", "))
	}
	if im, ok := component["inputModes"].([]any); ok && len(im) > 0 {
		names := make([]string, 0, len(im))
		for _, m := range im {
			if s, ok := m.(string); ok {
				names = append(names, s)
			}
		}
		fmt.Fprintf(&b, "Input Modes: %s\n", strings.Join(names, ", "))
	}
	if om, ok := component["outputModes"].([]any); ok && len(om) > 0 {
		names := make([]string, 0, len(om))
		for _, m := range om {
			if s, ok := m.(string); ok {
				names = append(names, s)
			}
		}
		fmt.Fprintf(&b, "Output Modes: %s\n", strings.Join(names, ", "))
	}
	if sp, ok := component["systemPrompt"].(string); ok && sp != "" {
		if len(sp) > 200 {
			sp = sp[:200] + "..."
		}
		fmt.Fprintf(&b, "System Prompt: %s\n", sp)
	}
	if ct, ok := component["type"].(string); ok && ct != "" {
		fmt.Fprintf(&b, "Type: %s\n", ct)
	}
	if st, ok := component["subtype"].(string); ok && st != "" {
		fmt.Fprintf(&b, "Subtype: %s\n", st)
	}
	if ds, ok := component["deploymentStatus"].(string); ok && ds != "" {
		fmt.Fprintf(&b, "Deployment Status: %s\n", ds)
	}
	if rs := runtimeStatusString(component["runtimeStatus"]); rs != "" {
		fmt.Fprintf(&b, "Runtime Status: %s\n", rs)
	}
	return b.String()
}

func runtimeStatusString(v any) string {
	switch rs := v.(type) {
	case string:
		return rs
	case map[string]any:
		status, _ := rs["status"].(string)
		if status == "" {
			return ""
		}
		if problems, ok := rs["problems"].([]any); ok && len(problems) > 0 {
			return fmt.Sprintf("%s (%d problem(s))", status, len(problems))
		}
		return status
	default:
		return ""
	}
}

func summaryLines(items []map[string]any, withDescription bool) []string {
	summaries := make([]string, 0, len(items))
	for _, item := range items {
		name, _ := item["name"].(string)
		if name == "" {
			continue
		}
		summary := name
		if id, ok := item["id"].(string); ok && id != "" {
			summary += fmt.Sprintf(" (%s)", id)
		}
		if st, ok := item["subtype"].(string); ok && st != "" {
			if ct, ok := item["type"].(string); ok && ct != "" {
				summary += fmt.Sprintf(" — %s/%s", ct, st)
			} else {
				summary += fmt.Sprintf(" — %s", st)
			}
		}
		if rs := runtimeStatusString(item["runtimeStatus"]); rs != "" {
			summary += fmt.Sprintf(" [%s]", rs)
		}
		if mt, _ := item["modelType"].(string); mt != "" {
			summary += fmt.Sprintf(" — %s model", mt)
		}
		if desc, _ := item["description"].(string); withDescription && desc != "" {
			summary += ": " + previewDescription(desc)
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func previewDescription(desc string) string {
	desc = strings.Join(strings.Fields(desc), " ")
	if utf8.RuneCountInString(desc) <= previewDescriptionMaxRunes {
		return desc
	}
	runes := []rune(desc)
	return strings.TrimRight(string(runes[:previewDescriptionMaxRunes]), " ") + "…"
}
