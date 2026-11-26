// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const testSpecContent = "openapi: 3.0.0\npaths: {}\n"

func resolvedOpenAPIConnector(t *testing.T, name, specContent string, mutate func(values map[string]any)) resolve.ResolvedResource {
	t.Helper()
	dir := t.TempDir()
	specName := name + ".openapi.yaml"
	if err := os.WriteFile(filepath.Join(dir, specName), []byte(specContent), 0o600); err != nil {
		t.Fatalf("write spec fixture: %v", err)
	}
	values := map[string]any{
		"specification_file": specName,
		"base_url":           "https://api.example.com",
		"auth_type":          "none",
	}
	if mutate != nil {
		mutate(values)
	}
	return resolve.ResolvedResource{
		Kind:       "connector",
		Name:       name,
		SourceFile: filepath.Join(dir, name+".yaml"),
		Spec: map[string]any{
			"type":    "api",
			"subtype": "openapi",
			"values":  values,
		},
		Description: "fixture",
	}
}

func openAPIActual(id, name, contentHash string) wire.ConnectorResponse {
	return wire.ConnectorResponse{
		ID:               id,
		Name:             name,
		Description:      sp("fixture"),
		ConnectorType:    "api",
		ConnectorSubtype: "openapi",
		HasResource:      true,
		ContentHash:      sp(contentHash),
		Values: map[string]any{
			"specification_url": entplatform.SpecResourcePath(id),
			"base_url":          "https://api.example.com",
			"auth_type":         "none",
		},
	}
}

func TestPlanConnectors_SpecFileCreate(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, nil)}

	plan, err := planConnectors(t.Context(), nil, desired, nil)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("Creates = %+v", plan.Creates)
	}
	op := plan.Creates[0]
	if op.Spec == nil || !op.SpecUploadNeeded {
		t.Fatalf("create op missing spec upload: %+v", op)
	}
	if op.Spec.Filename != "petstore.openapi.yaml" || string(op.Spec.Data) != testSpecContent {
		t.Errorf("spec upload = %q/%q", op.Spec.Filename, op.Spec.Data)
	}
	if op.Spec.Hash != sha256Hex([]byte(testSpecContent)) {
		t.Errorf("spec hash = %q", op.Spec.Hash)
	}
	values := op.Create.Values
	if _, leaked := values["specification_file"]; leaked {
		t.Error("specification_file leaked into the wire DTO values")
	}
}

func TestPlanConnectors_SpecFileUnchangedIsNoOp(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, nil)}
	actual := []wire.ConnectorResponse{
		openAPIActual("id-1", "petstore", sha256Hex([]byte(testSpecContent))),
	}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Fatalf("Unchanged = %+v, Updates = %+v, Creates = %+v", plan.Unchanged, plan.Updates, plan.Creates)
	}
	if len(plan.Creates)+len(plan.Updates)+len(plan.Deletes) != 0 {
		t.Errorf("expected pure no-op, got creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanConnectors_ChangedSpecPromotesToSpecOnlyUpdate(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, nil)}
	actual := []wire.ConnectorResponse{
		openAPIActual("id-1", "petstore", sha256Hex([]byte("openapi: 3.0.0\npaths: {/old: {}}\n"))),
	}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("Updates = %+v, Unchanged = %+v", plan.Updates, plan.Unchanged)
	}
	op := plan.Updates[0]
	if !op.SpecUploadNeeded || op.Spec == nil {
		t.Errorf("update op missing spec upload: %+v", op)
	}
	if op.PatchNeeded {
		t.Error("PatchNeeded = true for a spec-only change, want false")
	}
	if op.PriorSpecHash == "" || op.PriorSpecHash == op.Spec.Hash {
		t.Errorf("PriorSpecHash = %q, want the old stored hash", op.PriorSpecHash)
	}
}

func TestPlanConnectors_UnknownStoredHashForcesUpload(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, nil)}
	actual := []wire.ConnectorResponse{openAPIActual("id-1", "petstore", "")}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Updates) != 1 || !plan.Updates[0].SpecUploadNeeded {
		t.Fatalf("Updates = %+v, want one spec re-upload", plan.Updates)
	}
	if plan.Updates[0].PriorSpecHash != "" {
		t.Errorf("PriorSpecHash = %q, want empty (unknown)", plan.Updates[0].PriorSpecHash)
	}
}

