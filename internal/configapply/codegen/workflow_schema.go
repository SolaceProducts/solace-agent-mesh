// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"sort"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"
)

type NodeTypeSchema struct {
	NodeType    string
	Description string
	Fields      []FieldSchema
}

type WorkflowDefinitionSchema struct {
	Description string
	Fields      []FieldSchema
	NodeTypes   map[string]NodeTypeSchema
}

var nodeTypeTokens = []string{"agent", "switch", "loop", "map", "workflow", "tool", "decision", "timer", "wait"}

type nodeField struct {
	nodeType string
	jsonName string
}

var nodeFieldDisplayTypes = map[nodeField]string{
	{nodeType: "decision", jsonName: "questions"}: "map<string, object>",
}

var nodeFieldsTheParserRefuses = map[nodeField]bool{
	{nodeType: "timer", jsonName: "timeout"}: true,
}

const everyNodeTypeMarker = "every node type"

var nodeAuthoringKeys = map[string]string{
	"Retry":           "retry_strategy",
	"DefaultCase":     "default",
	"BodyNode":        "node",
	"TemplateNode":    "node",
	"ItemsExpression": "items",
	"WaitDuration":    "duration",
}

func ExtractWorkflowSchema(configFile string) (WorkflowDefinitionSchema, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, configFile, nil, parser.ParseComments)
	if err != nil {
		return WorkflowDefinitionSchema{}, fmt.Errorf("parse %s: %w", configFile, err)
	}

	wfStruct := findStruct(f, "WorkflowConfig")
	if wfStruct == nil {
		return WorkflowDefinitionSchema{}, fmt.Errorf("%s: WorkflowConfig struct not found", configFile)
	}
	nodeStruct := findStruct(f, "NodeConfig")
	if nodeStruct == nil {
		return WorkflowDefinitionSchema{}, fmt.Errorf("%s: NodeConfig struct not found", configFile)
	}

	docs, err := apidoc.ReadSibling(configFile)
	if err != nil {
		return WorkflowDefinitionSchema{}, fmt.Errorf("%s: %w", configFile, err)
	}
	out := WorkflowDefinitionSchema{
		Fields:    extractWorkflowFields(wfStruct, docs["WorkflowConfig"]),
		NodeTypes: extractNodeTypeFields(nodeStruct, docs["NodeConfig"]),
	}
	desc := sanitizeSchemaDoc(joinDocLines(docs["WorkflowConfig"].Type))
	if desc == "" || strings.Contains(desc, "WorkflowConfig") {
		desc = "Top-level workflow definition authored under a workflow resource's spec.appConfig."
	}
	out.Description = desc
	return out, nil
}

func extractWorkflowFields(st *ast.StructType, doc apidoc.Doc) []FieldSchema {
	skip := map[string]bool{"Name": true, "Namespace": true, "HILDatabaseURL": true}
	out := []FieldSchema{}
	for _, f := range st.Fields.List {
		for _, ident := range f.Names {
			if !ident.IsExported() || skip[ident.Name] {
				continue
			}
			out = append(out, fieldFromASTField(ident.Name, f, doc.Field(ident.Name)))
		}
	}
	return out
}

var deployOnlyNodeFields = map[string]bool{"AgentInstanceName": true}

func isDeployOnlyNodeField(f *ast.Field) bool {
	for _, ident := range f.Names {
		if deployOnlyNodeFields[ident.Name] {
			return true
		}
	}
	return false
}

