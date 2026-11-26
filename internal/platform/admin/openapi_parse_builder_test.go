// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestParseOpenAPISpecTool_AgainstThePlatform(t *testing.T) {
	target := serveSpec(t, parseTestSpec)
	env := newParseEnv(t)

	res, err := (&tools.ParseOpenAPISpecTool{}).Execute(t.Context(),
		map[string]any{"spec_url": target.srv.URL + "/spec.json"}, &tool.Context{ControlQuerier: env.Client})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %s: %s", res.Status, res.Message)
	}

	preview := res.DataObjects[0].Preview
	for _, want := range []string{"Note: " + openAPIParseAllowListNote, "Operations: 3", "get_pet: in(pet_id*)", "list_pets: in(limit)"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview omits %q:\n%s", want, preview)
		}
	}
	if res.DataObjects[0].Name != "openapi_spec.json" || !strings.Contains(string(res.DataObjects[0].Content), `"pet_id"`) {
		t.Errorf("artifact %s lacks the parameters: %s", res.DataObjects[0].Name, res.DataObjects[0].Content)
	}
}