func TestPlanConnectors_ValuesChangeReusesUnchangedSpec(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, func(values map[string]any) {
		values["allow_list"] = "getPet, listPets"
	})}
	actual := []wire.ConnectorResponse{
		openAPIActual("id-1", "petstore", sha256Hex([]byte(testSpecContent))),
	}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("Updates = %+v", plan.Updates)
	}
	op := plan.Updates[0]
	if !op.PatchNeeded {
		t.Error("PatchNeeded = false for a values change, want true")
	}
	if op.SpecUploadNeeded {
		t.Error("SpecUploadNeeded = true for a hash-matched spec, want false")
	}
}

func TestPlanConnectors_SpecFileMutualExclusivity(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, func(values map[string]any) {
		values["specification_url"] = "https://specs.example.com/openapi.yaml"
	})}

	_, err := planConnectors(t.Context(), nil, desired, nil)
	if err == nil {
		t.Fatal("planConnectors accepted specification_file + specification_url, want mutual-exclusivity error")
	}
}

func TestPlanConnectors_SpecFileRejectedOnNonOpenAPI(t *testing.T) {
	r := resolvedConnector("ops-slack", connectorFixture(func(m map[string]any) {
		m["values"].(map[string]any)["specification_file"] = "spec.yaml"
	}))

	_, err := planConnectors(t.Context(), nil, []resolve.ResolvedResource{r}, nil)
	if err == nil {
		t.Fatal("planConnectors accepted specification_file on a non-openapi connector, want error")
	}
}

func TestApplyConnectors_CreateUploadsSpec(t *testing.T) {
	var uploaded []byte
	var uploadedFilename string
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+platform.APIV2Prefix+connectorsResource.path, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusCreated, wire.ConnectorResponse{ID: "id-new", Name: "petstore"})
	})
	mux.HandleFunc("PUT "+platform.APIV2Prefix+connectorsResource.path+"/{id}/resource", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("missing file field: %v", err)
			return
		}
		defer func() { _ = file.Close() }()
		uploaded, _ = io.ReadAll(file)
		uploadedFilename = header.Filename
		if ct := header.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("spec part Content-Type = %q, want application/octet-stream", ct)
		}
		writeV2Single(w, http.StatusOK, wire.ConnectorResponse{ID: r.PathValue("id"), Name: "petstore", HasResource: true})
	})

	c := newV2PlatformClient(t, mux)
	plan := &connectorKindPlan{
		Kind: "connector",
		Creates: []connectorOp{{
			Name:   "petstore",
			Create: wire.CreateConnectorRequest{Name: "petstore", ConnectorType: "api", ConnectorSubtype: "openapi"},
			Spec: &connectorSpecUpload{
				Filename: "petstore.openapi.yaml",
				Data:     []byte(testSpecContent),
				Hash:     sha256Hex([]byte(testSpecContent)),
			},
			SpecUploadNeeded: true,
		}},
	}

	outcome := applyConnectors(t.Context(), c, plan, ApplyOptions{})
	if len(outcome.Failures) != 0 {
		t.Fatalf("Failures = %+v", outcome.Failures)
	}
	if len(outcome.Created) != 1 {
		t.Fatalf("Created = %+v", outcome.Created)
	}
	if string(uploaded) != testSpecContent {
		t.Errorf("uploaded bytes = %q, want the spec content", uploaded)
	}
	if uploadedFilename != "petstore.openapi.yaml" {
		t.Errorf("uploaded filename = %q", uploadedFilename)
	}
}

func TestPlanConnectors_AuthoredDisplayPathDoesNotConflictOrDrift(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, func(values map[string]any) {
		values["specification_url"] = entplatform.SpecResourcePath("id-1")
	})}
	actual := []wire.ConnectorResponse{
		openAPIActual("id-1", "petstore", sha256Hex([]byte(testSpecContent))),
	}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v, Updates = %+v", plan.Unchanged, plan.Updates)
	}
}

