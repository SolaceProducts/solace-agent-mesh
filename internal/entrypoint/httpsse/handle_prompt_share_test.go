// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth/authtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sharing"
)

type recordingShareAudit struct {
	coreauth.NoopAuditLogger
	grants            []coreauth.ShareGrantEvent
	levelChanges      []coreauth.ShareLevelChangeEvent
	revocations       []coreauth.ShareRevokeEvent
	templateDeletions []coreauth.TemplateDeletedEvent
}

func (r *recordingShareAudit) LogShareGrant(_ context.Context, e coreauth.ShareGrantEvent) {
	r.grants = append(r.grants, e)
}
func (r *recordingShareAudit) LogShareLevelChange(_ context.Context, e coreauth.ShareLevelChangeEvent) {
	r.levelChanges = append(r.levelChanges, e)
}
func (r *recordingShareAudit) LogShareRevoke(_ context.Context, e coreauth.ShareRevokeEvent) {
	r.revocations = append(r.revocations, e)
}
func (r *recordingShareAudit) LogTemplateDeleted(_ context.Context, e coreauth.TemplateDeletedEvent) {
	r.templateDeletions = append(r.templateDeletions, e)
}

func (r *recordingShareAudit) LogActivationChange(_ context.Context, _ coreauth.ActivationChangeEvent) {
}
func (r *recordingShareAudit) LogConsentChange(_ context.Context, _ coreauth.ConsentChangeEvent) {}

type errPromptSharing struct{}

func (errPromptSharing) GetShares(context.Context, string) ([]entrypoint.PromptShareResponse, error) {
	return nil, errors.New("simulated upstream failure")
}

func (errPromptSharing) BatchShare(context.Context, string, []entrypoint.ShareRequest, string) (*entrypoint.BatchPromptShareResponse, error) {
	return nil, errors.New("simulated upstream failure")
}

func (errPromptSharing) BulkReplaceShares(context.Context, string, []entrypoint.ShareRequest, string) (*entrypoint.PromptShareReplaceResult, error) {
	return nil, errors.New("simulated upstream failure")
}

func (errPromptSharing) UpdateShare(context.Context, string, string, string, string) (*entrypoint.PromptShareResponse, error) {
	return nil, errors.New("simulated upstream failure")
}

func (errPromptSharing) DeleteShare(context.Context, string, string, string) error {
	return errors.New("simulated upstream failure")
}

func (errPromptSharing) DeleteAllShares(context.Context, string, string) error {
	return errors.New("simulated upstream failure")
}

