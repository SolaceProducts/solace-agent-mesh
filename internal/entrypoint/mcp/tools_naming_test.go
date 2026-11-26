// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var providerSafeToolName = regexp.MustCompile(`^[a-z0-9_]+$`)

const (
	alphaCardName = "agent_019e9445_4061_7104_87a5_3e29ad2fe230"
	bravoCardName = "agent_019e9445_4061_7104_87a5_3e29ad2f1c4b"
	twinCardName  = "agent_01111111_4061_7104_87a5_3e29ad2fe230"

	sharedUUIDHead = "019e9445"
	alphaPrefix    = "agent_ad2fe230"
	bravoPrefix    = "agent_ad2f1c4b"
)

func cardWithSkills(cardName, displayName string, skills ...a2a.AgentSkill) *a2a.AgentCard {
	card := &a2a.AgentCard{Name: cardName, Skills: skills}
	if displayName != "" {
		card.Capabilities.Extensions = []a2a.AgentExtension{{
			URI:    a2a.ExtensionURIDisplayName,
			Params: map[string]any{"display_name": displayName},
		}}
	}
	return card
}

func cardWithDisplayName(cardName, displayName string, skills ...string) *a2a.AgentCard {
	named := make([]a2a.AgentSkill, len(skills))
	for i, s := range skills {
		named[i] = a2a.AgentSkill{ID: fmt.Sprintf("s%d", i), Name: s}
	}
	return cardWithSkills(cardName, displayName, named...)
}

func toolNamesFor(a *Adapter, agentName string) []string {
	a.toolMu.RLock()
	defer a.toolMu.RUnlock()
	return append([]string(nil), a.agentTools[agentName]...)
}

func newLoggingToolRegistryAdapter(t *testing.T) (*Adapter, *bytes.Buffer) {
	t.Helper()
	logged := &bytes.Buffer{}
	a := newToolRegistryAdapter(t)
	a.logger = slog.New(slog.NewTextHandler(logged, nil))
	return a, logged
}

func logLineWith(t *testing.T, logged *bytes.Buffer, msg string) string {
	t.Helper()
	for line := range strings.SplitSeq(logged.String(), "\n") {
		if strings.Contains(line, msg) {
			return line
		}
	}
	t.Fatalf("no log line reports %q; log was:\n%s", msg, logged.String())
	return ""
}

func logFieldValue(t *testing.T, line, key string) string {
	t.Helper()
	at := strings.Index(line, key+"=")
	if at < 0 {
		t.Fatalf("record carries no %s field: %q", key, line)
	}
	value := line[at+len(key)+1:]
	if !strings.HasPrefix(value, `"`) {
		if end := strings.IndexByte(value, ' '); end >= 0 {
			return value[:end]
		}
		return value
	}
	quoted, err := strconv.QuotedPrefix(value)
	if err != nil {
		t.Fatalf("record field %s is not a terminated quoted value in %q: %v", key, line, err)
	}
	unquoted, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("record field %s does not unquote in %q: %v", key, line, err)
	}
	return unquoted
}

func assertIdentifiesNothing(t *testing.T, line, key string) {
	t.Helper()
	value := logFieldValue(t, line, key)
	inner := strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	if identifying := strings.Fields(inner); len(identifying) != 0 {
		t.Errorf("record %s identifies %q, so it is not the blank the count stands in for: %q", key, identifying, line)
	}
}