func TestPlanConnectors_DisplayPathIsNoOpOnEitherVersion(t *testing.T) {
	const (
		v1Path = "/api/v1/platform/connectors/id-1/resource"
		v2Path = "/api/v2/platform/connectors/id-1/resource"
	)
	if got := entplatform.SpecResourcePath("id-1"); got != v2Path {
		t.Fatalf("SpecResourcePath = %q, want the v2 path %q this test stores", got, v2Path)
	}
	for _, stored := range []string{v1Path, v2Path} {
		for _, tc := range []struct {
			name   string
			mutate func(values map[string]any)
		}{
			{name: "specification_file in the repo"},
			{name: "v1 display path authored in the repo", mutate: func(values map[string]any) { values["specification_url"] = v1Path }},
			{name: "v2 display path authored in the repo", mutate: func(values map[string]any) { values["specification_url"] = v2Path }},
		} {
			t.Run(stored+"/"+tc.name, func(t *testing.T) {
				desired := []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, tc.mutate)}
				actual := openAPIActual("id-1", "petstore", sha256Hex([]byte(testSpecContent)))
				actual.Values["specification_url"] = stored

				plan, err := planConnectors(t.Context(), nil, desired, []wire.ConnectorResponse{actual})
				if err != nil {
					t.Fatalf("planConnectors: %v", err)
				}
				if len(plan.Unchanged) != 1 {
					t.Errorf("Unchanged = %+v, Updates = %+v, Creates = %+v", plan.Unchanged, plan.Updates, plan.Creates)
				}
			})
		}
	}
}

func TestApplyConnectors_CreateUploadFailureConverges(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+platform.APIV2Prefix+connectorsResource.path, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusCreated, wire.ConnectorResponse{ID: "id-new", Name: "petstore"})
	})
	mux.HandleFunc("PUT "+platform.APIV2Prefix+connectorsResource.path+"/{id}/resource", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Error(w, http.StatusInternalServerError, "boom", nil)
	})
	c := newV2PlatformClient(t, mux)

	desired := []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, nil)}
	plan, err := planConnectors(t.Context(), nil, desired, nil)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	outcome := applyConnectors(t.Context(), c, plan, ApplyOptions{})
	if len(outcome.Failures) != 1 || len(outcome.Created) != 0 {
		t.Fatalf("outcome = %+v, want the create reported as a failure", outcome)
	}

	actual := []wire.ConnectorResponse{{
		ID:               "id-new",
		Name:             "petstore",
		Description:      sp("fixture"),
		ConnectorType:    "api",
		ConnectorSubtype: "openapi",
		Values: map[string]any{
			"base_url":  "https://api.example.com",
			"auth_type": "none",
		},
	}}
	desired = []resolve.ResolvedResource{resolvedOpenAPIConnector(t, "petstore", testSpecContent, nil)}
	replan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if len(replan.Updates) != 1 || !replan.Updates[0].SpecUploadNeeded || replan.Updates[0].PatchNeeded {
		t.Fatalf("re-plan = %+v, want one spec-only retry update", replan)
	}
}

