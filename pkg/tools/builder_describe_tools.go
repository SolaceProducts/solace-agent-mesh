// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	describeToolsPath         = "tools/describe"
	describeMaxRefs           = 25
	describeMaxConnectorTools = 50
	describedToolsArtifact    = "described_tools.json"

	describeStatusOK           = "ok"
	describeStatusNotFound     = "not_found"
	describeStatusNotPermitted = "not_permitted"
	describeStatusUnavailable  = "unavailable"

	describeListTruncatedNote = "more tools than listed: describe one by name with tool"
)

var (
	describeStatusOrder = []string{describeStatusOK, describeStatusNotFound, describeStatusNotPermitted, describeStatusUnavailable}
	describeRefFields   = []string{"tool_name", "connector", "tool"}
)

type describeToolRef struct {
	ToolName  string `json:"toolName,omitempty"`
	Connector string `json:"connector,omitempty"`
	Tool      string `json:"tool,omitempty"`
}

type describeToolsRequest struct {
	Tools []describeToolRef `json:"tools"`
}

type describeToolNodeVerdict struct {
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
}

type describedTool struct {
	Ref           describeToolRef         `json:"ref"`
	Status        string                  `json:"status"`
	Reason        string                  `json:"reason,omitempty"`
	Kind          string                  `json:"kind,omitempty"`
	ConnectorName string                  `json:"connectorName,omitempty"`
	ConnectorType string                  `json:"connectorType,omitempty"`
	Name          string                  `json:"name,omitempty"`
	ConcreteName  string                  `json:"concreteName,omitempty"`
	Description   string                  `json:"description,omitempty"`
	Parameters    json.RawMessage         `json:"parameters,omitempty"`
	Result        json.RawMessage         `json:"result,omitempty"`
	Effect        string                  `json:"effect,omitempty"`
	ToolNode      describeToolNodeVerdict `json:"toolNode"`
	Notes         []string                `json:"notes,omitempty"`
	Suggestions   []string                `json:"suggestions,omitempty"`
}

type describeToolsResponse struct {
	Tools []describedTool `json:"tools"`
}

type DescribeToolsTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *DescribeToolsTool) Name() string { return "describe_tools" }

func (t *DescribeToolsTool) Description() string {
	return "Reports, for each tool you plan to use, the parameters it takes, the result fields it returns, " +
		"and whether it can back a workflow tool node on this deployment. " +
		"Give 1 to 25 refs: {tool_name} for a built-in tool or a <toolset>__<tool> tool, or {connector, tool} for a " +
		"connector that already exists on the platform (a connector without tool lists the tools it exposes, at most 50; " +
		"when it has more, the result says the list was cut, so describe the one you need by name with tool). " +
		"Write a tool node's input: keys only from the parameters shown, and reference its output only through the " +
		"result fields shown. A status other than ok (not_found, not_permitted, unavailable) means do not plan on " +
		"that tool, and the reason says why. Write a connector name exactly as shown. " +
		"The full schemas are in the described_tools.json artifact."
}

func (t *DescribeToolsTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"tools": map[string]any{
				"type":        "array",
				"description": "1 to 25 tools to describe. Each item has either tool_name, or connector with an optional tool.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool_name": map[string]any{"type": "string", "description": "A built-in tool name, or <toolset>__<tool> for a custom toolset tool."},
						"connector": map[string]any{"type": "string", "description": "The exact name of an existing connector."},
						"tool":      map[string]any{"type": "string", "description": "With connector: the tool's base name. Omit it to describe every tool the connector exposes."},
					},
				},
			},
		},
		Required: []string{"tools"},
	}
}

func (t *DescribeToolsTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "tools", typ: "array", description: "One entry per described tool, each {ref, status, reason, kind, connectorName, connectorType, name, toolNode, notes, suggestions}. Each tool's parameters, result schema and description are in the described_tools.json artifact, not here."},
		resultField{name: "summary", typ: "string", description: "Count of tools per status, for example \"4 tools: 2 ok, 1 not_found, 1 unavailable\", followed by the connectors whose tool list was cut when there are any."},
		resultField{name: "artifact", typ: "string", description: "Filename of the artifact holding the full platform response: described_tools.json."},
	)
}

