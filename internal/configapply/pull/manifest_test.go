// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/sources"
)

type stubURL struct{ url string }

func (s stubURL) TargetURL() string { return s.url }

func localRefs(names ...string) []ResourceRef {
	out := make([]ResourceRef, len(names))
	for i, n := range names {
		out[i] = ResourceRef{Name: n}
	}
	return out
}

func TestBuildManifestYAMLOmitsEmptyKinds(t *testing.T) {
	entries := map[string][]ResourceRef{
		"models":  localRefs("planning", "general"),
		"agents":  localRefs("orchestrator"),
		"workers": localRefs("never-emitted"),
	}
	raw, err := buildManifestYAML(Options{}, entries, nil, nil, stubURL{url: "http://localhost:8000"})
	if err != nil {
		t.Fatalf("buildManifestYAML: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "kind: manifest") {
		t.Errorf("missing kind: %s", got)
	}
	if !strings.Contains(got, "url: http://localhost:8000") {
		t.Errorf("missing target url: %s", got)
	}
	if !strings.Contains(got, "models:") || !strings.Contains(got, "- general") || !strings.Contains(got, "- planning") {
		t.Errorf("missing models block: %s", got)
	}
	if !strings.Contains(got, "agents:") || !strings.Contains(got, "- orchestrator") {
		t.Errorf("missing agents block: %s", got)
	}
	if strings.Contains(got, "workers:") {
		t.Errorf("unexpected non-in-scope kind in output: %s", got)
	}
	if strings.Contains(got, "connectors:") {
		t.Errorf("empty kinds should be omitted: %s", got)
	}
	if strings.Contains(got, "\nsources:") {
		t.Errorf("no imports were emitted but sources: block appeared: %s", got)
	}
}

func TestBuildManifestYAMLEmitsImports(t *testing.T) {
	entries := map[string][]ResourceRef{
		"agents": {
			{Name: "orchestrator"},
			{Name: "salesforce", From: "salesforce", Source: "bizops"},
			{Name: "team_research", From: "research", Source: "ai-research"},
		},
	}
	usedSources := map[string]string{
		"bizops":      "git+https://github.com/example/bizops.git@v1.0.0",
		"ai-research": "git+https://github.com/example/ai-research.git@v2.3.1",
	}
	raw, err := buildManifestYAML(Options{}, entries, usedSources, nil, stubURL{url: "http://localhost:8000"})
	if err != nil {
		t.Fatalf("buildManifestYAML: %v", err)
	}

	parsed, err := manifest.Parse(raw)
	if err != nil {
		t.Fatalf("parse pulled YAML: %v\n%s", err, string(raw))
	}
	if len(parsed.Sources) != 2 {
		t.Errorf("sources count = %d want 2; got %+v", len(parsed.Sources), parsed.Sources)
	}
	if src, ok := parsed.Sources["bizops"]; !ok || src.Ref != "v1.0.0" {
		t.Errorf("bizops source missing or wrong ref: %+v ok=%v", src, ok)
	}
	gotAgents := parsed.Resources["agents"]
	if len(gotAgents) != 3 {
		t.Fatalf("agents count = %d want 3; got %+v", len(gotAgents), gotAgents)
	}
	want := map[string]manifest.ResourceEntry{
		"orchestrator":  {Name: "orchestrator", From: "orchestrator", Source: ""},
		"salesforce":    {Name: "salesforce", From: "salesforce", Source: "bizops"},
		"team_research": {Name: "team_research", From: "research", Source: "ai-research"},
	}
	for _, e := range gotAgents {
		w, ok := want[e.Name]
		if !ok {
			t.Errorf("unexpected agent entry %+v", e)
			continue
		}
		if e != w {
			t.Errorf("agent %q: got %+v want %+v", e.Name, e, w)
		}
	}

	text := string(raw)
	if !strings.Contains(text, "- orchestrator") {
		t.Errorf("missing bare-local entry: %s", text)
	}
	if !strings.Contains(text, "- salesforce@bizops") {
		t.Errorf("missing name@source entry: %s", text)
	}
	if !strings.Contains(text, "from: research@ai-research") || !strings.Contains(text, "as: team_research") {
		t.Errorf("missing {from, as} entry: %s", text)
	}
	if !strings.Contains(text, "git+https://github.com/example/bizops.git@v1.0.0") {
		t.Errorf("sources block missing bizops URL: %s", text)
	}
}

func TestBuildManifestYAMLBareLocalRoundTrip(t *testing.T) {
	entries := map[string][]ResourceRef{
		"models": localRefs("default-model"),
		"agents": localRefs("local-only"),
	}
	raw, err := buildManifestYAML(Options{}, entries, nil, nil, stubURL{url: "http://localhost:8000"})
	if err != nil {
		t.Fatalf("buildManifestYAML: %v", err)
	}
	parsed, err := manifest.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, string(raw))
	}
	if len(parsed.Sources) != 0 {
		t.Errorf("local-only pull leaked a sources block: %+v", parsed.Sources)
	}
	if len(parsed.Resources["agents"]) != 1 || parsed.Resources["agents"][0].Source != "" {
		t.Errorf("local-only agent emitted with source: %+v", parsed.Resources["agents"])
	}
}

