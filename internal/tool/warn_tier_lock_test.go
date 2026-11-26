// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool_test

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/authwrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestRealDiscoveredToolTypes_StayOffTheFatalTier(t *testing.T) {
	types := map[string]tool.Tool{
		"mcp.MCPTool":         &mcp.MCPTool{},
		"openapi.OpenAPITool": &openapi.OpenAPITool{},
		"remote.Tool":         &remote.Tool{},
	}

	wrapped := 0
	for name, tl := range types {
		t.Run(name, func(t *testing.T) {
			if tool.HasAuthoredSchemas(tl) {
				t.Errorf("%s reports authored schemas; every workflow node bound to it now "+
					"fails on an argument its discovered schema omits", name)
			}
			if got := tool.ResultSchemaOf(tl); got != nil {
				t.Errorf("%s declares a result schema (%v) with nothing discovered; an "+
					"undeclared shape must read as unknown, never as complete", name, got)
			}

			if bt, ok := tl.(tool.BuiltinTool); ok {
				wrapped++
				w := authwrap.Wrap(bt, &toolauth.AuthHandler{}, nil)
				if tool.HasAuthoredSchemas(w) {
					t.Errorf("auth-wrapping %s promoted it onto the fatal tier", name)
				}
				if got := tool.ArtifactNotFoundHintOf(w); got != "" {
					t.Errorf("auth-wrapping %s invented an artifact-not-found hint (%q)", name, got)
				}
			}
		})
	}
	if wrapped == 0 {
		t.Error("no type reached the auth-wrapping check — that half of this test is vacuous")
	}
}

func TestRealHinter_SurvivesAuthWrap(t *testing.T) {
	inner := &tools.SearchAndReplaceInArtifactTool{}
	want := inner.ArtifactNotFoundHint()
	if want == "" {
		t.Fatal("SearchAndReplaceInArtifactTool declares no hint — this test no longer guards anything")
	}

	got := tool.ArtifactNotFoundHintOf(authwrap.Wrap(inner, &toolauth.AuthHandler{}, nil))
	if got != want {
		t.Errorf("auth-wrapping dropped the artifact-not-found hint: got %q, want %q", got, want)
	}
}
