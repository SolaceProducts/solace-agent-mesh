// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func sqlConnectorResource(values map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind: "connector", Name: "db", SourceFile: "/x/db.yaml", Description: "fixture",
		Spec: map[string]any{"type": "sql", "subtype": "postgres", "values": values},
	}
}

func sqlConnectorActual(hil any) wire.ConnectorResponse {
	values := map[string]any{"host": "h", "port": "5432"}
	if hil != nil {
		values["hil"] = hil
	}
	return wire.ConnectorResponse{ID: "id1", Name: "db", Description: sp("fixture"), ConnectorType: "sql", ConnectorSubtype: "postgres", Values: values}
}

func TestPlanConnectors_HILSpellings(t *testing.T) {
	t.Parallel()
	gate := map[string]any{"tools": map[string]any{"sql_query": map[string]any{"require_approval": true}}}
	withHIL := func(hil any) map[string]any { return map[string]any{"host": "h", "port": "5432", "hil": hil} }
	omitted := map[string]any{"host": "h", "port": "5432"}

	cases := map[string]struct {
		desired     map[string]any
		storedGate  any
		wantUpdates int
		wantPatch   any
	}{
		"omitted over a stored gate keeps it":    {desired: omitted, storedGate: gate},
		"the same gate":                          {desired: withHIL(gate), storedGate: gate},
		"null over a stored gate removes it":     {desired: withHIL(nil), storedGate: gate, wantUpdates: 1, wantPatch: map[string]any{}},
		"empty block over a stored gate removes": {desired: withHIL(map[string]any{}), storedGate: gate, wantUpdates: 1, wantPatch: map[string]any{}},
		"null over no gate is unchanged":         {desired: withHIL(nil)},
		"empty block over no gate is unchanged":  {desired: withHIL(map[string]any{})},
		"omitted over no gate is unchanged":      {desired: omitted},
		"a new gate over none":                   {desired: withHIL(gate), wantUpdates: 1, wantPatch: gate},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan, err := planConnectors(t.Context(), nil, []resolve.ResolvedResource{sqlConnectorResource(tc.desired)}, []wire.ConnectorResponse{sqlConnectorActual(tc.storedGate)})
			if err != nil {
				t.Fatalf("planConnectors: %v", err)
			}
			if len(plan.Updates) != tc.wantUpdates {
				t.Fatalf("updates = %d, want %d (unchanged %d)", len(plan.Updates), tc.wantUpdates, len(plan.Unchanged))
			}
			if tc.wantUpdates == 0 {
				return
			}
			patchValues := plan.Updates[0].Patch.Values.Value
			if got, want := toJSON(t, patchValues["hil"]), toJSON(t, tc.wantPatch); got != want {
				t.Errorf("patched hil = %s, want %s", got, want)
			}
		})
	}
}

