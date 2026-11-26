// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package directive

import (
	"strings"
	"testing"
)

func TestParseArtifactSpec(t *testing.T) {
	for _, tc := range []struct {
		spec, wantFile string
		wantVer        *int
	}{
		{"file.json", "file.json", nil},
		{"file.json:3", "file.json", new(3)},
		{"file.json:0", "file.json", new(0)},
		{"file.json:abc", "file.json", nil},
		{"", "", nil},
	} {
		file, ver := ParseArtifactSpec(tc.spec)
		if file != tc.wantFile || !eqVer(ver, tc.wantVer) {
			t.Errorf("ParseArtifactSpec(%q) = (%q, %v), want (%q, %v)", tc.spec, file, fmtVer(ver), tc.wantFile, fmtVer(tc.wantVer))
		}
	}
}

func TestParseInlineEmbed(t *testing.T) {
	typ, value, hasValue, ok := ParseInlineEmbed("«artifact_content:file.csv»")
	if !ok || typ != "artifact_content" || value != "file.csv" || !hasValue {
		t.Errorf("got (%q, %q, %v, %v)", typ, value, hasValue, ok)
	}
	typ, _, hasValue, ok = ParseInlineEmbed("«uuid»")
	if !ok || typ != "uuid" || hasValue {
		t.Errorf("type-only embed: got (%q, hasValue=%v, ok=%v)", typ, hasValue, ok)
	}
	if _, _, _, ok := ParseInlineEmbed("«»"); ok {
		t.Error("blank embed should not parse")
	}
	if _, _, _, ok := ParseInlineEmbed("x"); ok {
		t.Error("too-short raw should not parse")
	}
}

func TestScanArtifactRefs(t *testing.T) {
	text := `prose data.json mention
«artifact_content:data.json»
«artifact_content:report.csv:3 >>> jsonpath:$.x >>> apply_to_template:tmpl.liquid >>> format:json»
«artifact_return:out.html»
«artifact_meta:meta.json:2»
«math:1+1»
«««template_liquid: data="rows.json:5" jsonpath="$.items"
{% for r in rows %}{{ r }}{% endfor %}»»»`

	refs := ScanArtifactRefs(text)
	var specs []string
	for _, r := range refs {
		specs = append(specs, r.Spec)
	}
	want := []string{"data.json", "report.csv:3", "tmpl.liquid", "out.html", "meta.json:2", "rows.json:5"}
	if strings.Join(specs, "|") != strings.Join(want, "|") {
		t.Fatalf("specs = %v, want %v", specs, want)
	}
	if refs[1].Filename != "report.csv" || refs[1].Version == nil || *refs[1].Version != 3 {
		t.Errorf("versioned ref parsed wrong: %+v", refs[1])
	}
	if refs[5].Filename != "rows.json" || refs[5].Version == nil || *refs[5].Version != 5 {
		t.Errorf("block data ref parsed wrong: %+v", refs[5])
	}
}

func TestScanArtifactRefs_ChainSpecMatchesParseModifierChain(t *testing.T) {
	for _, expr := range []string{
		"file.csv",
		" file.csv :2 ",
		"file.csv >>> jsonpath:$.a >>> format:json",
		" >>> file.csv >>> format:json",
	} {
		wantSpec, _, _ := ParseModifierChain(expr)
		refs := ScanArtifactRefs("«artifact_content:" + expr + "»")
		if len(refs) == 0 {
			if wantSpec == "" {
				continue
			}
			t.Errorf("expr %q: scan found nothing, ParseModifierChain found %q", expr, wantSpec)
			continue
		}
		if refs[0].Spec != wantSpec {
			t.Errorf("expr %q: scan spec %q != ParseModifierChain spec %q", expr, refs[0].Spec, wantSpec)
		}
	}
}

func TestScanArtifactRefs_InsideTemplateBlockContent(t *testing.T) {
	text := "«««template_liquid: data=\"rows.json\"\n" +
		"{% for r in rows %}«artifact_content:first.png» «artifact_content:second.png»{% endfor %}\n»»»"
	refs := ScanArtifactRefs(text)
	var specs []string
	for _, r := range refs {
		specs = append(specs, r.Spec)
	}
	want := []string{"rows.json", "first.png", "second.png"}
	if strings.Join(specs, "|") != strings.Join(want, "|") {
		t.Fatalf("specs = %v, want %v", specs, want)
	}

	got := RewriteArtifactRefs(text, func(ref ArtifactRef) (string, bool) {
		if ref.Filename == "first.png" {
			return "bound.png:2", true
		}
		return "", false
	})
	if !strings.Contains(got, "«artifact_content:bound.png:2» «artifact_content:second.png»") {
		t.Errorf("nested directive not rewritten: %q", got)
	}
}

func TestScanArtifactRefs_IgnoresNonArtifactForms(t *testing.T) {
	text := `«status_update:working on data.json»
«uuid»
«datetime:iso»
plain « french quotes » here
«««save_artifact: filename="x.json"
content»»»`
	if refs := ScanArtifactRefs(text); len(refs) != 0 {
		t.Fatalf("expected no refs, got %+v", refs)
	}
}

func TestRewriteArtifactRefs(t *testing.T) {
	pins := map[string]string{
		"a.json": "b.json:1",
		"b.json": "c.json:1",
	}
	replace := func(ref ArtifactRef) (string, bool) {
		spec, ok := pins[ref.Filename]
		return spec, ok
	}

	t.Run("chained rename does not cascade", func(t *testing.T) {
		got := RewriteArtifactRefs("«artifact_content:a.json» «artifact_content:b.json»", replace)
		want := "«artifact_content:b.json:1» «artifact_content:c.json:1»"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("modifiers and prose untouched, existing pin replaced", func(t *testing.T) {
		got := RewriteArtifactRefs("see a.json — «artifact_content:a.json:7 >>> format:datauri»", replace)
		want := "see a.json — «artifact_content:b.json:1 >>> format:datauri»"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("substring filenames are not confused", func(t *testing.T) {
		got := RewriteArtifactRefs("«artifact_content:mya.json»", replace)
		if got != "«artifact_content:mya.json»" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("template block data param rewritten", func(t *testing.T) {
		text := "«««template_liquid: data=\"a.json\" limit=\"5\"\n{{ x }}»»»"
		got := RewriteArtifactRefs(text, replace)
		want := "«««template_liquid: data=\"b.json:1\" limit=\"5\"\n{{ x }}»»»"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("unmatched refs untouched", func(t *testing.T) {
		text := "«artifact_content:other.json»"
		if got := RewriteArtifactRefs(text, replace); got != text {
			t.Errorf("got %q", got)
		}
	})
}

func eqVer(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func fmtVer(v *int) any {
	if v == nil {
		return "nil"
	}
	return *v
}
