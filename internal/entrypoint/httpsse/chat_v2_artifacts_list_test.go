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
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type artifactListBody[T any] struct {
	Data []T `json:"data"`
	Meta struct {
		Pagination apicontract.PaginationMeta `json:"pagination"`
	} `json:"meta"`
}

func decodeArtifactList[T any](t *testing.T, rec *httptest.ResponseRecorder) artifactListBody[T] {
	t.Helper()
	var body artifactListBody[T]
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v (body %s)", err, rec.Body)
	}
	return body
}

func newV2ArtifactLists(t *testing.T) *v2Artifacts {
	t.Helper()
	h, svc, sess := setupArtifactHandler(t)
	h.allCache = newArtifactCache(time.Minute, 200)
	routes := h.chatV2Routes()
	mux := http.NewServeMux()
	apicontract.Mount(mux, chatv2.Prefix, routes, chatV2Guard, nil)
	return &v2Artifacts{t: t, h: h, svc: svc, sess: sess, mux: mux, routes: routes}
}

func (f *v2Artifacts) listed(path, caller string) *httptest.ResponseRecorder {
	return f.call(http.MethodGet, path, caller)
}

func (f *v2Artifacts) fileNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", rec.Code, rec.Body)
	}
	body := decodeArtifactList[chatv2.ChatArtifact](t, rec)
	names := make([]string, 0, len(body.Data))
	for _, a := range body.Data {
		names = append(names, a.ID)
	}
	slices.Sort(names)
	return names
}

func TestChatV2ArtifactSessionList_ShowsStoredFilesAndHidesWorkingUnlessAll(t *testing.T) {
	f := newV2ArtifactLists(t)
	session, _ := f.store(v2Alice, "report.txt", "report")
	if _, err := artifact.SaveWithMetadata(t.Context(), f.svc, "", v2Alice, session, "spill.json",
		[]byte(`{"big":true}`), "application/json",
		artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagWorking}}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("save working artifact: %v", err)
	}

	visible := f.fileNames(t, f.listed("/sessions/"+session+"/artifacts", v2Alice))
	if !slices.Equal(visible, []string{"report.txt"}) {
		t.Errorf("visible list = %v, want [report.txt]", visible)
	}

	all := f.fileNames(t, f.listed("/sessions/"+session+"/artifacts?visibility=all", v2Alice))
	if !slices.Equal(all, []string{"report.txt", "spill.json"}) {
		t.Errorf("visibility=all list = %v, want both files", all)
	}
}

func TestChatV2ArtifactSessionList_EmptySessionIsAnEmptyArrayNotNull(t *testing.T) {
	f := newV2ArtifactLists(t)
	session := idgen.New()
	if err := f.sess.Create(context.Background(), &store.Session{ID: session, UserID: v2Alice}); err != nil {
		t.Fatal(err)
	}

	rec := f.listed("/sessions/"+session+"/artifacts", v2Alice)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw.Data) != "[]" {
		t.Errorf("data = %s, want []", raw.Data)
	}
}

func TestChatV2ArtifactSessionList_AnotherUsersSessionListsNothingOfTheOwners(t *testing.T) {
	f := newV2ArtifactLists(t)
	session, _ := f.store(v2Alice, "secret.txt", "alice only")

	if got := f.fileNames(t, f.listed("/sessions/"+session+"/artifacts", v2Bob)); len(got) != 0 {
		t.Errorf("bob listed %v from alice's session, want nothing", got)
	}
}

func TestChatV2ArtifactListAll_PagesAndFiltersBySearch(t *testing.T) {
	f := newV2ArtifactLists(t)
	f.store(v2Alice, "alpha-report.txt", "a")
	f.store(v2Alice, "beta-notes.txt", "b")

	page := decodeArtifactList[chatv2.ChatArtifact](t, f.listed("/artifacts?pageSize=1", v2Alice))
	if len(page.Data) != 1 || page.Meta.Pagination.Count != 2 {
		t.Errorf("page of size 1 = %d items, count %d; want 1 item of count 2", len(page.Data), page.Meta.Pagination.Count)
	}

	searched := decodeArtifactList[chatv2.ChatArtifact](t, f.listed("/artifacts?search=REPORT", v2Alice))
	if len(searched.Data) != 1 || searched.Data[0].ID != "alpha-report.txt" || searched.Meta.Pagination.Count != 1 {
		t.Errorf("search = %+v, want only alpha-report.txt", searched.Data)
	}
}

func TestChatV2ArtifactListAll_ProjectFilesCarryANullSessionID(t *testing.T) {
	f := newV2ArtifactLists(t)
	f.h.projectRepo = store.NewProjectRepo(f.h.core.Store())
	project := &store.Project{Name: "Specs", UserID: v2Alice}
	if err := f.h.projectRepo.Create(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	seedArtifacts(t, f.h, v2Alice, projectSessionPrefix+project.ID, "spec.txt")
	sessionID, _ := f.store(v2Alice, "notes.txt", "n")

	got := map[string]chatv2.ChatArtifact{}
	for _, a := range decodeArtifactList[chatv2.ChatArtifact](t, f.listed("/artifacts", v2Alice)).Data {
		got[a.ID] = a
	}

	if spec := got["spec.txt"]; spec.SessionID != nil || spec.ProjectID == nil || *spec.ProjectID != project.ID {
		t.Errorf("project file = %+v, want a null sessionId and the project's id", spec)
	}
	if notes := got["notes.txt"]; notes.SessionID == nil || *notes.SessionID != sessionID {
		t.Errorf("session file = %+v, want sessionId %s", notes, sessionID)
	}
}

func TestChatV2ArtifactListAll_RejectsAMalformedProjectIDWith400(t *testing.T) {
	f := newV2ArtifactLists(t)

	if rec := f.listed("/artifacts?projectId=not-a-uuid", v2Alice); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
}

func TestChatV2ArtifactVersions_ListsEachVersionOldestFirst(t *testing.T) {
	f := newV2ArtifactLists(t)
	session, _ := f.store(v2Alice, "draft.txt", "first")
	loc := f.svc.Resolve("", v2Alice, session, "draft.txt")
	if _, err := f.svc.Inner.Save(context.Background(), loc, []byte("second"), "text/plain"); err != nil {
		t.Fatal(err)
	}

	rec := f.listed("/sessions/"+session+"/artifacts/draft.txt/versions", v2Alice)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var numbers []int
	for _, v := range decodeArtifactList[chatv2.ArtifactVersion](t, rec).Data {
		numbers = append(numbers, v.Version)
	}
	if !slices.Equal(numbers, []int{0, 1}) {
		t.Errorf("versions = %v, want [0 1]", numbers)
	}
}

func TestChatV2ArtifactList_InvalidVisibilityIs422(t *testing.T) {
	f := newV2ArtifactLists(t)
	session, _ := f.store(v2Alice, "notes.txt", "notes")

	rec := f.listed("/sessions/"+session+"/artifacts?visibility=some", v2Alice)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422: %s", rec.Code, rec.Body)
	}
}