func newPromptShareTest(t *testing.T) (*entrypoint.GatewayCore, *store.PromptGroupRepo, *http.ServeMux) {
	t.Helper()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	repo := store.NewPromptGroupRepo(core.Store())
	h := &promptShareHandler{
		core:            adapter.core,
		promptGroupRepo: repo,
		logger:          adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prompts/groups/{id}/shares", h.handleGetShares)
	mux.HandleFunc("POST /api/v1/prompts/groups/{id}/shares", h.handleCreateShares)
	mux.HandleFunc("PUT /api/v1/prompts/groups/{id}/shares", h.handleReplaceShares)
	mux.HandleFunc("PATCH /api/v1/prompts/groups/{id}/shares/{recipientEmail}", h.handleUpdateShare)
	mux.HandleFunc("DELETE /api/v1/prompts/groups/{id}/shares/{recipientEmail}", h.handleDeleteShare)
	return core, repo, mux
}

func enablePromptSharing(t *testing.T, core *entrypoint.GatewayCore) {
	t.Helper()
	core.SetIdentityService(&fakeIdentitySvc{})
}

func TestHandlePromptShares_NoService_OwnerGetsEmptyShares(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "My Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/prompts/groups/"+group.ID+"/shares", nil),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[entrypoint.PromptShareResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Data == nil {
		t.Errorf("expected 'data' array (possibly empty); got nil")
	}
	if len(resp.Data) != 0 {
		t.Errorf("len(data) = %d, want 0", len(resp.Data))
	}
}

func TestHandlePromptShares_NonOwner_GET_Returns404(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)

	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Someone Else's Templates",
		UserID: authtest.Unrelated().UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/prompts/groups/"+group.ID+"/shares", nil),
		authtest.Owner(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_NonExistentGroup_GET_Returns404(t *testing.T) {
	core, _, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/prompts/groups/"+idgen.New()+"/shares", nil),
		authtest.Owner(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_ServiceError_Returns500(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(errPromptSharing{})

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/prompts/groups/"+group.ID+"/shares", nil),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_MalformedGroupID_Returns400(t *testing.T) {
	core, _, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/prompts/groups/not-a-uuid/shares", nil),
		authtest.Owner(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_NoIdentity_Returns501(t *testing.T) {
	_, repo, mux := newPromptShareTest(t)

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/prompts/groups/"+group.ID+"/shares", nil),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501; body = %s", rec.Code, rec.Body.String())
	}
}

func TestPromptSharesRoute_IsWiredOnRealMux(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	repo := store.NewPromptGroupRepo(core.Store())
	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Wiring Check",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	shareBody := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	patchBody := `{"accessLevel":"resource_viewer"}`
	cases := []struct {
		method       string
		path         string
		body         string
		expectStatus int
	}{
		{http.MethodGet, "/api/v1/prompts/groups/" + group.ID + "/shares", "", http.StatusOK},
		{http.MethodPost, "/api/v1/prompts/groups/" + group.ID + "/shares", shareBody, http.StatusNotImplemented},
		{http.MethodPut, "/api/v1/prompts/groups/" + group.ID + "/shares", shareBody, http.StatusNotImplemented},
		{http.MethodPatch, "/api/v1/prompts/groups/" + group.ID + "/shares/editor@example.com", patchBody, http.StatusNotImplemented},
		{http.MethodDelete, "/api/v1/prompts/groups/" + group.ID + "/shares/editor@example.com", "", http.StatusNotImplemented},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			req := authtest.With(
				httptest.NewRequestWithContext(t.Context(), tc.method, tc.path,
					strings.NewReader(tc.body)),
				owner,
			)
			switch tc.method {
			case http.MethodPost, http.MethodPut, http.MethodPatch:
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			adapter.Mux().ServeHTTP(rec, req)

			if rec.Code != tc.expectStatus {
				t.Fatalf("status = %d, want %d (route may not be wired in adapter.go); body = %s",
					rec.Code, tc.expectStatus, rec.Body.String())
			}
		})
	}
}

func TestHandlePromptShares_WithService_OwnerSeesCollaborators(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Curated",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	if _, _, err := store.NewResourceAccessShareRepo(core.Store()).BatchShare(
		t.Context(), group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: "editor@example.com", AccessLevel: "resource_editor"}},
		owner.UserID(),
	); err != nil {
		t.Fatal(err)
	}

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/prompts/groups/"+group.ID+"/shares", nil),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[entrypoint.PromptShareResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1; body = %s", len(resp.Data), rec.Body.String())
	}
	if got := resp.Data[0].UserEmail; got != "editor@example.com" {
		t.Errorf("data[0].userEmail = %q, want editor@example.com", got)
	}
	if got := resp.Data[0].AccessLevel; got != "resource_editor" {
		t.Errorf("data[0].accessLevel = %q, want resource_editor", got)
	}
}

func TestHandlePromptShares_NoService_POST_Returns501(t *testing.T) {
	core, _, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+idgen.New()+"/shares",
			strings.NewReader(body)),
		authtest.Owner(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_NoIdentity_POST_Returns501(t *testing.T) {
	_, repo, mux := newPromptShareTest(t)

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_NonOwner_POST_Returns403(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Someone Else's Templates",
		UserID: authtest.Unrelated().UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		authtest.Owner(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_SelfShare_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"` + owner.Email() + `","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_EmptyShares_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(`{"shares":[]}`)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_InvalidPathUUID_POST_Returns400(t *testing.T) {
	core, _, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/not-a-uuid/shares",
			strings.NewReader(body)),
		authtest.Owner(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_NonExistentGroup_POST_Returns404(t *testing.T) {
	core, _, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+idgen.New()+"/shares",
			strings.NewReader(body)),
		authtest.Owner(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_GarbageBody_POST_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}],"extra":"nope"}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_PUT_EmptyShares_RevokesAll(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.NewResourceAccessShareRepo(core.Store()).BatchShare(
		t.Context(), group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: "editor@example.com", AccessLevel: "resource_editor"}},
		owner.UserID(),
	); err != nil {
		t.Fatal(err)
	}

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(`{"shares":[]}`)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	shares, err := core.PromptSharing().GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) after empty PUT = %d, want 0", len(shares))
	}
}

