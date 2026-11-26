// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

type profileProviderServer struct {
	current wire.ProfileProviderResponse

	puts    atomic.Int32
	deletes atomic.Int32

	toolsets []wire.ToolsetResponse
}

func newProfileProviderServer(t *testing.T) (*profileProviderServer, *httptest.Server) {
	t.Helper()
	srv := &profileProviderServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/profileProvider", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, srv.current)
	})
	mux.HandleFunc("PUT /api/v2/platform/profileProvider", func(w http.ResponseWriter, r *http.Request) {
		var body wire.ReplaceProfileProviderRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeV2Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		srv.puts.Add(1)
		tsID := body.ToolsetID
		srv.current = wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: body.LookupKey}
		writeV2Single(w, http.StatusOK, srv.current)
	})
	mux.HandleFunc("DELETE /api/v2/platform/profileProvider", func(w http.ResponseWriter, _ *http.Request) {
		srv.deletes.Add(1)
		srv.current = wire.ProfileProviderResponse{}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v2/platform/toolsets", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, srv.toolsets)
	})
	return srv, newV2Platform(t, mux)
}

func newProfileProviderClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return NewClient(target.Resolved{URL: srv.URL})
}

func TestPlanProfileProvider_NoCurrent_DesiredSet(t *testing.T) {
	srv, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)
	srv.toolsets = []wire.ToolsetResponse{{ID: "uuid-1", Name: "bamboohr"}}

	lookup := newToolsetLookup(srv.toolsets, nil)
	desired := &manifest.ProfileProviderSpec{Toolset: "bamboohr", Claim: "email"}
	plan, err := planProfileProvider(t.Context(), c, desired, lookup)
	if err != nil {
		t.Fatalf("planProfileProvider: %v", err)
	}
	if plan.Verb != profileProviderVerbPut {
		t.Errorf("Verb = %q; want %q", plan.Verb, profileProviderVerbPut)
	}
	if plan.ToolsetID != "uuid-1" {
		t.Errorf("ToolsetID = %q; want uuid-1", plan.ToolsetID)
	}
}

func TestPlanProfileProvider_Unchanged(t *testing.T) {
	srv, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)
	srv.toolsets = []wire.ToolsetResponse{{ID: "uuid-1", Name: "bamboohr"}}
	tsID := "uuid-1"
	srv.current = wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"}

	lookup := newToolsetLookup(srv.toolsets, nil)
	desired := &manifest.ProfileProviderSpec{Toolset: "bamboohr", Claim: "email"}
	plan, err := planProfileProvider(t.Context(), c, desired, lookup)
	if err != nil {
		t.Fatalf("planProfileProvider: %v", err)
	}
	if plan.Verb != profileProviderVerbUnchanged {
		t.Errorf("Verb = %q; want %q", plan.Verb, profileProviderVerbUnchanged)
	}
}

func TestPlanProfileProvider_Drift(t *testing.T) {
	srv, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)
	srv.toolsets = []wire.ToolsetResponse{
		{ID: "uuid-1", Name: "bamboohr"},
		{ID: "uuid-2", Name: "okta"},
	}
	tsID := "uuid-1"
	srv.current = wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"}

	lookup := newToolsetLookup(srv.toolsets, nil)
	desired := &manifest.ProfileProviderSpec{Toolset: "okta", Claim: "email"}
	plan, err := planProfileProvider(t.Context(), c, desired, lookup)
	if err != nil {
		t.Fatalf("planProfileProvider: %v", err)
	}
	if plan.Verb != profileProviderVerbPut {
		t.Errorf("Verb = %q; want %q (drift on toolset)", plan.Verb, profileProviderVerbPut)
	}
	if plan.ToolsetID != "uuid-2" {
		t.Errorf("ToolsetID = %q; want uuid-2", plan.ToolsetID)
	}
}

func TestPlanProfileProvider_BlockRemoved(t *testing.T) {
	srv, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)
	tsID := "uuid-1"
	srv.current = wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"}

	plan, err := planProfileProvider(t.Context(), c, nil, newToolsetLookup(nil, nil))
	if err != nil {
		t.Fatalf("planProfileProvider: %v", err)
	}
	if plan.Verb != profileProviderVerbDelete {
		t.Errorf("Verb = %q; want %q", plan.Verb, profileProviderVerbDelete)
	}
}

func TestPlanProfileProvider_DanglingToolset(t *testing.T) {
	_, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)

	desired := &manifest.ProfileProviderSpec{Toolset: "missing", Claim: "email"}
	_, err := planProfileProvider(t.Context(), c, desired, newToolsetLookup(nil, nil))
	if err == nil {
		t.Fatal("expected dangling-toolset error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error = %v; want it to name the missing toolset", err)
	}
}

func TestPlanProfileProvider_InFlightToolsetIsSentinel(t *testing.T) {
	_, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)

	lookup := newToolsetLookup(nil, []string{"in-flight"})
	desired := &manifest.ProfileProviderSpec{Toolset: "in-flight", Claim: "email"}
	plan, err := planProfileProvider(t.Context(), c, desired, lookup)
	if err != nil {
		t.Fatalf("planProfileProvider: %v", err)
	}
	if plan.ToolsetID != "in-flight" {
		t.Errorf("ToolsetID = %q; want sentinel %q", plan.ToolsetID, "in-flight")
	}
}

