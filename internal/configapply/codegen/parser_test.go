// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func writeTempGo(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestParseStructs_AllSupportedKinds(t *testing.T) {
	const src = `package fixture

type Thing struct {
	Name      string         ` + "`json:\"name\"`" + `
	Count     int            ` + "`json:\"count\"`" + `
	BigCount  int64          ` + "`json:\"bigCount\"`" + `
	Enabled   bool           ` + "`json:\"enabled\"`" + `
	NamePtr   *string        ` + "`json:\"namePtr,omitempty\"`" + `
	CountPtr  *int           ` + "`json:\"countPtr,omitempty\"`" + `
	Tags      []string       ` + "`json:\"tags,omitempty\"`" + `
	Config    map[string]any ` + "`json:\"config,omitempty\"`" + `
	IfaceCfg  map[string]interface{} ` + "`json:\"ifaceCfg,omitempty\"`" + `
	Values    any            ` + "`json:\"values\"`" + `
	IfaceVal  interface{}    ` + "`json:\"ifaceVal,omitempty\"`" + `
	Hidden    string         ` + "`json:\"-\"`" + `
	NoTag     string
}`

	path := writeTempGo(t, src)
	got, err := ParseStructs(path, "Thing")
	if err != nil {
		t.Fatalf("ParseStructs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 struct, got %d", len(got))
	}

	want := StructInfo{
		Name: "Thing",
		File: path,
		Fields: []Field{
			{GoName: "Name", JSONName: "name", Kind: KindString, GoTypeRaw: "string"},
			{GoName: "Count", JSONName: "count", Kind: KindInt, GoTypeRaw: "int"},
			{GoName: "BigCount", JSONName: "bigCount", Kind: KindInt64, GoTypeRaw: "int64"},
			{GoName: "Enabled", JSONName: "enabled", Kind: KindBool, GoTypeRaw: "bool"},
			{GoName: "NamePtr", JSONName: "namePtr", OmitEmpty: true, Kind: KindStringPtr, GoTypeRaw: "*string"},
			{GoName: "CountPtr", JSONName: "countPtr", OmitEmpty: true, Kind: KindIntPtr, GoTypeRaw: "*int"},
			{GoName: "Tags", JSONName: "tags", OmitEmpty: true, Kind: KindStringSlice, GoTypeRaw: "[]string"},
			{GoName: "Config", JSONName: "config", OmitEmpty: true, Kind: KindMapStringAny, GoTypeRaw: "map[string]any"},
			{GoName: "IfaceCfg", JSONName: "ifaceCfg", OmitEmpty: true, Kind: KindMapStringAny, GoTypeRaw: "map[string]interface{}"},
			{GoName: "Values", JSONName: "values", Kind: KindAny, GoTypeRaw: "any"},
			{GoName: "IfaceVal", JSONName: "ifaceVal", OmitEmpty: true, Kind: KindAny, GoTypeRaw: "interface{}"},
			{GoName: "NoTag", JSONName: "NoTag", Kind: KindString, GoTypeRaw: "string"},
		},
	}
	if diff := cmp.Diff(want, got[0]); diff != "" {
		t.Fatalf("StructInfo mismatch (-want +got):\n%s", diff)
	}
}

func TestParseStructs_UnsupportedTypeSkipped(t *testing.T) {
	const src = `package fixture

import "time"

type Timestamp int64

type Bad struct {
	Weird   complex64 ` + "`json:\"weird\"`" + `
	When    time.Time ` + "`json:\"when\"`" + `
	Stamp   Timestamp ` + "`json:\"stamp\"`" + `
	OK      string    ` + "`json:\"ok\"`" + `
}`
	path := writeTempGo(t, src)
	got, err := ParseStructs(path, "Bad")
	if err != nil {
		t.Fatalf("ParseStructs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 struct, got %d", len(got))
	}
	want := []Field{
		{GoName: "OK", JSONName: "ok", Kind: KindString, GoTypeRaw: "string"},
	}
	if diff := cmp.Diff(want, got[0].Fields); diff != "" {
		t.Fatalf("Fields mismatch (-want +got):\n%s", diff)
	}
	wantDropped := []Field{
		{GoName: "Weird", JSONName: "weird", GoTypeRaw: "complex64"},
		{GoName: "When", JSONName: "when", GoTypeRaw: "time.Time"},
		{GoName: "Stamp", JSONName: "stamp", GoTypeRaw: "Timestamp"},
	}
	if diff := cmp.Diff(wantDropped, got[0].Dropped); diff != "" {
		t.Fatalf("Dropped mismatch (-want +got):\n%s", diff)
	}
}