func TestHandlePromptShares_NoService_PUT_Returns501(t *testing.T) {
	core, _, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/v1/prompts/groups/"+idgen.New()+"/shares",
			strings.NewReader(body)),
		authtest.Owner(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_NoIdentity_PUT_Returns501(t *testing.T) {
	_, repo, mux := newPromptShareTest(t)

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_NonOwner_PUT_Returns403(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Someone Else's Templates",
		UserID: authtest.Unrelated().UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		authtest.Owner(),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_SelfShare_PUT_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"` + owner.Email() + `","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_WithService_OwnerDeletesShare(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.NewResourceAccessShareRepo(core.Store()).BatchShare(
		t.Context(), group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: "editor@example.com", AccessLevel: "resource_editor"}},
		owner.UserID(),
	); err != nil {
		t.Fatal(err)
	}

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
			"/api/v1/prompts/groups/"+group.ID+"/shares/editor@example.com", nil),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body for 204, got %q", rec.Body.String())
	}
	shares, err := core.PromptSharing().GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) after DELETE = %d, want 0", len(shares))
	}
}

func TestHandlePromptShares_SingleShare_NotFound_Returns404(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		method string
		body   string
	}{
		{http.MethodPatch, `{"accessLevel":"resource_viewer"}`},
		{http.MethodDelete, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			req := authtest.With(
				httptest.NewRequestWithContext(t.Context(), tc.method,
					"/api/v1/prompts/groups/"+group.ID+"/shares/ghost@example.com",
					strings.NewReader(tc.body)),
				owner,
			)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandlePromptShares_SingleShare_NoIdentity_Returns501(t *testing.T) {
	_, repo, mux := newPromptShareTest(t)

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "Templates", UserID: owner.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		method string
		body   string
	}{
		{http.MethodPatch, `{"accessLevel":"resource_viewer"}`},
		{http.MethodDelete, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			req := authtest.With(
				httptest.NewRequestWithContext(t.Context(), tc.method,
					"/api/v1/prompts/groups/"+group.ID+"/shares/editor@example.com",
					strings.NewReader(tc.body)),
				owner,
			)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Errorf("status = %d, want 501; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandlePromptShares_SingleShare_ServiceError_Returns500(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(errPromptSharing{})

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "Templates", UserID: owner.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		method string
		body   string
	}{
		{http.MethodPatch, `{"accessLevel":"resource_viewer"}`},
		{http.MethodDelete, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			req := authtest.With(
				httptest.NewRequestWithContext(t.Context(), tc.method,
					"/api/v1/prompts/groups/"+group.ID+"/shares/editor@example.com",
					strings.NewReader(tc.body)),
				owner,
			)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandlePromptShares_SingleShare_NonOwner_Returns403(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Someone Else's Templates",
		UserID: authtest.Unrelated().UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		method string
		body   string
	}{
		{http.MethodPatch, `{"accessLevel":"resource_viewer"}`},
		{http.MethodDelete, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			req := authtest.With(
				httptest.NewRequestWithContext(t.Context(), tc.method,
					"/api/v1/prompts/groups/"+group.ID+"/shares/editor@example.com",
					strings.NewReader(tc.body)),
				authtest.Owner(),
			)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandlePromptShares_SingleShare_MalformedEmail_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		method string
		body   string
	}{
		{http.MethodPatch, `{"accessLevel":"resource_viewer"}`},
		{http.MethodDelete, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			req := authtest.With(
				httptest.NewRequestWithContext(t.Context(), tc.method,
					"/api/v1/prompts/groups/"+group.ID+"/shares/not-an-email",
					strings.NewReader(tc.body)),
				owner,
			)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandlePromptShares_PATCH_InvalidAccessLevel_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.NewResourceAccessShareRepo(core.Store()).BatchShare(
		t.Context(), group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: "editor@example.com", AccessLevel: "resource_editor"}},
		owner.UserID(),
	); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ name, body string }{
		{"unknown_level", `{"accessLevel":"resource_admin"}`},
		{"empty_level", `{"accessLevel":""}`},
		{"missing_field", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := authtest.With(
				httptest.NewRequestWithContext(t.Context(), http.MethodPatch,
					"/api/v1/prompts/groups/"+group.ID+"/shares/editor@example.com",
					strings.NewReader(tc.body)),
				owner,
			)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			var resp ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if codes := resp.ValidationDetails["accessLevel"]; len(codes) != 1 || codes[0] != errCodeInvalidAccessLevel {
				t.Errorf("validationDetails.accessLevel = %v, want [%s]", codes, errCodeInvalidAccessLevel)
			}
		})
	}

	shares, err := core.PromptSharing().GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 || shares[0].AccessLevel != "resource_editor" {
		t.Errorf("share state after rejected PATCHes = %+v, want single resource_editor row", shares)
	}
}

func TestHandlePromptShares_WithService_OwnerUpdatesShareLevel(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.NewResourceAccessShareRepo(core.Store()).BatchShare(
		t.Context(), group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: "editor@example.com", AccessLevel: "resource_editor"}},
		owner.UserID(),
	); err != nil {
		t.Fatal(err)
	}

	body := `{"accessLevel":"resource_viewer"}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPatch,
			"/api/v1/prompts/groups/"+group.ID+"/shares/editor@example.com",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp entrypoint.PromptShareResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.UserEmail != "editor@example.com" {
		t.Errorf("UserEmail = %q, want editor@example.com", resp.UserEmail)
	}
	if resp.AccessLevel != "resource_viewer" {
		t.Errorf("AccessLevel = %q, want resource_viewer (downgrade)", resp.AccessLevel)
	}
}

func TestHandlePromptShares_WithService_OwnerReplacesShares(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.NewResourceAccessShareRepo(core.Store()).BatchShare(
		t.Context(), group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{
			{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
			{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"},
		},
		owner.UserID(),
	); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[` +
		`{"userEmail":"editor@example.com","accessLevel":"resource_editor"},` +
		`{"userEmail":"new@example.com","accessLevel":"resource_viewer"}` +
		`]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body for 204, got %q", rec.Body.String())
	}

	shares, err := core.PromptSharing().GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 2 {
		t.Fatalf("len(shares) after PUT = %d, want 2", len(shares))
	}
	got := map[string]string{}
	for _, s := range shares {
		got[s.UserEmail] = s.AccessLevel
	}
	want := map[string]string{
		"editor@example.com": "resource_editor",
		"new@example.com":    "resource_viewer",
	}
	for email, level := range want {
		if got[email] != level {
			t.Errorf("%s level = %q, want %q", email, got[email], level)
		}
	}
}

func TestHandlePromptShares_WithService_OwnerCreatesShare(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)

	owner := authtest.Owner()
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "My Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_editor"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var resp entrypoint.BatchPromptShareResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Created) != 1 {
		t.Fatalf("len(Created) = %d, want 1; body = %s", len(resp.Created), rec.Body.String())
	}
	if got := resp.Created[0].UserEmail; got != "editor@example.com" {
		t.Errorf("Created[0].UserEmail = %q, want editor@example.com", got)
	}
}

func TestPromptShares_MixedCaseOwner_SiblingsAgree(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Mixed-case owner",
		UserID: "Owner@Example.com",
	}
	if err := groupRepo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	caller := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "owner@example.com",
		Email:         "owner@example.com",
		Authenticated: true,
		Scopes:        []string{"agent:*:invoke"},
	})
	doGET := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), caller))
		rec := httptest.NewRecorder()
		adapter.Mux().ServeHTTP(rec, req)
		return rec
	}

	if rec := doGET("/api/v1/prompts/groups/" + group.ID + "/shares"); rec.Code != http.StatusOK {
		t.Errorf("/shares status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	rec := doGET("/api/v1/prompts/groups/" + group.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("/groups/{id} status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var resp PromptGroupResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.AccessLevel != "owner" {
		t.Errorf("accessLevel = %q, want owner", resp.AccessLevel)
	}
}

func TestValidateShareBatch_EmptyBatchReturnsNoErrors(t *testing.T) {
	errs := validateShareBatch(nil, "owner@example.com")
	if len(errs) != 0 {
		t.Errorf("len(errs) = %d, want 0; got %v", len(errs), errs)
	}
}

func TestValidateShareBatch_ValidEmailsReturnNoErrors(t *testing.T) {
	shares := []entrypoint.ShareRequest{
		{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "bob@example.com", AccessLevel: "resource_editor"},
	}
	errs := validateShareBatch(shares, "owner@example.com")
	if len(errs) != 0 {
		t.Errorf("len(errs) = %d, want 0; got %v", len(errs), errs)
	}
}

func TestValidateShareBatch_SelfShareTaggedCannotShareWithSelf(t *testing.T) {
	shares := []entrypoint.ShareRequest{{UserEmail: "Owner@Example.COM", AccessLevel: "resource_viewer"}}
	errs := validateShareBatch(shares, "owner@example.com")
	got := errs["shares[0].userEmail"]
	if len(got) != 1 || got[0] != errCodeCannotShareWithSelf {
		t.Errorf("shares[0].userEmail = %v, want [%s]", got, errCodeCannotShareWithSelf)
	}
}

func TestValidateShareBatch_InvalidFormatTaggedInvalidEmail(t *testing.T) {
	shares := []entrypoint.ShareRequest{
		{UserEmail: "not-an-email", AccessLevel: "resource_viewer"},
		{UserEmail: "missing@dot", AccessLevel: "resource_viewer"},
	}
	errs := validateShareBatch(shares, "owner@example.com")
	for i := range shares {
		field := fmt.Sprintf("shares[%d].userEmail", i)
		if !containsString(errs[field], errCodeInvalidEmail) {
			t.Errorf("%s = %v, want includes %s", field, errs[field], errCodeInvalidEmail)
		}
	}
}

func TestValidateShareBatch_DuplicateEmailsTaggedOnSecondOccurrence(t *testing.T) {
	shares := []entrypoint.ShareRequest{
		{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "Alice@Example.COM", AccessLevel: "resource_editor"},
	}
	errs := validateShareBatch(shares, "owner@example.com")
	if _, has := errs["shares[0].userEmail"]; has {
		t.Errorf("shares[0] should not be flagged (first occurrence); errs[0] = %v", errs["shares[0].userEmail"])
	}
	second := errs["shares[1].userEmail"]
	if len(second) != 1 || second[0] != errCodeDuplicateEmail {
		t.Errorf("shares[1].userEmail = %v, want [%s]", second, errCodeDuplicateEmail)
	}
}

func TestValidateShareBatch_MixedErrorsAllCollected(t *testing.T) {
	shares := []entrypoint.ShareRequest{
		{UserEmail: "owner@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "garbage", AccessLevel: "resource_viewer"},
		{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "alice@example.com", AccessLevel: "resource_editor"},
	}
	errs := validateShareBatch(shares, "owner@example.com")
	if !containsString(errs["shares[0].userEmail"], errCodeCannotShareWithSelf) {
		t.Errorf("shares[0] missing %s; got %v", errCodeCannotShareWithSelf, errs["shares[0].userEmail"])
	}
	if !containsString(errs["shares[1].userEmail"], errCodeInvalidEmail) {
		t.Errorf("shares[1] missing %s; got %v", errCodeInvalidEmail, errs["shares[1].userEmail"])
	}
	if _, has := errs["shares[2].userEmail"]; has {
		t.Errorf("shares[2] is valid first-occurrence; should not be flagged. got %v", errs["shares[2].userEmail"])
	}
	if got := errs["shares[3].userEmail"]; len(got) != 1 || got[0] != errCodeDuplicateEmail {
		t.Errorf("shares[3] expected duplicate flag; got %v", got)
	}
}

func TestHandlePromptShares_POST_BatchCap_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "T", UserID: owner.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	const overCap = maxSharesPerRequest + 1
	entries := make([]string, overCap)
	for i := range overCap {
		entries[i] = fmt.Sprintf(`{"userEmail":"u%d@example.com","accessLevel":"resource_viewer"}`, i)
	}
	body := `{"shares":[` + strings.Join(entries, ",") + `]}`

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	shares, err := core.PromptSharing().GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) after over-cap POST = %d, want 0", len(shares))
	}
}

func TestHandlePromptShares_PUT_BatchCap_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "T", UserID: owner.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	const overCap = maxSharesPerRequest + 1
	entries := make([]string, overCap)
	for i := range overCap {
		entries[i] = fmt.Sprintf(`{"userEmail":"u%d@example.com","accessLevel":"resource_viewer"}`, i)
	}
	body := `{"shares":[` + strings.Join(entries, ",") + `]}`

	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptShares_POST_SelfShare_UsesEmailNotUserID(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	const opaqueUserID = "uuid-1234-abcd"
	caller := auth.NewTestSubject(auth.SubjectFields{
		UserID:        opaqueUserID,
		Email:         "owner@example.com",
		Authenticated: true,
		Scopes:        []string{"agent:*:invoke"},
	})
	group := &store.PromptGroup{ID: idgen.New(), Name: "T", UserID: opaqueUserID}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"owner@example.com","accessLevel":"resource_viewer"}]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/prompts/groups/"+group.ID+"/shares",
		strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(req.Context(), caller))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	got := resp.ValidationDetails["shares[0].userEmail"]
	if !containsString(got, errCodeCannotShareWithSelf) {
		t.Errorf("validationDetails['shares[0].userEmail'] = %v, want includes %s", got, errCodeCannotShareWithSelf)
	}
}

func TestValidateShareBatch_UnknownAccessLevelTaggedInvalidAccessLevel(t *testing.T) {
	shares := []entrypoint.ShareRequest{
		{UserEmail: "alice@example.com", AccessLevel: "admin"},
		{UserEmail: "bob@example.com", AccessLevel: "RESOURCE_VIEWER"},
	}
	errs := validateShareBatch(shares, "owner@example.com")
	for i := range shares {
		field := fmt.Sprintf("shares[%d].accessLevel", i)
		if !containsString(errs[field], errCodeInvalidAccessLevel) {
			t.Errorf("%s = %v, want includes %s", field, errs[field], errCodeInvalidAccessLevel)
		}
	}
}

func TestValidateShareBatch_EmptyAccessLevelAllowed(t *testing.T) {
	shares := []entrypoint.ShareRequest{{UserEmail: "alice@example.com", AccessLevel: ""}}
	errs := validateShareBatch(shares, "owner@example.com")
	if got := errs["shares[0].accessLevel"]; len(got) != 0 {
		t.Errorf("shares[0].accessLevel = %v, want nil (empty access level is a permitted default)", got)
	}
}

func TestHandlePromptShares_POST_InvalidAccessLevel_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "T", UserID: owner.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"alice@example.com","accessLevel":"admin"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	got := resp.ValidationDetails["shares[0].accessLevel"]
	if !containsString(got, errCodeInvalidAccessLevel) {
		t.Errorf("validationDetails['shares[0].accessLevel'] = %v, want includes %s", got, errCodeInvalidAccessLevel)
	}

	shares, err := core.PromptSharing().GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) after 400 = %d, want 0 (no row should persist when validation fails)", len(shares))
	}
}

func TestLooksLikeEmail(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"alice@example.com", true},
		{"a.b+c@sub.example.io", true},
		{"", false},
		{"no-at-sign", false},
		{"missing@dot", false},
		{"@example.com", false},
		{"alice@", false},
		{"with space@example.com", false},
	} {
		if got := looksLikeEmail(c.in); got != c.want {
			t.Errorf("looksLikeEmail(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestHandlePromptShares_POST_MixedValidationErrors_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "T", UserID: owner.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[
		{"userEmail":"` + owner.Email() + `","accessLevel":"resource_viewer"},
		{"userEmail":"garbage","accessLevel":"resource_viewer"},
		{"userEmail":"alice@example.com","accessLevel":"resource_viewer"},
		{"userEmail":"alice@example.com","accessLevel":"resource_editor"}
	]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"shares[0].userEmail", "shares[1].userEmail", "shares[3].userEmail"} {
		if _, has := resp.ValidationDetails[field]; !has {
			t.Errorf("validationDetails missing %s; got %v", field, resp.ValidationDetails)
		}
	}
	if _, has := resp.ValidationDetails["shares[2].userEmail"]; has {
		t.Errorf("shares[2] is a valid first-occurrence; should not be flagged")
	}
}

func TestHandlePromptShares_POST_SelfShare_Returns400_WithFieldCode(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "T", UserID: owner.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"` + owner.Email() + `","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	got := resp.ValidationDetails["shares[0].userEmail"]
	if !containsString(got, errCodeCannotShareWithSelf) {
		t.Errorf("validationDetails['shares[0].userEmail'] = %v, want includes %s", got, errCodeCannotShareWithSelf)
	}
}

func TestHandlePromptShares_PUT_MixedValidationErrors_Returns400(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "T", UserID: owner.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[
		{"userEmail":"` + owner.Email() + `","accessLevel":"resource_viewer"},
		{"userEmail":"bad","accessLevel":"resource_viewer"}
	]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		owner,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if _, has := resp.ValidationDetails["shares[0].userEmail"]; !has {
		t.Errorf("missing shares[0]; got %v", resp.ValidationDetails)
	}
	if _, has := resp.ValidationDetails["shares[1].userEmail"]; !has {
		t.Errorf("missing shares[1]; got %v", resp.ValidationDetails)
	}
}

func TestHandlePromptShares_SharedByEmail_UsesIdentityEmail_NotUserID(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	owner := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "9c1f0000-0000-0000-0000-00000000abcd",
		Email:         "alice@example.com",
		Authenticated: true,
		Scopes:        []string{"agent:*:invoke"},
	})
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	postBody := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_editor"}]}`
	postReq := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(postBody)),
		owner,
	)
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body = %s", postRec.Code, postRec.Body.String())
	}

	getReq := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/prompts/groups/"+group.ID+"/shares", nil),
		owner,
	)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body = %s", getRec.Code, getRec.Body.String())
	}
	var resp PaginatedResponse[entrypoint.PromptShareResponse]
	if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1; body = %s", len(resp.Data), getRec.Body.String())
	}
	if got := resp.Data[0].SharedByEmail; got != owner.Email() {
		t.Errorf("POST sharedByEmail = %q, want %q (audit column must stamp Email, not UserID UUID)", got, owner.Email())
	}

	putBody := `{"shares":[{"userEmail":"viewer@example.com","accessLevel":"resource_viewer"}]}`
	putReq := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPut,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(putBody)),
		owner,
	)
	putRec := httptest.NewRecorder()
	mux.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204; body = %s", putRec.Code, putRec.Body.String())
	}

	getRec2 := httptest.NewRecorder()
	mux.ServeHTTP(getRec2, authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/prompts/groups/"+group.ID+"/shares", nil),
		owner,
	))
	var resp2 PaginatedResponse[entrypoint.PromptShareResponse]
	if err := json.NewDecoder(getRec2.Body).Decode(&resp2); err != nil {
		t.Fatal(err)
	}
	if len(resp2.Data) != 1 {
		t.Fatalf("len(data) after PUT = %d, want 1; body = %s", len(resp2.Data), getRec2.Body.String())
	}
	if got := resp2.Data[0].SharedByEmail; got != owner.Email() {
		t.Errorf("PUT sharedByEmail = %q, want %q", got, owner.Email())
	}
}