func extractNodeTypeFields(st *ast.StructType, doc apidoc.Doc) map[string]NodeTypeSchema {
	byType := make(map[string]NodeTypeSchema, len(nodeTypeTokens))
	for _, t := range nodeTypeTokens {
		byType[t] = NodeTypeSchema{NodeType: t}
	}

	const commonKey = "_common_"
	common := []FieldSchema{}

	currentSection := []string{commonKey}
	currentDesc := ""

	for _, f := range st.Fields.List {
		if isDeployOnlyNodeField(f) {
			continue
		}
		field := firstFieldDoc(doc, f)
		if field.Doc != "" {
			text := joinDocLines(field.Doc)
			if strings.Contains(strings.ToLower(text), everyNodeTypeMarker) {
				currentSection = []string{commonKey}
				currentDesc = text
			} else if targets := extractSectionTargets(text); len(targets) > 0 {
				currentSection = targets
				currentDesc = text
			}
		}

		for _, ident := range f.Names {
			if !ident.IsExported() {
				continue
			}
			schema := fieldFromASTField(ident.Name, f, field)
			if key, ok := nodeAuthoringKeys[ident.Name]; ok {
				schema.JSONName = key
			}
			if schema.Description == currentDesc {
				schema.Description = joinDocLines(field.Comment)
			}
			for _, t := range currentSection {
				if t == commonKey {
					common = append(common, schema)
					continue
				}
				cur := byType[t]
				cur.Fields = append(cur.Fields, schema)
				if currentDesc != "" && len(currentSection) == 1 {
					cur.Description = currentDesc
				} else if cur.Description == "" && currentDesc != "" {
					cur.Description = currentDesc
				}
				byType[t] = cur
			}
		}
	}

	for t, s := range byType {
		merged := make([]FieldSchema, 0, len(common)+len(s.Fields))
		merged = append(merged, common...)
		merged = append(merged, s.Fields...)
		merged = slices.DeleteFunc(merged, func(f FieldSchema) bool {
			return nodeFieldsTheParserRefuses[nodeField{nodeType: t, jsonName: f.JSONName}]
		})
		for i := range merged {
			merged[i].DisplayTypeOverride = nodeFieldDisplayTypes[nodeField{nodeType: t, jsonName: merged[i].JSONName}]
		}
		s.Fields = merged
		byType[t] = s
	}
	return byType
}

func extractSectionTargets(text string) []string {
	low := strings.ToLower(text)
	hits := map[string]bool{}
	for _, t := range nodeTypeTokens {
		if containsWord(low, t) {
			hits[t] = true
		}
	}
	if len(hits) == 0 {
		return nil
	}
	out := make([]string, 0, len(hits))
	for k := range hits {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func containsWord(s, word string) bool {
	idx := 0
	for {
		i := strings.Index(s[idx:], word)
		if i < 0 {
			return false
		}
		start := idx + i
		end := start + len(word)
		prevOK := start == 0 || !isLetter(s[start-1])
		nextOK := end == len(s) || !isLetter(s[end])
		if prevOK && nextOK {
			return true
		}
		idx = end
	}
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func findStruct(f *ast.File, name string) *ast.StructType {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if ok {
				return st
			}
		}
	}
	return nil
}

func fieldFromASTField(goName string, f *ast.Field, doc apidoc.Field) FieldSchema {
	desc := joinDocLines(doc.Text())
	goType := exprString(f.Type)
	return FieldSchema{
		JSONName:    goNameToYAML(goName),
		GoType:      humanType(goType),
		Description: desc,
		MinLen:      -1,
		MaxLen:      -1,
		Pointer:     strings.HasPrefix(strings.TrimSpace(goType), "*"),
	}
}

func goNameToYAML(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range name {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper && i > 0 {
			prev := rune(name[i-1])
			prevLower := prev >= 'a' && prev <= 'z'
			nextLower := false
			if i+1 < len(name) {
				next := rune(name[i+1])
				nextLower = next >= 'a' && next <= 'z'
			}
			if prevLower || (i > 1 && nextLower) {
				b.WriteByte('_')
			}
		}
		if isUpper {
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstFieldDoc(doc apidoc.Doc, f *ast.Field) apidoc.Field {
	if len(f.Names) == 0 {
		return apidoc.Field{}
	}
	return doc.Field(f.Names[0].Name)
}