func TestBuildImportLookupsMaps(t *testing.T) {
	bizopsSrc, _ := sources.Parse("git+https://github.com/example/bizops.git@v1.0.0")
	m := &manifest.Manifest{
		Sources: map[string]sources.Source{"bizops": bizopsSrc},
		Resources: map[string][]manifest.ResourceEntry{
			"agents": {
				{Name: "local-only", From: "local-only", Source: ""},
				{Name: "salesforce", From: "salesforce", Source: "bizops"},
				{Name: "team_x", From: "x", Source: "bizops"},
			},
		},
	}
	lookups := buildImportLookups(m)
	agents := lookups["agents"]
	if len(agents) != 2 {
		t.Fatalf("agent imports = %d want 2; %+v", len(agents), agents)
	}
	if e := agents["salesforce"]; e.Source != "bizops" || e.From != "salesforce" || e.Raw != bizopsSrc.Raw {
		t.Errorf("salesforce mapping wrong: %+v", e)
	}
	if e := agents["team_x"]; e.Source != "bizops" || e.From != "x" || e.Raw != bizopsSrc.Raw {
		t.Errorf("team_x mapping wrong: %+v", e)
	}
	if _, leaked := agents["local-only"]; leaked {
		t.Errorf("local-only leaked into import lookups")
	}
}

func TestDropImportFilesByKind(t *testing.T) {
	entries := []fileEntry{
		{relPath: "agents/local-only.yaml", content: []byte("local")},
		{relPath: "agents/salesforce.yaml", content: []byte("imported")},
		{relPath: "skills/imported-skill/SKILL.md", content: []byte("# skill")},
		{relPath: "skills/imported-skill/references/x.md", content: []byte("ref")},
		{relPath: "skills/local-skill/SKILL.md", content: []byte("# local")},
		{relPath: "toolsets/imp/imp.zip", content: []byte{0x50, 0x4b}},
		{relPath: "manifests/pulled-x.yaml", content: []byte("manifest")},
	}
	got := dropImportFiles(entries, "agents", map[string]struct{}{"salesforce": {}})
	if len(got) != 6 {
		t.Errorf("agents drop: got %d entries; want 6 (only salesforce dropped)", len(got))
	}
	for _, e := range got {
		if e.relPath == "agents/salesforce.yaml" {
			t.Errorf("agents drop did not remove imported file: %s", e.relPath)
		}
	}

	got = dropImportFiles(entries, "skills", map[string]struct{}{"imported-skill": {}})
	for _, e := range got {
		if strings.HasPrefix(e.relPath, "skills/imported-skill/") {
			t.Errorf("skills drop did not remove imported subtree entry: %s", e.relPath)
		}
	}
	hasLocalSkill := false
	for _, e := range got {
		if e.relPath == "skills/local-skill/SKILL.md" {
			hasLocalSkill = true
		}
	}
	if !hasLocalSkill {
		t.Errorf("skills drop incorrectly removed local skill file")
	}

	got = dropImportFiles(entries, "toolsets", map[string]struct{}{"imp": {}})
	for _, e := range got {
		if strings.HasPrefix(e.relPath, "toolsets/imp/") {
			t.Errorf("toolsets drop did not remove imported zip: %s", e.relPath)
		}
	}
}

