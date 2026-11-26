// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxtool

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestCreateDocument_RejectsMalformedSpec(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		wantSub string
	}{
		{
			name:    "field belonging to another block type",
			spec:    "- type: heading\n  level: 1\n  runs: [{text: \"Q3 Review\"}]",
			wantSub: `field "runs" is not used by this block type`,
		},
		{
			name:    "misspelled field",
			spec:    "- type: paragraph\n  txt: \"hello\"",
			wantSub: `unknown field "txt"`,
		},
		{
			name:    "heading missing its level",
			spec:    "- type: heading\n  text: \"No level\"",
			wantSub: `missing required field "level"`,
		},
		{
			name:    "heading missing its text",
			spec:    "- type: heading\n  level: 1",
			wantSub: `missing required field "text"`,
		},
		{
			name:    "colour name instead of hex",
			spec:    "- type: paragraph\n  runs: [{text: \"hi\", color: red}]",
			wantSub: "not a colour name",
		},
		{
			name:    "hex colour with an alpha channel",
			spec:    "- type: paragraph\n  runs: [{text: \"hi\", color: C0392BAA}]",
			wantSub: "must be 3 or 6 hex digits",
		},
		{
			name:    "comma-separated toc levels",
			spec:    "- type: table_of_contents\n  toc_levels: \"1,2\"",
			wantSub: "hyphenated range",
		},
		{
			name:    "inverted toc range",
			spec:    "- type: table_of_contents\n  toc_levels: \"3-1\"",
			wantSub: "inverted",
		},
		{
			name:    "capitalized alignment",
			spec:    "- type: paragraph\n  text: hi\n  align: Center",
			wantSub: "",
		},
		{
			name:    "unrecognized alignment",
			spec:    "- type: paragraph\n  text: hi\n  align: centre",
			wantSub: "align \"centre\" is not one of",
		},
		{
			name:    "col_widths with one share per column",
			spec:    "- type: table\n  headers: [\"Principle\", \"Description\"]\n  col_widths: [1, 3]\n  rows:\n    - [\"Event-Driven\", \"Flows through the broker.\"]",
			wantSub: "",
		},
		{
			name:    "col_widths shorter than the column count",
			spec:    "- type: table\n  headers: [\"A\", \"B\", \"C\"]\n  col_widths: [1, 3]\n  rows:\n    - [\"1\", \"2\", \"3\"]",
			wantSub: "col_widths has 2 entries but the table has 3 columns",
		},
		{
			name:    "col_widths longer than the column count",
			spec:    "- type: table\n  headers: [\"A\", \"B\"]\n  col_widths: [1, 2, 3]\n  rows:\n    - [\"1\", \"2\"]",
			wantSub: "col_widths has 3 entries but the table has 2 columns",
		},
		{
			name:    "col_widths matching the header but not the widest row",
			spec:    "- type: table\n  headers: [\"A\", \"B\"]\n  col_widths: [1, 3]\n  rows:\n    - [\"1\", \"2\", \"3\"]",
			wantSub: "col_widths has 2 entries but the table has 3 columns",
		},
		{
			name:    "zero share",
			spec:    "- type: table\n  headers: [\"A\", \"B\"]\n  col_widths: [0, 1]\n  rows:\n    - [\"1\", \"2\"]",
			wantSub: "col_widths[0] is 0; every share must be a positive number",
		},
		{
			name:    "negative share",
			spec:    "- type: table\n  headers: [\"A\", \"B\"]\n  col_widths: [1, -2]\n  rows:\n    - [\"1\", \"2\"]",
			wantSub: "col_widths[1] is -2; every share must be a positive number",
		},
		{
			name:    "unknown block type",
			spec:    "- type: sidebar\n  text: hi",
			wantSub: `unknown type "sidebar"`,
		},
		{
			name:    "col_widths on a block that has no table",
			spec:    "- type: paragraph\n  text: hi\n  col_widths: [1, 3]",
			wantSub: `field "col_widths" is not used by this block type`,
		},
		{
			name:    "block with no type",
			spec:    "- text: hi",
			wantSub: "missing 'type'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := createDocument(context.Background(), CreateDocumentParams{
				Title:   "T",
				Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(tc.spec)},
			}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("createDocument returned a transport error: %v", err)
			}
			if tc.wantSub == "" {
				if res.Status != sdk.StatusSuccess {
					t.Fatalf("status = %q, want success; message: %s", res.Status, res.Message)
				}
				return
			}
			if res.Status != sdk.StatusError {
				t.Fatalf("status = %q, want error; message: %s", res.Status, res.Message)
			}
			if !strings.Contains(res.Message, tc.wantSub) {
				t.Errorf("message %q does not contain %q", res.Message, tc.wantSub)
			}
		})
	}
}

