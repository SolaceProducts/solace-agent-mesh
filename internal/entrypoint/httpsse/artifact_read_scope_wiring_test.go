// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestProjectMemberReadsOnlyTheProjectsOwnFilesThroughTheAdapterRoutes(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service":       map[string]any{"type": "sql", "database_url": testDBURL(t, "readscope")},
	})
	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	arts := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "test-ns"}
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(flags), runtime.WithArtifacts(arts)).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Store().Close() })
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })

	project := &store.Project{Name: "shared", UserID: createOwnerAlice}
	if err := store.NewProjectRepo(core.Store()).Create(t.Context(), project); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	core.SetProjectSharingService(&mockProjectSharingService{hasAccess: true, sharedIDs: []string{project.ID}})

	save := func(session, filename, content string) {
		t.Helper()
		if _, err := arts.Inner.Save(t.Context(), arts.Resolve("", createOwnerAlice, session, filename), []byte(content), "text/plain"); err != nil {
			t.Fatalf("save %s: %v", filename, err)
		}
	}
	save(projectSessionPrefix+project.ID, projectFileName, projectFileBytes)
	save(otherSessionID, userScopedName, userScopedSecret)
	save(otherSessionID, userScopedName, userScopedSecret)

	serve := func(user, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("X-SAM-User-ID", user)
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, req)
		return rec
	}
	names := func(rec *httptest.ResponseRecorder) []string {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data []struct {
				Filename string `json:"filename"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := make([]string, 0, len(body.Data))
		for _, d := range body.Data {
			out = append(out, d.Filename)
		}
		slices.Sort(out)
		return out
	}

	query := "?projectId=" + project.ID
	userScoped := "/api/v1/artifacts/x/user%3Anotes.md"
	readRoutes := map[string]string{
		"latest":         userScoped + query,
		"preview":        userScoped + query + "&maxBytes=8",
		"versions":       userScoped + "/versions" + query,
		"version 0":      userScoped + "/versions/0" + query,
		"version 1":      userScoped + "/versions/1" + query,
		"version latest": userScoped + "/versions/latest" + query,
	}
	for name, target := range readRoutes {
		t.Run("the member is refused the owner's user-scoped file: "+name, func(t *testing.T) {
			rec := serve(createOwnerBob, target)
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), userScopedSecret) {
				t.Errorf("status = %d body = %q, want 404 without the bytes", rec.Code, rec.Body.String())
			}
			if rec = serve(createOwnerAlice, target); rec.Code != http.StatusOK {
				t.Errorf("the owner: status = %d, want 200", rec.Code)
			}
		})
	}

	t.Run("the member reads the project's own file", func(t *testing.T) {
		rec := serve(createOwnerBob, "/api/v1/artifacts/x/"+projectFileName+query)
		if rec.Code != http.StatusOK || rec.Body.String() != projectFileBytes {
			t.Errorf("status = %d body = %q, want 200 %q", rec.Code, rec.Body.String(), projectFileBytes)
		}
	})

	listings := map[string]string{
		"artifact list":      "/api/v1/artifacts/x" + query,
		"artifact list, all": "/api/v1/artifacts/x" + query + "&visibility=all",
		"artifacts/all":      "/api/v1/artifacts/all?pageSize=100",
		"project artifacts":  "/api/v1/projects/" + project.ID + "/artifacts",
	}
	for name, target := range listings {
		t.Run("listing for the member: "+name, func(t *testing.T) {
			if got := names(serve(createOwnerBob, target)); !slices.Equal(got, []string{projectFileName}) {
				t.Errorf("member sees %v, want only the project's own file", got)
			}
		})
	}
	t.Run("listing for the owner", func(t *testing.T) {
		if got := names(serve(createOwnerAlice, "/api/v1/artifacts/x"+query)); !slices.Equal(got, []string{projectFileName, userScopedName}) {
			t.Errorf("owner sees %v, want both files", got)
		}
	})

	t.Run("the member's project count leaves the owner's user-scoped file out", func(t *testing.T) {
		rec := serve(createOwnerBob, "/api/v1/projects/"+project.ID)
		var got ProjectResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || got.ArtifactCount == nil || *got.ArtifactCount != 1 {
			t.Errorf("status = %d body = %s, want artifactCount 1", rec.Code, rec.Body.String())
		}
	})
}
