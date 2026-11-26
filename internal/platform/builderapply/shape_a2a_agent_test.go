// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const builderA2AAgentJSON = `{
  "name": "WeatherProxy",
  "description": "Proxies the external weather forecasting agent into the mesh.",
  "agentUrl": "http://ec2-1-2-3-4.compute.amazonaws.com:9000",
  "agentCardLocation": "well_known",
  "agentAuthConfig": { "type": "bearer", "token": "<<__SAM_REQUIRED__>>" }
}`

func TestDesiredA2AAgent_DecodesBuilderArtifact(t *testing.T) {
	t.Parallel()
	got, err := desiredA2AAgent("WeatherProxy", builderA2AAgentJSON)
	if err != nil {
		t.Fatalf("desiredA2AAgent: %v", err)
	}
	create, ok := got.create.(entplatform.CreateRemoteAgentRequest)
	if !ok {
		t.Fatalf("create is %T, want CreateRemoteAgentRequest", got.create)
	}
	if create.Name != "WeatherProxy" {
		t.Errorf("Name = %q", create.Name)
	}
	if create.AgentURL != "http://ec2-1-2-3-4.compute.amazonaws.com:9000" {
		t.Errorf("AgentURL = %q", create.AgentURL)
	}
	if create.AgentCardLocation != "well_known" {
		t.Errorf("AgentCardLocation = %q", create.AgentCardLocation)
	}
	auth, ok := create.AgentAuthConfig.(map[string]any)
	if !ok {
		t.Fatalf("AgentAuthConfig is %T, want map", create.AgentAuthConfig)
	}
	if auth["token"] != "<<__SAM_REQUIRED__>>" {
		t.Errorf("token = %v, want the placeholder preserved", auth["token"])
	}
}

func TestDesiredA2AAgent_LeavesMissingAuthConfigNil(t *testing.T) {
	t.Parallel()
	got, err := desiredA2AAgent("p", `{"name":"p","agentUrl":"https://x","agentCardLocation":"well_known"}`)
	if err != nil {
		t.Fatalf("desiredA2AAgent: %v", err)
	}
	if cfg := got.create.(entplatform.CreateRemoteAgentRequest).AgentAuthConfig; cfg != nil {
		t.Errorf("AgentAuthConfig = %v, want nil so an update preserves the stored credential", cfg)
	}
}

func TestDefaultA2AAuthForCreate(t *testing.T) {
	t.Parallel()
	got, err := desiredA2AAgent("p", `{"name":"p","agentUrl":"https://x","agentCardLocation":"well_known"}`)
	if err != nil {
		t.Fatalf("desiredA2AAgent: %v", err)
	}
	create := defaultA2AAuthForCreate(got.create.(entplatform.CreateRemoteAgentRequest))
	auth, ok := create.AgentAuthConfig.(map[string]any)
	if !ok {
		t.Fatal("AgentAuthConfig was not defaulted on the create path")
	}
	if auth["type"] != "none" {
		t.Errorf(`default auth type = %v, want "none"`, auth["type"])
	}

	authored, err := desiredA2AAgent("p", builderA2AAgentJSON)
	if err != nil {
		t.Fatalf("desiredA2AAgent: %v", err)
	}
	kept := defaultA2AAuthForCreate(authored.create.(entplatform.CreateRemoteAgentRequest))
	if kept.AgentAuthConfig.(map[string]any)["type"] != "bearer" {
		t.Error("defaulting overwrote an authored auth config")
	}
}

func TestDesiredA2AAgent_UnknownFieldIsRejected(t *testing.T) {
	t.Parallel()
	_, err := desiredA2AAgent("p", `{"name":"p","agentUrl":"https://x","agentCardLocation":"well_known","tpyo":1}`)
	if err == nil {
		t.Fatal("expected an error for an unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "tpyo") {
		t.Errorf("error should name the offending field, got: %v", err)
	}
}

func TestDesiredA2AAgent_RejectsMissingRequiredFields(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"no agentUrl":          `{"name":"p","agentCardLocation":"well_known"}`,
		"no agentCardLocation": `{"name":"p","agentUrl":"https://x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := desiredA2AAgent("p", body); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}