func TestCreateDocument_AcceptsMixedCaseKeysTheDecoderAccepts(t *testing.T) {
	cases := map[string]string{
		"capitalized type key":   "- Type: heading\n  level: 1\n  text: \"Heading text\"",
		"capitalized field key":  "- type: paragraph\n  Text: \"Paragraph text\"",
		"capitalized type value": "- type: Heading\n  level: 1\n  text: \"Heading text\"",
		"capitalized run key":    "- type: paragraph\n  runs: [{Text: \"Run text\", Bold: true}]",
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := createDocument(context.Background(), CreateDocumentParams{
				Title:   "T",
				Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec)},
			}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("createDocument: %v", err)
			}
			if res.Status != sdk.StatusSuccess {
				t.Fatalf("status = %q, want success; message: %s", res.Status, res.Message)
			}
			doc := documentXML(t, res.DataObjects[0].Content)
			if !strings.Contains(doc, "text") && !strings.Contains(doc, "Text") {
				t.Errorf("content missing from document.xml:\n%s", doc)
			}
		})
	}
}

func TestCreateDocument_ValidatesKeysInsideRuns(t *testing.T) {
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title: "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(
			"- type: paragraph\n  runs:\n    - txt: \"Status: DEGRADED\"\n      bold: true")},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error — 'DEGRADED' would otherwise vanish silently; message: %s",
			res.Status, res.Message)
	}
	for _, want := range []string{"run 1", `"txt"`} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q should mention %q", res.Message, want)
		}
	}
}

func TestCreateDocument_ReportsEveryProblemAtOnce(t *testing.T) {
	spec := `
- type: heading
  runs: [{text: "wrong field and no level"}]
- type: paragraph
  txt: "typo"
- type: sidebar
  text: "unknown type"
`
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec)},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error", res.Status)
	}
	for _, want := range []string{"block 1", "block 2", "block 3", `"runs"`, `"txt"`, "sidebar"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message should mention %q so one retry fixes everything:\n%s", want, res.Message)
		}
	}
}

func TestCreateDocument_ReportsValueProblemsAlongsideStructuralOnes(t *testing.T) {
	spec := `
- type: heading
  text: "no level"
- type: paragraph
  runs: [{text: "x", color: red}]
- type: table_of_contents
  toc_levels: "1,2"
- type: paragraph
  text: "x"
  align: Centre
`
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec)},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error", res.Status)
	}
	for _, want := range []string{
		`missing required field "level"`,
		"not a colour name",
		"hyphenated range",
		"is not one of",
	} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message should mention %q — all four problems belong in one report:\n%s", want, res.Message)
		}
	}
}

func TestCreateDocument_ReportsAnOmittedFieldOnce(t *testing.T) {
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte("- type: heading\n  text: \"no level\"")},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if !strings.Contains(res.Message, `missing required field "level"`) {
		t.Errorf("message should report the omission:\n%s", res.Message)
	}
	if strings.Contains(res.Message, "got 0") {
		t.Errorf("message should not also report the zero value as a range violation:\n%s", res.Message)
	}
	if got := strings.Count(res.Message, "block 1 "); got != 1 {
		t.Errorf("expected exactly 1 problem for block 1, got %d:\n%s", got, res.Message)
	}
}

