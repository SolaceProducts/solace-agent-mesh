// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const seededArtifact = "seed.txt"

func seedTestArtifactMetadata(t *testing.T, svc *mockArtifactService, toolCtx *tool.Context, filename string) {
	t.Helper()
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", filename)
	meta := &artifact.VersionMetadata{
		MIMEType:    "text/plain",
		Description: "conformance fixture",
		SizeBytes:   int64(len("seed content\nsecond line\n")),
		Schema:      map[string]any{"type": "string"},
		Custom:      map[string]any{"origin": "conformance-fixture"},
	}
	if err := svc.SaveMetadata(t.Context(), loc, 0, meta); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}
}

func searchReplaceArtifactArg() *tool.Artifact {
	return &tool.Artifact{
		Content:  []byte("seed content\nsecond line\n"),
		Filename: seededArtifact,
		Version:  0,
		MIMEType: "text/plain",
	}
}

func declaredResultSchemas(t *testing.T) map[string]*tool.JSONSchema {
	t.Helper()
	set := tool.NewSet()
	RegisterAll(set)

	out := map[string]*tool.JSONSchema{}
	for _, registered := range set.ListTools() {
		if schema := tool.ResultSchemaOf(registered); schema != nil {
			out[registered.Name()] = schema
		}
	}
	return out
}

func TestResultSchemas_AreWellFormed(t *testing.T) {
	schemas := declaredResultSchemas(t)
	if len(schemas) == 0 {
		t.Fatal("no registered built-in declares a result schema; expected the annotated set")
	}

	for name, schema := range schemas {
		t.Run(name, func(t *testing.T) {
			if schema.Type != "object" {
				t.Errorf("Type = %q, want \"object\" (Result.Data is always a map)", schema.Type)
			}
			if schema.Properties == nil {
				t.Fatal("Properties = nil; declare emptyResultSchema() for a tool that returns no Data")
			}
			for _, req := range schema.Required {
				if _, ok := schema.Properties[req]; !ok {
					t.Errorf("required field %q is not present in Properties", req)
				}
			}
			for field, raw := range schema.Properties {
				prop, ok := raw.(map[string]any)
				if !ok {
					t.Errorf("property %q = %T, want map[string]any", field, raw)
					continue
				}
				if raw, present := prop["type"]; present {
					if typ, _ := raw.(string); typ == "" {
						t.Errorf("property %q declares an empty type; omit it entirely to mean \"any\"", field)
					}
				}
				if desc, _ := prop["description"].(string); desc == "" {
					t.Errorf("property %q declares no description", field)
				}
			}
		})
	}
}

func completesNode(status string) bool {
	return status == tool.StatusSuccess || status == tool.StatusPartial
}

type conformanceCase struct {
	name string
	tl   tool.BuiltinTool
	args map[string]any

	seed bool

	wantKeys []string

	seedMetadata bool
}

func executeConformanceCases() []conformanceCase {
	return append(additionalConformanceCases(), []conformanceCase{
		{
			name: "get_current_time",
			tl:   &GetCurrentDatetimeTool{},
			args: nil,
		},
		{
			name: "select_voice",
			tl:   &SelectVoiceTool{},
			args: map[string]any{"gender": "female", "tone": "neutral"},
		},
		{
			name: "list_artifacts",
			tl:   &ListArtifactsTool{},
			args: nil,
		},
		{
			name: "append_to_artifact",
			tl:   &AppendToArtifactTool{},
			args: map[string]any{
				"filename":      seededArtifact,
				"content_chunk": "more text",
				"mime_type":     "text/plain",
			},
			seed: true,
		},
		{
			name:     "artifact_grep",
			tl:       &GrepTool{},
			args:     map[string]any{"pattern": "seed", "filename": seededArtifact},
			seed:     true,
			wantKeys: []string{"matches", "count"},
		},
		{
			name:     "artifact_grep/file_path alias",
			tl:       &GrepTool{},
			args:     map[string]any{"pattern": "seed", "file_path": seededArtifact},
			seed:     true,
			wantKeys: []string{"matches", "count"},
		},
		{
			name: "delete_artifact/confirmed",
			tl:   &DeleteArtifactTool{},
			args: map[string]any{"filename": seededArtifact, "confirm_delete": true},
			seed: true,
		},
		{
			name: "delete_artifact/confirmation_prompt",
			tl:   &DeleteArtifactTool{},
			args: map[string]any{"filename": seededArtifact},
			seed: true,
		},
		{
			name: "load_artifact/text",
			tl:   &LoadArtifactTool{},
			args: map[string]any{"filename": seededArtifact},
			seed: true,
		},
		{
			name:         "load_artifact/metadata_only",
			tl:           &LoadArtifactTool{},
			args:         map[string]any{"filename": seededArtifact, "load_metadata_only": true},
			seed:         true,
			seedMetadata: true,
		},
		{
			name: "load_artifact/line_range",
			tl:   &LoadArtifactTool{},
			args: map[string]any{"filename": seededArtifact, "limit": float64(1)},
			seed: true,
		},
		{
			name: "artifact_search_and_replace_regex/single",
			tl:   &SearchAndReplaceInArtifactTool{},
			args: map[string]any{
				"filename":           searchReplaceArtifactArg(),
				"search_expression":  "seed",
				"replace_expression": "grown",
			},
		},
		{
			name: "artifact_search_and_replace_regex/batch",
			tl:   &SearchAndReplaceInArtifactTool{},
			args: map[string]any{
				"filename": searchReplaceArtifactArg(),
				"replacements": []any{
					map[string]any{"search": "seed", "replace": "grown"},
					map[string]any{"search": "second", "replace": "third"},
				},
			},
		},
		{
			name: "artifact_search_and_replace_regex/no_matches",
			tl:   &SearchAndReplaceInArtifactTool{},
			args: map[string]any{
				"filename":           searchReplaceArtifactArg(),
				"search_expression":  "nothing-here-matches",
				"replace_expression": "x",
			},
		},
	}...)
}

