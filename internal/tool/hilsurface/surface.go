// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hilsurface

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const maxApprovalMessageBytes = 1024

const maxRenderedTemplateBytes = 64 << 10

var errTemplateOutputLimit = errors.New("approval message template output limit reached")

type limitedWriter struct {
	buf   strings.Builder
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	room := w.limit - w.buf.Len()
	if len(p) > room {
		w.buf.Write(p[:max(room, 0)])
		return max(room, 0), errTemplateOutputLimit
	}
	return w.buf.Write(p)
}

func renderApprovalMessage(message string, toolArgs map[string]any) string {
	if message == "" || !strings.Contains(message, "{{") {
		return message
	}
	tmpl, err := parseDataOnlyApprovalTemplate(message)
	if err != nil {
		return message
	}
	out := &limitedWriter{limit: maxRenderedTemplateBytes}
	if err := tmpl.Execute(out, toolArgs); err != nil && !errors.Is(err, errTemplateOutputLimit) {
		return message
	}
	return capApprovalMessage(strings.ReplaceAll(out.buf.String(), "<no value>", ""))
}

func capApprovalMessage(out string) string {
	if len(out) <= maxApprovalMessageBytes {
		return out
	}
	const ellipsis = "…"
	cut := maxApprovalMessageBytes - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + ellipsis
}

func parseDataOnlyApprovalTemplate(message string) (*template.Template, error) {
	return template.New("approval").Option("missingkey=zero").Parse(message)
}

type RequesterKind string

const (
	RequesterAgent    RequesterKind = "Agent"
	RequesterWorkflow RequesterKind = "Workflow"
)

type Request struct {
	RequestID      string
	ToolName       string
	RequesterKind  RequesterKind
	RequesterName  string
	ToolArgs       map[string]any
	Schema         *tool.JSONSchema
	Message        string
	LiteralMessage string
	ShowArgs       bool
	Timeout        time.Duration
}

func Generate(req Request) (json.RawMessage, error) {
	if req.Message != "" && req.LiteralMessage != "" {
		return nil, errors.New("an approval carries a Message template or a LiteralMessage, not both")
	}
	requestID, toolName := req.RequestID, req.ToolName
	requesterKind, agentName := req.RequesterKind, req.RequesterName
	schema, message, showArgs, timeout := req.Schema, req.Message, req.ShowArgs, req.Timeout
	toolArgs := req.ToolArgs
	toolArgs = RedactToolArgs(toolArgs)

	var components []map[string]any
	var columnChildren []string

	components = append(components,
		map[string]any{
			"id":        "warn-icon",
			"component": "Icon",
			"icon":      "warning",
		},
		map[string]any{
			"id":        "title",
			"component": "Text",
			"text":      A2UILiteral("Approval Required"),
			"variant":   "h3",
		},
		map[string]any{
			"id":        "title-row",
			"component": "Row",
			"children":  map[string]any{"explicitList": []string{"warn-icon", "title"}},
		},
	)
	columnChildren = append(columnChildren, "title-row")

	infoText := fmt.Sprintf("%s %q wants to run tool: %s", string(requesterKind), agentName, toolName)
	components = append(components, map[string]any{
		"id":        "info",
		"component": "Text",
		"text":      A2UILiteral(infoText),
		"variant":   "body",
	})
	columnChildren = append(columnChildren, "info")

	if showArgs && len(toolArgs) > 0 {
		argComponents, argChildren := buildArgFields(toolArgs, schema)
		components = append(components, argComponents...)
		columnChildren = append(columnChildren, argChildren...)
	}

	rendered := capApprovalMessage(req.LiteralMessage)
	if message != "" {
		rendered = renderApprovalMessage(message, toolArgs)
	}
	if message != "" || req.LiteralMessage != "" {
		components = append(components, map[string]any{
			"id":        "message",
			"component": "Text",
			"text":      A2UILiteral(rendered),
			"variant":   "body",
		})
		columnChildren = append(columnChildren, "message")
	}

	components = append(components,
		map[string]any{
			"id":        "approve-text",
			"component": "Text",
			"text":      A2UILiteral("Approve"),
		},
		map[string]any{
			"id":        "approve-btn",
			"component": "Button",
			"child":     "approve-text",
			"variant":   "primary",
			"action": map[string]any{
				"event": map[string]any{
					"name":           "submit",
					"context":        map[string]any{"approved": true},
					"completionText": "Approved",
				},
			},
		},
		map[string]any{
			"id":        "deny-text",
			"component": "Text",
			"text":      A2UILiteral("Deny"),
		},
		map[string]any{
			"id":        "deny-btn",
			"component": "Button",
			"child":     "deny-text",
			"action": map[string]any{
				"event": map[string]any{
					"name":           "submit",
					"context":        map[string]any{"approved": false},
					"completionText": "Denied",
				},
			},
		},
		map[string]any{
			"id":        "actions",
			"component": "Row",
			"children":  map[string]any{"explicitList": []string{"approve-btn", "deny-btn"}},
			"justify":   "end",
		},
	)
	columnChildren = append(columnChildren, "actions")

	minutes := int(math.Round(timeout.Minutes()))
	unit := "minutes"
	if minutes == 1 {
		unit = "minute"
	}
	expiryText := fmt.Sprintf("Expires in %d %s", minutes, unit)
	components = append(components, map[string]any{
		"id":        "countdown",
		"component": "Text",
		"text":      A2UILiteral(expiryText),
		"variant":   "caption",
	})
	columnChildren = append(columnChildren, "countdown")

	components = append(components,
		map[string]any{
			"id":        "main-col",
			"component": "Column",
			"children":  map[string]any{"explicitList": columnChildren},
		},
		map[string]any{
			"id":        "root",
			"component": "Card",
			"child":     "main-col",
		},
	)

	surface := map[string]any{
		"surfaceId":  requestID,
		"catalogId":  "https://a2ui.org/specification/v0_9/basic_catalog.json",
		"components": components,
		"dataModel":  map[string]any{},
	}

	return json.Marshal(surface)
}

