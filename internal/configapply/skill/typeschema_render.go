// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/codegen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/typeschema"
)

func RenderGatewayTypeSchema(out io.Writer, s typeschema.GatewayTypeSchema) {
	_, _ = fmt.Fprintf(out, "## type: %s\n\n", s.Type)
	if s.Name != "" {
		_, _ = fmt.Fprintf(out, "**%s**\n\n", s.Name)
	}
	if s.Description != "" {
		_, _ = fmt.Fprintf(out, stringNL2Fmt, s.Description)
	}
	if s.Warning != "" {
		_, _ = fmt.Fprintf(out, "> ⚠ %s\n\n", s.Warning)
	}
	renderTypeFieldsTable(out, s.Fields)
	if len(s.RequiredEnvVars) > 0 {
		_, _ = fmt.Fprintf(out, "\n**Required environment variables**: %s\n", strings.Join(s.RequiredEnvVars, ", "))
	}
}

func RenderConnectorSubtypeSchema(out io.Writer, s typeschema.ConnectorSubtypeSchema) {
	_, _ = fmt.Fprintf(out, "### subtype: %s%s\n\n", s.Subtype, experimentalTag(s.Experimental))
	if s.Name != "" {
		_, _ = fmt.Fprintf(out, "**%s**\n\n", s.Name)
	}
	if s.Description != "" {
		_, _ = fmt.Fprintf(out, stringNL2Fmt, s.Description)
	}
	if s.UsageGuidance != "" {
		_, _ = fmt.Fprintf(out, "**Usage guidance**\n\n%s\n\n", strings.TrimRight(s.UsageGuidance, "\n"))
	}
	if s.Warning != "" {
		_, _ = fmt.Fprintf(out, "> ⚠ %s\n\n", s.Warning)
	}
	if s.ConnectionTemplate != "" {
		_, _ = fmt.Fprintf(out, "**Connection template**: `%s`\n\n", s.ConnectionTemplate)
	}
	renderTypeFieldsTable(out, s.Fields)
}

func experimentalTag(experimental bool) string {
	if experimental {
		return " *(experimental)*"
	}
	return ""
}

func RenderNodeTypeSchema(out io.Writer, s codegen.NodeTypeSchema) {
	_, _ = fmt.Fprintf(out, "## node type: %s\n\n", s.NodeType)
	if s.Description != "" {
		_, _ = fmt.Fprintf(out, stringNL2Fmt, s.Description)
	}
	if len(s.Fields) == 0 {
		_, _ = fmt.Fprintln(out, "_No fields._")
		return
	}
	_, _ = fmt.Fprintln(out, "| Field | Type | Required | Description |")
	_, _ = fmt.Fprintln(out, "|---|---|---|---|")
	for _, f := range s.Fields {
		req := ""
		if f.Required {
			req = "yes"
		}
		desc := f.Description
		if desc == "" {
			desc = noDescriptionFallback
		}
		_, _ = fmt.Fprintf(out, "| `%s` | `%s` | %s | %s |\n",
			f.JSONName, f.DisplayType(), req, MarkdownEscape(desc))
	}
}

func renderTypeFieldsTable(out io.Writer, fs []typeschema.TypeFieldSchema) {
	if len(fs) == 0 {
		_, _ = fmt.Fprintln(out, "_No fields._")
		return
	}
	_, _ = fmt.Fprintln(out, "| Field | Type | Required | Default | Validation | Description |")
	_, _ = fmt.Fprintln(out, "|---|---|---|---|---|---|")
	for _, f := range fs {
		req := requiredCell(f)
		def := ""
		if f.Default != nil {
			def = fmt.Sprintf("`%v`", f.Default)
		}
		desc := f.Hint
		if desc == "" {
			desc = noDescriptionFallback
		}
		_, _ = fmt.Fprintf(out, "| `%s` | `%s` | %s | %s | %s | %s |\n",
			f.Name,
			fieldTypeCell(f),
			req,
			def,
			validationCellForType(f),
			MarkdownEscape(desc),
		)
	}
	for _, f := range fs {
		if c, ok := typeschema.LookupCustomType(f.Type); ok {
			renderCustomFieldType(out, f.Name, c)
		}
	}
}

func requiredCell(f typeschema.TypeFieldSchema) string {
	if !f.Required {
		return ""
	}
	if len(f.ShownWhen) == 0 {
		return "yes"
	}
	keys := make([]string, 0, len(f.ShownWhen))
	for k := range f.ShownWhen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	conds := make([]string, 0, len(keys))
	for _, k := range keys {
		conds = append(conds, fmt.Sprintf("`%s`=%s", k, conditionValues(f.ShownWhen[k])))
	}
	return "yes, when " + strings.Join(conds, " and ")
}

func conditionValues(want any) string {
	var vals []string
	switch w := want.(type) {
	case []string:
		vals = w
	case []any:
		for _, v := range w {
			vals = append(vals, fmt.Sprint(v))
		}
	default:
		vals = []string{fmt.Sprint(w)}
	}
	return "`" + strings.Join(vals, "` or `") + "`"
}

