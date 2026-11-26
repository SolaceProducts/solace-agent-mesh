// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings_test

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	platform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type phantomTool struct {
	name   string
	bareOK bool
}

var phantomTools = []phantomTool{
	{name: "Save"},
	{name: "Read"},
	{name: "Edit"},
	{name: "create_artifact", bareOK: true},
	{name: "edit_artifact", bareOK: true},
	{name: "search_artifact", bareOK: true},
	{name: "sql_database", bareOK: true},
	{name: "ConvertToMarkdown", bareOK: true},
}

var backtickedIdentifier = regexp.MustCompile("`([A-Za-z_][A-Za-z_0-9]*)`")

var boldedIdentifier = regexp.MustCompile(`\*\*([A-Za-z_][A-Za-z_0-9]*)\*\*`)

var calledIdentifier = regexp.MustCompile(`([A-Za-z_][A-Za-z_0-9]*)\(`)

var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

var bareToolShapedIdentifier = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+|[A-Za-z][a-z0-9]+(?:[A-Z][a-z0-9]*)+)\b`)

var identifierSeparator = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func containsBareIdentifier(text, identifier string) bool {
	return strings.Contains(" "+identifierSeparator.ReplaceAllString(text, " ")+" ", " "+identifier+" ")
}

var citationForms = []struct {
	label string
	re    *regexp.Regexp
	call  bool
}{
	{label: "`%s`", re: backtickedIdentifier},
	{label: "**%s**", re: boldedIdentifier},
	{label: "%s(", re: calledIdentifier, call: true},
}

func citedIdentifiers(text string) []string {
	var out []string
	for _, form := range citationForms {
		for _, m := range form.re.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

func citedFormsOf(text, name string) []string {
	var out []string
	for _, form := range citationForms {
		for _, m := range form.re.FindAllStringSubmatch(text, -1) {
			if m[1] == name {
				out = append(out, fmt.Sprintf(form.label, name))
				break
			}
		}
	}
	return out
}

func phantomFormsIn(text string) []string {
	var out []string
	for _, phantom := range phantomTools {
		out = append(out, citedFormsOf(text, phantom.name)...)
		if strings.Contains(text, phantom.name+" tool") {
			out = append(out, phantom.name+" tool")
		}
		if phantom.bareOK && containsBareIdentifier(text, phantom.name) {
			out = append(out, phantom.name)
		}
	}
	return out
}

func casingCitations(text string) []string {
	out := citedIdentifiers(text)
	for _, m := range bareToolShapedIdentifier.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

func reportCasingDrift(t *testing.T, where, text string, real map[string]bool, canonicalFor func(string) (string, bool)) {
	t.Helper()
	seen := make(map[string]bool)
	for _, cited := range casingCitations(text) {
		if real[cited] || seen[cited] {
			continue
		}
		canonical, ok := canonicalFor(cited)
		if !ok {
			continue
		}
		seen[cited] = true
		t.Errorf("%s says %q; the registered tool is %q", where, cited, canonical)
	}
}

func byNormalizedToolName(real map[string]bool) map[string]string {
	byNormalized := make(map[string]string, len(real))
	for name := range real {
		byNormalized[normalizeToolName(name)] = name
	}
	return byNormalized
}

type builderPromptDoc struct {
	Apps []struct {
		AppConfig struct {
			Instruction string `yaml:"instruction"`
			Tools       []struct {
				ToolType  string `yaml:"tool_type"`
				ToolName  string `yaml:"tool_name"`
				GroupName string `yaml:"group_name"`
			} `yaml:"tools"`
		} `yaml:"app_config"`
	} `yaml:"apps"`
}

func loadBuilderPrompt(t *testing.T) builderPromptDoc {
	t.Helper()
	raw, ok := settings.BuilderConfigYAML()["builder_agent.yaml"]
	if !ok {
		t.Fatal("builder_agent.yaml missing from BuilderConfigYAML()")
	}
	var doc builderPromptDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("builder_agent.yaml is not valid YAML: %v", err)
	}
	if len(doc.Apps) == 0 || doc.Apps[0].AppConfig.Instruction == "" {
		t.Fatal("builder_agent.yaml has no apps[0].app_config.instruction")
	}
	return doc
}

func builderToolNames(t *testing.T, doc builderPromptDoc) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	for _, n := range runtimeRegisteredToolNames() {
		names[n] = true
	}
	for _, decl := range doc.Apps[0].AppConfig.Tools {
		switch decl.ToolType {
		case "builtin":
			if !names[decl.ToolName] && toolinit.LookupBuiltinTool(decl.ToolName, nil, toolinit.Options{}) == nil {
				t.Errorf("builder declares builtin %q, which resolves to no tool", decl.ToolName)
				continue
			}
			names[decl.ToolName] = true
		case "builtin-group":
			members := toolinit.LookupBuiltinGroup(decl.GroupName, toolinit.Options{})
			if len(members) == 0 {
				t.Errorf("builder declares builtin-group %q, which resolves to no tools", decl.GroupName)
			}
			for _, m := range members {
				names[m] = true
			}
		}
	}
	addFencedBlockNames(names)
	if len(names) < 10 {
		t.Fatalf("resolved only %d builder tool names; the resolution above is broken", len(names))
	}
	return names
}

func runtimeRegisteredToolNames() []string {
	names := []string{"sub_task", "load_skill", "unload_skill"}
	names = append(names, toolinit.SkillResourceToolNames()...)
	names = append(names, toolinit.TemplateToolNames()...)
	names = append(names, toolinit.TemplateAuthoringToolNames()...)
	return names
}

func addFencedBlockNames(names map[string]bool) {
	for _, prefix := range []string{agent.BlockPrefixSaveArtifact, agent.BlockPrefixTemplateLiquid} {
		names[strings.TrimSuffix(strings.TrimPrefix(prefix, agent.BlockDelimiterOpen), ":")] = true
	}
}

func catalogToolNames(t *testing.T) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	for name := range schema.ValidBuiltinToolNames {
		names[name] = true
	}
	for group := range schema.ValidBuiltinGroups {
		members := toolinit.LookupBuiltinGroup(group, toolinit.Options{})
		if len(members) == 0 {
			t.Errorf("schema.ValidBuiltinGroups advertises %q, which resolves to no tools", group)
		}
		for _, m := range members {
			names[m] = true
		}
	}
	for _, n := range runtimeRegisteredToolNames() {
		names[n] = true
	}
	addFencedBlockNames(names)
	if len(names) < 60 {
		t.Fatalf("resolved only %d catalog tool names; the catalog is far larger, so the resolution above is broken", len(names))
	}
	return names
}

func catalogCanonicalFor(byNormalized map[string]string) func(string) (string, bool) {
	return func(cited string) (string, bool) {
		if canonical, ok := byNormalized[normalizeToolName(cited)]; ok {
			return canonical, true
		}
		probe := strings.ToLower(camelBoundary.ReplaceAllString(cited, "${1}_${2}"))
		if probe != cited && toolinit.LookupBuiltinTool(probe, nil, toolinit.Options{}) != nil {
			return probe, true
		}
		return "", false
	}
}

func normalizeToolName(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "")
}

func TestBuilderPromptNamesNoPhantomTools(t *testing.T) {
	doc := loadBuilderPrompt(t)
	for _, form := range phantomFormsIn(doc.Apps[0].AppConfig.Instruction) {
		t.Errorf("builder prompt names %q as a tool, but no such tool exists; "+
			"artifacts are created with a %s block and edited with artifact_search_and_replace_regex",
			form, agent.BlockPrefixSaveArtifact)
	}
}

func TestPhantomFormsMatchWholeIdentifiersOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"bare phantom", "then create_artifact writes it", []string{"create_artifact"}},
		{"backticked phantom", "then `create_artifact` writes it", []string{"`create_artifact`", "create_artifact"}},
		{"longer identifier ending in a phantom", "the docs list apply_embed_and_create_artifact", nil},
		{"longer identifier starting with a phantom", "the docs list create_artifact_version", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := phantomFormsIn(tc.text); !slices.Equal(got, tc.want) {
				t.Errorf("phantomFormsIn(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestBuilderPromptToolNamesMatchRegisteredCasing(t *testing.T) {
	doc := loadBuilderPrompt(t)
	real := catalogToolNames(t)
	maps.Copy(real, builderToolNames(t, doc))
	canonicalFor := catalogCanonicalFor(byNormalizedToolName(real))

	reportCasingDrift(t, "builder prompt", doc.Apps[0].AppConfig.Instruction, real, canonicalFor)
}

func TestBuilderPromptDocumentsArtifactBlock(t *testing.T) {
	doc := loadBuilderPrompt(t)
	instruction := doc.Apps[0].AppConfig.Instruction

	const manifestMIMEArg = `mime_type="application/vnd.sam-build-manifest+yaml"`
	worked := false
	for line := range strings.SplitSeq(instruction, "\n") {
		if strings.Contains(line, agent.BlockPrefixSaveArtifact) && strings.Contains(line, manifestMIMEArg) {
			worked = true
			break
		}
	}
	if !worked {
		t.Errorf("builder prompt has no line carrying both %q and %s; it mandates saving a plan, so it "+
			"must show the only mechanism that creates one", agent.BlockPrefixSaveArtifact, manifestMIMEArg)
	}

	const noSaveTool = "there is no save tool"
	if !strings.Contains(strings.ToLower(instruction), noSaveTool) {
		t.Errorf("builder prompt no longer says %q; that sentence is what stops the model reaching for a "+
			"save tool it does not have, so the %s block below it reads as optional", noSaveTool,
			agent.BlockPrefixSaveArtifact)
	}

	closerAlone := false
	for line := range strings.SplitSeq(instruction, "\n") {
		if strings.TrimSpace(line) == agent.BlockDelimiterClose {
			closerAlone = true
			break
		}
	}
	if !closerAlone {
		t.Errorf("builder prompt has no line carrying the closing delimiter %q alone, so the block it shows "+
			"cannot be closed the way the parser requires", agent.BlockDelimiterClose)
	}
}

var sentenceBreak = regexp.MustCompile(`(?:[.!?]\s+|\s+[-*]\s+|\s+\d+\.\s+)`)

var (
	mandateMarker      = regexp.MustCompile(`(?i)\b(?:mandatory|must|always|required)\b`)
	universalQuestions = regexp.MustCompile(`(?i)\b(?:all|every|each|any)(?:\s+\w+){0,3}\s+questions?\b`)
)

func universalAskMandates(text string) []string {
	var out []string
	for _, sentence := range sentenceBreak.Split(strings.Join(strings.Fields(text), " "), -1) {
		if !strings.Contains(sentence, "ask_user_question") {
			continue
		}
		if mandateMarker.MatchString(sentence) && universalQuestions.MatchString(sentence) {
			out = append(out, sentence)
		}
	}
	return out
}

func reportUniversalAskMandates(t *testing.T, where, text string) {
	t.Helper()
	for _, sentence := range universalAskMandates(text) {
		t.Errorf("%s mandates ask_user_question for a whole class of questions:\n\t%s\n"+
			"The runtime refuses a call whose questions are all free-text, so a universal mandate points the "+
			"model at a form the user never sees; open-ended questions belong in the reply.", where, sentence)
	}
}

const askAwareCorpusDocs = 5

func TestBuilderPromptKeepsAskUserQuestionOptional(t *testing.T) {
	doc := loadBuilderPrompt(t)
	instruction := doc.Apps[0].AppConfig.Instruction

	reportUniversalAskMandates(t, "builder prompt", instruction)

	askAware := 0
	for _, src := range promptCorpora(t) {
		for name, body := range src.docs {
			if !strings.Contains(body, "ask_user_question") {
				continue
			}
			askAware++
			reportUniversalAskMandates(t, fmt.Sprintf("%s: %s", src.label, name), body)
		}
	}
	if askAware < askAwareCorpusDocs {
		t.Errorf("only %d prompt-corpus documents name ask_user_question, want at least %d; the mandate scan "+
			"skips every sentence that does not, so it is now passing over less than it was written to read",
			askAware, askAwareCorpusDocs)
	}

	prompt := strings.Join(strings.Fields(instruction), " ")

	askInReply := regexp.MustCompile(`(?i)ask (?:them|those|these|it) in your reply`)
	if n := len(askInReply.FindAllString(prompt, -1)); n < 2 {
		t.Errorf("the prompt sends a question to the assistant's own reply in %d place(s), want 2 — the "+
			"all-open-ended case and the long-form case. Without one of them the model has no stated way to "+
			"ask a question the form cannot carry", n)
	}

	for _, required := range []struct {
		what string
		pat  *regexp.Regexp
	}{
		{"the prose alternative to the form", regexp.MustCompile(`(?i)ask in prose`)},
		{"the runtime fact that makes the alternative necessary — an all-free-text call is refused",
			regexp.MustCompile(`(?i)free-text[^.]{0,120}refus|refus[^.]{0,120}free-text`)},
	} {
		if !required.pat.MatchString(prompt) {
			t.Errorf("builder prompt no longer carries %s (looked for /%s/).\n"+
				"That is the escape route that makes ask_user_question optional; without it every question has "+
				"to be forced into the form, which is the shape this bug came in.", required.what, required.pat)
		}
	}
}

func TestBuilderPromptCitedGroupsResolveInToolinit(t *testing.T) {
	doc := loadBuilderPrompt(t)
	seen := make(map[string]bool)
	for _, m := range backtickedIdentifier.FindAllStringSubmatch(doc.Apps[0].AppConfig.Instruction, -1) {
		cited := m[1]
		if seen[cited] {
			continue
		}
		if _, isGroup := schema.ValidBuiltinGroups[cited]; !isGroup {
			continue
		}
		seen[cited] = true
		if len(toolinit.LookupBuiltinGroup(cited, toolinit.Options{})) == 0 {
			t.Errorf("builder prompt cites builtin-group `%s`, which resolves to no tools", cited)
		}
	}
}

func TestBuilderCorpusNamesNoPhantomTools(t *testing.T) {
	for _, src := range promptCorpora(t) {
		for name, body := range src.docs {
			for _, form := range phantomFormsIn(body) {
				t.Errorf("%s: %s names %q as a tool, but no such tool exists", src.label, name, form)
			}
		}
	}
}

var blockNameCallFormExempt = map[string]bool{
	"sam-python-executor/SKILL.md": true,
}

type blockNameFinding struct {
	blockName string
	found     string
}

func blockNamesUsedAsTools(text string, real map[string]bool, callFormExempt bool) []blockNameFinding {
	blockNames := make(map[string]bool)
	addFencedBlockNames(blockNames)

	var out []blockNameFinding
	for _, name := range slices.Sorted(maps.Keys(blockNames)) {
		forms := []string{name + " tool"}
		for _, form := range citationForms {
			marked := fmt.Sprintf(form.label, name)
			if !form.call {
				forms = append(forms, marked+" tool")
				continue
			}
			if !callFormExempt {
				forms = append(forms, marked)
			}
		}
		for _, form := range forms {
			if strings.Contains(text, form) {
				out = append(out, blockNameFinding{blockName: name, found: form})
			}
		}
	}
	return append(out, blockNamesListedAmongTools(text, blockNames, real)...)
}

var listSeparator = regexp.MustCompile(`^(?:\s*[,;/]\s*(?:(?:and|or)\s+)?|\s+(?:and|or)\s+)$`)

func blockNamesListedAmongTools(text string, blockNames, real map[string]bool) []blockNameFinding {
	isTool := func(name string) bool { return real[name] && !blockNames[name] }
	cited := backtickedIdentifier.FindAllStringSubmatchIndex(text, -1)
	var out []blockNameFinding
	for i := 1; i < len(cited); i++ {
		prev, cur := cited[i-1], cited[i]
		if !listSeparator.MatchString(text[prev[1]:cur[0]]) {
			continue
		}
		left, right := text[prev[2]:prev[3]], text[cur[2]:cur[3]]
		switch {
		case blockNames[left] && isTool(right):
			out = append(out, blockNameFinding{blockName: left, found: text[prev[0]:cur[1]]})
		case blockNames[right] && isTool(left):
			out = append(out, blockNameFinding{blockName: right, found: text[prev[0]:cur[1]]})
		}
	}
	return out
}

func TestBuilderCorpusDoesNotCallBlockNamesTools(t *testing.T) {
	doc := loadBuilderPrompt(t)
	for _, finding := range blockNamesUsedAsTools(doc.Apps[0].AppConfig.Instruction, builderToolNames(t, doc), false) {
		t.Errorf("builder prompt writes %q, presenting %s as a callable tool; it is a fenced-block type in assistant text",
			finding.found, finding.blockName)
	}

	catalog := catalogToolNames(t)
	for _, src := range promptCorpora(t) {
		for name, body := range src.docs {
			for _, finding := range blockNamesUsedAsTools(body, catalog, blockNameCallFormExempt[name]) {
				t.Errorf("%s: %s writes %q, presenting %s as a callable tool; it is a fenced-block type in assistant text",
					src.label, name, finding.found, finding.blockName)
			}
		}
	}
}

func TestBuilderCorpusToolNamesMatchRegisteredCasing(t *testing.T) {
	real := catalogToolNames(t)
	canonicalFor := catalogCanonicalFor(byNormalizedToolName(real))

	for _, src := range promptCorpora(t) {
		for name, body := range src.docs {
			reportCasingDrift(t, fmt.Sprintf("%s: %s", src.label, name), body, real, canonicalFor)
		}
	}
}

var agentConfigDirs = []struct {
	label        string
	dir          string
	minAgentApps int
	internalOnly bool
}{
	{"examples/agents", filepath.Join("..", "..", "examples", "agents"), 8, true},
	{"internal/platform/admin/builtin_agents", filepath.Join("..", "platform", "admin", "builtin_agents"), 3, false},
}

type agentPromptDoc struct {
	Apps []struct {
		AppConfig struct {
			AgentName   string `yaml:"agent_name"`
			DisplayName string `yaml:"display_name"`
			Instruction string `yaml:"instruction"`
			AgentCard   struct {
				Description string `yaml:"description"`
				Skills      []struct {
					Name        string `yaml:"name"`
					Description string `yaml:"description"`
				} `yaml:"skills"`
			} `yaml:"agent_card"`
		} `yaml:"app_config"`
	} `yaml:"apps"`
}

func agentPromptDocs(t *testing.T, label, dir string, minAgentApps int) map[string]string {
	t.Helper()
	docs := make(map[string]string)
	agentApps := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || (path.Ext(p) != ".yaml" && path.Ext(p) != ".yml") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", rel, readErr)
		}
		var doc agentPromptDoc
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s is not valid YAML: %w", rel, err)
		}
		for i, app := range doc.Apps {
			where := fmt.Sprintf("%s#apps[%d]", rel, i)
			before := len(docs)
			card := app.AppConfig.AgentCard
			recordPromptDoc(docs, where+".instruction", app.AppConfig.Instruction)
			recordPromptDoc(docs, where+".agent_card.description", card.Description)
			for j, skill := range card.Skills {
				recordPromptDoc(docs, fmt.Sprintf("%s.agent_card.skills[%d].name", where, j), skill.Name)
				recordPromptDoc(docs, fmt.Sprintf("%s.agent_card.skills[%d].description", where, j), skill.Description)
			}
			if app.AppConfig.AgentName == "" && app.AppConfig.DisplayName == "" {
				continue
			}
			agentApps++
			if len(docs) == before {
				t.Errorf("%s: %s declares an agent but yielded no prompt text; the load above is broken", label, where)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s: walk %s: %v", label, dir, err)
	}
	if agentApps < minAgentApps {
		t.Fatalf("%s: %s holds %d agent apps, expected at least %d; the load above is broken",
			label, dir, agentApps, minAgentApps)
	}
	return docs
}

func recordPromptDoc(docs map[string]string, key, body string) {
	if strings.TrimSpace(body) != "" {
		docs[key] = body
	}
}

const (
	connectorTypesLabel = "internal/platform/admin/connector_types"
	minGuidedSubtypes   = 15
)

func connectorUsageGuidance(t *testing.T) map[string]string {
	t.Helper()
	defs, err := platform.LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("%s: load: %v", connectorTypesLabel, err)
	}
	docs := make(map[string]string)
	for _, def := range defs {
		for _, subtype := range def.Subtypes {
			recordPromptDoc(docs, fmt.Sprintf("%s.yaml#%s.usage_guidance", def.Type, subtype.Subtype), subtype.UsageGuidance)
		}
	}
	if len(docs) < minGuidedSubtypes {
		t.Fatalf("%s: %d subtypes carry usage_guidance, expected at least %d; the load above is broken",
			connectorTypesLabel, len(docs), minGuidedSubtypes)
	}
	return docs
}

type promptCorpus struct {
	label string
	docs  map[string]string
}

func promptCorpora(t *testing.T) []promptCorpus {
	t.Helper()
	var out []promptCorpus
	for _, src := range []struct {
		label      string
		fsys       fs.FS
		skipSubdir string
	}{
		{label: "settings.SchemaSkillsFS", fsys: settings.SchemaSkillsFS()},
		{label: "design.FS", fsys: design.FS()},
		{label: "builtinskills.FS", fsys: builtinskills.FS(), skipSubdir: customerDocsSubtree},
	} {
		docs := markdownDocs(t, src.label, src.fsys, src.skipSubdir)
		if len(docs) == 0 {
			t.Fatalf("%s exposed no markdown; the walk above is broken", src.label)
		}
		out = append(out, promptCorpus{label: src.label, docs: docs})
	}
	for _, src := range agentConfigDirs {
		if src.internalOnly && !sourcetree.IsInternal(t) {
			continue
		}
		out = append(out, promptCorpus{label: src.label, docs: agentPromptDocs(t, src.label, src.dir, src.minAgentApps)})
	}
	out = append(out, promptCorpus{label: connectorTypesLabel, docs: connectorUsageGuidance(t)})
	return out
}

const customerDocsSubtree = "sam-docs/references/documentation"

func markdownDocs(t *testing.T, label string, fsys fs.FS, skipSubdir string) map[string]string {
	t.Helper()
	docs := make(map[string]string)
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skipSubdir != "" && (p == skipSubdir || strings.HasPrefix(p, skipSubdir+"/")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || path.Ext(p) != ".md" {
			return nil
		}
		b, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", p, readErr)
		}
		docs[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("%s: walk: %v", label, err)
	}
	return docs
}