func TestHandlePromptShares_AuditEvents_StampActorWithEmail(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)

	audit := &recordingShareAudit{}
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, audit))

	owner := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "9c1f0000-0000-0000-0000-00000000abcd",
		Email:         "alice@example.com",
		Authenticated: true,
		Scopes:        []string{"agent:*:invoke"},
	})
	group := &store.PromptGroup{
		ID:     idgen.New(),
		Name:   "Templates",
		UserID: owner.UserID(),
	}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	postReq := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(`{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_editor"}]}`)),
		owner,
	)
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body = %s", postRec.Code, postRec.Body.String())
	}
	if len(audit.grants) != 1 || audit.grants[0].Actor != owner.Email() {
		t.Errorf("grants = %+v, want one with Actor = %q", audit.grants, owner.Email())
	}

	patchReq := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPatch,
			"/api/v1/prompts/groups/"+group.ID+"/shares/editor@example.com",
			strings.NewReader(`{"accessLevel":"resource_viewer"}`)),
		owner,
	)
	patchRec := httptest.NewRecorder()
	mux.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200; body = %s", patchRec.Code, patchRec.Body.String())
	}
	if len(audit.levelChanges) != 1 || audit.levelChanges[0].Actor != owner.Email() {
		t.Errorf("levelChanges = %+v, want one with Actor = %q", audit.levelChanges, owner.Email())
	}

	delReq := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
			"/api/v1/prompts/groups/"+group.ID+"/shares/editor@example.com", nil),
		owner,
	)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body = %s", delRec.Code, delRec.Body.String())
	}
	if len(audit.revocations) != 1 || audit.revocations[0].Actor != owner.Email() {
		t.Errorf("revocations = %+v, want one with Actor = %q", audit.revocations, owner.Email())
	}

	if _, _, err := store.NewResourceAccessShareRepo(core.Store()).BatchShare(
		t.Context(), group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"}},
		owner.Email(),
	); err != nil {
		t.Fatal(err)
	}
	if err := core.PromptSharing().DeleteAllShares(t.Context(), group.ID, owner.Email()); err != nil {
		t.Fatal(err)
	}
	if len(audit.templateDeletions) != 1 || audit.templateDeletions[0].Actor != owner.Email() {
		t.Errorf("templateDeletions = %+v, want one with Actor = %q", audit.templateDeletions, owner.Email())
	}
}

