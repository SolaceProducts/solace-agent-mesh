// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"strings"
	"testing"
)

const prepareSidecar = `
template:
  file: report.html
  output_filename: out.html
  mime_type: text/html
substitutions:
  title:
    description: H1 title
    required: true
data_inputs:
  rows:
    description: data
    artifact: data.json
    mime_type: application/json
    required: true
    schema:
      type: array
`

const prepareBody = "<h1>@@title@@</h1>«artifact_content:data.json»"

func TestPrepareCandidate(t *testing.T) {
	ctx := t.Context()

	t.Run("clean candidate keeps embeds, applies substitutions", func(t *testing.T) {
		store := newFakeStore()
		store.put("data.json", "application/json", []byte(`[1,2,3]`))
		cand, tmpl, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody, map[string]string{"title": "Q3"}, nil, store)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(findings) != 0 {
			t.Fatalf("findings: %+v", findings)
		}
		if tmpl.Meta.OutputFilename != "out.html" {
			t.Errorf("output_filename = %q", tmpl.Meta.OutputFilename)
		}
		if !strings.Contains(cand, "<h1>Q3</h1>") {
			t.Errorf("substitution not applied: %q", cand)
		}
		if !strings.Contains(cand, "«artifact_content:data.json:1»") {
			t.Errorf("embed must survive unresolved, pinned to the validated version: %q", cand)
		}
	})

	t.Run("missing required substitution is a finding", func(t *testing.T) {
		store := newFakeStore()
		store.put("data.json", "application/json", []byte(`[]`))
		_, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody, nil, nil, store)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(findings) == 0 || findings[0].Kind != errKindSubstitution {
			t.Fatalf("findings: %+v", findings)
		}
	})

	t.Run("missing required data input is a finding", func(t *testing.T) {
		store := newFakeStore()
		_, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody, map[string]string{"title": "x"}, nil, store)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(findings) == 0 || findings[0].Kind != errKindNotFound {
			t.Fatalf("findings: %+v", findings)
		}
	})

	t.Run("inlined data input is a finding", func(t *testing.T) {
		store := newFakeStore()
		store.put("data.json", "application/json", []byte(`[1,2,3]`))
		inlinedBody := "<h1>@@title@@</h1><pre>[1,2,3]</pre>"
		_, _, findings, err := PrepareCandidate(ctx, []byte(prepareSidecar), inlinedBody, map[string]string{"title": "Q3"}, nil, store)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(findings) == 0 {
			t.Fatal("want an inlined_data finding")
		}
		found := false
		for _, f := range findings {
			if f.Kind == errKindInlinedData && f.Artifact == "data.json" {
				found = true
			}
		}
		if !found {
			t.Fatalf("want inlined_data finding for data.json, got %+v", findings)
		}
	})

	t.Run("malformed sidecar is an error", func(t *testing.T) {
		_, _, _, err := PrepareCandidate(ctx, []byte("template:\n  mime_type: text/html\n"), prepareBody, nil, nil, newFakeStore())
		if err == nil {
			t.Fatal("want error for missing template.file")
		}
	})

	t.Run("binary mime is rejected", func(t *testing.T) {
		sc := "template:\n  file: x.bin\n  mime_type: application/octet-stream\n"
		_, _, _, err := PrepareCandidate(ctx, []byte(sc), "x", nil, nil, newFakeStore())
		if err == nil {
			t.Fatal("want error for binary mime_type")
		}
	})

	t.Run("nil store is an error", func(t *testing.T) {
		_, _, _, err := PrepareCandidate(ctx, []byte(prepareSidecar), prepareBody, nil, nil, nil)
		if err == nil {
			t.Fatal("want error for nil store")
		}
	})
}

func TestSidecarFilename(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"report.html", "report.template.yaml"},
		{"assets/report.html", "assets/report.template.yaml"},
		{"data.json", "data.template.yaml"},
		{"noext", "noext.template.yaml"},
	} {
		if got := sidecarFilename(tc.in); got != tc.want {
			t.Errorf("sidecarFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