func TestParseStructs_PassthroughKinds(t *testing.T) {
	const src = `package fixture

type Skill struct {
	Name string ` + "`json:\"name\"`" + `
}

type Wrapper struct {
	Skills    []Skill            ` + "`json:\"skills\"`" + `
	Excluded  map[string][]string ` + "`json:\"excluded,omitempty\"`" + `
	Anything  any                ` + "`json:\"anything,omitempty\"`" + `
	Names     []string           ` + "`json:\"names\"`" + `
}`
	path := writeTempGo(t, src)
	got, err := ParseStructsWithAlias(path, "platform", "Wrapper")
	if err != nil {
		t.Fatalf("ParseStructsWithAlias: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 struct, got %d", len(got))
	}
	want := []Field{
		{GoName: "Skills", JSONName: "skills", Kind: KindPassthrough, GoTypeQualified: "[]platform.Skill", GoTypeRaw: "[]Skill", HoldsStructs: true},
		{GoName: "Excluded", JSONName: "excluded", OmitEmpty: true, Kind: KindPassthrough, GoTypeQualified: "map[string][]string", GoTypeRaw: "map[string][]string"},
		{GoName: "Anything", JSONName: "anything", OmitEmpty: true, Kind: KindAny, GoTypeRaw: "any"},
		{GoName: "Names", JSONName: "names", Kind: KindStringSlice, GoTypeRaw: "[]string"},
	}
	if diff := cmp.Diff(want, got[0].Fields); diff != "" {
		t.Fatalf("Fields mismatch (-want +got):\n%s", diff)
	}
}

func TestParseStructs_JSONObjectKind(t *testing.T) {
	const src = `package fixture

import "encoding/json"

type Mixed struct {
	Tagged   json.RawMessage ` + "`json:\"tagged,omitempty\" cfgapply:\"json-object\"`" + `
	Untagged json.RawMessage ` + "`json:\"untagged,omitempty\"`" + `
	Wrong    string          ` + "`json:\"wrong\" cfgapply:\"json-object\"`" + `
	OK       string          ` + "`json:\"ok\"`" + `
}`
	path := writeTempGo(t, src)
	got, err := ParseStructs(path, "Mixed")
	if err != nil {
		t.Fatalf("ParseStructs: %v", err)
	}
	want := []Field{
		{
			GoName:    "Tagged",
			JSONName:  "tagged",
			OmitEmpty: true,
			Kind:      KindJSONObject,
			GoTypeRaw: "json.RawMessage",
		},
		{GoName: "Wrong", JSONName: "wrong", Kind: KindString, GoTypeRaw: "string"},
		{GoName: "OK", JSONName: "ok", Kind: KindString, GoTypeRaw: "string"},
	}
	if diff := cmp.Diff(want, got[0].Fields); diff != "" {
		t.Fatalf("Fields mismatch (-want +got):\n%s", diff)
	}
}

