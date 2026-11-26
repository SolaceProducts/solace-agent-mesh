// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestValidateDeployerAuthBlock_DisableConsentPrompt(t *testing.T) {
	if d := validateDeployerAuthBlock(map[string]any{
		"scheme": map[string]any{"disable_consent_prompt": true},
	}); len(d) != 0 {
		t.Fatalf("disable_consent_prompt=true should be allowed, got %v", d)
	}
	d := validateDeployerAuthBlock(map[string]any{
		"scheme": map[string]any{"disable_consent_prompt": "yes"},
	})
	if _, ok := d["auth.scheme.disable_consent_prompt"]; !ok {
		t.Fatalf("expected type-error at auth.scheme.disable_consent_prompt, got %v", d)
	}
}

func TestBuildToolAuthBlock_PassesDisableConsentPromptToRuntime(t *testing.T) {
	authJSON := marshalJSON(t, remotetool.AuthDeclaration{Type: "oauth2"})
	f := store.ToolPackageFile{Auth: &authJSON}
	deployer := map[string]any{"scheme": map[string]any{"disable_consent_prompt": true}}

	block := buildToolAuthBlock(f, deployer)
	scheme, _ := block["scheme"].(map[string]any)
	if got, ok := scheme["disable_consent_prompt"].(bool); !ok || !got {
		t.Errorf("disable_consent_prompt must reach runtime YAML as bool true, got %T %v",
			scheme["disable_consent_prompt"], scheme["disable_consent_prompt"])
	}
}
