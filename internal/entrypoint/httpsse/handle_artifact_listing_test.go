// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type selfReferentialWrapper struct {
	artifact.Service
}

func (s *selfReferentialWrapper) Inner() artifact.Service { return s }

func TestUnwrapArtifactService(t *testing.T) {
	_, artSvc, _ := setupArtifactHandler(t)
	base := artSvc.Inner

	if got := unwrapArtifactService(base); got != base {
		t.Error("bare backend should return itself")
	}
	if got := unwrapArtifactService(instrumentationLikeWrapper{Service: base}); got != base {
		t.Error("single decorator should unwrap to the backend")
	}
	if got := unwrapArtifactService(instrumentationLikeWrapper{Service: instrumentationLikeWrapper{Service: base}}); got != base {
		t.Error("nested decorators should unwrap to the backend")
	}
	if got := unwrapArtifactService(instrumentationLikeWrapper{Service: nil}); got == nil {
		t.Error("nil-Inner decorator should return the (non-nil) decorator, not nil")
	}
	if got := unwrapArtifactService(&selfReferentialWrapper{}); got == nil {
		t.Error("cyclic chain should terminate with a non-nil service")
	}
}

func TestArtifactList_UnwrapsInstrumentationForVersionLister(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	ctx := context.Background()
	sess := idgen.New()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if _, err := artSvc.Inner.Save(ctx, artSvc.Resolve("", "sam_dev_user", sess, name), []byte("x"), "text/plain"); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	counting := &listCountingStore{Service: artSvc.Inner}
	artSvc.Inner = instrumentationLikeWrapper{Service: counting}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/"+sess, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	listVersions, listArtifactVersions := counting.counts()
	if listArtifactVersions == 0 {
		t.Error("single-session list must engage ListArtifactVersions through the instrumentation wrapper, got 0 calls")
	}
	if listVersions != 0 {
		t.Errorf("per-artifact ListVersions fallback must not run when the version lister is reachable; got %d calls", listVersions)
	}
}

func TestScopeArtifactVersions_FallbackBackend(t *testing.T) {
	fsStore := filesystem.New(t.TempDir())
	if _, ok := artifact.Service(fsStore).(scopeArtifactVersionLister); ok {
		t.Fatal("filesystem now implements scopeArtifactVersionLister; this test no longer exercises the fallback")
	}

	artSvc := &artifact.ScopedService{Inner: fsStore, Scope: "namespace", Namespace: "test-ns"}
	ctx := context.Background()
	sess := "sess-1"

	for range 3 {
		_, _ = artSvc.Inner.Save(ctx, artSvc.Resolve("", "u1", sess, "data.txt"), []byte("x"), "text/plain")
	}
	_, _ = artSvc.Inner.Save(ctx, artSvc.Resolve("", "u1", sess, "a.txt"), []byte("a"), "text/plain")
	_, _ = artSvc.Inner.Save(ctx, artSvc.Resolve("", "u1", sess, "user:profile.json"), []byte("p"), "application/json")

	scope := artifact.KeyScope{AppName: "test-ns", UserID: "u1", SessionID: sess}
	got, err := scopeArtifactVersions(ctx, artSvc, scope)
	if err != nil {
		t.Fatal(err)
	}

	keys, err := fsStore.ListKeys(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string][]int, len(keys))
	for _, k := range keys {
		vs, err := fsStore.ListVersions(ctx, artSvc.Resolve("", "u1", sess, k))
		if err != nil {
			t.Fatal(err)
		}
		want[k] = vs
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback scopeArtifactVersions = %v, want %v", got, want)
	}

	if !reflect.DeepEqual(got["data.txt"], []int{0, 1, 2}) {
		t.Errorf("data.txt versions = %v, want [0 1 2]", got["data.txt"])
	}
	if len(got) != 3 {
		t.Errorf("expected 3 artifacts (incl user-scoped), got %d: %v", len(got), got)
	}
	if _, ok := got["user:profile.json"]; !ok {
		t.Errorf("user-scoped artifact missing from fallback result: %v", got)
	}
}

func TestArtifactList_MultiVersionFastPath(t *testing.T) {
	h, artSvc, _ := setupArtifactHandler(t)
	ctx := context.Background()
	sess := idgen.New()

	for range 3 {
		_, _ = artSvc.Inner.Save(ctx, artSvc.Resolve("", "sam_dev_user", sess, "report.pdf"), []byte("x"), "application/pdf")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artifacts/{id}", h.handleList)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/artifacts/"+sess, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[ArtifactInfoResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 artifact, got %d: %v", len(resp.Data), resp.Data)
	}
	a := resp.Data[0]
	if a.Version == nil || *a.Version != 2 {
		t.Errorf("latest version = %v, want 2", a.Version)
	}
	if a.VersionCount == nil || *a.VersionCount != 3 {
		t.Errorf("version count = %v, want 3", a.VersionCount)
	}
}

func TestListScopeArtifactInfos_ParallelOrderingAndMetadata(t *testing.T) {
	_, artSvc, _ := setupArtifactHandler(t)
	ctx := context.Background()
	sess := idgen.New()

	const n = 40
	wantNames := make([]string, 0, n)
	for i := range n {
		name := fmt.Sprintf("report-%02d.csv", i)
		wantNames = append(wantNames, name)
		_, err := artifact.SaveWithMetadata(ctx, artSvc,
			"", "sam_dev_user", sess, name,
			[]byte("a,b\n1,2"), "text/csv",
			artifact.MetadataOpts{
				SuppressSchema: true,
				Source:         fmt.Sprintf("src-%02d", i),
				Tags:           []string{fmt.Sprintf("tag-%02d", i)},
			}, slog.Default())
		if err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	slices.Sort(wantNames)

	infos, err := listScopeArtifactInfos(ctx, artSvc, "sam_dev_user", sess)
	if err != nil {
		t.Fatalf("listScopeArtifactInfos: %v", err)
	}
	if len(infos) != n {
		t.Fatalf("got %d artifacts, want %d", len(infos), n)
	}

	gotNames := make([]string, len(infos))
	for i, info := range infos {
		gotNames[i] = info.Filename
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("names not complete/sorted:\n got %v\nwant %v", gotNames, wantNames)
	}

	for _, info := range infos {
		var idx int
		if _, err := fmt.Sscanf(info.Filename, "report-%02d.csv", &idx); err != nil {
			t.Fatalf("unexpected filename %q: %v", info.Filename, err)
		}
		wantTag := fmt.Sprintf("tag-%02d", idx)
		if !slices.Contains(info.Tags, wantTag) {
			t.Errorf("%s: tags %v missing %q (companion metadata mismatch)", info.Filename, info.Tags, wantTag)
		}
		wantSrc := fmt.Sprintf("src-%02d", idx)
		if info.Source == nil || *info.Source != wantSrc {
			t.Errorf("%s: source = %v, want %q", info.Filename, info.Source, wantSrc)
		}
		if info.MIMEType == nil || *info.MIMEType != "text/csv" {
			t.Errorf("%s: mime = %v, want text/csv (version metadata mismatch)", info.Filename, info.MIMEType)
		}
	}
}