func TestPlanConnectors_SSLConfigSpellings(t *testing.T) {
	t.Parallel()
	ssl := map[string]any{"ca_bundle": "/etc/sam/mcp-tls/ca.pem", "server_name": "mcp.internal"}
	other := map[string]any{"server_name": "mcp.other"}
	desired := func(set func(map[string]any)) resolve.ResolvedResource {
		values := map[string]any{"server_url": "https://mcp.example.com/mcp"}
		set(values)
		return resolve.ResolvedResource{
			Kind: "connector", Name: "orders-mcp", SourceFile: "/x/orders-mcp.yaml", Description: "fixture",
			Spec: map[string]any{"type": "mcp", "subtype": "remote", "values": values},
		}
	}
	actual := func(stored any) wire.ConnectorResponse {
		values := map[string]any{"server_url": "https://mcp.example.com/mcp"}
		if stored != nil {
			values["ssl_config"] = stored
		}
		return wire.ConnectorResponse{ID: "id1", Name: "orders-mcp", Description: sp("fixture"), ConnectorType: "mcp", ConnectorSubtype: "remote", Values: values}
	}
	omit := func(map[string]any) {}
	with := func(block any) func(map[string]any) { return func(v map[string]any) { v["ssl_config"] = block } }

	cases := map[string]struct {
		set         func(map[string]any)
		stored      any
		wantUpdates int
		wantPatch   any
	}{
		"omitted over a stored block keeps it":       {set: omit, stored: ssl},
		"the same block":                             {set: with(ssl), stored: ssl},
		"null over a stored block removes it":        {set: with(nil), stored: ssl, wantUpdates: 1, wantPatch: map[string]any{}},
		"an empty block over a stored block removes": {set: with(map[string]any{}), stored: ssl, wantUpdates: 1, wantPatch: map[string]any{}},
		"a different block replaces it":              {set: with(other), stored: ssl, wantUpdates: 1, wantPatch: other},
		"null over no block is unchanged":            {set: with(nil)},
		"an empty block over no block is unchanged":  {set: with(map[string]any{})},
		"an empty block over a stored empty one":     {set: with(map[string]any{}), stored: map[string]any{}},
		"omitted over no block is unchanged":         {set: omit},
		"a new block over none":                      {set: with(ssl), wantUpdates: 1, wantPatch: ssl},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan, err := planConnectors(t.Context(), nil, []resolve.ResolvedResource{desired(tc.set)}, []wire.ConnectorResponse{actual(tc.stored)})
			if err != nil {
				t.Fatalf("planConnectors: %v", err)
			}
			if len(plan.Updates) != tc.wantUpdates {
				t.Fatalf("updates = %d, want %d (unchanged %d)", len(plan.Updates), tc.wantUpdates, len(plan.Unchanged))
			}
			if tc.wantUpdates == 0 {
				return
			}
			patchValues := plan.Updates[0].Patch.Values.Value
			if got, want := toJSON(t, patchValues["ssl_config"]), toJSON(t, tc.wantPatch); got != want {
				t.Errorf("patched ssl_config = %s, want %s", got, want)
			}
		})
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyConnectors_SpecUploadWarningsReachTheResult(t *testing.T) {
	const warning = `approval gate on "delete_pet" was removed: the connector no longer provides that tool`
	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+platform.APIV2Prefix+connectorsResource.path+"/{id}/resource", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ConnectorResponse{ID: r.PathValue("id"), HasResource: true, Warnings: []string{warning}})
	})

	plan := &connectorKindPlan{Kind: "connector", Updates: []connectorOp{{
		Name: "petstore", ID: "id-1", SpecUploadNeeded: true,
		Spec: &connectorSpecUpload{Filename: "petstore.openapi.yaml", Data: []byte(testSpecContent), Hash: sha256Hex([]byte(testSpecContent))},
	}}}
	outcome := applyConnectors(t.Context(), newV2PlatformClient(t, mux), plan, ApplyOptions{})

	if len(outcome.Failures) != 0 || len(outcome.Updated) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got := outcome.Updated[0].Warnings; len(got) != 1 || got[0] != warning {
		t.Errorf("warnings = %v, want [%s]", got, warning)
	}
}

const gateConsequence = "so a gate on an operation the specification lacks stops the agents bound to this connector from starting, or is not applied when an agent selects operations with an allow_list"

const (
	specWithExternalRef  = "openapi: 3.0.0\ninfo:\n  title: Refs\n  version: 1.0.0\npaths:\n  /pets:\n    $ref: \"http://192.0.2.1/path-item.yaml#/pets\"\n"
	uncheckedSpecWarning = `approval gates on "delete_pet" could not be checked: the connector has no uploaded specification and none could be read from its specification URL, ` + gateConsequence
	unparsedSpecWarning  = `approval gates on "delete_pet" could not be checked: the uploaded specification could not be parsed, ` + gateConsequence
)

func gatedPetResource(t *testing.T, spec string) resolve.ResolvedResource {
	t.Helper()
	return resolvedOpenAPIConnector(t, "petstore", spec, func(values map[string]any) {
		values["hil"] = map[string]any{"tools": map[string]any{"delete_pet": map[string]any{"require_approval": true}}}
	})
}