func TestResultSchemas_MatchExecuteOutput(t *testing.T) {
	produced := map[string]map[string]bool{}
	exercised := map[string]tool.BuiltinTool{}

	for _, tc := range executeConformanceCases() {
		t.Run(tc.name, func(t *testing.T) {
			schema := tool.ResultSchemaOf(tc.tl)
			if schema == nil {
				t.Fatalf("%s declares no result schema", tc.name)
			}

			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			if tc.seed {
				saveTestArtifact(t.Context(), svc, toolCtx, seededArtifact, "seed content\nsecond line\n", "text/plain")
			}
			if tc.seedMetadata {
				seedTestArtifactMetadata(t, svc, toolCtx, seededArtifact)
			}

			res, err := tc.tl.Execute(t.Context(), tc.args, toolCtx)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !completesNode(res.Status) {
				t.Fatalf("Status = %q (%s), want a status a workflow node completes on",
					res.Status, res.Message)
			}
			for _, key := range tc.wantKeys {
				if _, ok := res.Data[key]; !ok {
					t.Errorf("Data has no %q — this case no longer reaches the branch it was "+
						"written for (got keys %v, message %q)",
						key, slices.Sorted(maps.Keys(res.Data)), res.Message)
				}
			}

			declared := make([]string, 0, len(schema.Properties))
			for field := range schema.Properties {
				declared = append(declared, field)
			}
			slices.Sort(declared)

			name := tc.tl.Name()
			exercised[name] = tc.tl
			if produced[name] == nil {
				produced[name] = map[string]bool{}
			}
			for actual := range res.Data {
				produced[name][actual] = true
			}

			for actual := range res.Data {
				if !slices.Contains(declared, actual) {
					t.Errorf("Execute wrote key %q that the result schema does not declare (declared: %v)",
						actual, declared)
				}
			}

			for _, req := range schema.Required {
				if _, ok := res.Data[req]; !ok {
					t.Errorf("result schema marks %q required but Execute did not write it (wrote: %v)",
						req, mapKeys(res.Data))
				}
			}
		})
	}

	t.Run("every declared key is produced by some case", func(t *testing.T) {
		for name, seen := range produced {
			schema := tool.ResultSchemaOf(exercised[name])
			if schema == nil {
				t.Errorf("%s declares no result schema", name)
				continue
			}
			for field := range schema.Properties {
				if seen[field] {
					continue
				}
				if unexercisedDeclarations[name][field] {
					continue
				}
				t.Errorf("%s declares %q but no conformance case produces it — add a case that "+
					"reaches it, or record it in unexercisedDeclarations with the reason",
					name, field)
			}
		}
	})
}

var unexercisedDeclarations = map[string]map[string]bool{
	"artifact_grep": {
		"skipped_files":  true,
		"binary_files":   true,
		"message_to_llm": true,
	},
}

var requiresExternalService = map[string]string{
	"web_request":                   "issues an outbound HTTP request",
	"web_search_google":             "calls the Google Custom Search API",
	"web_search":                    "calls the configured web search provider's API",
	"deep_research":                 "drives an LLM research loop",
	"ask_user_question":             "blocks on a human answer through the gateway",
	"describe_image":                "calls a vision model",
	"describe_audio":                "calls an audio model",
	"transcribe_audio":              "calls a speech-to-text model",
	"create_image_from_description": "calls an image model",
	"edit_image_with_gemini":        "calls the Gemini image API",
	"generate_image_with_gemini":    "calls the Gemini image API",
	"query_data_with_sql":           "not yet covered — runs in-process, a case should be added",
	"create_sqlite_db":              "not yet covered — runs in-process, a case should be added",
	"transform_data_with_jmespath":  "not yet covered — runs in-process, a case should be added",
	"merge_structured_data":         "not yet covered — runs in-process, a case should be added",
	"reinterpret_artifact":          "not yet covered — runs in-process, a case should be added",
}

