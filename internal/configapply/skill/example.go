// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/codegen"
)

func RenderExample(buf *bytes.Buffer, ks codegen.KindSchema, name string) {
	if name == "" {
		name = "example_" + ks.Kind
	}
	upperName := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))

	fmt.Fprintf(buf, "kind: %s\n", ks.Kind)
	fmt.Fprintf(buf, "name: %s\n", name)

	var specFields []codegen.FieldSchema
	for _, f := range ks.Fields {
		switch {
		case f.JSONName == "kind" || isNameDerivedField(ks.Kind, f.JSONName):
			continue
		case f.JSONName == "description":
			writeExampleField(buf, f, upperName, ks.Kind)
		default:
			specFields = append(specFields, f)
		}
	}

	if len(specFields) == 0 {
		return
	}
	fmt.Fprintf(buf, "spec:\n")
	var specBuf bytes.Buffer
	for _, f := range specFields {
		writeExampleField(&specBuf, f, upperName, ks.Kind)
	}
	for _, line := range strings.Split(strings.TrimRight(specBuf.String(), "\n"), "\n") {
		fmt.Fprintf(buf, "  %s\n", line)
	}
}

var nameDerivedFields = map[string]string{
	"agent":      "name",
	"connector":  "name",
	"dataset":    "name",
	"entrypoint": "name",
	"evaluator":  "name",
	"experiment": "name",
	"linkAgent":  "name",
	"rbacRole":   "name",
	"toolset":    "name",
	"workflow":   "name",
	"model":      "alias",
}

func isNameDerivedField(kind, jsonName string) bool {
	field, ok := nameDerivedFields[kind]
	return ok && field == jsonName
}

func writeExampleField(buf *bytes.Buffer, f codegen.FieldSchema, upperResourceName, kind string) {
	prefix := ""
	suffix := ""
	if !f.Required {
		prefix = "# optional: "
	}
	if f.Secret {
		placeholder := fmt.Sprintf("${%s_%s}", upperResourceName, strings.ToUpper(strings.ReplaceAll(f.JSONName, "-", "_")))
		suffix = "  # secret — provide via env var"
		fmt.Fprintf(buf, "%s%s: %s%s\n", prefix, f.JSONName, placeholder, suffix)
		return
	}

	switch typeKey(f.GoType) {
	case "string":
		fmt.Fprintf(buf, "%s%s: %q\n", prefix, f.JSONName, stringPlaceholder(f, kind))
	case "int":
		v := max(1, f.MinLen)
		fmt.Fprintf(buf, "%s%s: %d\n", prefix, f.JSONName, v)
	case "bool":
		fmt.Fprintf(buf, "%s%s: false\n", prefix, f.JSONName)
	case stringSliceTypeName:
		fmt.Fprintf(buf, "%s%s: []\n", prefix, f.JSONName)
	case "map":
		fmt.Fprintf(buf, "%s%s: {}\n", prefix, f.JSONName)
	case "any":
		fmt.Fprintf(buf, "%s%s: null\n", prefix, f.JSONName)
	case "[]struct":
		fmt.Fprintf(buf, "%s%s: []  # see schema for element shape\n", prefix, f.JSONName)
	default:
		fmt.Fprintf(buf, "%s%s: null  # value of type %s\n", prefix, f.JSONName, f.DisplayType())
	}
}

func typeKey(t string) string {
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, "*")
	switch {
	case t == "string":
		return "string"
	case t == "int" || t == "int64" || t == "int32":
		return "int"
	case t == "bool":
		return "bool"
	case t == stringSliceTypeName:
		return stringSliceTypeName
	case strings.HasPrefix(t, "map["):
		return "map"
	case t == "any" || t == "interface{}":
		return "any"
	case strings.HasPrefix(t, "[]"):
		return "[]struct"
	}
	return ""
}

func stringPlaceholder(f codegen.FieldSchema, kind string) string {
	if len(f.OneOf) > 0 {
		return f.OneOf[0]
	}
	switch f.JSONName {
	case "description":
		return fmt.Sprintf("Example %s description (replace me).", kind)
	case "systemPrompt":
		return strings.Repeat("Example system prompt. Replace with the agent's real instructions. ", 2)
	case "provider":
		return "openai"
	case "modelName":
		return "gpt-4o"
	case "type":
		return "standard"
	case "subtype":
		return "default"
	case "url", "publicUrl":
		return "https://example.com"
	case "entrypoint":
		return "example_agent_link_entrypoint"
	}
	return ""
}

const (
	stringSliceTypeName = "[]string"
)
