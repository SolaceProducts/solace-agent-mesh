// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"

	bedrockkb "github.com/SolaceProducts/solace-agent-mesh/tools/go/bedrock-kb/bedrockkb"
	buildapply "github.com/SolaceProducts/solace-agent-mesh/tools/go/build_apply/buildapply"
	concataudio "github.com/SolaceProducts/solace-agent-mesh/tools/go/concatenate_audio/concataudio"
	converttomarkdown "github.com/SolaceProducts/solace-agent-mesh/tools/go/convert_to_markdown/converttomarkdown"
	documentdb "github.com/SolaceProducts/solace-agent-mesh/tools/go/document-db/documentdb"
	docxtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/docx/docxtool"
	eventmesh "github.com/SolaceProducts/solace-agent-mesh/tools/go/event-mesh/eventmesh"
	eventmeshprofile "github.com/SolaceProducts/solace-agent-mesh/tools/go/event_mesh_profile_provider/eventmeshprofile"
	ffmpegtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/ffmpeg/ffmpegtool"
	graphdb "github.com/SolaceProducts/solace-agent-mesh/tools/go/graph-db/graphdb"
	htmltopdf "github.com/SolaceProducts/solace-agent-mesh/tools/go/html_to_pdf/htmltopdf"
	imagemagicktool "github.com/SolaceProducts/solace-agent-mesh/tools/go/imagemagick/imagemagicktool"
	libreofficetool "github.com/SolaceProducts/solace-agent-mesh/tools/go/libreoffice/libreofficetool"
	mermaidtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/mermaid/mermaidtool"
	multispeakertts "github.com/SolaceProducts/solace-agent-mesh/tools/go/multi_speaker_text_to_speech/multispeakertts"
	pdfextracttext "github.com/SolaceProducts/solace-agent-mesh/tools/go/pdf_extract_text/pdfextracttext"
	pdfops "github.com/SolaceProducts/solace-agent-mesh/tools/go/pdf_ops/pdfops"
	plotlytool "github.com/SolaceProducts/solace-agent-mesh/tools/go/plotly/plotlytool"
	pptxtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/pptx/pptxtool"
	rendertoimages "github.com/SolaceProducts/solace-agent-mesh/tools/go/render_to_images/rendertoimages"
	samcoder "github.com/SolaceProducts/solace-agent-mesh/tools/go/sam_coder/coder"
	searchtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/search/searchtool"
	sendemail "github.com/SolaceProducts/solace-agent-mesh/tools/go/send_email/sendemail"
	slacktool "github.com/SolaceProducts/solace-agent-mesh/tools/go/slack/slacktool"
	sqltool "github.com/SolaceProducts/solace-agent-mesh/tools/go/sql/sqltool"
	testharness "github.com/SolaceProducts/solace-agent-mesh/tools/go/test_harness/testharness"
	texttospeech "github.com/SolaceProducts/solace-agent-mesh/tools/go/text_to_speech/texttospeech"
	websearchtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/web_search/websearchtool"
	xlsxtool "github.com/SolaceProducts/solace-agent-mesh/tools/go/xlsx/xlsxtool"
)

func TestToolResolutionRejectsNonTools(t *testing.T) {
	resolvable := builtinToolNames(t)
	for name := range strToolNames(t) {
		resolvable[name] = true
	}

	for _, tc := range []struct {
		name string
		want bool
		why  string
	}{
		{"totally_fake_tool", false, "fabricated name"},
		{"volume_params", false, "nested key in tools/go/volume_fs/manifest.yaml, not a tool"},
		{"convert_to_markdown", false, "manifest key in tools/go/convert_to_markdown; the binary registers convert_file_to_markdown"},
		{"ffmpeg_tools", false, "manifest key in tools/go/ffmpeg; the binary registers ffmpeg/ffprobe"},
		{"pptx_tools", false, "manifest key in tools/go/pptx; the binary registers pptx_create_presentation"},
		{"execute_python_code", true, "member of the code_execution/builtin_code_execution builtin groups"},
		{"convert_file_to_markdown", true, "member of the general_agent_tools/builtin_file_tools builtin groups"},
		{"libreoffice_convert", true, "STR-mirror half only: goSTRToolDefs, in no builtin group"},
		{"pdf_extract_text", true, "in two builtin groups AND the STR mirror; no longer mirror-only"},
		{"artifact_grep", true, "in-process builtin"},
	} {
		if got := resolvable[tc.name]; got != tc.want {
			t.Errorf("resolvable[%q] = %v, want %v (%s)", tc.name, got, tc.want, tc.why)
		}
	}

	excel := manifestToolKeys(t, filepath.Join("..", "builtinskills", "files", "sam-excel", "tools", "manifest.yaml"))
	if slices.Contains(excel, "timeout_seconds") {
		t.Error("sam-excel manifest keys include \"timeout_seconds\" — the skill check is matching " +
			"nested keys or prose, not top-level tool names")
	}
	if !slices.Contains(excel, "xlsx_read") {
		t.Errorf("sam-excel manifest keys %v are missing xlsx_read — the skill check proves nothing", excel)
	}
}

func builtinToolNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for name := range builtinToolFactories {
		out[name] = true
	}
	opts := Options{}
	for _, group := range slices.Sorted(maps.Keys(schema.ValidBuiltinGroups)) {
		for _, name := range LookupBuiltinGroup(group, opts) {
			out[name] = true
		}
	}
	return out
}

func strToolNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, td := range goSTRToolDefs() {
		out[td.Name()] = true
	}
	if len(out) == 0 {
		t.Fatal("no Go STR tool definitions — the resolver would silently accept nothing")
	}
	for _, dir := range manifestOnlySTRPackageDirs(t) {
		for _, name := range manifestToolKeys(t, filepath.Join(dir, "manifest.yaml")) {
			out[name] = true
		}
	}
	return out
}

func goSTRToolDefs() []*sdk.ToolDef {
	var defs []*sdk.ToolDef
	defs = append(defs, sqltool.Tools()...)
	defs = append(defs, bedrockkb.Tools()...)
	defs = append(defs, documentdb.Tools()...)
	defs = append(defs, graphdb.Tools()...)
	defs = append(defs, searchtool.Tools()...)
	defs = append(defs, websearchtool.Tools()...)
	defs = append(defs, slacktool.Tools()...)
	defs = append(defs, sendemail.Tools()...)
	defs = append(defs, eventmesh.Tools()...)
	defs = append(defs, eventmeshprofile.Tools()...)
	defs = append(defs, buildapply.Tools()...)
	defs = append(defs, concataudio.Tools()...)
	defs = append(defs, converttomarkdown.Tools()...)
	defs = append(defs, ffmpegtool.Tools()...)
	defs = append(defs, htmltopdf.Tools()...)
	defs = append(defs, imagemagicktool.Tools()...)
	defs = append(defs, libreofficetool.Tools()...)
	defs = append(defs, mermaidtool.Tools()...)
	defs = append(defs, multispeakertts.Tools()...)
	defs = append(defs, pdfextracttext.Tools()...)
	defs = append(defs, pdfops.Tools()...)
	defs = append(defs, plotlytool.Tools()...)
	defs = append(defs, pptxtool.Tools()...)
	defs = append(defs, rendertoimages.Tools()...)
	defs = append(defs, texttospeech.Tools()...)
	defs = append(defs, xlsxtool.Tools()...)
	defs = append(defs, docxtool.Tools()...)
	defs = append(defs, testharness.Tools()...)
	defs = append(defs, samcoder.Tools()...)
	return defs
}

func manifestOnlySTRPackageDirs(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, lang := range []string{"go", "python"} {
		manifests, err := filepath.Glob(filepath.Join("..", "..", "tools", lang, "*", "manifest.yaml"))
		if err != nil {
			t.Fatalf("glob tools/%s manifests: %v", lang, err)
		}
		if len(manifests) == 0 {
			t.Fatalf("no tools/%s manifests found — the resolver would silently accept nothing", lang)
		}
		for _, m := range manifests {
			dir := filepath.Dir(m)
			goPkg, err := filepath.Glob(filepath.Join(dir, "*", "*.go"))
			if err != nil {
				t.Fatalf("glob %s sub-packages: %v", dir, err)
			}
			if len(goPkg) == 0 {
				out = append(out, dir)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no manifest-only STR package dirs found — every tools/python package plus " +
			"tools/go/volume_fs, tools/go/bamboohr_profile_provider and " +
			"tools/go/dev_profile_provider should qualify")
	}
	return out
}

type strPackageManifest struct {
	Tools        map[string]yaml.Node `yaml:"tools"`
	Capabilities struct {
		ProfileProvider bool `yaml:"profileProvider"`
	} `yaml:"capabilities"`
}

func manifestToolKeys(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read STR manifest %s: %v", path, err)
	}
	var m strPackageManifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse STR manifest %s: %v", path, err)
	}
	if len(m.Tools) == 0 {
		t.Fatalf("STR manifest %s declares no tools", path)
	}
	return slices.Sorted(maps.Keys(m.Tools))
}

func assertSkillToolDeclared(t *testing.T, skill, bare string) {
	t.Helper()
	path := filepath.Join("..", "builtinskills", "files", skill, "tools", "manifest.yaml")
	if !slices.Contains(manifestToolKeys(t, path), bare) {
		t.Errorf("skill %q manifest %s does not declare tool %q", skill, path, bare)
	}
}
