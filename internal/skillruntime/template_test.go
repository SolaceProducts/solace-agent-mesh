// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"strings"
	"testing"
)

func TestParseTemplateSidecar_Valid(t *testing.T) {
	tmpl, err := ParseTemplateSidecar([]byte(`
template:
  file: report.html
  output_filename: quarterly_report.html
  mime_type: text/html
  description: Quarterly report
substitutions:
  report_title:
    description: H1 title
    default: "Quarterly Report"
  prepared_by:
    description: Author
    required: true
data_inputs:
  sales_rows:
    description: Monthly rows
    artifact: sales_data.json
    mime_type: application/json
    required: true
    schema:
      type: array
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tmpl.Meta.File != "report.html" || tmpl.Meta.MIMEType != "text/html" {
		t.Errorf("meta = %+v", tmpl.Meta)
	}
	if tmpl.Substitutions["prepared_by"].Required != true {
		t.Errorf("prepared_by should be required")
	}
	if d := tmpl.Substitutions["report_title"].Default; d == nil || *d != "Quarterly Report" {
		t.Errorf("report_title default = %v", d)
	}
	if di := tmpl.DataInputs["sales_rows"]; di.Artifact != "sales_data.json" || !di.Required {
		t.Errorf("sales_rows = %+v", di)
	}
}

func TestParseTemplateSidecar_MissingFields(t *testing.T) {
	cases := map[string]string{
		"no file":      "template:\n  mime_type: text/html\n",
		"no mime_type": "template:\n  file: report.html\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplateSidecar([]byte(src)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseTemplateSidecar_SchemaAndColumnsMutuallyExclusive(t *testing.T) {
	_, err := ParseTemplateSidecar([]byte(`
template:
  file: r.csv
  mime_type: text/csv
data_inputs:
  rows:
    artifact: rows.csv
    schema: {type: array}
    columns:
      - {name: a, type: string}
`))
	if err == nil || !strings.Contains(err.Error(), "both schema and columns") {
		t.Fatalf("expected schema/columns exclusivity error, got %v", err)
	}
}

func TestSubstitute_HappyLeavesOtherSyntaxAlone(t *testing.T) {
	body := `<h1>@@report_title@@</h1><p>By @@prepared_by@@</p><script>const x=` + "`cell-${i}`" + `;</script>{{ liquid }}`
	def := defaultString("Quarterly Report")
	decl := map[string]Substitution{
		"report_title": {Default: &def},
		"prepared_by":  {Required: true},
	}
	out, errs := substitute(body, decl, map[string]string{"prepared_by": "Ed"})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if !strings.Contains(out, "<h1>Quarterly Report</h1>") || !strings.Contains(out, "By Ed") {
		t.Errorf("substitution wrong: %s", out)
	}
	if !strings.Contains(out, "`cell-${i}`") || !strings.Contains(out, "{{ liquid }}") {
		t.Errorf("non-declared syntax was touched: %s", out)
	}
}

func TestSubstitute_UnknownKeySuggestsClosest(t *testing.T) {
	decl := map[string]Substitution{"prepared_by": {Required: true}}
	_, errs := substitute("@@prepared_by@@", decl, map[string]string{"prepared_by": "x", "preparedby": "y"})
	if len(errs) != 1 || errs[0].Kind != errKindSubstitution {
		t.Fatalf("errs = %+v", errs)
	}
	if !strings.Contains(errs[0].Detail, "did you mean \"prepared_by\"") {
		t.Errorf("missing suggestion: %s", errs[0].Detail)
	}
}

func TestSubstitute_RequiredMissing(t *testing.T) {
	decl := map[string]Substitution{"prepared_by": {Required: true}}
	_, errs := substitute("@@prepared_by@@", decl, nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Detail, "no value and no default") {
		t.Fatalf("errs = %+v", errs)
	}
}

func TestSubstitute_ResidualToken(t *testing.T) {
	decl := map[string]Substitution{"report_title": {Required: true}}
	_, errs := substitute("@@report_title@@ @@reportTitle@@", decl, map[string]string{"report_title": "T"})
	if len(errs) != 1 || !strings.Contains(errs[0].Detail, "@@reportTitle@@") {
		t.Fatalf("errs = %+v", errs)
	}
}

func TestSubstitute_OptionalNoDefaultBecomesEmpty(t *testing.T) {
	decl := map[string]Substitution{"opt": {}}
	out, errs := substitute("[@@opt@@]", decl, nil)
	if len(errs) > 0 {
		t.Fatalf("errs = %+v", errs)
	}
	if out != "[]" {
		t.Errorf("out = %q", out)
	}
}

func TestValidateColumns(t *testing.T) {
	di := DataInput{
		Artifact: "regions.csv",
		Columns: []ColumnSpec{
			{Name: "region", Type: "string", Required: true},
			{Name: "total", Type: "number", Required: true},
		},
	}
	t.Run("ok", func(t *testing.T) {
		if errs := validateColumns("rows", di, []byte("region,total\nEMEA,10.5\nAPAC,3\n")); len(errs) > 0 {
			t.Fatalf("errs = %+v", errs)
		}
	})
	t.Run("missing required column", func(t *testing.T) {
		errs := validateColumns("rows", di, []byte("region\nEMEA\n"))
		if len(errs) != 1 || !strings.Contains(errs[0].Detail, "required column \"total\" missing") {
			t.Fatalf("errs = %+v", errs)
		}
	})
	t.Run("type mismatch", func(t *testing.T) {
		errs := validateColumns("rows", di, []byte("region,total\nEMEA,lots\n"))
		if len(errs) != 1 || !strings.Contains(errs[0].Detail, "not a number") {
			t.Fatalf("errs = %+v", errs)
		}
	})
	t.Run("empty required cell", func(t *testing.T) {
		errs := validateColumns("rows", di, []byte("region,total\n,5\n"))
		if len(errs) != 1 || !strings.Contains(errs[0].Detail, "empty but required") {
			t.Fatalf("errs = %+v", errs)
		}
	})
}

func defaultString(s string) string { return s }
