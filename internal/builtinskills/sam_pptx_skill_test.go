// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builtinskills

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxtool"
	"github.com/SolaceProducts/solace-agent-mesh/tools/go/render_to_images/rendertoimages"
)

func samPptxRaw(t *testing.T) string {
	t.Helper()
	body, err := fs.ReadFile(FS(), "sam-pptx/SKILL.md")
	if err != nil {
		t.Fatalf("read sam-pptx/SKILL.md: %v", err)
	}
	return string(body)
}

func samPptxSkill(t *testing.T) string {
	t.Helper()
	return strings.Join(strings.Fields(samPptxRaw(t)), " ")
}

func samPptxSection(t *testing.T, skill, heading string) string {
	t.Helper()
	_, after, found := strings.Cut(skill, heading)
	if !found {
		t.Fatalf("the skill lost its %q section", heading)
	}
	section, _, _ := strings.Cut(after, " ## ")
	return section
}

type toolSchema struct {
	Tools map[string]struct {
		Parameters struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"parameters"`
		Instructions string `json:"instructions"`
	} `json:"tools"`
}

func schemaOf(t *testing.T, tools []*sdk.ToolDef) toolSchema {
	t.Helper()
	raw, err := sdk.SchemaJSON(tools, nil)
	if err != nil {
		t.Fatalf("SchemaJSON: %v", err)
	}
	var s toolSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return s
}

func jsonFieldNames(t reflect.Type, into map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			into[name] = true
		}
		jsonFieldNames(f.Type, into)
	}
}

var (
	backtickSpan = regexp.MustCompile("`([^`\n]+)`")
	nameLike     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	keyValue     = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*): *(.*)$`)
	codeFence    = regexp.MustCompile("(?s)```([a-z]*)\n(.*?)```")
	yamlEntry    = regexp.MustCompile(`(?m)^\s*(?:- )?([A-Za-z][A-Za-z0-9_-]*):[ \t]*"?([^"\n]*)"?[ \t]*$`)
	retiredName  = regexp.MustCompile(`\bpartial\b`)
)

func valuesOf() map[string][]string {
	return map[string][]string{
		"layout":     pptxtool.LayoutNames(),
		"chart_type": pptxtool.ChartTypeNames(),
	}
}

func TestSamPptx_NamesOnlyWhatTheToolsAccept(t *testing.T) {
	pptx := schemaOf(t, pptxtool.Tools()).Tools["pptx_create_presentation"]
	render := schemaOf(t, rendertoimages.Tools()).Tools["render_document_to_images"]
	if len(pptx.Parameters.Properties) == 0 || len(render.Parameters.Properties) == 0 {
		t.Fatal("the pptx or render tool published no parameters")
	}

	known := map[string]string{}
	add := func(kind string, names ...string) {
		for _, n := range names {
			if _, seen := known[n]; !seen {
				known[n] = kind
			}
		}
	}
	for p := range pptx.Parameters.Properties {
		add("pptx_create_presentation parameter", p)
	}
	for p := range render.Parameters.Properties {
		add("render_document_to_images parameter", p)
	}
	slideFields := map[string]bool{}
	jsonFieldNames(reflect.TypeFor[pptxtool.LayoutSlide](), slideFields)
	for f := range slideFields {
		add("slide spec field", f)
	}
	add("layout", pptxtool.LayoutNames()...)
	add("chart type", pptxtool.ChartTypeNames()...)
	for _, td := range append(pptxtool.Tools(), rendertoimages.Tools()...) {
		add("tool", td.Name())
	}
	for name, tool := range map[string]struct{ name, instructions string }{
		"load_artifact":                     {"render_document_to_images", render.Instructions},
		"artifact_search_and_replace_regex": {"pptx_create_presentation", pptx.Instructions},
	} {
		if !strings.Contains(tool.instructions, name) {
			t.Errorf("%s's instructions no longer name %q; the skill should not either", tool.name, name)
		}
		add("agent tool", name)
	}
	add("tool_config key", "template_profile")
	add("SQL keyword", "UNION", "ALL")

	raw := samPptxRaw(t)
	if m := retiredName.FindString(raw); m != "" {
		t.Errorf("the skill mentions %q, which the pptx tool has never had", m)
	}
	accepted := valuesOf()
	checkValue := func(where, key, value string) {
		if want, ok := accepted[key]; ok && !slices.Contains(want, value) {
			t.Errorf("%s sets %s to %q; the tool accepts only %v (case matters)", where, key, value, want)
		}
	}
	var names []string
	for _, m := range backtickSpan.FindAllStringSubmatch(raw, -1) {
		span := m[1]
		if kv := keyValue.FindStringSubmatch(span); kv != nil {
			names = append(names, kv[1])
			span = strings.Trim(kv[2], `"`)
			if nameLike.MatchString(span) {
				checkValue("`"+m[1]+"`", kv[1], span)
			}
		}
		for _, word := range strings.Fields(span) {
			if nameLike.MatchString(word) {
				names = append(names, word)
			}
		}
	}
	fences := codeFence.FindAllStringSubmatch(raw, -1)
	if len(fences) == 0 {
		t.Fatal("the skill lost its YAML slide example")
	}
	for _, f := range fences {
		if f[1] != "yaml" {
			t.Errorf("the skill has a %q code block; slide examples must be fenced as yaml so this test reads them", f[1])
		}
		for _, e := range yamlEntry.FindAllStringSubmatch(f[2], -1) {
			if !slideFields[e[1]] {
				t.Errorf("the YAML example uses %q, which is not a slide spec field", e[1])
			}
			checkValue("the YAML example", e[1], e[2])
		}
	}
	if len(names) == 0 {
		t.Fatal("found no code names in the skill; the extraction is broken")
	}
	for _, n := range names {
		if _, ok := known[n]; !ok {
			t.Errorf("the skill names `%s`, which is no parameter, slide field, layout, chart type or tool the deck tools know", n)
		}
	}
}

