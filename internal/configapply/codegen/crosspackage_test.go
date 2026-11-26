// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const apiv2ImportPath = "github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"

func TestParseStructs_CrossPackageTypes(t *testing.T) {
	got, err := ParseStructsWithAlias("testdata/crosspackage/thing.go", "copies", "CreateThingRequest", "ThingResponse")
	if err != nil {
		t.Fatalf("ParseStructsWithAlias: %v", err)
	}
	imports := map[string]string{"apiv2": apiv2ImportPath}
	wantRequest := []Field{
		{GoName: "Name", JSONName: "name", Kind: KindString, GoTypeRaw: "string"},
		{GoName: "Pricing", JSONName: "pricing", OmitEmpty: true, Kind: KindPassthrough, GoTypeQualified: "*apiv2.Pricing", GoTypeRaw: "*apiv2.Pricing", TypeImports: imports, HoldsStructs: true},
		{GoName: "ToolsetConfigs", JSONName: "toolsetConfigs", Kind: KindPassthrough, GoTypeQualified: "[]apiv2.ToolsetConfig", GoTypeRaw: "[]apiv2.ToolsetConfig", TypeImports: imports, HoldsStructs: true},
	}
	if diff := cmp.Diff(wantRequest, got[0].Fields); diff != "" {
		t.Errorf("request fields mismatch (-want +got):\n%s", diff)
	}
	wantDropped := []Field{{GoName: "CreatedTime", JSONName: "createdTime", GoTypeRaw: "*apiv2.Instant"}}
	if diff := cmp.Diff(wantDropped, got[1].Dropped); diff != "" {
		t.Errorf("response dropped mismatch (-want +got):\n%s", diff)
	}
}

func TestGenerate_ImportsPackagesFieldTypesName(t *testing.T) {
	structs, err := ParseStructsWithAlias("testdata/crosspackage/thing.go", "copies", "CreateThingRequest", "ThingResponse")
	if err != nil {
		t.Fatalf("ParseStructsWithAlias: %v", err)
	}
	src, err := Generate("translate", Spec{
		Kind:           "Thing",
		Request:        structs[0],
		Response:       structs[1],
		DTOImportPath:  "example.com/copies",
		DTOImportAlias: "copies",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "out.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("emitted source did not parse: %v\n%s", err, src)
	}
	var got []string
	for _, spec := range f.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		if spec.Name != nil {
			importPath = spec.Name.Name + " " + importPath
		}
		got = append(got, importPath)
	}
	want := []string{"copies example.com/copies", apiv2ImportPath}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("imports mismatch (-want +got):\n%s", diff)
	}
	for _, w := range []string{"var typed *apiv2.Pricing", "var typed []apiv2.ToolsetConfig"} {
		if !strings.Contains(string(src), w) {
			t.Errorf("output missing %q\n%s", w, src)
		}
	}
}

func TestGenerate_FailsOnDroppedFieldsConfigApplyCarries(t *testing.T) {
	spec := Spec{
		Kind: "Thing",
		Request: StructInfo{
			Name:    "CreateThingRequest",
			File:    "copies/thing.go",
			Fields:  []Field{{GoName: "Name", JSONName: "name", Kind: KindString}},
			Dropped: []Field{{GoName: "Window", JSONName: "window", GoTypeRaw: "apiv2.Window"}},
		},
		Response: StructInfo{
			Name:   "ThingResponse",
			File:   "copies/thing.go",
			Fields: []Field{{GoName: "Name", JSONName: "name", Kind: KindString}},
			Dropped: []Field{
				{GoName: "Window", JSONName: "window", GoTypeRaw: "apiv2.Window"},
				{GoName: "CreatedTime", JSONName: "createdTime", GoTypeRaw: "*apiv2.Instant"},
			},
		},
		DTOImportPath:  "example.com/copies",
		DTOImportAlias: "copies",
	}
	_, err := Generate("translate", spec)
	if err == nil {
		t.Fatal("Generate succeeded, want it to fail on the dropped window field")
	}
	msg := err.Error()
	for _, w := range []string{
		`Thing: field CreateThingRequest.Window (json "window", type apiv2.Window) in copies/thing.go`,
		"sam config would never send it",
		`Thing: field ThingResponse.Window (json "window", type apiv2.Window) in copies/thing.go`,
		"sam config would never read it back",
	} {
		if !strings.Contains(msg, w) {
			t.Errorf("error missing %q\ngot: %s", w, msg)
		}
	}
	if strings.Contains(msg, "createdTime") {
		t.Errorf("a response-only field was reported: %s", msg)
	}
}

