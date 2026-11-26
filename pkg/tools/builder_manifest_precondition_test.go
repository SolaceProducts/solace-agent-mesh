// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const validAgentConfigYAML = `apps:
  - name: TestAgent
    app_config:
      namespace: test
      agent_name: TestAgent
      model: general
      instruction: do the thing
`

const minimalManifestYAML = `name: test-build
components:
  - filename: test_agent.yaml
    type: agent
    name: TestAgent
    status: draft
    origin: session
`

func validateConfigIn(t *testing.T, toolCtx *tool.Context, config, mime string) *tool.Result {
	t.Helper()
	args := map[string]any{"config_yaml": config}
	if mime != "" {
		args["mime_type"] = mime
	}
	res, err := (&ValidateComponentConfigTool{}).Execute(context.Background(), args, toolCtx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

func TestValidateComponentConfig_RefusesWithoutABuildManifest(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	res := validateConfigIn(t, toolCtx, validAgentConfigYAML, SAMAgentConfigMIMEType)

	if res.Status != tool.StatusError {
		t.Fatalf("status = %q, want error — a config with no plan to belong to must not validate", res.Status)
	}
	if !strings.Contains(res.Message, manifestFilename) {
		t.Errorf("message does not name the artifact to create:\n\t%s", res.Message)
	}
	if !strings.Contains(res.Message, "lead Builder") || !strings.Contains(res.Message, "sub-task") {
		t.Errorf("message does not tell both the lead and a sub-task what to do:\n\t%s", res.Message)
	}
}

func TestValidateComponentConfig_RefusesEveryComponentKind(t *testing.T) {
	for _, tc := range []struct{ name, config, mime string }{
		{"agent", validAgentConfigYAML, SAMAgentConfigMIMEType},
		{"connector", `{"name":"c","connectorType":"http"}`, SAMConnectorConfigMIMEType},
		{"skill", "name: s\ndescription: d\n", SAMSkillConfigMIMEType},
		{"a2a_agent", `{"name":"a","endpointUrl":"https://x"}`, SAMA2AAgentConfigMIMEType},
		{"no mime supplied", validAgentConfigYAML, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := validateConfigIn(t, newTestToolCtx(newMockArtifactService()), tc.config, tc.mime)
			if res.Status != tool.StatusError {
				t.Errorf("status = %q, want error", res.Status)
			}
		})
	}
}

func TestValidateComponentConfig_ProceedsOnceThePlanExists(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, toolCtx, manifestFilename, minimalManifestYAML, SAMBuildManifestMIMEType)

	res := validateConfigIn(t, toolCtx, validAgentConfigYAML, SAMAgentConfigMIMEType)

	if res.Status == tool.StatusError && strings.Contains(res.Message, manifestFilename) {
		t.Fatalf("still refused with a manifest present: %s", res.Message)
	}
}

func TestValidateComponentConfig_LooksInTheCallersOwnSession(t *testing.T) {
	svc := newMockArtifactService()
	lead := newTestToolCtx(svc)
	saveTestArtifact(context.Background(), svc, lead, manifestFilename, minimalManifestYAML, SAMBuildManifestMIMEType)

	worker := newTestToolCtx(svc)
	if res := validateConfigIn(t, worker, validAgentConfigYAML, SAMAgentConfigMIMEType); res.Status == tool.StatusError {
		t.Fatalf("a caller in the lead's session could not see the lead's manifest: %s", res.Message)
	}

	elsewhere := newTestToolCtx(svc)
	elsewhere.SessionID = "some-other-session"
	res := validateConfigIn(t, elsewhere, validAgentConfigYAML, SAMAgentConfigMIMEType)
	if res.Status != tool.StatusError {
		t.Fatal("a caller outside the session read its manifest — the lookup is not session-scoped, so the gate would pass on any unrelated build's plan")
	}
}

func TestValidateComponentConfig_FailsOpenWhenTheStoreCannotAnswer(t *testing.T) {
	svc := newMockArtifactService()
	svc.failListVersions = map[string]bool{manifestFilename: true}
	toolCtx := newTestToolCtx(svc)

	res := validateConfigIn(t, toolCtx, validAgentConfigYAML, SAMAgentConfigMIMEType)

	if res.Status == tool.StatusError && strings.Contains(res.Message, manifestFilename) {
		t.Fatalf("refused on a lookup failure — a store hiccup must not block a build: %s", res.Message)
	}
}

func TestValidateComponentConfig_FailsOpenWithoutAnArtifactService(t *testing.T) {
	for _, tc := range []struct {
		name    string
		toolCtx *tool.Context
	}{
		{"no tool context", nil},
		{"no artifact service", &tool.Context{UserID: "u", SessionID: "s", AgentName: "a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := validateConfigIn(t, tc.toolCtx, validAgentConfigYAML, SAMAgentConfigMIMEType)
			if res.Status == tool.StatusError && strings.Contains(res.Message, manifestFilename) {
				t.Fatalf("refused with nothing to check against: %s", res.Message)
			}
		})
	}
}
