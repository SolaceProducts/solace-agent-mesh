// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseStructs_PointerToNamedStructIsPassthrough(t *testing.T) {
	const src = `package fixture

type Pricing struct {
	Input *string ` + "`json:\"input\"`" + `
}

type Stamp int64

type Wrapper struct {
	Pricing   *Pricing ` + "`json:\"pricing,omitempty\"`" + `
	Nickname  *string  ` + "`json:\"nickname,omitempty\"`" + `
	Limit     *int     ` + "`json:\"limit,omitempty\"`" + `
	Size      *int64   ` + "`json:\"size,omitempty\"`" + `
	Flagged   *bool    ` + "`json:\"flagged,omitempty\"`" + `
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
		{GoName: "Pricing", JSONName: "pricing", OmitEmpty: true, Kind: KindPassthrough, GoTypeQualified: "*platform.Pricing", GoTypeRaw: "*Pricing", HoldsStructs: true},
		{GoName: "Nickname", JSONName: "nickname", OmitEmpty: true, Kind: KindStringPtr, GoTypeRaw: "*string"},
		{GoName: "Limit", JSONName: "limit", OmitEmpty: true, Kind: KindIntPtr, GoTypeRaw: "*int"},
		{GoName: "Size", JSONName: "size", OmitEmpty: true, Kind: KindInt64Ptr, GoTypeRaw: "*int64"},
		{GoName: "Flagged", JSONName: "flagged", OmitEmpty: true, Kind: KindBoolPtr, GoTypeRaw: "*bool"},
	}
	if diff := cmp.Diff(want, got[0].Fields); diff != "" {
		t.Fatalf("Fields mismatch (-want +got):\n%s", diff)
	}
}

func TestParseStructs_PointerToNamedScalarStaysUnsupported(t *testing.T) {
	const src = `package fixture

type Timestamp int64

type Wrapper struct {
	ExpiresAt *Timestamp ` + "`json:\"expiresAt,omitempty\"`" + `
	Name      string     ` + "`json:\"name\"`" + `
}`

	path := writeTempGo(t, src)
	got, err := ParseStructsWithAlias(path, "platform", "Wrapper")
	if err != nil {
		t.Fatalf("ParseStructsWithAlias: %v", err)
	}

	for _, f := range got[0].Fields {
		if f.JSONName == "expiresAt" {
			t.Fatalf("a pointer to a named scalar was generated as %v, want it dropped", f.Kind)
		}
	}
}