func renderCustomFieldType(out io.Writer, fieldName string, c typeschema.CustomFieldType) {
	_, _ = fmt.Fprintf(out, "\n**Inner schema for `%s` (`%s`)**:\n\n", fieldName, c.Type)
	if c.Description != "" {
		_, _ = fmt.Fprintf(out, stringNL2Fmt, c.Description)
	}
	switch c.ItemKind {
	case "array<object>":
		_, _ = fmt.Fprintln(out, "Each entry is an object with:")
		_, _ = fmt.Fprintln(out, "")
	case "object":
		_, _ = fmt.Fprintln(out, "Object with:")
		_, _ = fmt.Fprintln(out, "")
	}
	_, _ = fmt.Fprintln(out, "| Field | Type | Required | Default | Validation | Description |")
	_, _ = fmt.Fprintln(out, "|---|---|---|---|---|---|")
	for _, f := range typeschema.SortedFields(c.Fields) {
		req := ""
		if f.Required {
			req = "yes"
		}
		def := ""
		if f.Default != nil {
			def = fmt.Sprintf("`%v`", f.Default)
		}
		desc := f.Hint
		if desc == "" {
			desc = noDescriptionFallback
		}
		_, _ = fmt.Fprintf(out, "| `%s` | `%s` | %s | %s | %s | %s |\n",
			f.Name,
			fieldTypeCell(f),
			req,
			def,
			validationCellForType(f),
			MarkdownEscape(desc),
		)
	}
	_, _ = fmt.Fprintln(out, "")
}

func fieldTypeCell(f typeschema.TypeFieldSchema) string {
	if f.Secret {
		return f.Type + " (secret)"
	}
	return f.Type
}

func validationCellForType(f typeschema.TypeFieldSchema) string {
	parts := []string{}
	if f.MinLen >= 0 && f.MaxLen >= 0 {
		parts = append(parts, fmt.Sprintf("len %d–%d", f.MinLen, f.MaxLen))
	} else if f.MinLen >= 0 {
		parts = append(parts, fmt.Sprintf("min len %d", f.MinLen))
	} else if f.MaxLen >= 0 {
		parts = append(parts, fmt.Sprintf("max len %d", f.MaxLen))
	}
	switch {
	case f.MinValue != nil && f.MaxValue != nil:
		parts = append(parts, fmt.Sprintf("range %d–%d", *f.MinValue, *f.MaxValue))
	case f.MinValue != nil:
		parts = append(parts, fmt.Sprintf("min %d", *f.MinValue))
	case f.MaxValue != nil:
		parts = append(parts, fmt.Sprintf("max %d", *f.MaxValue))
	}
	if len(f.OneOf) > 0 {
		vs := make([]string, len(f.OneOf))
		for i, o := range f.OneOf {
			vs[i] = o.Value
		}
		parts = append(parts, "one of: "+strings.Join(vs, ", "))
	}
	if f.Pattern != "" {
		parts = append(parts, "matches regex")
	}
	if f.Secret {
		parts = append(parts, "secret")
	}
	return strings.Join(parts, "; ")
}

func RenderGatewayExample(buf *bytes.Buffer, name string, s typeschema.GatewayTypeSchema) {
	if name == "" {
		name = "example_" + s.Type + "_entrypoint"
	}
	upperName := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))

	fmt.Fprintln(buf, "kind: entrypoint")
	fmt.Fprintf(buf, "name: %s\n", name)
	fmt.Fprintf(buf, "description: \"Example %s entrypoint. Replace with a real description (10+ chars).\"\n", s.Type)
	fmt.Fprintln(buf, "spec:")
	fmt.Fprintf(buf, "  type: %s\n", s.Type)
	fmt.Fprintln(buf, "  values:")
	if len(s.Fields) == 0 {
		fmt.Fprintln(buf, "    {}  # this entrypoint type has no values fields")
		return
	}
	writeApplicableFieldExamples(buf, s.Fields, name, upperName)
}

func RenderConnectorExample(buf *bytes.Buffer, name string, s typeschema.ConnectorSubtypeSchema) {
	if name == "" {
		name = "example_" + s.Type + "_" + s.Subtype
	}
	upperName := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))

	fmt.Fprintln(buf, "kind: connector")
	fmt.Fprintf(buf, "name: %s\n", name)
	fmt.Fprintf(buf, "description: \"Example %s/%s connector. Replace with a real description (10+ chars).\"\n", s.Type, s.Subtype)
	fmt.Fprintln(buf, "spec:")
	fmt.Fprintf(buf, "  type: %s\n", s.Type)
	fmt.Fprintf(buf, "  subtype: %s\n", s.Subtype)
	fmt.Fprintln(buf, "  values:")
	if len(s.Fields) == 0 {
		fmt.Fprintln(buf, "    {}  # this subtype has no values fields")
		return
	}
	writeApplicableFieldExamples(buf, s.Fields, name, upperName)
}