func TestSamPptx_ChartExampleBuildsADeck(t *testing.T) {
	fence := codeFence.FindStringSubmatch(samPptxRaw(t))
	if fence == nil {
		t.Fatal("the skill lost its YAML slide example")
	}
	dir := t.TempDir()
	spec := filepath.Join(dir, "deck-spec.yaml")
	if err := os.WriteFile(spec, []byte(fence[2]), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o700); err != nil {
		t.Fatal(err)
	}
	wire, err := sdk.InvokeInProcess(t.Context(), pptxtool.Tools(), "pptx_create_presentation",
		map[string]any{"title": "Example", "slides": "deck-spec.yaml"}, nil,
		sdk.InProcessBindings{
			OutputDir:     out,
			ArtifactPaths: map[string]string{"slides": spec},
			ArtifactMeta:  map[string]map[string]any{"slides": {"filename": "deck-spec.yaml", "mime_type": "application/yaml"}},
		})
	if err != nil {
		t.Fatalf("pptx_create_presentation: %v", err)
	}
	result, _ := wire.(map[string]any)
	if result["status"] != "success" {
		t.Fatalf("the skill's example did not build: %v", wire)
	}
	decks, _ := filepath.Glob(filepath.Join(out, "*.pptx"))
	if len(decks) != 1 {
		t.Errorf("want one .pptx written, got %v", decks)
	}
}

func TestSamPptx_BrandsDoesNotPromiseALogoOnTheTitleSlide(t *testing.T) {
	brands := samPptxSection(t, samPptxSkill(t), "## Brands")
	if strings.Contains(brands, "logo as an image on the title slide") {
		t.Error("the title layout has no logo slot (bg_image_ref is stretched full-slide under an overlay); only the image_* layouts place an image")
	}
	if strings.Contains(brands, "order of precedence") {
		t.Error("theme does not outrank the template profile for backgrounds, title colour or the logo; the Brands section must say what each controls, not rank them")
	}
	for _, want := range []string{"template profile", "`image_ref`", "image_center"} {
		if !strings.Contains(brands, want) {
			t.Errorf("the Brands section no longer mentions %q", want)
		}
	}
}

func TestSamPptx_CheckStepLoadsThePagesAndCanBeSkipped(t *testing.T) {
	check := samPptxSection(t, samPptxSkill(t), "5. **Check it once.**")
	for _, want := range []string{"`load_artifact`", "Skip"} {
		if !strings.Contains(check, want) {
			t.Errorf("the check step no longer says %q: rendering returns page images that nothing views until they are loaded, and the check is optional", want)
		}
	}
}

func TestSamPptx_SQLAdviceIsPortable(t *testing.T) {
	if strings.Contains(samPptxSkill(t), "grouping sets") {
		t.Error("grouping sets are not supported by MySQL or SQLite")
	}
}

func TestSamPptx_CalloutIconAndBrandAdviceMatchTheRenderer(t *testing.T) {
	skill := samPptxSkill(t)
	for _, stale := range []string{"empty circle", "empty medallion"} {
		if strings.Contains(skill, stale) {
			t.Errorf("the skill says %q, but an item without an icon gets a guessed glyph or a letter and a callout gets none", stale)
		}
	}
	if !strings.Contains(skill, "the lead-in in `title`") {
		t.Error("the callout advice must put the lead-in in `title`: the renderer wraps the title in ==...== itself, so markers written there print literally")
	}
	if brands := samPptxSection(t, skill, "## Brands"); !strings.Contains(brands, "When the user asks for a brand") {
		t.Error("the Brands section must apply a skill's brand only when the user asks for one; the tool says to leave `theme` out otherwise")
	}
}
