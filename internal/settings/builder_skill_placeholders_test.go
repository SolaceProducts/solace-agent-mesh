// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

var brokerFieldEnvSubstitution = regexp.MustCompile(`(?i)\b[a-z_]*(broker_?url|broker_?vpn|broker_?username|broker_?password)[a-z_]*\s*:\s*"?\$\{`)

var placeholderTeachingLines = []string{
	"never write `${VAR}`",
	"the literal string `${BROKER_URL}` reaches validation",
	"NEVER use `${ENV_VAR}`",
	"`${BROKER_URL}` reaches validation and is rejected",
}

func TestBuilderCorpus_TeachesNoEnvSubstitutionOnBrokerFields(t *testing.T) {
	files := builderCorpusFiles(t)
	if len(files) < 5 {
		t.Fatalf("read %d files from the builder corpus, want the skills tree and the prompt — an empty domain asserts nothing", len(files))
	}

	sawPrompt := false
	for path, body := range files {
		if strings.HasSuffix(path, "builder_agent.yaml") {
			sawPrompt = true
		}
		for i, line := range strings.Split(body, "\n") {
			if !brokerFieldEnvSubstitution.MatchString(line) || teachesTheRule(line) {
				continue
			}
			t.Errorf("%s:%d sets a broker field from an env substitution:\n\t%s\n"+
				"Builder artifacts are stored in the platform database and read back verbatim — the literal ${...} reaches the validator and the adapter. Use <<__SAM_REQUIRED__>> for a value the admin must supply.",
				path, i+1, strings.TrimSpace(line))
		}
	}
	if !sawPrompt {
		t.Error("builder_agent.yaml was not scanned; the prompt is the half read on every build")
	}
}

func TestEventMeshSkill_ExamplesStayComplete(t *testing.T) {
	files := builderCorpusFiles(t)
	var body string
	for path, b := range files {
		if strings.Contains(path, "sam-event-mesh-schema") {
			body = b
		}
	}
	if body == "" {
		t.Fatal("sam-event-mesh-schema/SKILL.md not found in the corpus")
	}

	blocks := brokerBlocks(body)
	if len(blocks) < 4 {
		t.Errorf("found %d event_mesh_broker_config blocks, want the reference plus three complete examples", len(blocks))
	}
	for i, block := range blocks {
		for _, field := range []string{"broker_url", "broker_vpn", "broker_username", "broker_password"} {
			if !strings.Contains(block, field+":") {
				t.Errorf("event_mesh_broker_config block %d does not set %s.\n"+
					"A partial bag is rejected wherever a system broker is configured: broker connection requires fields: broker_url, broker_vpn, broker_username.",
					i+1, field)
			}
		}
	}
}

func teachesTheRule(line string) bool {
	for _, allowed := range placeholderTeachingLines {
		if strings.Contains(line, allowed) {
			return true
		}
	}
	return false
}

func builderCorpusFiles(t *testing.T) map[string]string {
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

func TestBuilderPrompt_KeepsTheEnvSubstitutionProhibition(t *testing.T) {
	prompt := string(builderAgentYAML)

	for _, required := range []struct{ what, text string }{
		{"the global constraint forbidding ${VAR} in artifacts", "NEVER use `${ENV_VAR}`"},
		{"the reason it cannot work", "stored in the platform database"},
		{"the broker-specific rule", "Never write `${VAR}` in a broker field"},
		{"the remedy", "<<__SAM_REQUIRED__>>"},
	} {
		if !strings.Contains(prompt, required.text) {
			t.Errorf("builder_agent.yaml no longer carries %s (looked for %q).\n"+
				"The prompt is read on every build; a skill is read only when loaded. Losing this is how the agent goes back to writing ${BROKER_URL}.",
				required.what, required.text)
		}
	}

	if strings.Contains(prompt, "Use `${ENV_VAR, default}` syntax for sensitive values") {
		t.Error("builder_agent.yaml again tells the agent to use ${ENV_VAR} for sensitive values, under a heading that applies to every artifact kind — which is what overrode the local rule.")
	}
}

func brokerBlocks(body string) []string {
	var out []string
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "event_mesh_broker_config:" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		var block []string
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			if len(next)-len(strings.TrimLeft(next, " ")) <= indent {
				break
			}
			block = append(block, next)
		}
		out = append(out, strings.Join(block, "\n"))
	}
	return out
}