func TestApplyConnectors_UploadRunsBeforePatch(t *testing.T) {
	var calls []string
	var patched map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH "+platform.APIV2Prefix+connectorsResource.path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "patch")
		if err := json.NewDecoder(r.Body).Decode(&patched); err != nil {
			t.Errorf("decode PATCH body: %v", err)
		}
		writeV2Single(w, http.StatusOK, wire.ConnectorResponse{ID: r.PathValue("id")})
	})
	mux.HandleFunc("PUT "+platform.APIV2Prefix+connectorsResource.path+"/{id}/resource", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "upload")
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ConnectorResponse{ID: r.PathValue("id"), HasResource: true})
	})

	plan := &connectorKindPlan{
		Kind: "connector",
		Updates: []connectorOp{{
			Name:        "petstore",
			ID:          "id-1",
			Create:      wire.CreateConnectorRequest{Name: "petstore", Values: map[string]any{"base_url": "https://petstore.example.com"}},
			Patch:       wire.UpdateConnectorRequest{Values: wire.Patch[map[string]any]{Set: true, Value: map[string]any{"base_url": "https://petstore.example.com"}}},
			PatchNeeded: true,
			Spec: &connectorSpecUpload{
				Filename: "petstore.openapi.yaml",
				Data:     []byte(testSpecContent),
				Hash:     sha256Hex([]byte(testSpecContent)),
			},
			SpecUploadNeeded: true,
		}},
	}
	outcome := applyConnectors(t.Context(), newV2PlatformClient(t, mux), plan, ApplyOptions{})
	if len(outcome.Failures) != 0 || len(outcome.Updated) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(calls) != 2 || calls[0] != "upload" || calls[1] != "patch" {
		t.Errorf("call order = %v, want [upload patch]", calls)
	}
	want := map[string]any{"values": map[string]any{"base_url": "https://petstore.example.com"}}
	if got, wantJSON := toJSON(t, patched), toJSON(t, want); got != wantJSON {
		t.Errorf("PATCH body = %s, want the planned patch %s", got, wantJSON)
	}
}

func TestConnectorPlanLabels(t *testing.T) {
	t.Parallel()
	spec := &connectorSpecUpload{Hash: "bbbbbbbbbbbbbbbbbbbb"}

	if got := connectorSpecSuffix(connectorOp{Spec: spec}); got != " (spec bbbbbbbb)" {
		t.Errorf("create suffix = %q", got)
	}
	if got := connectorSpecSuffix(connectorOp{}); got != "" {
		t.Errorf("create suffix without spec = %q", got)
	}
	if got := connectorUpdateLabel(connectorOp{PatchNeeded: true}); got != "update" {
		t.Errorf("values-only label = %q", got)
	}
	specOnly := connectorOp{Spec: spec, SpecUploadNeeded: true, PriorSpecHash: "aaaaaaaaaaaaaaaaaaaa"}
	if got := connectorUpdateLabel(specOnly); got != "update (spec aaaaaaaa → bbbbbbbb)" {
		t.Errorf("spec-only label = %q", got)
	}
	both := connectorOp{Spec: spec, SpecUploadNeeded: true, PatchNeeded: true}
	if got := connectorUpdateLabel(both); got != "update (values, spec none → bbbbbbbb)" {
		t.Errorf("values+spec label = %q", got)
	}
}

func TestApplyConnectors_SpecOnlyUpdateSkipsPatch(t *testing.T) {
	var patchCalls, uploadCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH "+platform.APIV2Prefix+connectorsResource.path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		patchCalls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ConnectorResponse{ID: r.PathValue("id")})
	})
	mux.HandleFunc("PUT "+platform.APIV2Prefix+connectorsResource.path+"/{id}/resource", func(w http.ResponseWriter, r *http.Request) {
		uploadCalls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ConnectorResponse{ID: r.PathValue("id"), HasResource: true})
	})

	c := newV2PlatformClient(t, mux)
	plan := &connectorKindPlan{
		Kind: "connector",
		Updates: []connectorOp{{
			Name: "petstore",
			ID:   "id-1",
			Spec: &connectorSpecUpload{
				Filename: "petstore.openapi.yaml",
				Data:     []byte(testSpecContent),
				Hash:     sha256Hex([]byte(testSpecContent)),
			},
			SpecUploadNeeded: true,
			PatchNeeded:      false,
		}},
	}

	outcome := applyConnectors(t.Context(), c, plan, ApplyOptions{})
	if len(outcome.Failures) != 0 {
		t.Fatalf("Failures = %+v", outcome.Failures)
	}
	if patchCalls.Load() != 0 {
		t.Errorf("PATCH calls = %d, want 0 for a spec-only update", patchCalls.Load())
	}
	if uploadCalls.Load() != 1 {
		t.Errorf("upload calls = %d, want 1", uploadCalls.Load())
	}
	if len(outcome.Updated) != 1 {
		t.Errorf("Updated = %+v", outcome.Updated)
	}
}
