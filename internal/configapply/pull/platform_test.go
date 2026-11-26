// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type pullPlatformFakeServer struct {
	current            wire.ProfileProviderResponse
	toolsets           []wire.ToolsetResponse
	webuiSettings      wire.WebuiSettingsResponse
	defaultRolesStatus int
}

func newPullPlatformFakeServer(t *testing.T, srv *pullPlatformFakeServer) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/profileProvider", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, srv.current)
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsets", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, srv.toolsets)
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/rbac/defaultRoles", func(w http.ResponseWriter, _ *http.Request) {
		if srv.defaultRolesStatus != 0 {
			writeV2Error(w, srv.defaultRolesStatus, http.StatusText(srv.defaultRolesStatus))
			return
		}
		writeV2Single(w, wire.DefaultRolesResponse{})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/webuiSettings", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, srv.webuiSettings)
	})
	return newV2Platform(t, mux)
}

func TestPullPlatformBlock_UnconfiguredReturnsNil(t *testing.T) {
	srv := &pullPlatformFakeServer{current: wire.ProfileProviderResponse{LookupKey: "email"}}
	httpsrv := newPullPlatformFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	block, err := pullPlatformBlock(t.Context(), c, Options{}, &Result{})
	if err != nil {
		t.Fatalf("pullPlatformBlock: %v", err)
	}
	if block != nil {
		t.Errorf("block = %+v; want nil for unconfigured state", block)
	}
}

func TestPullPlatformBlock_DefaultRolesFetchFailure(t *testing.T) {
	for _, tt := range []struct {
		status  int
		wantErr bool
	}{
		{status: http.StatusNotImplemented, wantErr: false},
		{status: http.StatusInternalServerError, wantErr: true},
	} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			httpsrv := newPullPlatformFakeServer(t, &pullPlatformFakeServer{defaultRolesStatus: tt.status})
			c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})
			res := &Result{}

			_, err := pullPlatformBlock(t.Context(), c, Options{}, res)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && len(res.Warnings) != 1 {
				t.Errorf("warnings = %v, want one feature-off warning", res.Warnings)
			}
		})
	}
}

func TestPullPlatformBlock_OnlyKindSkipsWhenNonToolset(t *testing.T) {
	tsID := "uuid-1"
	srv := &pullPlatformFakeServer{
		current:  wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"},
		toolsets: []wire.ToolsetResponse{{ID: "uuid-1", Name: "bamboohr"}},
	}
	httpsrv := newPullPlatformFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	block, err := pullPlatformBlock(t.Context(), c, Options{OnlyKind: "agent"}, &Result{})
	if err != nil {
		t.Fatalf("pullPlatformBlock: %v", err)
	}
	if block != nil {
		t.Errorf("block = %+v; want nil when OnlyKind=agent", block)
	}
}

func TestPullPlatformBlock_NoScopedPullEmitsTheBlock(t *testing.T) {
	tsID := "uuid-1"
	for _, only := range []string{"toolset", "agent", "model"} {
		t.Run(only, func(t *testing.T) {
			srv := &pullPlatformFakeServer{
				current:  wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"},
				toolsets: []wire.ToolsetResponse{{ID: "uuid-1", Name: "bamboohr"}},
			}
			httpsrv := newPullPlatformFakeServer(t, srv)
			c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

			block, err := pullPlatformBlock(t.Context(), c, Options{OnlyKind: only}, &Result{})
			if err != nil {
				t.Fatalf("pullPlatformBlock: %v", err)
			}
			if block != nil {
				t.Errorf("block = %+v; want nil — a scoped pull manages no platform singletons", block)
			}
		})
	}
}

func TestPullPlatformBlock_FullPullEmitsEverySingleton(t *testing.T) {
	tsID := "uuid-1"
	srv := &pullPlatformFakeServer{
		current:  wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"},
		toolsets: []wire.ToolsetResponse{{ID: "uuid-1", Name: "bamboohr"}},
	}
	httpsrv := newPullPlatformFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	block, err := pullPlatformBlock(t.Context(), c, Options{}, &Result{})
	if err != nil {
		t.Fatalf("pullPlatformBlock: %v", err)
	}
	if block == nil || block.ProfileProvider == nil {
		t.Fatalf("block = %+v; want non-nil with profileProvider populated", block)
	}
	if block.ProfileProvider.Toolset != "bamboohr" {
		t.Errorf("toolset = %q; want bamboohr", block.ProfileProvider.Toolset)
	}
}

func TestPullPlatformBlock_DanglingToolsetIDIsHardError(t *testing.T) {
	tsID := "uuid-missing"
	srv := &pullPlatformFakeServer{
		current:  wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"},
		toolsets: []wire.ToolsetResponse{{ID: "uuid-1", Name: "bamboohr"}},
	}
	httpsrv := newPullPlatformFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	_, err := pullPlatformBlock(t.Context(), c, Options{}, &Result{})
	if err == nil {
		t.Fatal("expected error for wired toolset ID missing from the platform's list")
	}
}

func TestPullPlatformBlock_ScopedPullOmitsEvenAnUnrelatedSingleton(t *testing.T) {
	appName := "Acme Assistant"
	tsID := "uuid-1"
	srv := &pullPlatformFakeServer{
		current:       wire.ProfileProviderResponse{ToolsetID: &tsID, LookupKey: "email"},
		toolsets:      []wire.ToolsetResponse{{ID: "uuid-1", Name: "bamboohr"}},
		webuiSettings: wire.WebuiSettingsResponse{AppName: &appName},
	}
	httpsrv := newPullPlatformFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	block, err := pullPlatformBlock(t.Context(), c, Options{OnlyKind: "agent"}, &Result{})
	if err != nil {
		t.Fatalf("pullPlatformBlock: %v", err)
	}
	if block != nil {
		t.Errorf("block = %+v; want nil — emitting webuiSettings alone asserts removal of the singletons --only skipped", block)
	}
}
