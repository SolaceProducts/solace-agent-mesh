// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

const gatewayYAMLFixture = `
name: webui
slug: webui-prod
description: "Web UI gateway for end users."
type: webui
publicUrl: https://chat.example.com
values:
  fastapiHost: 0.0.0.0
  fastapiPort: 8080
  frontendAppName: Solace Agent Mesh
  fastapi_host: ignored
`

func TestForwardEntrypointV2_FullSpec(t *testing.T) {
	in := decodeYAMLMap(t, gatewayYAMLFixture)

	got, err := ForwardEntrypointV2(in, EntrypointV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardEntrypointV2: %v", err)
	}

	wantValues := map[string]any{
		"fastapiHost":     "0.0.0.0",
		"fastapiPort":     8080,
		"frontendAppName": "Solace Agent Mesh",
		"fastapi_host":    "ignored",
	}
	want := wire.CreateEntrypointRequest{
		Name:           "webui",
		Slug:           "webui-prod",
		Description:    "Web UI gateway for end users.",
		EntrypointType: "webui",
		PublicURL:      "https://chat.example.com",
		Values:         wantValues,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ForwardEntrypointV2 mismatch (-want +got):\n%s", diff)
	}
}

func TestForwardEntrypointV2_OmittedSlugIsEmpty(t *testing.T) {
	yaml := `
name: webui
description: "Web UI gateway for end users."
type: webui
`
	in := decodeYAMLMap(t, yaml)
	got, err := ForwardEntrypointV2(in, EntrypointV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardEntrypointV2: %v", err)
	}
	if got.Slug != "" {
		t.Errorf("out.Slug = %q; want empty string when slug omitted from YAML", got.Slug)
	}
}

func TestForwardEntrypointV2_ValuesPassthrough(t *testing.T) {
	yaml := `
name: webui
description: "Web UI gateway for end users."
type: webui
values:
  nested:
    deep:
      key: value
  list: [a, b, c]
  number: 42
  flag: true
`
	in := decodeYAMLMap(t, yaml)
	got, err := ForwardEntrypointV2(in, EntrypointV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardEntrypointV2: %v", err)
	}
	wantValues := map[string]any{
		"nested": map[string]any{
			"deep": map[string]any{"key": "value"},
		},
		"list":   []any{"a", "b", "c"},
		"number": 42,
		"flag":   true,
	}
	if diff := cmp.Diff(wantValues, got.Values); diff != "" {
		t.Errorf("Values mismatch (-want +got):\n%s", diff)
	}
}

func TestReverseEntrypointV2_DropsServerManagedFields(t *testing.T) {
	slug, publicURL, syncStatus := "webui-prod", "https://chat.example.com", "in_sync"
	resp := wire.EntrypointResponse{
		ID:               "abc-123",
		Type:             "entrypoint",
		Slug:             &slug,
		Name:             "webui",
		Description:      "Web UI",
		EntrypointType:   "webui",
		PublicURL:        &publicURL,
		Values:           map[string]any{"k": "v"},
		DeploymentStatus: "deployed",
		SyncStatus:       &syncStatus,
		Deployed:         &apiv2.DeployedEntrypoint{Name: "webui", EntrypointType: "webui"},
		RuntimeStatus:    apiv2.RuntimeStatus{Status: "running"},
		CreatedBy:        "u",
		UpdatedBy:        "u",
		CreatedTime:      apiv2.Instant(1_700_000_000_000),
		UpdatedTime:      apiv2.Instant(1_700_001_000_000),
	}
	got, err := ReverseEntrypointV2(resp, EntrypointV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseEntrypointV2: %v", err)
	}
	for _, k := range []string{"id", "entrypointType", "deploymentStatus", "syncStatus", "deployed", "runtimeStatus", "createdBy", "updatedBy", "createdTime", "updatedTime"} {
		if _, ok := got[k]; ok {
			t.Errorf("server-managed field %q leaked into reverse output", k)
		}
	}
	for _, k := range []string{"name", "slug", "description", "type", "publicUrl", "values"} {
		if _, ok := got[k]; !ok {
			t.Errorf("authoring field %q missing from reverse output", k)
		}
	}
	if got["slug"] != "webui-prod" {
		t.Errorf("got[\"slug\"] = %v; want %q", got["slug"], "webui-prod")
	}
	if got["type"] != "webui" {
		t.Errorf("got[\"type\"] = %v; want the entrypoint type %q, not the resource type", got["type"], "webui")
	}
}
