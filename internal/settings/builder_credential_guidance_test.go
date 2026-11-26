// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

var dialogClaims = []string{
	"secure dialog",
	"completion dialog",
	"secure input dialog",
	"collects them securely",
	"handles secure input",
	"enter it later in the ui",
	"on its card here",
	"re-prompts",
}

func TestBuilderCorpus_PromisesNoCredentialDialog(t *testing.T) {
	files := credentialCorpusFiles(t)

	for _, claim := range dialogClaims {
		if normalised := strings.Join(strings.Fields(strings.ToLower(claim)), " "); claim != normalised {
			t.Fatalf("dialogClaims entry %q never matches: the corpus is scanned lowercased and single-spaced, so it must be written that way (%q)", claim, normalised)
		}
	}

	for path, body := range files {
		flowing := flattenForClaimScan(body)
		for _, claim := range dialogClaims {
			for _, at := range flowing.occurrences(claim) {
				t.Errorf("%s:%s promises a credential dialog that does not exist:\n\t%s\n"+
					"ConnectorEditDialog is unreachable (promptForConnectorEdit is never called). A placeholder blocks build and test; the value arrives by the user telling the builder, or by editing the connector on the Connectors page once it is saved.",
					path, at.where, at.quoted)
			}
		}
	}
}

type flowingText struct {
	text   string
	lineOf []int
	lines  []string
}

func flattenForClaimScan(body string) flowingText {
	lines := strings.Split(body, "\n")
	var b strings.Builder
	lineOf := make([]int, 0, len(body))
	for i, line := range lines {
		for _, word := range strings.Fields(strings.ToLower(line)) {
			if b.Len() > 0 {
				b.WriteByte(' ')
				lineOf = append(lineOf, i+1)
			}
			b.WriteString(word)
			for range len(word) {
				lineOf = append(lineOf, i+1)
			}
		}
	}
	return flowingText{text: b.String(), lineOf: lineOf, lines: lines}
}

type claimSite struct {
	where  string
	quoted string
}

func (f flowingText) occurrences(claim string) []claimSite {
	var out []claimSite
	for at := 0; ; {
		i := strings.Index(f.text[at:], claim)
		if i < 0 {
			return out
		}
		start := at + i
		first, last := f.lineOf[start], f.lineOf[start+len(claim)-1]
		where := fmt.Sprintf("%d", first)
		if last != first {
			where = fmt.Sprintf("%d-%d", first, last)
		}
		out = append(out, claimSite{
			where:  where,
			quoted: strings.Join(strings.Fields(strings.Join(f.lines[first-1:last], " ")), " "),
		})
		at = start + len(claim)
	}
}

func TestBuilderPrompt_KeepsTheCredentialRemedy(t *testing.T) {
	prompt := strings.Join(strings.Fields(string(builderAgentYAML)), " ")

	if n := strings.Count(prompt, "already been saved"); n < 2 {
		t.Errorf("the Connectors-page route is qualified in %d places, want both the step-level block and the authoritative section — the unqualified claim is false on the flow that blocks before saving", n)
	}

	if n := strings.Count(prompt, "changes only this session's copy"); n < 3 {
		t.Errorf("the session-copy caveat appears in %d places, want the connector, entrypoint and remote agent guidance: the credential kinds are connectors, entrypoints and remote agents", n)
	}

	for _, required := range []struct{ what, text string }{
		{"the correction that the build is refused, not prompted", "the UI refuses the build instead"},
		{"the always-available route", "in the chat"},
		{"the qualification that makes the Connectors page route true", "already been saved"},
		{"the ban on promising a prompt", "Never tell the user they will be prompted"},
		{"the statement that a rewritten artifact is only the session's copy", "changes only this session's copy"},
		{"the ban on announcing the credentials as in place or the build as ready", "Do not say the credentials are in place"},
		{"the ban on announcing the build as ready or deployed", "or that the build is ready or deployed"},
		{"the statement that the agent goes live only after the key is delivered", "agent goes live only then"},
		{"the entrypoint's ban on announcing it ready before Try again", "if the entrypoint has already been saved, do not say it is ready until the user has selected **Try again** (or **Build & Activate**)"},
		{"the remote agent's statement that it goes live only after Try again", "if the remote agent has already been saved, the value reaches the platform when the user selects **Try again** (or **Build & Activate**)"},
		{"the ban on repeating a key", "never repeat the key"},
		{"the step that carries a chat key to a saved connector", "selects **Try again** (or **Build & Activate**)"},
	} {
		if !strings.Contains(prompt, required.text) {
			t.Errorf("builder_agent.yaml no longer carries %s (looked for %q).\n"+
				"Without it the agent goes back to telling users a dialog will collect the credential at Build & Activate, which leaves them at a dead end.",
				required.what, required.text)
		}
	}
}

func TestConnectorSkills_DescribeTheBlockNotADialog(t *testing.T) {
	want := map[string]string{
		"sam-connector-design": "blocks build and test",
		"sam-connector-schema": "blocks build and test",
	}
	files := credentialCorpusFiles(t)
	for skill, phrase := range want {
		found := false
		for path, body := range files {
			if strings.Contains(path, skill) && strings.Contains(body, phrase) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s no longer says a placeholder %q — it previously said a dialog collects the value, which is false", skill, phrase)
		}
	}
}

func credentialCorpusFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := fs.WalkDir(skillsFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		data, readErr := fs.ReadFile(skillsFS, p)
		if readErr != nil {
			return readErr
		}
		out[p] = string(data)
		return nil
	}); err != nil {
		t.Fatalf("walk skills: %v", err)
	}
	out["builder_agent.yaml"] = string(builderAgentYAML)
	return out
}
