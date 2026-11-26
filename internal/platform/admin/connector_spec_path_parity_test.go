// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestUploadedSpecPathParity(t *testing.T) {
	t.Parallel()
	for _, u := range []string{
		SpecResourcePath("01a12741-271c-7a25-939e-56a916f4217e"),
		"/api/v1/platform/connectors/01a12741-271c-7a25-939e-56a916f4217e/resource",
		"/api/v1/platform/connectors/",
		"/api/v1/platform/connectors",
		"/api/v2/platform/connectors/01a12741-271c-7a25-939e-56a916f4217e/resource",
		"/api/v2/platform/connectors/",
		"/api/v2/platform/connectors",
		"/api/v3/platform/connectors/x/resource",
		"api/v1/platform/connectors/x/resource",
		" /api/v1/platform/connectors/x/resource",
		"/API/V1/PLATFORM/CONNECTORS/x/resource",
		"/api/v1/platform/agents/x/resource",
		"https://api.example.test/openapi.json",
		"",
	} {
		if got, want := tools.IsUploadedSpecPath(u), IsSpecDisplayPath(u); got != want {
			t.Errorf("tools.IsUploadedSpecPath(%q) = %v, IsSpecDisplayPath = %v", u, got, want)
		}
	}
}

func TestIsSpecResourcePathFor(t *testing.T) {
	t.Parallel()
	const id = "01a12741-271c-7a25-939e-56a916f4217e"
	for u, want := range map[string]bool{
		SpecResourcePath(id):                                    true,
		"/api/v1/platform/connectors/" + id + "/resource":       true,
		"/api/v1/platform/connectors/other-id/resource":         false,
		"/api/v2/platform/connectors/" + id + "/resource":       true,
		"/api/v2/platform/connectors/other-id/resource":         false,
		"/api/v3/platform/connectors/" + id + "/resource":       false,
		"/api/v2/platform/connectors/" + id + "/resource/extra": false,
		"https://sam.example.test" + SpecResourcePath(id):       false,
		"": false,
	} {
		if got := IsSpecResourcePathFor(u, id); got != want {
			t.Errorf("IsSpecResourcePathFor(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestSpecResourcePathNamesV2(t *testing.T) {
	t.Parallel()
	const id = "01a12741-271c-7a25-939e-56a916f4217e"
	if got, want := SpecResourcePath(id), "/api/v2/platform/connectors/"+id+"/resource"; got != want {
		t.Errorf("SpecResourcePath(%q) = %q, want %q", id, got, want)
	}
}