func TestGenerate_FailsWhenTwoPackagesShareAnImportName(t *testing.T) {
	spec := Spec{
		Kind: "Thing",
		Request: StructInfo{
			Name: "CreateThingRequest",
			File: "copies/thing.go",
			Fields: []Field{{
				GoName: "Items", JSONName: "items", Kind: KindPassthrough,
				GoTypeQualified: "[]copies.Item",
				TypeImports:     map[string]string{"copies": "example.com/other/copies"},
			}},
		},
		Response: StructInfo{
			Name:   "ThingResponse",
			Fields: []Field{{GoName: "Items", JSONName: "items", Kind: KindPassthrough}},
		},
		DTOImportPath:  "example.com/copies",
		DTOImportAlias: "copies",
	}
	_, err := Generate("translate", spec)
	if err == nil || !strings.Contains(err.Error(), "import name copies refers to both example.com/copies and example.com/other/copies") {
		t.Fatalf("Generate error = %v, want the import name clash", err)
	}
}

func TestGenerate_FailsOnEmbeddedRequestField(t *testing.T) {
	file := writeDTOFiles(t, map[string]string{"thing.go": `package copies

type Base struct {
	Name string ` + "`json:\"name\"`" + `
}

type CreateThingRequest struct {
	Base
	Skipped Base ` + "`json:\"-\"`" + `
}

type ThingResponse struct {
	*Base
	Name string ` + "`json:\"name\"`" + `
}
`})
	structs, err := ParseStructsWithAlias(file, "copies", "CreateThingRequest", "ThingResponse")
	if err != nil {
		t.Fatalf("ParseStructsWithAlias: %v", err)
	}
	if diff := cmp.Diff([]Field{{GoName: "Base", JSONName: "Base", GoTypeRaw: "Base"}}, structs[0].Dropped); diff != "" {
		t.Errorf("request dropped mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]Field{{GoName: "Base", JSONName: "Base", GoTypeRaw: "*Base"}}, structs[1].Dropped); diff != "" {
		t.Errorf("response dropped mismatch (-want +got):\n%s", diff)
	}
	_, err = Generate("translate", Spec{Kind: "Thing", Request: structs[0], Response: structs[1], DTOImportPath: "example.com/copies", DTOImportAlias: "copies"})
	if err == nil || !strings.Contains(err.Error(), `field CreateThingRequest.Base (json "Base", type Base)`) {
		t.Fatalf("Generate error = %v, want the embedded Base reported", err)
	}
}

func TestGenerate_FailsOnRequestFieldMissingFromResponse(t *testing.T) {
	spec := Spec{
		Kind: "Thing",
		Request: StructInfo{
			Name: "CreateThingRequest",
			File: "copies/thing.go",
			Fields: []Field{
				{GoName: "Name", JSONName: "name", Kind: KindString},
				{GoName: "DisplayName", JSONName: "displayName", Kind: KindString},
				{GoName: "Credentials", JSONName: "credentials", Kind: KindMapStringAny, Secret: true},
				{GoName: "Deploy", JSONName: "deploy", Kind: KindBool, OmitHash: true},
				{GoName: "ExamplesFile", JSONName: "examples_file", Kind: KindString, Local: true},
			},
		},
		Response: StructInfo{
			Name:   "ThingResponse",
			Fields: []Field{{GoName: "Name", JSONName: "name", Kind: KindString}, {GoName: "Label", JSONName: "label", Kind: KindString}},
		},
		DTOImportPath:  "example.com/copies",
		DTOImportAlias: "copies",
	}
	_, err := Generate("translate", spec)
	if err == nil {
		t.Fatal("Generate succeeded, want it to fail on displayName")
	}
	msg := err.Error()
	if !strings.Contains(msg, `Thing: field CreateThingRequest.DisplayName (json "displayName") in copies/thing.go has no field with that JSON name on ThingResponse`) {
		t.Errorf("error missing displayName\ngot: %s", msg)
	}
	for _, exempt := range []string{"credentials", "deploy", "examples_file"} {
		if strings.Contains(msg, exempt) {
			t.Errorf("exempt field %s was reported: %s", exempt, msg)
		}
	}
}

func TestParseStructs_PointerToStructInSiblingFile(t *testing.T) {
	file := writeDTOFiles(t, map[string]string{
		"thing.go": `package copies

type CreateThingRequest struct {
	Window *Window ` + "`json:\"window\"`" + `
	Other  *Other  ` + "`json:\"other\"`" + `
}
`,
		"window.go": `package copies

type Window struct{ Days int }
`,
		"other_pkg.go": `package other

type Other struct{ Days int }
`,
	})
	structs, err := ParseStructsWithAlias(file, "copies", "CreateThingRequest")
	if err != nil {
		t.Fatalf("ParseStructsWithAlias: %v", err)
	}
	want := []Field{{GoName: "Window", JSONName: "window", Kind: KindPassthrough, GoTypeQualified: "*copies.Window", GoTypeRaw: "*Window", HoldsStructs: true}}
	if diff := cmp.Diff(want, structs[0].Fields); diff != "" {
		t.Errorf("fields mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]Field{{GoName: "Other", JSONName: "other", GoTypeRaw: "*Other"}}, structs[0].Dropped); diff != "" {
		t.Errorf("a struct from another package in the same directory was carried (-want +got):\n%s", diff)
	}
}

func writeDTOFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return filepath.Join(dir, "thing.go")
}