func TestHandlePromptShares_NoEmailIdentity_Returns500(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	caller := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "9c1f0000-0000-0000-0000-00000000abcd",
		Email:         "",
		Authenticated: true,
		Scopes:        []string{"agent:*:invoke"},
	})
	group := &store.PromptGroup{ID: idgen.New(), Name: "T", UserID: caller.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares",
			strings.NewReader(body)),
		caller,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.ErrorID == "" {
		t.Errorf("errorId is empty; want a UUID for support correlation")
	}

	shares, err := core.PromptSharing().GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) after rejected mutation = %d, want 0", len(shares))
	}
}

func TestHandlePromptShares_SystemPrincipal_Returns403(t *testing.T) {
	core, repo, mux := newPromptShareTest(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	caller := auth.NewTestSubject(auth.SubjectFields{
		UserID:         "system:ci-deployer",
		Authenticated:  true,
		AuthMethod:     auth.APIKeyAuthMethod,
		Scopes:         []string{"agent:*:invoke"},
		ScopesResolved: true,
	})
	group := &store.PromptGroup{ID: idgen.New(), Name: "T", UserID: caller.UserID()}
	if err := repo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}

	body := `{"shares":[{"userEmail":"editor@example.com","accessLevel":"resource_viewer"}]}`
	req := authtest.With(
		httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/api/v1/prompts/groups/"+group.ID+"/shares", strings.NewReader(body)),
		caller,
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	shares, err := core.PromptSharing().GetShares(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) after refused mutation = %d, want 0", len(shares))
	}
}
