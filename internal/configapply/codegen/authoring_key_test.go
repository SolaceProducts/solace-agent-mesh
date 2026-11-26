// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"strings"
	"testing"
)

func TestGenerate_UsesTheAuthoringKey(t *testing.T) {
	field := Field{GoName: "ConnectorIDs", JSONName: "connectorIds", Key: "connectors", Kind: KindStringSlice}
	src, err := Generate("translate", Spec{
		Kind:           "Thing",
		Request:        StructInfo{Name: "CreateThingRequest", Fields: []Field{field}},
		Response:       StructInfo{Name: "ThingResponse", Fields: []Field{{GoName: "ConnectorIDs", JSONName: "connectorIds", Kind: KindStringSlice}}},
		DTOImportPath:  "example.com/copies",
		DTOImportAlias: "copies",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(src)
	for _, want := range []string{`in["connectors"]`, `coerceStringSlice("connectors", v)`, `out["connectors"] = in.ConnectorIDs`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, `"connectorIds"`) {
		t.Errorf("output uses the JSON name connectorIds as a clean-spec key, so authors would see it renamed\n%s", out)
	}
}

func TestGenerate_FailsWhenTwoFieldsShareAnAuthoringKey(t *testing.T) {
	fields := []Field{
		{GoName: "Type", JSONName: "type", Kind: KindString},
		{GoName: "ThingType", JSONName: "thingType", Key: "type", Kind: KindString},
	}
	_, err := Generate("translate", Spec{
		Kind:           "Thing",
		Request:        StructInfo{Name: "CreateThingRequest", File: "copies/thing.go", Fields: fields},
		Response:       StructInfo{Name: "ThingResponse", Fields: fields},
		DTOImportPath:  "example.com/copies",
		DTOImportAlias: "copies",
	})
	want := `Thing: fields CreateThingRequest.Type (json "type") and CreateThingRequest.ThingType (json "thingType") in copies/thing.go both have the YAML key "type"`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Generate error = %v, want it to contain %q", err, want)
	}
}

func TestParseStructs_ReadsTheAuthoringKey(t *testing.T) {
	path := writeTempGo(t, `package fixture

type CreateThingRequest struct {
	ConnectorIDs []string `+"`json:\"connectorIds\" cfgapply:\"key=connectors,xref=connector\"`"+`
	Name         string   `+"`json:\"name\"`"+`
}`)
	got, err := ParseStructs(path, "CreateThingRequest")
	if err != nil {
		t.Fatalf("ParseStructs: %v", err)
	}
	if key := got[0].Fields[0].AuthoringKey(); key != "connectors" {
		t.Errorf("ConnectorIDs authoring key = %q, want connectors", key)
	}
	if key := got[0].Fields[1].AuthoringKey(); key != "name" {
		t.Errorf("Name authoring key = %q, want its JSON name", key)
	}
}

func TestGenerate_NullIsAbsent(t *testing.T) {
	request := []Field{
		{GoName: "Toolsets", JSONName: "toolsets", Kind: KindStringSlice},
		{GoName: "Skills", JSONName: "skills", Kind: KindPassthrough, GoTypeQualified: "[]copies.Skill", HoldsStructs: true},
		{GoName: "Excluded", JSONName: "excluded", Kind: KindPassthrough, GoTypeQualified: "map[string][]string"},
	}
	spec := Spec{
		Kind:           "Thing",
		Request:        StructInfo{Name: "CreateThingRequest", Fields: request},
		Response:       StructInfo{Name: "ThingResponse", Fields: request},
		DTOImportPath:  "example.com/copies",
		DTOImportAlias: "copies",
	}
	plain, err := Generate("translate", spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	spec.NullIsAbsent = true
	nullIsAbsent, err := Generate("translate", spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !strings.Contains(string(plain), `out["toolsets"] = in.Toolsets`) || strings.Contains(string(plain), "withoutEmptyMembers") {
		t.Errorf("without NullIsAbsent the output should keep today's reverse\n%s", plain)
	}
	for _, want := range []string{"if len(in.Toolsets) > 0 {", "anySkills = withoutEmptyMembers(anySkills)"} {
		if !strings.Contains(string(nullIsAbsent), want) {
			t.Errorf("NullIsAbsent output missing %q\n%s", want, nullIsAbsent)
		}
	}
	if strings.Contains(string(nullIsAbsent), "anyExcluded = withoutEmptyMembers") {
		t.Errorf("NullIsAbsent output drops empty members of a map field, whose keys are the author's\n%s", nullIsAbsent)
	}
}