func TestApplyConnectors_CreateWithGatesAndSpecKeepsTheUncheckedWarningOnlyWhenTheUploadFails(t *testing.T) {
	for name, tc := range map[string]struct {
		spec          string
		uploadStatus  int
		uploadWarning []string
		wantWarnings  []string
		wantFailure   bool
	}{
		"an upload that checks the gates": {
			spec: specWithExternalRef, uploadStatus: http.StatusOK,
		},
		"an upload that cannot parse the spec reports only its own warning": {
			spec: specWithExternalRef, uploadStatus: http.StatusOK,
			uploadWarning: []string{unparsedSpecWarning}, wantWarnings: []string{unparsedSpecWarning},
		},
		"an upload that fails": {
			spec: specWithExternalRef, uploadStatus: http.StatusUnprocessableEntity,
			wantWarnings: []string{uncheckedSpecWarning}, wantFailure: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("POST "+platform.APIV2Prefix+connectorsResource.path, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				writeV2Single(w, http.StatusCreated, wire.ConnectorResponse{ID: "id-1", Name: "petstore", Warnings: []string{uncheckedSpecWarning}})
			})
			mux.HandleFunc("PUT "+platform.APIV2Prefix+connectorsResource.path+"/{id}/resource", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if tc.uploadStatus != http.StatusOK {
					writeV2Error(w, tc.uploadStatus, "Validation failed", nil)
					return
				}
				writeV2Single(w, http.StatusOK, wire.ConnectorResponse{ID: r.PathValue("id"), HasResource: true, Warnings: tc.uploadWarning})
			})

			plan, err := planConnectors(t.Context(), nil, []resolve.ResolvedResource{gatedPetResource(t, tc.spec)}, nil)
			if err != nil {
				t.Fatalf("planConnectors: %v", err)
			}
			outcome := applyConnectors(t.Context(), newV2PlatformClient(t, mux), plan, ApplyOptions{})

			var res connectorOpResult
			switch {
			case tc.wantFailure && len(outcome.Failures) == 1 && len(outcome.Created) == 0:
				res = outcome.Failures[0]
				if res.Err == nil || !strings.Contains(res.Err.Error(), "upload spec") {
					t.Errorf("err = %v, want the upload error", res.Err)
				}
			case !tc.wantFailure && len(outcome.Failures) == 0 && len(outcome.Created) == 1:
				res = outcome.Created[0]
			default:
				t.Fatalf("outcome = %+v", outcome)
			}
			if !slices.Equal(res.Warnings, tc.wantWarnings) {
				t.Errorf("warnings = %q, want %q", res.Warnings, tc.wantWarnings)
			}
		})
	}
}

func TestApplyConnectors_UpdateWithSpecAndPatchReportsTheUncheckedWarningOnce(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+platform.APIV2Prefix+connectorsResource.path+"/{id}/resource", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ConnectorResponse{ID: r.PathValue("id"), HasResource: true, Warnings: []string{unparsedSpecWarning}})
	})
	mux.HandleFunc("PATCH "+platform.APIV2Prefix+connectorsResource.path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeV2Single(w, http.StatusOK, wire.ConnectorResponse{ID: r.PathValue("id"), Warnings: []string{unparsedSpecWarning}})
	})
	plan := &connectorKindPlan{Kind: "connector", Updates: []connectorOp{{
		Name: "petstore", ID: "id-1", PatchNeeded: true, SpecUploadNeeded: true,
		Spec: &connectorSpecUpload{Filename: "petstore.openapi.yaml", Data: []byte(specWithExternalRef), Hash: sha256Hex([]byte(specWithExternalRef))},
	}}}

	outcome := applyConnectors(t.Context(), newV2PlatformClient(t, mux), plan, ApplyOptions{})

	if len(outcome.Failures) != 0 || len(outcome.Updated) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if got := outcome.Updated[0].Warnings; !slices.Equal(got, []string{unparsedSpecWarning}) {
		t.Errorf("warnings = %q, want the one from the PATCH", got)
	}
}
