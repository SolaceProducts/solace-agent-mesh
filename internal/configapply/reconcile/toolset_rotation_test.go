// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func unchangedToolsetOp(id string, redacted bool) toolsetOp {
	op := toolsetOp{
		Name: "celebrate_tools", ID: id, Verb: "noop",
		Desired: &toolsetDesired{name: "celebrate_tools", config: map[string]any{"database_url": "postgres://new"}},
	}
	if redacted {
		op.SecretConfigKeys = []string{"config.database_url"}
	}
	return op
}

func TestReclassifyToolsetRotations_Rotated_PromotesToUpdate(t *testing.T) {
	plan := &toolsetPlan{Kind: "toolset", Unchanged: []toolsetOp{unchangedToolsetOp("id-1", true)}}
	differ := &stubSecretDiffer{matchByID: map[string]bool{"id-1": false}}

	if err := reclassifyToolsetRotations(t.Context(), differ, plan); err != nil {
		t.Fatal(err)
	}

	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-1" {
		t.Fatalf("Updates = %+v; expected the rotated toolset promoted", plan.Updates)
	}
	if len(plan.Unchanged) != 0 {
		t.Errorf("Unchanged = %+v; expected empty after reclassification", plan.Unchanged)
	}
	op := plan.Updates[0]
	if !op.ConfigChanged {
		t.Errorf("promoted op missing ConfigChanged=true; apply would skip PATCH /config")
	}
	if op.UploadNeeded {
		t.Errorf("promoted op set UploadNeeded=true; a secret rotation needs no bundle re-upload")
	}
	if len(differ.calls) != 1 || differ.calls[0].kind != "toolsets" || differ.calls[0].id != "id-1" {
		t.Errorf("expected one DiffSecrets call for toolsets/id-1; got %+v", differ.calls)
	}
	vals, _ := differ.calls[0].values.(map[string]any)
	if vals["database_url"] != "postgres://new" {
		t.Errorf("candidate values = %+v; want the desired flat config", differ.calls[0].values)
	}
}

func TestReclassifyToolsetRotations_Matches_StaysUnchanged(t *testing.T) {
	plan := &toolsetPlan{Kind: "toolset", Unchanged: []toolsetOp{unchangedToolsetOp("id-1", true)}}
	differ := &stubSecretDiffer{matchByID: map[string]bool{"id-1": true}}

	if err := reclassifyToolsetRotations(t.Context(), differ, plan); err != nil {
		t.Fatal(err)
	}

	if len(plan.Unchanged) != 1 {
		t.Fatalf("Unchanged = %+v; expected the op to stay when secrets match", plan.Unchanged)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("Updates = %+v; expected empty when secrets match", plan.Updates)
	}
}

func TestReclassifyToolsetRotations_NoRedacted_SkipsDiffer(t *testing.T) {
	plan := &toolsetPlan{Kind: "toolset", Unchanged: []toolsetOp{unchangedToolsetOp("id-1", false)}}
	differ := &stubSecretDiffer{matchByID: map[string]bool{"id-1": false}}

	if err := reclassifyToolsetRotations(t.Context(), differ, plan); err != nil {
		t.Fatal(err)
	}

	if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
		t.Fatalf("unchanged=%d updates=%d; expected op left alone", len(plan.Unchanged), len(plan.Updates))
	}
	if len(differ.calls) != 0 {
		t.Errorf("expected zero diff calls when nothing was redacted; got %+v", differ.calls)
	}
}

func TestReclassifyToolsetRotations_DifferError_Fails(t *testing.T) {
	plan := &toolsetPlan{Kind: "toolset", Unchanged: []toolsetOp{unchangedToolsetOp("id-1", true)}}
	diffErr := errors.New("network unreachable")
	differ := &stubSecretDiffer{errByID: map[string]error{"id-1": diffErr}}

	if err := reclassifyToolsetRotations(t.Context(), differ, plan); !errors.Is(err, diffErr) {
		t.Fatalf("reclassifyToolsetRotations error = %v; want it to wrap %v", err, diffErr)
	}
}

func TestReclassifyToolsetRotations_DifferForbidden_NamesScope(t *testing.T) {
	plan := &toolsetPlan{Kind: "toolset", Unchanged: []toolsetOp{unchangedToolsetOp("id-1", true)}}
	differ := &stubSecretDiffer{errByID: map[string]error{"id-1": &HTTPError{StatusCode: http.StatusForbidden}}}

	err := reclassifyToolsetRotations(t.Context(), differ, plan)
	if err == nil {
		t.Fatal("reclassifyToolsetRotations succeeded; want an error on 403")
	}
	for _, want := range []string{`toolset "celebrate_tools"`, "config.database_url", rbac.ScopeToolsetUpdate} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestReclassifyToolsetRotations_NilDiffer_NoOp(t *testing.T) {
	plan := &toolsetPlan{Kind: "toolset", Unchanged: []toolsetOp{unchangedToolsetOp("id-1", true)}}

	if err := reclassifyToolsetRotations(t.Context(), nil, plan); err != nil {
		t.Fatal(err)
	}

	if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
		t.Errorf("unchanged=%d updates=%d; expected plan untouched with nil differ", len(plan.Unchanged), len(plan.Updates))
	}
}