func listTools(t *testing.T, a *Adapter) []mcplib.Tool {
	t.Helper()
	resp := a.mcpServer.HandleMessage(t.Context(), []byte(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/list"
	}`))
	jsonResp, ok := resp.(mcplib.JSONRPCResponse)
	if !ok {
		t.Fatalf("tools/list response is %T = %+v, want JSONRPCResponse", resp, resp)
	}
	result, ok := jsonResp.Result.(mcplib.ListToolsResult)
	if !ok {
		t.Fatalf("tools/list result is %T = %+v, want ListToolsResult", jsonResp.Result, jsonResp.Result)
	}
	return result.Tools
}

func listToolCatalog(t *testing.T, a *Adapter) []string {
	t.Helper()
	return names(listTools(t, a))
}

func sortedToolCatalog(t *testing.T, a *Adapter) []string {
	t.Helper()
	catalog := listToolCatalog(t, a)
	slices.Sort(catalog)
	return catalog
}

func onlyAdvertisedTool(t *testing.T, a *Adapter) mcplib.Tool {
	t.Helper()
	tools := listTools(t, a)
	if len(tools) != 1 {
		t.Fatalf("tools/list advertised %v, want exactly one tool", names(tools))
	}
	return tools[0]
}

func toolMapHas(a *Adapter, toolName string) bool {
	a.toolMu.RLock()
	defer a.toolMu.RUnlock()
	_, taken := a.toolMap[toolName]
	return taken
}

func TestRegisterAgentTools_PrefixesWithTheUUIDTailNotItsHead(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support Desk", "Search"))
	a.registerAgentTools(cardWithDisplayName(bravoCardName, "Helpdesk", "Search"))

	wantAlpha, wantBravo := alphaPrefix+"_search", bravoPrefix+"_search"
	if got := toolNamesFor(a, alphaCardName); len(got) != 1 || got[0] != wantAlpha {
		t.Errorf("first agent tools = %v, want [%s]", got, wantAlpha)
	}
	if got := toolNamesFor(a, bravoCardName); len(got) != 1 || got[0] != wantBravo {
		t.Errorf("second agent tools = %v, want [%s]", got, wantBravo)
	}

	for _, name := range sortedToolCatalog(t, a) {
		if strings.Contains(name, sharedUUIDHead) {
			t.Errorf("tool name %q carries the shared uuid head, so it cannot distinguish same-batch ids", name)
		}
	}
	if strings.Contains(logged.String(), logUsedCardName) {
		t.Errorf("the two ids contended for one name, so the prefix is not tail-derived:\n%s", logged.String())
	}
}

func TestRegisterAgentTools_KeepsAPlainCardNameAsThePrefix(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithDisplayName("plain-agent", "Support Desk", "Greet")

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	if len(got) != 1 || got[0] != "plain_agent_greet" {
		t.Errorf("tool names = %v, want [plain_agent_greet] from the card name, not the display name", got)
	}
}

func TestRegisterAgentTools_PrefixesAWorkflowCardWithItsKind(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithDisplayName("workflow_019e9445_4061_7104_87a5_3e29ad2fe230", "Nightly Report", "Run Report")

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	if len(got) != 1 || got[0] != "workflow_ad2fe230_run_report" {
		t.Errorf("tool names = %v, want [workflow_ad2fe230_run_report]", got)
	}
}

func TestRegisterAgentTools_RenamingTheDisplayNameChangesNoToolName(t *testing.T) {
	a := newToolRegistryAdapter(t)

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support Desk", "Search"))
	before := onlyAdvertisedTool(t, a)

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Helpdesk", "Search"))
	after := onlyAdvertisedTool(t, a)

	if before.Name != after.Name {
		t.Errorf("tool name changed with the display name: %q then %q", before.Name, after.Name)
	}
	if got := toolNamesFor(a, alphaCardName); len(got) != 1 || got[0] != alphaPrefix+"_search" {
		t.Errorf("agent tools = %v, want [%s]", got, alphaPrefix+"_search")
	}
	a.toolMu.RLock()
	entries := len(a.toolMap)
	a.toolMu.RUnlock()
	if entries != 1 {
		t.Errorf("toolMap holds %d entries, want 1 — a renamed display name orphaned a name", entries)
	}

	if before.Annotations.Title == after.Annotations.Title {
		t.Errorf("the display name never reached the tool: title stayed %q", after.Annotations.Title)
	}
}

func TestRegisterAgentTools_KnownLimitationReRegisteringWithARenamedSkillOrphansTheOldName(t *testing.T) {
	a := newToolRegistryAdapter(t)
	orphaned, current := alphaPrefix+"_search", alphaPrefix+"_lookup"

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support Desk", "Search"))
	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support Desk", "Lookup"))

	if got := toolNamesFor(a, alphaCardName); len(got) != 1 || got[0] != current {
		t.Fatalf("agent tools = %v, want [%s]", got, current)
	}
	if got, want := sortedToolCatalog(t, a), []string{current, orphaned}; !slices.Equal(got, want) {
		t.Errorf("tools/list catalog = %v, want %v", got, want)
	}
	if !toolMapHas(a, orphaned) {
		t.Errorf("toolMap no longer holds the orphaned name %q", orphaned)
	}

	a.deregisterAgentTools(alphaCardName)

	if !toolMapHas(a, orphaned) {
		t.Errorf("toolMap no longer holds %q after deregistration", orphaned)
	}
	if got, want := listToolCatalog(t, a), []string{orphaned}; !slices.Equal(got, want) {
		t.Errorf("tools/list catalog after deregistration = %v, want %v", got, want)
	}
}

func TestRegisterAgentTools_ReRegisteringTheSameCardKeepsTheSameToolName(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithDisplayName(alphaCardName, "Support Desk", "Search")

	a.registerAgentTools(card)
	first := toolNamesFor(a, card.Name)
	a.registerAgentTools(card)
	second := toolNamesFor(a, card.Name)

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("tool names = %v then %v, want exactly one each", first, second)
	}
	if first[0] != second[0] {
		t.Errorf("tool name changed on re-registration: %q then %q", first[0], second[0])
	}

	a.toolMu.RLock()
	defer a.toolMu.RUnlock()
	if len(a.toolMap) != 1 {
		t.Errorf("toolMap = %+v, want exactly 1 entry", a.toolMap)
	}
	if m := a.toolMap[second[0]]; m.agentName != card.Name {
		t.Errorf("%q routes to %q, want %q", second[0], m.agentName, card.Name)
	}
}

func TestRegisterAgentTools_DescriptionAndTitleCarryTheAgentDisplayName(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithSkills(alphaCardName, "Jira and Confluence",
		a2a.AgentSkill{ID: "s0", Name: "Search Knowledge Base", Description: "Finds runbooks and postmortems"},
	)

	a.registerAgentTools(card)

	tool := onlyAdvertisedTool(t, a)
	if want := "agent_ad2fe230_search_knowledge_base"; tool.Name != want {
		t.Errorf("tool name = %q, want %q", tool.Name, want)
	}
	if strings.Contains(tool.Name, "jira") {
		t.Errorf("tool name is derived from the display name: %q", tool.Name)
	}
	if !strings.Contains(tool.Description, "Jira and Confluence") {
		t.Errorf("description does not say which agent the tool routes to: %q", tool.Description)
	}
	if !strings.Contains(tool.Description, "Finds runbooks and postmortems") {
		t.Errorf("description lost the skill's own text: %q", tool.Description)
	}
	if want := "Jira and Confluence: Search Knowledge Base"; tool.Annotations.Title != want {
		t.Errorf("title annotation = %q, want %q", tool.Annotations.Title, want)
	}
}

func TestRegisterAgentTools_DistinguishesTwoAgentsSameNamedSkillInTheDescription(t *testing.T) {
	a := newToolRegistryAdapter(t)
	a.registerAgentTools(cardWithSkills(alphaCardName, "Jira and Confluence",
		a2a.AgentSkill{ID: "s0", Name: "Search", Description: "Searches docs"}))
	a.registerAgentTools(cardWithSkills(bravoCardName, "Salesforce",
		a2a.AgentSkill{ID: "s0", Name: "Search", Description: "Searches docs"}))

	descriptions := map[string]string{}
	titles := map[string]string{}
	for _, tool := range listTools(t, a) {
		descriptions[tool.Name] = tool.Description
		titles[tool.Name] = tool.Annotations.Title
	}
	if len(descriptions) != 2 {
		t.Fatalf("tools/list advertised %v, want two tools", descriptions)
	}
	if !strings.Contains(descriptions[alphaPrefix+"_search"], "Jira and Confluence") {
		t.Errorf("first agent's tool does not identify it: %q", descriptions[alphaPrefix+"_search"])
	}
	if !strings.Contains(descriptions[bravoPrefix+"_search"], "Salesforce") {
		t.Errorf("second agent's tool does not identify it: %q", descriptions[bravoPrefix+"_search"])
	}
	if titles[alphaPrefix+"_search"] == titles[bravoPrefix+"_search"] {
		t.Errorf("both tools carry the title %q, so a client renders them identically", titles[alphaPrefix+"_search"])
	}
}

func TestRegisterAgentTools_DescribesACardWithNoDisplayNameByItsCardName(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithSkills("plain-agent", "", a2a.AgentSkill{ID: "s0", Name: "Greet", Description: "Says hello"})

	a.registerAgentTools(card)

	tool := onlyAdvertisedTool(t, a)
	if !strings.Contains(tool.Description, "plain-agent") {
		t.Errorf("description does not name the agent: %q", tool.Description)
	}
	if want := "plain-agent: Greet"; tool.Annotations.Title != want {
		t.Errorf("title annotation = %q, want %q", tool.Annotations.Title, want)
	}
}

func TestRegisterAgentTools_TitlesASkillCarryingNoNameByItsID(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithSkills(alphaCardName, "Support Desk", a2a.AgentSkill{ID: "search_docs"})

	a.registerAgentTools(card)

	tool := onlyAdvertisedTool(t, a)
	if want := "Support Desk: search_docs"; tool.Annotations.Title != want {
		t.Errorf("title annotation = %q, want %q", tool.Annotations.Title, want)
	}
}

func TestRegisterAgentTools_FallsBackToTheFullCardNameWhenTwoIDsShareATail(t *testing.T) {
	a := newToolRegistryAdapter(t)

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support", "Search"))
	a.registerAgentTools(cardWithDisplayName(twinCardName, "Helpdesk", "Search"))

	preferred := alphaPrefix + "_search"
	fallback := "agent_01111111_4061_7104_87a5_3e29ad2fe230_search"
	if got := toolNamesFor(a, alphaCardName); len(got) != 1 || got[0] != preferred {
		t.Errorf("first agent tools = %v, want [%s]", got, preferred)
	}
	if got := toolNamesFor(a, twinCardName); len(got) != 1 || got[0] != fallback {
		t.Errorf("second agent tools = %v, want [%s]", got, fallback)
	}
	if len(fallback) > maxToolNameLen {
		t.Errorf("the fallback is %d chars, over the %d providers allow: %q", len(fallback), maxToolNameLen, fallback)
	}

	wantCatalog := []string{fallback, preferred}
	if got := sortedToolCatalog(t, a); !slices.Equal(got, wantCatalog) {
		t.Errorf("tools/list catalog = %v, want %v", got, wantCatalog)
	}

	a.toolMu.RLock()
	defer a.toolMu.RUnlock()
	if m := a.toolMap[preferred]; m.agentName != alphaCardName {
		t.Errorf("%q must still route to the first agent, got %q", preferred, m.agentName)
	}
	if m, ok := a.toolMap[fallback]; !ok || m.agentName != twinCardName {
		t.Errorf("second agent must remain routable, got %+v", m)
	}
}

func TestRegisterAgentTools_ReportsTheFallbackNameAndTheAgentOwningThePreferredOne(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support", "Search"))
	a.registerAgentTools(cardWithDisplayName(twinCardName, "Helpdesk", "Search"))

	line := logLineWith(t, logged, logUsedCardName)
	for _, want := range []string{
		"agentName=" + twinCardName,
		"toolNames=[" + twinCardName + "_search]",
		"conflictingAgentNames=[" + alphaCardName + "]",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("record %q does not carry %q", line, want)
		}
	}
}

func TestRegisterAgentTools_ConflictRecordsNameTheReportingEntrypoint(t *testing.T) {
	a, _ := newDispatchingToolRegistryAdapter(t)
	logged := &bytes.Buffer{}
	a.logger = slog.New(slog.NewTextHandler(logged, nil))

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support", "Search"))
	a.registerAgentTools(cardWithDisplayName(twinCardName, "Helpdesk", "Search"))

	line := logLineWith(t, logged, logUsedCardName)
	if !strings.Contains(line, "gatewayID=test-mcp-gw") {
		t.Errorf("record does not name the entrypoint that renamed the tool: %q", line)
	}
}

func TestRegisterAgentTools_ReportsEveryContendingAgentInSortedOrder(t *testing.T) {
	const (
		ownerA = "agent_0000000a_4061_7104_87a5_3e29ad2fe230"
		ownerB = "agent_0000000b_4061_7104_87a5_3e29ad2fe230"
		ownerC = "agent_0000000c_4061_7104_87a5_3e29ad2fe230"
	)
	a, logged := newLoggingToolRegistryAdapter(t)
	a.registerAgentTools(cardWithDisplayName(ownerC, "Charlie Desk", "Charlie"))
	a.registerAgentTools(cardWithDisplayName(ownerB, "Bravo Desk", "Bravo"))
	a.registerAgentTools(cardWithDisplayName(ownerA, "Alpha Desk", "Alpha"))

	contender := cardWithDisplayName("agent_0000000d_4061_7104_87a5_3e29ad2fe230", "Delta Desk", "Charlie", "Bravo", "Alpha")
	a.registerAgentTools(contender)

	if got := toolNamesFor(a, contender.Name); len(got) != 3 {
		t.Fatalf("contending agent registered %q, want three fallback names", got)
	}

	line := logLineWith(t, logged, logUsedCardName)
	want := fmt.Sprintf(`conflictingAgentNames="[%s %s %s]"`, ownerA, ownerB, ownerC)
	if !strings.Contains(line, want) {
		t.Errorf("record %q does not report the contending agents as %s", line, want)
	}
}

func TestRegisterAgentTools_RefusesANameAnotherAgentOwnsWhenTheFallbackMatchesIt(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)

	a.registerAgentTools(cardWithDisplayName("my-agent", "", "Search"))
	a.registerAgentTools(cardWithDisplayName("my_agent", "", "Search"))

	if got := toolNamesFor(a, "my-agent"); len(got) != 1 || got[0] != "my_agent_search" {
		t.Fatalf("first agent tools = %v, want [my_agent_search]", got)
	}
	if got := toolNamesFor(a, "my_agent"); len(got) != 0 {
		t.Errorf("second agent registered %v, want none", got)
	}

	line := logLineWith(t, logged, logNameUnavailable)
	for _, want := range []string{
		"agentName=my_agent",
		"toolNames=[my_agent_search]",
		"conflictingAgentNames=[my-agent]",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("omission log %q does not carry %q", line, want)
		}
	}

	a.toolMu.RLock()
	defer a.toolMu.RUnlock()
	if m := a.toolMap["my_agent_search"]; m.agentName != "my-agent" {
		t.Errorf("my_agent_search routes to %q, want my-agent", m.agentName)
	}
	if len(a.toolMap) != 1 {
		t.Errorf("toolMap = %+v, want exactly 1 entry", a.toolMap)
	}
}

func TestRegisterAgentTools_ClampsALongNameToTheProviderLimit(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithDisplayName(alphaCardName, "Support Desk",
		"Search The Unified Jira And Confluence Enterprise Knowledge Base Articles")

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	if len(got) != 1 {
		t.Fatalf("expected 1 tool, got %v", got)
	}
	name := got[0]
	if len(name) != maxToolNameLen {
		t.Errorf("tool name is %d chars, want exactly %d: %q", len(name), maxToolNameLen, name)
	}
	if !strings.HasPrefix(name, alphaPrefix+"_") {
		t.Errorf("tool name lost the id-derived prefix: %q", name)
	}
	if !regexp.MustCompile(`_[0-9a-f]{8}$`).MatchString(name) {
		t.Errorf("tool name does not end in an 8-hex-digit digest: %q", name)
	}
}

func TestRegisterAgentTools_LongNamesSharingATruncatedHeadStayDistinct(t *testing.T) {
	a := newToolRegistryAdapter(t)
	const skill = "Search Knowledge Base Articles"
	alpha := cardWithDisplayName("unified-jira-and-confluence-enterprise-knowledge-assistant-for-the-alpha-team", "", skill)
	bravo := cardWithDisplayName("unified-jira-and-confluence-enterprise-knowledge-assistant-for-the-bravo-team", "", skill)

	a.registerAgentTools(alpha)
	a.registerAgentTools(bravo)

	alphaTools := toolNamesFor(a, alpha.Name)
	bravoTools := toolNamesFor(a, bravo.Name)
	if len(alphaTools) != 1 || len(bravoTools) != 1 {
		t.Fatalf("tool names = %v and %v, want exactly one each", alphaTools, bravoTools)
	}

	const head = maxToolNameLen - len("_00000000")
	if len(alphaTools[0]) < head || len(bravoTools[0]) < head {
		t.Fatalf("names are shorter than the %d-byte head the digest leaves (%q, %q), so they were not clamped",
			head, alphaTools[0], bravoTools[0])
	}
	if alphaTools[0][:head] != bravoTools[0][:head] {
		t.Fatalf("names diverge before the digest (%q vs %q), so the digest is not what this test exercises",
			alphaTools[0], bravoTools[0])
	}
	if alphaTools[0] == bravoTools[0] {
		t.Errorf("both agents got the tool name %q", alphaTools[0])
	}
}

func TestRegisterAgentTools_TruncatedHeadDoesNotDoubleTheSeparator(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithDisplayName(
		"Support Desk Knowledge Base Assistant For Alpha Squads",
		"",
		"Search Knowledge Base Articles",
	)

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	if len(got) != 1 {
		t.Fatalf("tool names = %v, want exactly one", got)
	}
	name := got[0]
	if !regexp.MustCompile(`_[0-9a-f]{8}$`).MatchString(name) {
		t.Fatalf("tool name was not clamped, so it cannot exercise the head trim: %q", name)
	}
	if len(name) > maxToolNameLen {
		t.Errorf("tool name is %d chars, over the %d providers allow: %q", len(name), maxToolNameLen, name)
	}
	if strings.Contains(name, "__") {
		t.Errorf("truncation doubled the separator before the digest: %q", name)
	}
}

func TestRegisterAgentTools_ClampsAtTheProviderLimitBoundary(t *testing.T) {
	cardName := strings.Repeat("a", 32)
	tests := []struct {
		name        string
		composedLen int
		wantClamped bool
	}{
		{name: "exactly 64 bytes survives intact", composedLen: 64},
		{name: "65 bytes is truncated", composedLen: 65, wantClamped: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, logged := newLoggingToolRegistryAdapter(t)
			skillName := strings.Repeat("b", tc.composedLen-len(cardName)-len("_"))
			card := cardWithDisplayName(cardName, "Support Desk", skillName)

			a.registerAgentTools(card)

			got := toolNamesFor(a, card.Name)
			if len(got) != 1 {
				t.Fatalf("tool names = %v, want exactly one", got)
			}
			name := got[0]
			if !tc.wantClamped {
				if want := cardName + "_" + skillName; name != want {
					t.Errorf("tool name = %q, want the %d-char composed name %q unchanged",
						name, tc.composedLen, want)
				}
				if strings.Contains(logged.String(), logClampedToLimit) {
					t.Errorf("a %d-char name was reported as truncated:\n%s", tc.composedLen, logged.String())
				}
				return
			}
			if len(name) != maxToolNameLen {
				t.Errorf("tool name is %d chars, want exactly %d: %q", len(name), maxToolNameLen, name)
			}
			if !regexp.MustCompile(`_[0-9a-f]{8}$`).MatchString(name) {
				t.Errorf("tool name does not end in an 8-hex-digit digest: %q", name)
			}
		})
	}
}

func TestRegisterAgentTools_ReportsTheTruncatedNameFilterPatternsMustMatch(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	card := cardWithDisplayName(alphaCardName, "Support Desk",
		"Search The Unified Jira And Confluence Enterprise Knowledge Base Articles")

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	if len(got) != 1 {
		t.Fatalf("tool names = %v, want exactly one", got)
	}
	line := logLineWith(t, logged, logClampedToLimit)
	if !strings.Contains(line, "toolNames=["+got[0]+"]") {
		t.Errorf("record does not carry the truncated name %q: %q", got[0], line)
	}
	if !strings.Contains(line, `skillNames="[Search The Unified Jira And Confluence Enterprise Knowledge Base Articles]"`) {
		t.Errorf("record does not name the truncated tool's skill: %q", line)
	}
	if !strings.Contains(line, "level=WARN") {
		t.Errorf("truncation is a clamp notice and must report at WARN: %q", line)
	}
}

func TestRegisterAgentTools_TruncatedRecordIdentifiesASkillCarryingNoNameByID(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	cardName := strings.Repeat("a", 40)
	skillID := strings.Repeat("b", 40)
	card := cardWithSkills(cardName, "Support Desk", a2a.AgentSkill{ID: skillID})

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	if len(got) != 1 {
		t.Fatalf("tool names = %v, want exactly one", got)
	}
	if len(got[0]) != maxToolNameLen {
		t.Fatalf("tool name is %d chars, not the clamped %d, so nothing truncated: %q",
			len(got[0]), maxToolNameLen, got[0])
	}

	line := logLineWith(t, logged, logClampedToLimit)
	if count := logFieldValue(t, line, "skillCount"); count != "1" {
		t.Errorf("truncated record says %q skills were renamed, want 1: %q", count, line)
	}
	for _, want := range []string{
		"toolNames=[" + got[0] + "]",
		"skillIDs=[" + skillID + "]",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("truncated record %q does not carry %q", line, want)
		}
	}
	assertIdentifiesNothing(t, line, "skillNames")
}

func TestRegisterAgentTools_UsesTheSkillIDWhenTheSkillNameIsNotASCII(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithDisplayName(alphaCardName, "Support Desk", "検索")

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	if len(got) != 1 {
		t.Fatalf("tool names = %v, want exactly one", got)
	}
	if !providerSafeToolName.MatchString(got[0]) {
		t.Errorf("tool name is not a provider-safe token: %q", got[0])
	}
	want := alphaPrefix + "_" + card.Skills[0].ID
	if got[0] != want {
		t.Errorf("tool name = %q, want %q", got[0], want)
	}

	a.toolMu.RLock()
	defer a.toolMu.RUnlock()
	if m := a.toolMap[want]; m.skillID != card.Skills[0].ID {
		t.Errorf("%q routes to skill %q, want %q", want, m.skillID, card.Skills[0].ID)
	}
}

func TestRegisterAgentTools_UsesASkillNameNameableOnlyAfterCaseFolding(t *testing.T) {
	kelvinSign := string(rune(0x212A))
	for _, r := range kelvinSign {
		if r < utf8.RuneSelf {
			t.Fatalf("fixture rune %q (%U) is ASCII, so no case fold is exercised", r, r)
		}
	}

	a := newToolRegistryAdapter(t)
	card := cardWithDisplayName(alphaCardName, "Support Desk", kelvinSign)

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	if len(got) != 1 || got[0] != alphaPrefix+"_k" {
		t.Errorf("tool names = %v, want [%s_k] from the folded skill name rather than the skill id", got, alphaPrefix)
	}
}

func TestRegisterAgentTools_KeepsNonASCIISkillsOnOneCardDistinct(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	card := cardWithDisplayName(alphaCardName, "Support", "検索", "要約", "翻訳")

	a.registerAgentTools(card)

	want := []string{alphaPrefix + "_s0", alphaPrefix + "_s1", alphaPrefix + "_s2"}
	if got := toolNamesFor(a, card.Name); !slices.Equal(got, want) {
		t.Fatalf("agent tools = %v, want %v", got, want)
	}
	if got := sortedToolCatalog(t, a); !slices.Equal(got, want) {
		t.Errorf("tools/list catalog = %v, want %v", got, want)
	}

	a.toolMu.RLock()
	if len(a.toolMap) != len(want) {
		t.Errorf("toolMap = %+v, want %d entries", a.toolMap, len(want))
	}
	for i, name := range want {
		if m := a.toolMap[name]; m.skillID != card.Skills[i].ID {
			t.Errorf("%q routes to skill %q, want %q", name, m.skillID, card.Skills[i].ID)
		}
	}
	a.toolMu.RUnlock()

	if strings.Contains(logged.String(), logIntraCardCollision) {
		t.Errorf("skills collided within the card: %s", logged.String())
	}
}

func TestRegisterAgentTools_OmitsASkillNoPrefixCanQualify(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	card := cardWithDisplayName("エージェント一号", "Support Desk", "Search")

	a.registerAgentTools(card)

	if got := toolNamesFor(a, card.Name); len(got) != 0 {
		t.Errorf("agent tools = %v, want none", got)
	}
	if got := listToolCatalog(t, a); len(got) != 0 {
		t.Errorf("tools/list catalog = %v, want empty", got)
	}

	a.toolMu.RLock()
	if len(a.toolMap) != 0 {
		t.Errorf("toolMap = %+v, want no entries", a.toolMap)
	}
	a.toolMu.RUnlock()

	line := logLineWith(t, logged, logUnnameable)
	for _, want := range []string{"agentName=" + card.Name, "skillNames=[Search]"} {
		if !strings.Contains(line, want) {
			t.Errorf("omission log %q does not carry %q", line, want)
		}
	}
}

func TestRegisterAgentTools_OmitsSkillsWhoseOwnSegmentCannotQualifyThem(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	card := cardWithSkills(alphaCardName, "Support Desk",
		a2a.AgentSkill{ID: "スキル一", Name: "検索"},
		a2a.AgentSkill{ID: "スキル二", Name: "要約"},
	)

	a.registerAgentTools(card)

	if got := toolNamesFor(a, card.Name); len(got) != 0 {
		t.Errorf("agent tools = %v, want none", got)
	}
	if got := listToolCatalog(t, a); len(got) != 0 {
		t.Errorf("tools/list catalog = %v, want empty", got)
	}

	a.toolMu.RLock()
	if len(a.toolMap) != 0 {
		t.Errorf("toolMap = %+v, want no entries", a.toolMap)
	}
	a.toolMu.RUnlock()

	line := logLineWith(t, logged, logUnnameable)
	for _, want := range []string{
		"agentName=" + alphaCardName,
		`skillNames="[検索 要約]"`,
		`skillIDs="[スキル一 スキル二]"`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("omission log %q does not carry %q", line, want)
		}
	}
}

func TestRegisterAgentTools_UnnameableRecordIdentifiesASkillCarryingNoName(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	card := cardWithSkills(alphaCardName, "Support Desk", a2a.AgentSkill{ID: "スキル一"})

	a.registerAgentTools(card)

	if got := toolNamesFor(a, card.Name); len(got) != 0 {
		t.Fatalf("agent tools = %v, want none", got)
	}

	line := logLineWith(t, logged, logUnnameable)
	if !strings.Contains(line, "skillNames=[]") {
		t.Errorf("record was expected to render the empty name as nothing: %q", line)
	}
	if !strings.Contains(line, "skillIDs=[スキル一]") {
		t.Errorf("record does not identify the omitted skill by id: %q", line)
	}
}

func TestRegisterAgentTools_UnnameableRecordCountsSkillsThatIdentifyNothing(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	card := cardWithSkills(alphaCardName, "Support Desk",
		a2a.AgentSkill{Description: "searches the knowledge base"},
		a2a.AgentSkill{Description: "summarizes a thread"},
	)

	a.registerAgentTools(card)

	if got := toolNamesFor(a, card.Name); len(got) != 0 {
		t.Fatalf("agent tools = %q, want none", got)
	}

	line := logLineWith(t, logged, logUnnameable)
	if got := logFieldValue(t, line, "skillCount"); got != "2" {
		t.Errorf("record says %q skills were dropped, want 2: %q", got, line)
	}
	assertIdentifiesNothing(t, line, "skillNames")
	assertIdentifiesNothing(t, line, "skillIDs")
}

func TestRegisterAgentTools_NeverRegistersThePlaceholderAsAToolName(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	card := cardWithSkills("エージェント", "日本語", a2a.AgentSkill{ID: "スキル", Name: "検索"})

	a.registerAgentTools(card)

	if got := toolNamesFor(a, card.Name); len(got) != 0 {
		t.Errorf("agent tools = %v, want none", got)
	}
	if got := listToolCatalog(t, a); len(got) != 0 {
		t.Errorf("tools/list catalog = %v, want empty", got)
	}

	a.toolMu.RLock()
	if _, taken := a.toolMap[unnamedToolPlaceholder]; taken {
		t.Errorf("the placeholder was registered as a tool: %+v", a.toolMap)
	}
	if len(a.toolMap) != 0 {
		t.Errorf("toolMap = %+v, want no entries", a.toolMap)
	}
	a.toolMu.RUnlock()

	line := logLineWith(t, logged, logUnnameable)
	if !strings.Contains(line, "skillNames=[検索]") {
		t.Errorf("omission log %q does not name the omitted skill", line)
	}
}

func TestRegisterAgentTools_UsesACardNameThatSanitizesToThePlaceholder(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithDisplayName("Unnamed Tool", "Support Desk", "Greet")

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	if len(got) != 1 || got[0] != unnamedToolPlaceholder+"_greet" {
		t.Errorf("tool names = %v, want [%s_greet]", got, unnamedToolPlaceholder)
	}
}

func TestRegisterAgentTools_LogsNothingForACleanRegistration(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support Desk", "Search"))

	if got := toolNamesFor(a, alphaCardName); len(got) != 1 {
		t.Fatalf("agent tools = %v, want exactly one", got)
	}
	if logged.Len() != 0 {
		t.Errorf("a clean registration logged:\n%s", logged.String())
	}
}

func TestRegisterAgentTools_ToolCallDispatchesToTheAgentTheNameWasRegisteredFor(t *testing.T) {
	a, brk := newDispatchingToolRegistryAdapter(t)
	card := cardWithDisplayName(alphaCardName, "Support Desk", "Search")
	a.registerAgentTools(card)

	want := alphaPrefix + "_search"
	if got := toolNamesFor(a, card.Name); len(got) != 1 || got[0] != want {
		t.Fatalf("agent tools = %v, want [%s]", got, want)
	}

	q, err := brk.CreateQueue("mcp-dispatch-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic(dispatchTestNamespace, card.Name)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	responses := make(chan mcplib.JSONRPCMessage, 1)
	go func() {
		responses <- a.mcpServer.HandleMessage(context.Background(), []byte(`{
			"jsonrpc": "2.0",
			"id": 1,
			"method": "tools/call",
			"params": {"name": "`+want+`", "arguments": {"message": "find the runbook"}}
		}`))
	}()

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if received, _ := brk.Receive(recvCtx, q); received == nil {
		t.Fatalf("tools/call %q never dispatched to %q; the handler returned %s",
			want, card.Name, drainResponse(responses))
	}

	taskID, dispatchedAs := trackedToolCall(t, a)
	if dispatchedAs != want {
		t.Errorf("tools/call dispatched as %q, want %q", dispatchedAs, want)
	}

	a.OnFinalResponse(context.Background(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{
			State:   "completed",
			Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("found it")}},
		},
	}, nil)

	select {
	case resp := <-responses:
		jsonResp, ok := resp.(mcplib.JSONRPCResponse)
		if !ok {
			t.Fatalf("tools/call response is %T = %+v, want JSONRPCResponse", resp, resp)
		}
		result, ok := jsonResp.Result.(*mcplib.CallToolResult)
		if !ok {
			t.Fatalf("tools/call result is %T = %+v, want CallToolResult", jsonResp.Result, jsonResp.Result)
		}
		if result.IsError {
			t.Fatalf("tools/call returned an error result: %+v", result.Content)
		}
		if len(result.Content) == 0 {
			t.Fatalf("tools/call returned no content, want the agent's text")
		}
		if text, ok := result.Content[0].(mcplib.TextContent); !ok || text.Text != "found it" {
			t.Errorf("tools/call result = %+v, want the agent's text", result.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tools/call never returned after the final response")
	}
}

const dispatchTestNamespace = "test-ns"

func newDispatchingToolRegistryAdapter(t *testing.T, opts ...runtime.Option) (*Adapter, *broker.DevBroker) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(stubGatewayAdapter{}, map[string]any{
		"namespace":  dispatchTestNamespace,
		"gateway_id": "test-mcp-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil, opts...).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("core start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	a := newToolRegistryAdapter(t)
	a.core = core
	a.cfg = mustParseConfig(t, map[string]any{"task_timeout_seconds": float64(10)})
	return a, brk
}

func trackedToolCall(t *testing.T, a *Adapter) (taskID, toolName string) {
	t.Helper()
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	if len(a.tasks) != 1 {
		t.Fatalf("in-flight tool calls = %d, want exactly 1", len(a.tasks))
	}
	for id, ts := range a.tasks {
		return id, ts.toolName
	}
	return "", ""
}

func drainResponse(responses <-chan mcplib.JSONRPCMessage) string {
	select {
	case resp := <-responses:
		return fmt.Sprintf("%+v", resp)
	case <-time.After(time.Second):
		return "nothing"
	}
}

func TestRegisterAgentTools_ReplacesANonASCIICharacterInsideASkillName(t *testing.T) {
	a := newToolRegistryAdapter(t)
	card := cardWithDisplayName(alphaCardName, "Support Desk", "Café Lookup")

	a.registerAgentTools(card)

	got := toolNamesFor(a, card.Name)
	want := alphaPrefix + "_caf_lookup"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("tool names = %v, want [%s]", got, want)
	}
	if !providerSafeToolName.MatchString(got[0]) {
		t.Errorf("tool name is not a provider-safe token: %q", got[0])
	}
}

func TestRegisterAgentTools_KeepsOnlyTheFirstOfTwoSkillsThatComposeToTheSameName(t *testing.T) {
	tests := []struct {
		name        string
		skills      []a2a.AgentSkill
		wantTool    string
		wantDropped string
	}{
		{
			name: "sanitizing collapses two skill names",
			skills: []a2a.AgentSkill{
				{ID: "s0", Name: "Search Docs"},
				{ID: "s1", Name: "Search-Docs"},
			},
			wantTool:    alphaPrefix + "_search_docs",
			wantDropped: "Search-Docs",
		},
		{
			name: "one skill's id equals another skill's name",
			skills: []a2a.AgentSkill{
				{ID: "search", Name: "検索"},
				{ID: "s1", Name: "Search"},
			},
			wantTool:    alphaPrefix + "_search",
			wantDropped: "Search",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, logged := newLoggingToolRegistryAdapter(t)
			card := cardWithSkills(alphaCardName, "Support", tc.skills...)

			a.registerAgentTools(card)

			if got := toolNamesFor(a, card.Name); len(got) != 1 || got[0] != tc.wantTool {
				t.Fatalf("agent tools = %v, want [%s]", got, tc.wantTool)
			}
			if got := listToolCatalog(t, a); len(got) != 1 || got[0] != tc.wantTool {
				t.Errorf("tools/list catalog = %v, want [%s]", got, tc.wantTool)
			}

			a.toolMu.RLock()
			if len(a.toolMap) != 1 {
				t.Errorf("toolMap = %+v, want exactly 1 entry", a.toolMap)
			}
			if m := a.toolMap[tc.wantTool]; m.skillID != card.Skills[0].ID {
				t.Errorf("%q routes to skill %q, want the first skill %q", tc.wantTool, m.skillID, card.Skills[0].ID)
			}
			a.toolMu.RUnlock()

			line := logLineWith(t, logged, logIntraCardCollision)
			if !strings.Contains(line, "toolNames=["+tc.wantTool+"]") {
				t.Errorf("collision record does not name the shadowed tool: %q", line)
			}
			if !strings.Contains(line, "skillNames=["+tc.wantDropped+"]") {
				t.Errorf("collision record does not name the skill that was dropped: %q", line)
			}
		})
	}
}

func TestRegisterAgentTools_CollisionRecordIdentifiesASkillCarryingNoNameByID(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	card := cardWithSkills(alphaCardName, "Support",
		a2a.AgentSkill{ID: "skill_one"},
		a2a.AgentSkill{ID: "skill-one"},
	)

	a.registerAgentTools(card)

	registeredTool := alphaPrefix + "_skill_one"
	if got := toolNamesFor(a, card.Name); len(got) != 1 || got[0] != registeredTool {
		t.Fatalf("agent tools = %q, want [%s]", got, registeredTool)
	}

	line := logLineWith(t, logged, logIntraCardCollision)
	for _, want := range []string{
		"toolNames=[" + registeredTool + "]",
		"skillNames=[]",
		"skillIDs=[skill-one]",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("collision record %q does not carry %q", line, want)
		}
	}
	if got := logFieldValue(t, line, "skillCount"); got != "1" {
		t.Errorf("collision record says %q skills collided, want 1: %q", got, line)
	}
}

func TestRegisterAgentTools_DoesNotReportAToolTheFilterExcluded(t *testing.T) {
	a, logged := newLoggingToolRegistryAdapter(t)
	a.cfg = mustParseConfig(t, map[string]any{
		"exclude_tools": []any{twinCardName + "_search"},
	})

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support", "Search"))
	a.registerAgentTools(cardWithDisplayName(twinCardName, "Helpdesk", "Search"))

	if got := toolNamesFor(a, twinCardName); len(got) != 0 {
		t.Fatalf("excluded agent registered %v, want none", got)
	}
	for _, msg := range []string{logUsedCardName, logNameUnavailable, logUnnameable, logIntraCardCollision} {
		if strings.Contains(logged.String(), msg) {
			t.Errorf("excluded tool reported as a conflict (%q): %s", msg, logged.String())
		}
	}
}

func TestRegisterAgentTools_RecordsAtDebugWhichSkillsTheFilterExcluded(t *testing.T) {
	a, _ := newDispatchingToolRegistryAdapter(t)
	logged := &bytes.Buffer{}
	a.logger = slog.New(slog.NewTextHandler(logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a.cfg = mustParseConfig(t, map[string]any{
		"exclude_tools": []any{alphaPrefix + "_summarize"},
	})

	a.registerAgentTools(cardWithDisplayName(alphaCardName, "Support Desk", "Search", "Summarize"))

	registered := alphaPrefix + "_search"
	if got := toolNamesFor(a, alphaCardName); len(got) != 1 || got[0] != registered {
		t.Fatalf("agent tools = %v, want [%s]", got, registered)
	}

	line := logLineWith(t, logged, logFilteredOut)
	for _, want := range []string{
		"level=DEBUG",
		"gatewayID=test-mcp-gw",
		"agentName=" + alphaCardName,
		"toolNames=[" + alphaPrefix + "_summarize]",
		"skillNames=[Summarize]",
		"skillIDs=[s1]",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("filter record %q does not carry %q", line, want)
		}
	}
	if strings.Contains(line, registered) {
		t.Errorf("filter record names a skill the filter admitted: %q", line)
	}
}

func TestRegisterAgentTools_DoesNotReportAnUnnameableSkillTheFilterExcluded(t *testing.T) {
	for _, excluded := range []string{"エージェント一号", "Search"} {
		t.Run(excluded, func(t *testing.T) {
			a, logged := newLoggingToolRegistryAdapter(t)
			a.cfg = mustParseConfig(t, map[string]any{"exclude_tools": []any{excluded}})

			a.registerAgentTools(cardWithDisplayName("エージェント一号", "Support Desk", "Search"))

			if got := toolNamesFor(a, "エージェント一号"); len(got) != 0 {
				t.Fatalf("excluded agent registered %v, want none", got)
			}
			if logged.Len() != 0 {
				t.Errorf("an excluded agent's unnameable skill was reported:\n%s", logged.String())
			}
		})
	}
}

func TestRegisterAgentTools_APermissiveFilterCannotAdmitAnEmptyToolName(t *testing.T) {
	a := newToolRegistryAdapter(t)
	a.cfg = mustParseConfig(t, map[string]any{"include_tools": []any{".*"}})

	a.registerAgentTools(cardWithDisplayName("エージェント一号", "Support Desk", "Search"))

	if got := toolNamesFor(a, "エージェント一号"); len(got) != 0 {
		t.Errorf("agent tools = %q, want none", got)
	}
	if got := listToolCatalog(t, a); len(got) != 0 {
		t.Errorf("tools/list catalog = %q, want empty", got)
	}

	a.toolMu.RLock()
	defer a.toolMu.RUnlock()
	if _, taken := a.toolMap[""]; taken {
		t.Errorf("the empty name was claimed in toolMap: %+v", a.toolMap)
	}
	if len(a.toolMap) != 0 {
		t.Errorf("toolMap = %+v, want no entries", a.toolMap)
	}
}

func TestRegisterAgentTools_RecordsWhetherTheBackingCardIsAWorkflow(t *testing.T) {
	tests := []struct {
		name           string
		agentType      string
		wantIsWorkflow bool
	}{
		{name: "workflow card", agentType: "workflow", wantIsWorkflow: true},
		{name: "standard card", agentType: "standard"},
		{name: "card with no agent-type extension"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newToolRegistryAdapter(t)
			card := cardWithDisplayName(alphaCardName, "Support Desk", "Search")
			if tc.agentType != "" {
				card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.AgentExtension{
					URI:    a2a.ExtensionURIAgentType,
					Params: map[string]any{"type": tc.agentType},
				})
			}

			a.registerAgentTools(card)

			got := toolNamesFor(a, card.Name)
			if len(got) != 1 {
				t.Fatalf("tool names = %v, want exactly one", got)
			}
			a.toolMu.RLock()
			defer a.toolMu.RUnlock()
			if m := a.toolMap[got[0]]; m.isWorkflow != tc.wantIsWorkflow {
				t.Errorf("%q mapping isWorkflow = %v, want %v", got[0], m.isWorkflow, tc.wantIsWorkflow)
			}
		})
	}
}
