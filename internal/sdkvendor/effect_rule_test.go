// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sdkvendor

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const effectRule = "A tool is read_only only if all three hold: it cannot change anything outside the run, whatever its input; it fetches no URL chosen by its input or by the content of a file it reads; it writes, deletes or replaces no artifact under a filename chosen by its caller or by the tool itself. " +
	"The run is the one task that called the tool, and changing something outside it means writing, sending, deleting, posting, publishing or indexing in an external system, another session or user scope, or starting a job. " +
	"Changing the run's own session, such as the skills it has loaded, is not outside it. " +
	"A filename is chosen by the caller when it comes from a parameter, and by the tool when its code fixes or derives it, including a name set on a data object it returns. " +
	"A result the runtime itself saves under a fresh filename it picks, which it does only when a tool returns data with no name set (auto-disposition), is not a write. " +
	"Sending the caller's query to one fixed external service, as a search or lookup does, is not fetching a chosen URL. " +
	"Every other tool is side_effecting."

var effectRuleCopies = []string{
	"../../pkg/samtoolsdk/tool.go",
	"embed/gosdk/tool.gotxt",
	"../../pkg/samtoolsdk/README.md",
	"embed/gosdk/README.md",
	"../../python/sam-tool-sdk/README.md",
	"../../python/sam-tool-sdk/sam_tool_sdk/tool_options.py",
	"../../pkg/schema/schemas/tool.schema.yaml",
	"../aiassist/skills/sam-tools-and-skills/references/go-tools.md",
	"../aiassist/skills/sam-tools-and-skills/references/python-tools.md",
	"../aiassist/skills/sam-author-agent/references/mcp-on-agent.md",
	"../settings/files/skills/sam-agent-schema/SKILL.md",
	"../../python/sam-tool-sdk/sam_tool_sdk/dynamic_tool.py",
	"../../python/sam-tool-sdk/CHANGELOG.md",
}

var commentMarker = regexp.MustCompile(`(?m)^\s*//\s?`)

func TestTheEffectRuleReadsTheSameInEveryCopy(t *testing.T) {
	t.Parallel()
	for _, path := range effectRuleCopies {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		flat := strings.Join(strings.Fields(commentMarker.ReplaceAllString(string(raw), "")), " ")
		if !strings.Contains(flat, effectRule) {
			t.Errorf("%s does not contain the effect rule word for word", path)
		}
	}
}

func TestNoCopyOfTheEffectRuleMentionsAPracticeRun(t *testing.T) {
	t.Parallel()
	for _, path := range effectRuleCopies {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if strings.Contains(strings.ToLower(string(raw)), "practice run") {
			t.Errorf("%s promises a practice run that nothing implements", path)
		}
	}
}

func TestNoCopyOfTheEffectRuleKeepsTheOldLooserFraming(t *testing.T) {
	t.Parallel()
	for _, path := range effectRuleCopies {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		flat := strings.Join(strings.Fields(commentMarker.ReplaceAllString(string(raw), "")), " ")
		for _, stale := range []string{"only reads or may change", "A tool that only creates a new artifact", "it changes nothing outside the run;"} {
			if strings.Contains(flat, stale) {
				t.Errorf("%s still says %q", path, stale)
			}
		}
	}
}
