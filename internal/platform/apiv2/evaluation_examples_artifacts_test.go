// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"strings"
	"testing"
)

func evaluationsArtifactExample(t *testing.T, api *entrypointAPI) string {
	t.Helper()
	dataset := newDataset(api, "artifact-slot-examples")
	return examplesPath + "/" + newExample(api, dataset.ID, map[string]any{"prompt": "Review the file"}).ID
}

func TestExamples_ArtifactSlotsAreIndependent(t *testing.T) {
	api := newExampleArtifactAPI(t)
	example := evaluationsArtifactExample(t, api)

	expectStatus(t, api.uploadParts(http.MethodPut, example+"/inputArtifact", nil, formPart{name: "file", filename: "config.yaml", content: "name: sam\n"}), http.StatusOK)
	expectStatus(t, api.uploadParts(http.MethodPut, example+"/expectedArtifact", nil, formPart{name: "file", filename: "reference.md", content: "the answer\n"}), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, example+"/inputArtifact", nil), http.StatusNoContent)

	rec := api.call(http.MethodGet, example+"/expectedArtifact/reference.md", nil)
	expectStatus(t, rec, http.StatusOK)
	if rec.Body.String() != "the answer\n" {
		t.Errorf("expected file after the input was deleted = %q, want it unchanged", rec.Body.String())
	}
	raw := rawData(t, api.call(http.MethodGet, example, nil))
	expectNull(t, raw, "inputArtifact")
	if got, _ := raw["expectedArtifact"].(map[string]any); got["filename"] != "reference.md" {
		t.Errorf("expectedArtifact = %v, want reference.md kept", raw["expectedArtifact"])
	}
}

func TestExamples_ArtifactOfExactlyTheLimitIsStored(t *testing.T) {
	t.Setenv("EVAL_EXAMPLE_ARTIFACT_MAX_BYTES", "16")
	api := newExampleArtifactAPI(t)
	slot := evaluationsArtifactExample(t, api) + "/inputArtifact"

	expectStatus(t, api.uploadParts(http.MethodPut, slot, nil, formPart{name: "file", filename: "exact.txt", content: strings.Repeat("x", 16)}), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, slot+"/exact.txt", nil), http.StatusOK)
}

func TestExamples_ArtifactUnderAReservedFilenameIs422(t *testing.T) {
	api := newExampleArtifactAPI(t)
	slot := evaluationsArtifactExample(t, api) + "/inputArtifact"

	rec := api.uploadParts(http.MethodPut, slot, nil, formPart{name: "file", filename: "sneaky.metadata.json", content: "x"})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	decodeError(t, rec)
	expectNull(t, rawData(t, api.call(http.MethodGet, strings.TrimSuffix(slot, "/inputArtifact"), nil)), "inputArtifact")
}
