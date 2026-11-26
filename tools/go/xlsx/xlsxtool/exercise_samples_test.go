// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestExerciseSamples(t *testing.T) {
	dir := os.Getenv("XLSX_DIR")
	if dir == "" {
		t.Skip("set XLSX_DIR=<dir with generated samples> to exercise read/modify")
	}
	ctx := context.Background()
	tc := &sdk.ToolContext{}

	load := func(name string) sdk.Artifact {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return sdk.Artifact{Filename: name, Content: b}
	}
	read := func(label string, p ReadParams) {
		res, err := readWorkbook(ctx, p, tc)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		t.Logf("\n========== READ: %s ==========\n%s", label, res.Message)
	}
	modify := func(name string, p WriteParams) sdk.Artifact {
		res, err := writeWorkbook(ctx, p, tc)
		if err != nil {
			t.Fatalf("modify %s: %v", name, err)
		}
		if res.Status != sdk.StatusSuccess {
			t.Fatalf("modify %s: %s", name, res.Message)
		}
		out := *p.OutputName + ".xlsx"
		if err := os.WriteFile(filepath.Join(dir, out), res.DataObjects[0].Content, 0o644); err != nil {
			t.Fatalf("write %s: %v", out, err)
		}
		t.Logf("\n########## MODIFIED → %s ##########\n%s", out, res.Message)
		return sdk.Artifact{Filename: out, Content: res.DataObjects[0].Content}
	}

	sales := load("02_data_fill.xlsx")
	read("02 describe", ReadParams{Source: sales})
	read("02 search 'West'", ReadParams{Source: sales, Mode: new("search"), Query: new("West")})
	read("02 Sales A1:E5", ReadParams{Source: sales, Mode: new("values"), Sheet: new("Sales"), Range: new("A1:E5")})
	sales2 := modify("02_data_fill", WriteParams{
		Source:     &sales,
		OutputName: new("02_data_fill_modified"),
		Operations: &[]Operation{
			{Op: "set_cell", Sheet: new("Sales"), Cell: new("F1"), Value: "Bonus (5%)"},
			{Op: "set_formula_range", Sheet: new("Sales"), Range: new("F2:F13"), Formula: new("=D2*0.05")},
			{Op: "set_style", Sheet: new("Sales"), Range: new("F1"), Style: &CellStyle{Bold: new(true), FontColor: new("FFFFFF"), Fill: new("4472C4")}},
			{Op: "set_style", Sheet: new("Sales"), Range: new("F2:F13"), Style: &CellStyle{NumberFormat: new("$#,##0.00")}},
			{Op: "auto_filter", Sheet: new("Sales"), Range: new("A1:F13")},
		},
	})
	read("02 modified formulas F2:F4", ReadParams{Source: sales2, Mode: new("formulas"), Sheet: new("Sales"), Range: new("F2:F4")})

	multi := load("04_multisheet.xlsx")
	read("04 describe", ReadParams{Source: multi})
	read("04 search 'Gizmo' (all sheets)", ReadParams{Source: multi, Mode: new("search"), Query: new("Gizmo")})
	multi2 := modify("04_multisheet", WriteParams{
		Source:     &multi,
		OutputName: new("04_multisheet_modified"),
		Operations: &[]Operation{
			{Op: "set_cell", Sheet: new("Q1"), Cell: new("B2"), Value: float64(500)},
			{Op: "create_sheet", Name: new("Notes")},
			{Op: "set_cell", Sheet: new("Notes"), Cell: new("A1"), Value: "Q1 Widget volume revised up to 500 units"},
		},
	})
	read("04 modified describe (note new Notes sheet)", ReadParams{Source: multi2})
	read("04 modified Q1 B2", ReadParams{Source: multi2, Mode: new("values"), Sheet: new("Q1"), Range: new("B2:B2")})

	ks := load("07_kitchen_sink.xlsx")
	read("07 search 'Ana'", ReadParams{Source: ks, Mode: new("search"), Query: new("Ana")})
	read("07 Deals A1:E4", ReadParams{Source: ks, Mode: new("values"), Sheet: new("Deals"), Range: new("A1:E4")})
	ks2 := modify("07_kitchen_sink", WriteParams{
		Source:     &ks,
		OutputName: new("07_kitchen_sink_modified"),
		Operations: &[]Operation{
			{Op: "set_cell", Sheet: new("Deals"), Cell: new("D2"), Value: float64(200000)},
		},
	})
	read("07 modified Deals A1:E4", ReadParams{Source: ks2, Mode: new("values"), Sheet: new("Deals"), Range: new("A1:E4")})

	entries, _ := filepath.Glob(filepath.Join(dir, "*_modified.xlsx"))
	t.Logf("\nmodified files: %s", strings.Join(entries, "\n  "))
}