func TestCreateDocument_GroupsProblemsByBlock(t *testing.T) {
	spec := `
- type: paragraph
  txt: "structural problem in block 1"
- type: paragraph
  text: "value problem in block 2"
  align: Centre
`
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec)},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if first, second := strings.Index(res.Message, "block 1 "), strings.Index(res.Message, "block 2 "); first > second {
		t.Errorf("block 1's problem should precede block 2's regardless of which tier found them:\n%s", res.Message)
	}
}

func TestCreateDocument_RejectsTwoSpellingsOfOneField(t *testing.T) {
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte("- type: paragraph\n  text: \"lower\"\n  Text: \"upper\"")},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error — one spelling's content would be dropped; message: %s",
			res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "same field") {
		t.Errorf("message should explain the two keys collide:\n%s", res.Message)
	}
}

func TestCreateDocument_ReportsProblemsInBlockOrder(t *testing.T) {
	var spec strings.Builder
	for range 11 {
		spec.WriteString("- type: paragraph\n  txt: \"x\"\n")
	}
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec.String())},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if got, want := strings.Index(res.Message, "block 2 "), strings.Index(res.Message, "block 10 "); got > want {
		t.Errorf("block 2 should be reported before block 10:\n%s", res.Message)
	}
}

func TestCreateDocument_NormalizesAcceptableColourForms(t *testing.T) {
	for _, spec := range []string{
		"- type: paragraph\n  runs: [{text: hi, color: \"#C0392B\"}]",
		"- type: paragraph\n  runs: [{text: hi, color: c0392b}]",
		"- type: paragraph\n  runs: [{text: hi, color: \"#abc\"}]",
	} {
		res, err := createDocument(context.Background(), CreateDocumentParams{
			Title:   "T",
			Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec)},
		}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("createDocument: %v", err)
		}
		if res.Status != sdk.StatusSuccess {
			t.Errorf("spec %q: status = %q, want success; message: %s", spec, res.Status, res.Message)
			continue
		}
		doc := documentXML(t, res.DataObjects[0].Content)
		if !strings.Contains(doc, `<w:color w:val="C0392B">`) && !strings.Contains(doc, `<w:color w:val="AABBCC">`) {
			t.Errorf("spec %q did not emit an uppercase 6-digit colour:\n%s", spec, doc)
		}
	}
}

func TestCreateDocument_PreservesAspectRatioAsDocumented(t *testing.T) {
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte("- type: image\n  image_ref: \"0\"\n  width_in: 6")},
		Images: []sdk.Artifact{{
			Filename: "shot.png", MIMEType: "image/png", Content: makePNG(t, 1600, 900),
		}},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success; message: %s", res.Status, res.Message)
	}

	doc := documentXML(t, res.DataObjects[0].Content)
	if !strings.Contains(doc, `cx="5486400" cy="3086100"`) {
		t.Errorf("expected a 6in x 3.375in extent (cx=5486400 cy=3086100) as examples.md promises:\n%s", doc)
	}
}

func TestCreateDocument_ClampsOversizedImage(t *testing.T) {
	spec := "- type: image\n  image_ref: \"0\"\n  width_in: 1e300"
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec)},
		Images:  []sdk.Artifact{{Filename: "p.png", MIMEType: "image/png", Content: makeTestPNG(t)}},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusPartial {
		t.Fatalf("status = %q, want partial; message: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "scaled") {
		t.Errorf("message %q should report the scaling", res.Message)
	}
}

func TestCreateDocument_UndecodableImageDegradesVisibly(t *testing.T) {
	spec := "- type: image\n  image_ref: \"0\""
	res, err := createDocument(context.Background(), CreateDocumentParams{
		Title:   "T",
		Content: sdk.Artifact{Filename: "spec.yaml", Content: []byte(spec)},
		Images:  []sdk.Artifact{{Filename: "broken.png", MIMEType: "image/png", Content: []byte("not an image")}},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("createDocument: %v", err)
	}
	if res.Status != sdk.StatusPartial {
		t.Fatalf("status = %q, want partial; message: %s", res.Status, res.Message)
	}
	if !strings.Contains(documentXML(t, res.DataObjects[0].Content), "image unavailable") {
		t.Error("the document should carry a visible placeholder where the image failed")
	}
}