func (t *DescribeToolsTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx.ControlQuerier == nil {
		return tool.Error("Platform service not available. Describing tools requires the platform service to be running."), nil
	}
	refs, err := describeRefsFromArgs(args)
	if err != nil {
		return tool.Error(err.Error()), nil
	}
	body, err := json.Marshal(describeToolsRequest{Tools: refs})
	if err != nil {
		return tool.Error("failed to marshal request: " + err.Error()), nil
	}

	raw, err := toolCtx.ControlQuerier.Custom(ctx, "platform", "post", describeToolsPath, json.RawMessage(body))
	if err != nil {
		return tool.Error("describing tools failed: " + err.Error()), nil
	}
	var resp describeToolsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return tool.Error("the platform returned a tools/describe response this tool cannot read: " + err.Error()), nil
	}

	summary := describeSummary(resp.Tools) + cutListsSummary(resp.Tools)
	return tool.OK("Described "+summary,
		tool.WithData(map[string]any{
			"tools":    describedToolSummaries(resp.Tools),
			"summary":  summary,
			"artifact": describedToolsArtifact,
		}),
		tool.WithDataObjects(tool.DataObject{
			Name:        describedToolsArtifact,
			Content:     indentedJSON(raw),
			MIMEType:    "application/json",
			Disposition: tool.DispositionArtifactWithPreview,
			Preview:     describedToolsPreview(resp.Tools),
			Description: "Parameters, result fields and tool-node usability of the tools described",
			Tags:        []string{a2a.ArtifactTagWorking},
		}),
	), nil
}

func describeRefsFromArgs(args map[string]any) ([]describeToolRef, error) {
	items, _ := args["tools"].([]any)
	if len(items) == 0 {
		return nil, errors.New("tools is required: give 1 to 25 refs")
	}
	if len(items) > describeMaxRefs {
		return nil, fmt.Errorf("tools has %d refs; describe at most %d per call", len(items), describeMaxRefs)
	}
	refs := make([]describeToolRef, 0, len(items))
	for i, item := range items {
		ref, err := describeRefFromArg(i, item)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func describeRefFromArg(i int, item any) (describeToolRef, error) {
	entry, ok := item.(map[string]any)
	if !ok {
		return describeToolRef{}, fmt.Errorf("tools[%d] must be an object with tool_name, or connector and optionally tool", i)
	}
	for _, key := range slices.Sorted(maps.Keys(entry)) {
		if !slices.Contains(describeRefFields, key) {
			return describeToolRef{}, fmt.Errorf("tools[%d]: unknown field %q (use tool_name, connector, tool)", i, key)
		}
	}
	values := make([]string, len(describeRefFields))
	for field, key := range describeRefFields {
		value, err := refString(entry, key)
		if err != nil {
			return describeToolRef{}, fmt.Errorf("tools[%d].%w", i, err)
		}
		values[field] = value
	}
	ref := describeToolRef{ToolName: values[0], Connector: values[1], Tool: values[2]}
	switch {
	case ref.ToolName != "" && (ref.Connector != "" || ref.Tool != ""):
		return describeToolRef{}, fmt.Errorf("tools[%d]: give either tool_name or connector (with an optional tool), not both", i)
	case ref.Tool != "" && ref.Connector == "":
		return describeToolRef{}, fmt.Errorf("tools[%d]: tool needs connector", i)
	case ref.ToolName == "" && ref.Connector == "":
		return describeToolRef{}, fmt.Errorf("tools[%d]: needs tool_name or connector", i)
	}
	return ref, nil
}

func refString(entry map[string]any, key string) (string, error) {
	v, present := entry[key]
	if !present || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	if strings.TrimSpace(s) == "" {
		return "", nil
	}
	return s, nil
}

func cutConnectors(tools []describedTool) []string {
	var names []string
	for _, t := range tools {
		if slices.Contains(t.Notes, describeListTruncatedNote) && !slices.Contains(names, t.ConnectorName) {
			names = append(names, t.ConnectorName)
		}
	}
	return names
}

func cutListsSummary(tools []describedTool) string {
	names := cutConnectors(tools)
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("; the tool list of %s was cut at %d tools", strings.Join(names, ", "), describeMaxConnectorTools)
}

func cutListsNotice(tools []describedTool) string {
	var b strings.Builder
	for _, name := range cutConnectors(tools) {
		fmt.Fprintf(&b, "%s: only the first %d tools are listed; %s.\n", name, describeMaxConnectorTools, describeListTruncatedNote)
	}
	return b.String()
}

func describeSummary(tools []describedTool) string {
	counts := map[string]int{}
	for _, t := range tools {
		counts[t.Status]++
	}
	var parts []string
	for _, status := range describeStatusOrder {
		if n := counts[status]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, status))
			delete(counts, status)
		}
	}
	for _, status := range slices.Sorted(maps.Keys(counts)) {
		parts = append(parts, fmt.Sprintf("%d %s", counts[status], status))
	}
	noun := "tools"
	if len(tools) == 1 {
		noun = "tool"
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d %s", len(tools), noun)
	}
	return fmt.Sprintf("%d %s: %s", len(tools), noun, strings.Join(parts, ", "))
}