func buildArgFields(toolArgs map[string]any, schema *tool.JSONSchema) (components []map[string]any, columnChildren []string) {
	keys := make([]string, 0, len(toolArgs))
	for k := range toolArgs {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	type propMeta struct {
		description string
		propType    string
	}
	propLookup := make(map[string]propMeta)
	if schema != nil {
		for name, raw := range schema.Properties {
			if m, ok := raw.(map[string]any); ok {
				desc, _ := m["description"].(string)
				ptype, _ := m["type"].(string)
				propLookup[name] = propMeta{description: desc, propType: ptype}
			}
		}
	}

	var argFieldIDs []string
	for _, key := range keys {
		value := toolArgs[key]
		fieldID := fmt.Sprintf("arg-%s", key)

		valueStr := formatArgValue(value)

		meta := propLookup[key]

		labelComp := map[string]any{
			"id":        fieldID + "-label",
			"component": "Text",
			"text":      A2UILiteral(key),
			"variant":   "caption",
		}
		if meta.description != "" {
			labelComp["tooltip"] = meta.description
		}
		components = append(components, labelComp)

		components = append(components, map[string]any{
			"id":          fieldID + "-value",
			"component":   "Text",
			"text":        A2UILiteral(valueStr),
			"variant":     "body",
			"multiline":   true,
			"contentType": detectArgContentType(value),
		})

		components = append(components, map[string]any{
			"id":        fieldID,
			"component": "Column",
			"children":  map[string]any{"explicitList": []string{fieldID + "-label", fieldID + "-value"}},
		})
		argFieldIDs = append(argFieldIDs, fieldID)
	}

	argsColID := "args-fields"
	components = append(components, map[string]any{
		"id":        argsColID,
		"component": "Column",
		"children":  map[string]any{"explicitList": argFieldIDs},
	})
	components = append(components, map[string]any{
		"id":        "args-card",
		"component": "Card",
		"child":     argsColID,
	})
	columnChildren = append(columnChildren, "args-card")

	return components, columnChildren
}

func formatArgValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

var htmlTagPattern = regexp.MustCompile(`(?i)</?(?:!doctype|a|article|aside|audio|b|blockquote|body|br|button|caption|code|col|colgroup|dd|div|dl|dt|em|figcaption|figure|footer|form|h[1-6]|head|header|hr|html|i|iframe|img|input|label|li|link|main|meta|nav|ol|option|p|pre|script|section|select|small|source|span|strong|style|sub|summary|sup|svg|table|tbody|td|tfoot|th|thead|title|tr|u|ul|video)(?:\s[^>]*)?/?>`)

func detectArgContentType(value any) string {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		switch {
		case trimmed == "":
			return "text"
		case htmlTagPattern.MatchString(trimmed):
			return "html"
		case looksLikeJSON(trimmed):
			return "json"
		default:
			return "text"
		}
	case nil, bool, float64:
		return "text"
	default:
		return "json"
	}
}

func looksLikeJSON(s string) bool {
	if len(s) < 2 {
		return false
	}
	first, last := s[0], s[len(s)-1]
	if (first == '{' && last == '}') || (first == '[' && last == ']') {
		return json.Valid([]byte(s))
	}
	return false
}

func A2UILiteral(s string) map[string]any {
	return map[string]any{"literalString": s}
}