func TestParseStructs_DocRequiredTag(t *testing.T) {
	const src = `package fixture

type Thing struct {
	SystemPrompt string ` + "`json:\"systemPrompt\" validate:\"omitempty,min=100\" doc:\"required\"`" + `
	Plain        string ` + "`json:\"plain\" validate:\"omitempty\"`" + `
}`
	path := writeTempGo(t, src)
	got, err := ParseStructs(path, "Thing")
	if err != nil {
		t.Fatalf("ParseStructs: %v", err)
	}
	want := []Field{
		{GoName: "SystemPrompt", JSONName: "systemPrompt", Kind: KindString, GoTypeRaw: "string", Validate: "omitempty,min=100", DocRequired: true},
		{GoName: "Plain", JSONName: "plain", Kind: KindString, GoTypeRaw: "string", Validate: "omitempty"},
	}
	if diff := cmp.Diff(want, got[0].Fields); diff != "" {
		t.Fatalf("Fields mismatch (-want +got):\n%s", diff)
	}
}

func TestParseStructs_LocalField(t *testing.T) {
	const src = `package fixture

type Thing struct {
	Name         string ` + "`json:\"name\"`" + `
	Hidden       string ` + "`json:\"-\"`" + `
	ExamplesFile string ` + "`json:\"-\" cfgapply:\"local=examples_file\"`" + `
}`
	path := writeTempGo(t, src)
	got, err := ParseStructs(path, "Thing")
	if err != nil {
		t.Fatalf("ParseStructs: %v", err)
	}
	want := []Field{
		{GoName: "Name", JSONName: "name", Kind: KindString, GoTypeRaw: "string"},
		{GoName: "ExamplesFile", JSONName: "examples_file", OmitEmpty: true, Kind: KindString, GoTypeRaw: "string", Local: true},
	}
	if diff := cmp.Diff(want, got[0].Fields); diff != "" {
		t.Fatalf("Fields mismatch (-want +got):\n%s", diff)
	}
}

func TestParseStructs_DefaultTag(t *testing.T) {
	const src = `package fixture

type Thing struct {
	ModelType string ` + "`json:\"modelType,omitempty\" cfgapply:\"default=chat\"`" + `
	Plain     string ` + "`json:\"plain\"`" + `
}`
	path := writeTempGo(t, src)
	got, err := ParseStructs(path, "Thing")
	if err != nil {
		t.Fatalf("ParseStructs: %v", err)
	}
	want := []Field{
		{GoName: "ModelType", JSONName: "modelType", OmitEmpty: true, Kind: KindString, GoTypeRaw: "string", Default: "chat"},
		{GoName: "Plain", JSONName: "plain", Kind: KindString, GoTypeRaw: "string"},
	}
	if diff := cmp.Diff(want, got[0].Fields); diff != "" {
		t.Fatalf("Fields mismatch (-want +got):\n%s", diff)
	}
}

func TestParseStructs_BareLocalErrors(t *testing.T) {
	const src = `package fixture

type Thing struct {
	BareLocal string ` + "`json:\"-\" cfgapply:\"local\"`" + `
}`
	path := writeTempGo(t, src)
	_, err := ParseStructs(path, "Thing")
	if err == nil {
		t.Fatal("ParseStructs: expected error for bare cfgapply:\"local\", got nil")
	}
	if !strings.Contains(err.Error(), "requires a key") {
		t.Fatalf("ParseStructs: error %q does not mention the missing key", err)
	}
}

func TestFieldKind_String(t *testing.T) {
	cases := map[FieldKind]string{
		KindString:       "string",
		KindInt:          "int",
		KindInt64:        "int64",
		KindBool:         "bool",
		KindStringPtr:    "*string",
		KindIntPtr:       "*int",
		KindInt64Ptr:     "*int64",
		KindFloat64Ptr:   "*float64",
		KindBoolPtr:      "*bool",
		KindStringSlice:  "[]string",
		KindMapStringAny: "map[string]any",
		KindAny:          "any",
		KindPassthrough:  "passthrough",
		KindJSONObject:   "json-object",
		KindUnsupported:  "unsupported",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("FieldKind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestParseStructs_StructNotFound(t *testing.T) {
	const src = `package fixture

type Other struct{}`
	path := writeTempGo(t, src)
	if _, err := ParseStructs(path, "Missing"); err == nil {
		t.Fatal("expected error when struct is missing")
	}
}