func writeApplicableFieldExamples(buf *bytes.Buffer, fields []typeschema.TypeFieldSchema, resourceName, upperResourceName string) {
	chosen := make(map[string]string, len(fields))
	for _, f := range fields {
		if v, ok := exampleEffectiveValue(f); ok {
			chosen[f.Name] = v
		}
	}
	for _, f := range fields {
		if !shownUnder(f.ShownWhen, chosen) {
			continue
		}
		writeTypeFieldExample(buf, "    ", f, resourceName, upperResourceName)
	}
}

func exampleEffectiveValue(f typeschema.TypeFieldSchema) (string, bool) {
	if f.Secret {
		return "", false
	}
	if !f.Required {
		if f.Default == nil {
			return "", false
		}
		return fmt.Sprint(f.Default), true
	}
	switch typeFieldValueKind(f) {
	case "string":
		return stringExampleValue(f), true
	case "bool":
		d, _ := f.Default.(bool)
		return fmt.Sprint(d), true
	case "select":
		v := selectExampleValue(f)
		return v, v != ""
	}
	return "", false
}

func selectExampleValue(f typeschema.TypeFieldSchema) string {
	d, _ := f.Default.(string)
	if len(f.OneOf) == 0 {
		return d
	}
	if d != "" && slices.ContainsFunc(f.OneOf, func(o typeschema.TypeFieldOption) bool { return o.Value == d }) {
		return d
	}
	return f.OneOf[0].Value
}

func shownUnder(conditions map[string]any, chosen map[string]string) bool {
	for key, want := range conditions {
		got, settled := chosen[key]
		if !settled {
			continue
		}
		if !conditionAccepts(want, got) {
			return false
		}
	}
	return true
}

func conditionAccepts(want any, got string) bool {
	switch w := want.(type) {
	case []string:
		return slices.Contains(w, got)
	case []any:
		return slices.ContainsFunc(w, func(v any) bool { return fmt.Sprint(v) == got })
	default:
		return fmt.Sprint(w) == got
	}
}

func writeTypeFieldExample(buf *bytes.Buffer, indent string, f typeschema.TypeFieldSchema, resourceName, upperResourceName string) {
	prefix := indent
	if !f.Required {
		prefix = indent + "# optional: "
	}
	if f.Secret {
		placeholder := fmt.Sprintf("${%s_%s}", upperResourceName, strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_")))
		fmt.Fprintf(buf, "%s%s: %s  # secret — provide via env var\n", indent, f.Name, placeholder)
		return
	}
	if c, ok := typeschema.LookupCustomType(f.Type); ok && c.Example != "" {
		fmt.Fprintf(buf, "%s%s:\n", prefix, f.Name)
		writeIndentedSnippet(buf, indent+"  ", c.Example)
		return
	}

	switch typeFieldValueKind(f) {
	case "string":
		val := stringExampleValue(f)
		if val == "" {
			val = "..."
		}
		fmt.Fprintf(buf, "%s%s: %q\n", prefix, f.Name, val)
	case "int":
		v := int(0)
		switch d := f.Default.(type) {
		case int:
			v = d
		case int64:
			v = int(d)
		case float64:
			v = int(d)
		}
		if v == 0 && f.MinValue != nil {
			v = *f.MinValue
		}
		if v == 0 && f.MinLen > 0 {
			v = f.MinLen
		}
		fmt.Fprintf(buf, "%s%s: %d\n", prefix, f.Name, v)
	case "bool":
		v := false
		if d, ok := f.Default.(bool); ok {
			v = d
		}
		fmt.Fprintf(buf, "%s%s: %t\n", prefix, f.Name, v)
	case "select":
		val := selectExampleValue(f)
		if val == "" {
			val = "..."
		}
		fmt.Fprintf(buf, "%s%s: %q\n", prefix, f.Name, val)
	default:
		fmt.Fprintf(buf, "%s%s: null  # value of type %s\n", prefix, f.Name, f.Type)
	}
}

func writeIndentedSnippet(buf *bytes.Buffer, indent, snippet string) {
	for line := range strings.SplitSeq(strings.TrimRight(snippet, "\n"), "\n") {
		if line == "" {
			fmt.Fprintln(buf)
			continue
		}
		fmt.Fprintf(buf, "%s%s\n", indent, line)
	}
}

func typeFieldValueKind(f typeschema.TypeFieldSchema) string {
	switch strings.ToLower(f.Type) {
	case "string", "textarea", "agent_select", "workflow_select":
		return "string"
	case "number", "int", "integer":
		return "int"
	case "bool", "boolean":
		return "bool"
	case "select", "radio":
		if len(f.OneOf) > 0 {
			return "select"
		}
		return "string"
	}
	return ""
}

func stringExampleValue(f typeschema.TypeFieldSchema) string {
	if d, ok := f.Default.(string); ok && d != "" {
		return d
	}
	if f.Placeholder != "" {
		return f.Placeholder
	}
	if f.MinLen > 0 {
		return strings.Repeat("x", f.MinLen)
	}
	return ""
}

const (
	noDescriptionFallback = "(no description)"
	stringNL2Fmt          = "%s\n\n"
)