func TestApplyProfileProvider_Put(t *testing.T) {
	srv, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)
	srv.toolsets = []wire.ToolsetResponse{{ID: "uuid-1", Name: "bamboohr"}}

	plan := &profileProviderPlan{
		Verb:        profileProviderVerbPut,
		ToolsetID:   "uuid-1",
		ToolsetName: "bamboohr",
		LookupKey:   "email",
	}
	out := applyProfileProvider(t.Context(), c, plan, ApplyOptions{})
	if out == nil || len(out.Failures) != 0 {
		t.Fatalf("apply had failures: %+v", out)
	}
	if out.Applied == nil || out.Applied.Verb != profileProviderVerbPut {
		t.Errorf("Applied = %+v; want PUT", out.Applied)
	}
	if got := srv.puts.Load(); got != 1 {
		t.Errorf("server PUTs = %d; want 1", got)
	}
	if srv.current.ToolsetID == nil || *srv.current.ToolsetID != "uuid-1" {
		t.Errorf("server current ToolsetID = %+v; want uuid-1", srv.current.ToolsetID)
	}
}

func TestApplyProfileProvider_Delete(t *testing.T) {
	srv, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)
	tsID := "uuid-1"
	srv.current = wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"}

	plan := &profileProviderPlan{Verb: profileProviderVerbDelete}
	out := applyProfileProvider(t.Context(), c, plan, ApplyOptions{Prune: true})
	if out == nil || len(out.Failures) != 0 {
		t.Fatalf("apply had failures: %+v", out)
	}
	if out.Applied == nil || out.Applied.Verb != profileProviderVerbDelete {
		t.Errorf("Applied = %+v; want DELETE", out.Applied)
	}
	if got := srv.deletes.Load(); got != 1 {
		t.Errorf("server DELETEs = %d; want 1", got)
	}
}

func TestApplyProfileProvider_DeleteIsPruneGated(t *testing.T) {
	srv, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)
	tsID := "uuid-1"
	srv.current = wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"}

	out := applyProfileProvider(t.Context(), c, &profileProviderPlan{Verb: profileProviderVerbDelete}, ApplyOptions{})
	if out == nil || out.Skipped == nil || out.Skipped.Verb != profileProviderVerbDelete {
		t.Fatalf("out = %+v; want the delete skipped without --prune", out)
	}
	if got := srv.deletes.Load(); got != 0 {
		t.Errorf("server DELETEs = %d; want 0 — a removal must not happen without --prune", got)
	}
}

func TestApplyProfileProvider_UnmanagedHasNoOutcome(t *testing.T) {
	if out := applyProfileProvider(t.Context(), nil, &profileProviderPlan{Verb: profileProviderVerbNone}, ApplyOptions{}); out != nil {
		t.Errorf("out = %+v; want nil for an unmanaged profileProvider", out)
	}
}

func TestApplyProfileProvider_Unchanged(t *testing.T) {
	srv, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)

	plan := &profileProviderPlan{Verb: profileProviderVerbUnchanged}
	out := applyProfileProvider(t.Context(), c, plan, ApplyOptions{Prune: true})
	if out == nil {
		t.Fatal("outcome nil")
	}
	if len(out.Failures) != 0 {
		t.Errorf("unexpected failures: %+v", out.Failures)
	}
	if out.Applied != nil {
		t.Errorf("Applied = %+v; want nil for unchanged", out.Applied)
	}
	if got := srv.puts.Load() + srv.deletes.Load(); got != 0 {
		t.Errorf("server saw %d mutations; want 0", got)
	}
}

func TestCurrentMatches_EmptyClaim(t *testing.T) {
	id := "uuid-1"
	current := &wire.ProfileProviderResponse{ToolsetID: &id, LookupKey: "preferred_username"}
	if !currentMatches(current, "uuid-1", "") {
		t.Errorf("empty desired claim should match any current LookupKey; got drift")
	}
	if currentMatches(current, "uuid-2", "") {
		t.Errorf("toolset drift must still register even with empty claim")
	}
}

func TestApplyProfileProvider_SentinelResolvedAfterToolsetsPhase(t *testing.T) {
	srv, httpsrv := newProfileProviderServer(t)
	c := newProfileProviderClient(t, httpsrv)
	srv.toolsets = []wire.ToolsetResponse{{ID: "uuid-new", Name: "in-flight"}}

	plan := &profileProviderPlan{
		Verb:        profileProviderVerbPut,
		ToolsetID:   "in-flight",
		ToolsetName: "in-flight",
		LookupKey:   "email",
	}
	out := applyProfileProvider(t.Context(), c, plan, ApplyOptions{Prune: true})
	if out == nil || len(out.Failures) != 0 {
		t.Fatalf("apply had failures: %+v", out)
	}
	if srv.current.ToolsetID == nil || *srv.current.ToolsetID != "uuid-new" {
		t.Errorf("server current ToolsetID = %+v; want uuid-new", srv.current.ToolsetID)
	}
}
