// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const skillsSentinelManifest = "---\nname: sentinel-skill\ndescription: d\ntools:\n  - tool_type: builtin\n    tool_name: web_search_google\n    tool_config:\n      google_search_api_key: \"<REDACTED>\"\n---\nbody\n"

func skillsManifestFileSKILLmd(manifestFile string) string {
	return fmt.Sprintf("---\nname: manifest-file-skill\ndescription: test\ntools:\n"+
		"  - tool_type: mcp\n    connection_params: {type: streamable-http, url: \"https://mcp.example.com/mcp\"}\n"+
		"    manifest_file: %s\n---\n\nBody.\n", manifestFile)
}

func (a *skillAPI) skillsCreateFrom(skillMD string) *httptest.ResponseRecorder {
	a.t.Helper()
	return uploadForm(a.entrypointAPI, http.MethodPost, "/skills", "skill.zip", zipOf(a.t, map[string]string{"SKILL.md": skillMD}), map[string]string{"description": "test"})
}

func TestSkills_CreateTakesTheNameFromSKILLmdAndTheDescriptionFromTheForm(t *testing.T) {
	api := newSkillAPI(t)
	rec := uploadForm(api.entrypointAPI, http.MethodPost, "/skills", "any-filename.zip",
		zipOf(t, map[string]string{"SKILL.md": "---\nname: my-skill\ndescription: test\n---\n\nContent here.\n"}),
		map[string]string{"description": "user-supplied description"})
	expectStatus(t, rec, http.StatusCreated)
	if got := decodeData[Skill](t, rec); got.Name != "my-skill" || got.Description == nil || *got.Description != "user-supplied description" {
		t.Errorf("name %q and description %v, want my-skill from SKILL.md and the form's description", got.Name, got.Description)
	}
}

func TestSkills_CreateAcceptsAnMCPManifestFileAbsentOnThePlatformHost(t *testing.T) {
	api := newSkillAPI(t)
	absent := filepath.Join(t.TempDir(), "only-in-the-runtime-pod.yaml")
	expectStatus(t, api.skillsCreateFrom(skillsManifestFileSKILLmd(absent)), http.StatusCreated)
}

func TestSkills_CreateWithoutAFrontmatterNameIs422(t *testing.T) {
	api := newSkillAPI(t)
	expectDetailSaying(t, api.skillsCreateFrom("---\ndescription: nameless\n---\n\nContent.\n"), "file", "has no 'name' field")
}

func TestSkills_CreateRefusesARedactionSentinelAndKeepsNoRow(t *testing.T) {
	api := newSkillAPI(t)
	rec := api.skillsCreateFrom(skillsSentinelManifest)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if !strings.Contains(rec.Body.String(), "google_search_api_key") {
		t.Errorf("the refusal does not name the sentinel's setting: %s", rec.Body.String())
	}
	if customs := decodeList[Skill](t, api.call(http.MethodGet, "/skills?origin=custom", nil)); len(customs.Data) != 0 {
		t.Errorf("a refused create left %d skills behind", len(customs.Data))
	}
}

func TestSkills_ConfigOfAMissingOrMalformedID(t *testing.T) {
	api := newSkillAPI(t)
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		body := map[string]any{"values": map[string]any{}}
		expectStatus(t, api.call(method, "/skills/"+missingID+"/config", body), http.StatusNotFound)
		expectDetail(t, api.call(method, "/skills/Not-A-UUID/config", body), http.StatusBadRequest, "id")
	}
}

func TestSkills_ConfigPatchRejectsAnUnknownTool(t *testing.T) {
	api := newSkillAPI(t)
	sk := api.createReady("order-lookup")
	expectDetail(t, api.call(http.MethodPatch, "/skills/"+sk.ID+"/config", map[string]any{"values": map[string]any{"ghost": map[string]any{"x": "y"}}}),
		http.StatusUnprocessableEntity, "ghost")
}

func TestSkills_ConfigPatchRejectsAnUnknownEnvelopeField(t *testing.T) {
	api := newSkillAPI(t)
	sk := api.createReady("order-lookup")
	expectDetail(t, api.call(http.MethodPatch, "/skills/"+sk.ID+"/config", map[string]any{
		"values":     map[string]any{"find_order": map[string]any{"api_key": "sk-1"}},
		"extraField": "bad",
	}), http.StatusBadRequest, "extraField")
}