func TestResultSchemas_EveryDeclarationIsExercisedOrWaived(t *testing.T) {
	exercised := map[string]bool{}
	for _, tc := range executeConformanceCases() {
		exercised[tc.tl.Name()] = true
	}

	for name := range declaredResultSchemas(t) {
		if exercised[name] {
			continue
		}
		if reason := requiresExternalService[name]; reason != "" {
			continue
		}
		t.Errorf("%s declares a result schema that no conformance case exercises — add a case to "+
			"executeConformanceCases, or record it in requiresExternalService with the reason", name)
	}

	declared := declaredResultSchemas(t)
	for name := range requiresExternalService {
		if _, ok := declared[name]; !ok {
			t.Errorf("requiresExternalService lists %q, which declares no result schema — remove it", name)
		}
		if exercised[name] {
			t.Errorf("requiresExternalService lists %q, but a conformance case exercises it — remove the waiver", name)
		}
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestBuiltinTools_AllDeclareAuthoredSchemas(t *testing.T) {
	builtins := BuiltinTools()
	if len(builtins) == 0 {
		t.Fatal("no built-ins registered")
	}
	for _, bt := range builtins {
		if !tool.HasAuthoredSchemas(bt) {
			t.Errorf("%s: missing the authoredSchemas marker — embed it so the workflow "+
				"validators reject an undeclared name instead of warning", bt.Name())
		}
		if tool.ResultSchemaOf(bt) == nil {
			t.Errorf("%s: carries the authored marker but declares no ResultSchema — "+
				"declare one, or emptyResultSchema() if the tool returns no Result.Data", bt.Name())
		}
	}
}

func TestAuthoredParametersCoverEveryHonouredArg(t *testing.T) {
	readsBySourceType := argsReadByExecute(t)

	if len(readsBySourceType) < 10 {
		t.Fatalf("parsed argument reads for only %d types — the source walk has stopped "+
			"matching, so this test is passing vacuously", len(readsBySourceType))
	}
	if !slices.Contains(readsBySourceType["GrepTool"], "file_path") {
		t.Error("the walk no longer sees artifact_grep's file_path alias, the read this test was written for")
	}

	for _, bt := range BuiltinTools() {
		if !tool.HasAuthoredSchemas(bt) {
			continue
		}
		schema := bt.Parameters()
		if schema == nil || schema.Raw != nil {
			continue
		}
		goType := reflect.TypeOf(bt)
		for goType.Kind() == reflect.Pointer {
			goType = goType.Elem()
		}
		for _, arg := range readsBySourceType[goType.Name()] {
			if _, declared := schema.Properties[arg]; !declared {
				t.Errorf("%s (%s) reads argument %q in Execute but does not declare it; a workflow "+
					"node passing it would be failed at dispatch by undeclaredToolArgs",
					bt.Name(), goType.Name(), arg)
			}
		}
	}
}

func argsReadByExecute(t *testing.T) map[string][]string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	reads := map[string][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		{
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "Execute" || fn.Recv == nil || fn.Body == nil {
					return true
				}
				recv := receiverTypeName(fn.Recv)
				params := executeParamsIdent(fn)
				if recv == "" || params == "" {
					return true
				}
				seen := map[string]bool{}
				ast.Inspect(fn.Body, func(inner ast.Node) bool {
					for _, key := range argKeyRead(inner, params) {
						if !seen[key] {
							seen[key] = true
							reads[recv] = append(reads[recv], key)
						}
					}
					return true
				})
				return true
			})
		}
	}
	return reads
}

func receiverTypeName(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func executeParamsIdent(fn *ast.FuncDecl) string {
	if fn.Type.Params == nil || len(fn.Type.Params.List) < 2 {
		return ""
	}
	names := fn.Type.Params.List[1].Names
	if len(names) == 0 || names[0].Name == "_" {
		return ""
	}
	return names[0].Name
}

func argKeyRead(n ast.Node, params string) []string {
	switch v := n.(type) {
	case *ast.IndexExpr:
		if ident, ok := v.X.(*ast.Ident); ok && ident.Name == params {
			if lit := stringLit(v.Index); lit != "" {
				return []string{lit}
			}
		}
	case *ast.CallExpr:
		if len(v.Args) < 2 {
			return nil
		}
		if ident, ok := v.Args[0].(*ast.Ident); ok && ident.Name == params {
			if lit := stringLit(v.Args[1]); lit != "" {
				return []string{lit}
			}
		}
	}
	return nil
}

func stringLit(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}