func describedToolSummaries(tools []describedTool) []describedTool {
	out := make([]describedTool, len(tools))
	for i, t := range tools {
		t.Parameters, t.Result, t.Description, t.ConcreteName = nil, nil, "", ""
		out[i] = t
	}
	return out
}

func indentedJSON(raw []byte) []byte {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return raw
	}
	return buf.Bytes()
}

const previewDash = " — "

type describedDetail int

const (
	detailNames describedDetail = iota
	detailInputs
	detailFull
)

func describedToolsPreview(tools []describedTool) string {
	tiers := []struct {
		detail describedDetail
		elided string
	}{
		{detailFull, ""},
		{detailInputs, "result fields and notes"},
		{detailNames, "parameters, result fields and notes"},
	}
	notice := cutListsNotice(tools)
	var rendered, previousBody string
	for i, tier := range tiers {
		body := renderDescribedTools(tools, tier.detail)
		if i > 0 && body == previousBody {
			continue
		}
		previousBody, rendered = body, notice+body
		if tier.elided != "" {
			rendered += fmt.Sprintf("load_artifact %s for %s.\n", describedToolsArtifact, tier.elided)
		}
		if previewChars(rendered) <= previewBudgetChars {
			break
		}
	}
	return capPreview(rendered, describedToolsArtifact)
}

func renderDescribedTools(tools []describedTool, detail describedDetail) string {
	var b strings.Builder
	for _, t := range tools {
		b.WriteString(renderDescribedTool(t, detail))
		b.WriteString("\n")
	}
	return b.String()
}

func renderDescribedTool(t describedTool, detail describedDetail) string {
	label := describedLabel(t)
	if t.Status != describeStatusOK {
		return label + ": " + describedProblem(t)
	}
	var b strings.Builder
	b.WriteString(label + ":")
	if detail >= detailInputs {
		fmt.Fprintf(&b, " in(%s)", schemaSignature(t.Parameters, true))
	}
	if detail == detailFull {
		fmt.Fprintf(&b, " out(%s)", schemaSignature(t.Result, false))
	}
	if detail == detailNames {
		b.WriteString(" ok")
	}
	if t.ToolNode.Usable {
		b.WriteString(" toolNode=yes")
	} else {
		b.WriteString(" toolNode=no")
		if t.ToolNode.Reason != "" {
			b.WriteString(" (" + t.ToolNode.Reason + ")")
		}
	}
	if detail == detailFull && len(t.Notes) > 0 {
		b.WriteString(previewDash + "notes: " + strings.Join(t.Notes, "; "))
	}
	return b.String()
}

func describedLabel(t describedTool) string {
	if t.Status == describeStatusOK {
		switch {
		case t.ConnectorName != "" && t.Name != "":
			return t.ConnectorName + "." + t.Name
		case t.Name != "":
			return t.Name
		}
	}
	switch {
	case t.Ref.ToolName != "":
		return t.Ref.ToolName
	case t.Ref.Tool != "":
		return t.Ref.Connector + "." + t.Ref.Tool
	default:
		return t.Ref.Connector
	}
}

func describedProblem(t describedTool) string {
	if t.Status == describeStatusNotFound && len(t.Suggestions) > 0 {
		return t.Status + " (did you mean: " + strings.Join(t.Suggestions, ", ") + ")"
	}
	if t.Reason == "" {
		return t.Status
	}
	return t.Status + previewDash + t.Reason
}