func TestDropImportFilesRBACPaths(t *testing.T) {
	entries := []fileEntry{
		{relPath: "rbac/roles/admin.yaml", content: []byte("admin")},
		{relPath: "rbac/roles/viewer.yaml", content: []byte("viewer")},
		{relPath: "rbac/assignments/alice.yaml", content: []byte("alice")},
		{relPath: "rbac/claim-mappings/group_a.yaml", content: []byte("group_a")},
		{relPath: "skills/rbac/SKILL.md", content: []byte("# rbac skill")},
	}

	got := dropImportFiles(entries, "rbac/roles", map[string]struct{}{"admin": {}})
	if len(got) != 4 {
		t.Errorf("rbac/roles drop: got %d entries; want 4 (admin only dropped)", len(got))
	}
	for _, e := range got {
		if e.relPath == "rbac/roles/admin.yaml" {
			t.Errorf("imported rbacRole not dropped: %s", e.relPath)
		}
	}
	hasViewer, hasSkill := false, false
	for _, e := range got {
		if e.relPath == "rbac/roles/viewer.yaml" {
			hasViewer = true
		}
		if e.relPath == "skills/rbac/SKILL.md" {
			hasSkill = true
		}
	}
	if !hasViewer {
		t.Errorf("local rbacRole viewer was incorrectly dropped")
	}
	if !hasSkill {
		t.Errorf("skill named 'rbac' was incorrectly dropped by rbac/roles filter")
	}

	got = dropImportFiles(entries, "rbac/assignments", map[string]struct{}{"alice": {}})
	for _, e := range got {
		if e.relPath == "rbac/assignments/alice.yaml" {
			t.Errorf("imported rbacAssignment not dropped: %s", e.relPath)
		}
	}

	got = dropImportFiles(entries, "rbac/claim-mappings", map[string]struct{}{"group_a": {}})
	for _, e := range got {
		if e.relPath == "rbac/claim-mappings/group_a.yaml" {
			t.Errorf("imported rbacClaimMapping not dropped: %s", e.relPath)
		}
	}
}

func TestApplyProvenanceMixed(t *testing.T) {
	bizopsSrc, _ := sources.Parse("git+https://github.com/example/bizops.git@v1.0.0")
	research, _ := sources.Parse("git+https://github.com/example/ai-research.git@v2.3.1")
	m := &manifest.Manifest{
		Sources: map[string]sources.Source{
			"bizops":      bizopsSrc,
			"ai-research": research,
		},
		Resources: map[string][]manifest.ResourceEntry{
			"agents": {
				{Name: "orchestrator", From: "orchestrator", Source: ""},
				{Name: "salesforce", From: "salesforce", Source: "bizops"},
				{Name: "team_research", From: "research", Source: "ai-research"},
			},
		},
	}
	lookups := buildImportLookups(m)
	refs, importedResources, usedSources := applyProvenance(
		[]string{"orchestrator", "salesforce", "team_research"},
		lookups["agents"],
	)
	if len(refs) != 3 {
		t.Fatalf("ref count = %d want 3; %+v", len(refs), refs)
	}
	if refs[0] != (ResourceRef{Name: "orchestrator"}) {
		t.Errorf("local ref = %+v", refs[0])
	}
	if refs[1] != (ResourceRef{Name: "salesforce", From: "salesforce", Source: "bizops"}) {
		t.Errorf("bare import ref = %+v", refs[1])
	}
	if refs[2] != (ResourceRef{Name: "team_research", From: "research", Source: "ai-research"}) {
		t.Errorf("aliased import ref = %+v", refs[2])
	}
	if _, ok := usedSources["bizops"]; !ok {
		t.Errorf("bizops missing from used-source set")
	}
	if _, ok := usedSources["ai-research"]; !ok {
		t.Errorf("ai-research missing from used-source set")
	}
	if _, ok := importedResources["salesforce"]; !ok {
		t.Errorf("salesforce missing from imported-resource set (drives file-suppression)")
	}
	if _, ok := importedResources["team_research"]; !ok {
		t.Errorf("team_research missing from imported-resource set")
	}
	if _, ok := importedResources["orchestrator"]; ok {
		t.Errorf("local orchestrator must not appear in imported-resource set")
	}
}

func TestManifestNameRespectsOverride(t *testing.T) {
	if got := manifestName("staging.yaml"); got != "staging" {
		t.Errorf("manifestName(staging.yaml) = %q want %q", got, "staging")
	}
	if got := manifestName("manifests/prod"); got != "prod" {
		t.Errorf("manifestName(manifests/prod) = %q want %q", got, "prod")
	}
	if got := manifestName(""); !strings.HasPrefix(got, "pulled-") {
		t.Errorf("default manifestName = %q want pulled-*", got)
	}
}

func TestManifestRelPath(t *testing.T) {
	if got := manifestRelPath(""); !strings.HasPrefix(got, "manifests/pulled-") {
		t.Errorf("default relPath = %q", got)
	}
	if got := manifestRelPath("staging"); got != "manifests/staging.yaml" {
		t.Errorf("bare relPath = %q", got)
	}
	if got := manifestRelPath("custom/dir/prod.yaml"); got != "custom/dir/prod.yaml" {
		t.Errorf("path-shaped relPath = %q", got)
	}
}

func TestManifestRelPathRejectsTraversal(t *testing.T) {
	cases := map[string]string{
		"../../etc/escape.yaml": "manifests/escape.yaml",
		"../../staging.yaml":    "manifests/staging.yaml",
		"./prod.yaml":           "manifests/prod.yaml",
	}
	for in, want := range cases {
		if got := manifestRelPath(in); got != want {
			t.Errorf("manifestRelPath(%q) = %q want %q", in, got, want)
		}
	}
}
