// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth/authtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sharing"
)

func TestHandlePromptGroupCreate(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		logger:     adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/prompts/groups", pgh.handleCreate)

	body := `{"name":"Test","initialPrompt":"Hello world"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/prompts/groups", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PromptGroupResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty id")
	}
	if resp.Name != "Test" {
		t.Errorf("expected name='Test', got %q", resp.Name)
	}
	if resp.UserID == "" {
		t.Error("expected non-empty userId")
	}
	if resp.ProductionPrompt == nil {
		t.Error("expected productionPrompt to be set")
	}
}

func TestHandlePromptGroupList(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())

	ctx := context.Background()
	_ = groupRepo.Create(ctx, &store.PromptGroup{Name: "Group A", UserID: "sam_dev_user"})
	_ = groupRepo.Create(ctx, &store.PromptGroup{Name: "Group B", UserID: "sam_dev_user"})

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		logger:     adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prompts/groups", pgh.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/prompts/groups", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[PromptGroupResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 groups, got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.Count != 2 {
		t.Errorf("expected count=2, got %d", resp.Meta.Pagination.Count)
	}
}

func TestHandlePromptGroupList_SharingEnabled_StoreClosed_Returns500(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	if db := core.Store(); db != nil {
		_ = db.Close()
	}

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		shareRepo:  shareRepo,
		logger:     adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prompts/groups", pgh.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/prompts/groups", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "viewer@example.com",
		Email:  "viewer@example.com",
	})))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when store is unavailable, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlePromptGroupList_SharingEnabled_IncludesSharedGroups(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	ctx := context.Background()
	owned := &store.PromptGroup{Name: "Mine", UserID: "viewer@example.com"}
	shared := &store.PromptGroup{Name: "Shared", UserID: "owner@example.com"}
	if err := groupRepo.Create(ctx, owned); err != nil {
		t.Fatal(err)
	}
	if err := groupRepo.Create(ctx, shared); err != nil {
		t.Fatal(err)
	}
	if _, _, err := shareRepo.BatchShare(ctx, shared.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"}},
		"owner@example.com"); err != nil {
		t.Fatal(err)
	}

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		shareRepo:  shareRepo,
		logger:     adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prompts/groups", pgh.handleList)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/prompts/groups", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "viewer@example.com",
		Email:  "viewer@example.com",
	})))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[PromptGroupResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(resp.Data))
	for _, g := range resp.Data {
		names[g.Name] = true
	}
	if !names["Mine"] {
		t.Error("expected owned group Mine in list")
	}
	if !names["Shared"] {
		t.Error("expected shared group Shared in list (was the prompt_group_users replacement working?)")
	}
}

func TestHandlePromptGroupListAll_StampsAccessLevel(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	ctx := context.Background()
	const ownedID = "10000000-0000-0000-0000-000000000001"
	const sharedID = "10000000-0000-0000-0000-000000000002"
	_ = groupRepo.Create(ctx, &store.PromptGroup{ID: ownedID, Name: "Mine", UserID: "viewer@example.com"})
	_ = groupRepo.Create(ctx, &store.PromptGroup{ID: sharedID, Name: "Shared", UserID: "owner@example.com"})
	if _, _, err := shareRepo.BatchShare(ctx, sharedID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"}},
		"owner@example.com"); err != nil {
		t.Fatal(err)
	}

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		shareRepo:  shareRepo,
		logger:     adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prompts/groups/all", pgh.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/prompts/groups/all", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "viewer@example.com",
		Email:  "viewer@example.com",
	})))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[PromptGroupResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(resp.Data))
	}
	levels := map[string]string{}
	for _, g := range resp.Data {
		levels[g.Name] = g.AccessLevel
	}
	if levels["Mine"] != "owner" {
		t.Errorf("Mine accessLevel = %q, want owner", levels["Mine"])
	}
	if levels["Shared"] != "resource_viewer" {
		t.Errorf("Shared accessLevel = %q, want resource_viewer", levels["Shared"])
	}
}

func TestHandlePromptGroupListAll_ShareLookupFailure_DegradesToOwnedOnly(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	ctx := context.Background()
	const ownedID = "10000000-0000-0000-0000-000000000010"
	if err := groupRepo.Create(ctx, &store.PromptGroup{ID: ownedID, Name: "Mine", UserID: "viewer@example.com"}); err != nil {
		t.Fatal(err)
	}

	if _, err := core.Store().ExecContext(ctx, `DROP TABLE resource_access_shares`); err != nil {
		t.Fatalf("drop shares table: %v", err)
	}

	pgh := &promptGroupHandler{
		core: adapter.core, groupRepo: groupRepo, promptRepo: promptRepo,
		shareRepo: shareRepo, logger: adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prompts/groups/all", pgh.handleListAll)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/prompts/groups/all", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "viewer@example.com", Email: "viewer@example.com",
	})))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (handler must degrade, not fail); body = %s", rec.Code, rec.Body.String())
	}
	var resp PaginatedResponse[PromptGroupResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1 (owned-only fallback)", len(resp.Data))
	}
	if resp.Data[0].Name != "Mine" || resp.Data[0].AccessLevel != "owner" {
		t.Errorf("got %+v, want Mine/owner", resp.Data[0])
	}
}

func TestHandlePromptGroupGet_StampsAccessLevel(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	ctx := t.Context()
	const ownedID = "20000000-0000-0000-0000-000000000001"
	const sharedID = "20000000-0000-0000-0000-000000000002"
	_ = groupRepo.Create(ctx, &store.PromptGroup{ID: ownedID, Name: "Mine", UserID: "viewer@example.com"})
	_ = groupRepo.Create(ctx, &store.PromptGroup{ID: sharedID, Name: "Shared", UserID: "owner@example.com"})
	if _, _, err := shareRepo.BatchShare(ctx, sharedID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"}},
		"owner@example.com"); err != nil {
		t.Fatal(err)
	}

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		shareRepo:  shareRepo,
		logger:     adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prompts/groups/{id}", pgh.handleGet)

	doGet := func(id string) PromptGroupResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/prompts/groups/"+id, nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
			UserID: "viewer@example.com",
			Email:  "viewer@example.com",
		})))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d; body: %s", id, rec.Code, rec.Body.String())
		}
		var resp PromptGroupResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	if got := doGet(ownedID).AccessLevel; got != "owner" {
		t.Errorf("owned accessLevel = %q, want owner", got)
	}
	if got := doGet(sharedID).AccessLevel; got != "resource_viewer" {
		t.Errorf("shared accessLevel = %q, want resource_viewer", got)
	}
}

func TestResolveSharedGroupRole_AllRoles(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	groupRepo := store.NewPromptGroupRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())
	ctx := context.Background()

	const ownerEmail = "owner@example.com"
	group := &store.PromptGroup{Name: "Templates", UserID: ownerEmail}
	if err := groupRepo.Create(ctx, group); err != nil {
		t.Fatal(err)
	}

	if _, _, err := shareRepo.BatchShare(ctx, group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{
			{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
			{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"},
		}, ownerEmail); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		userID string
		want   string
	}{
		{"owner", ownerEmail, "owner"},
		{"editor", "editor@example.com", "editor"},
		{"viewer", "viewer@example.com", "viewer"},
		{"no_access", "stranger@example.com", ""},
		{"owner_upper", "OWNER@EXAMPLE.COM", "owner"},
		{"editor_upper", "EDITOR@EXAMPLE.COM", "editor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSharedGroupRole(ctx, groupRepo, shareRepo, group.ID, tc.userID)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("role = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveSharedGroupRole_MixedCaseOwnerStorage(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	groupRepo := store.NewPromptGroupRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	group := &store.PromptGroup{Name: "Stored mixed case", UserID: "Owner@Example.com"}
	if err := groupRepo.Create(context.Background(), group); err != nil {
		t.Fatal(err)
	}

	got, err := resolveSharedGroupRole(context.Background(), groupRepo, shareRepo, group.ID, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "owner" {
		t.Errorf("role = %q, want owner", got)
	}
}

func TestResolveSharedGroupRole_NilShareRepo_ReturnsError(t *testing.T) {
	_, err := resolveSharedGroupRole(context.Background(), nil, nil, "any-id", "user@example.com")
	if err == nil {
		t.Fatal("expected error when shareRepo is nil; got nil")
	}
}

func TestResolveSharedGroupRole_MissingGroup(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	groupRepo := store.NewPromptGroupRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	role, err := resolveSharedGroupRole(context.Background(), groupRepo, shareRepo,
		"00000000-0000-0000-0000-000000000000", "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if role != "" {
		t.Errorf("role = %q, want \"\" for missing group", role)
	}
}

func TestResolvePromptGroupRole_SharingOff_StoreError_ReturnsError(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	groupRepo := store.NewPromptGroupRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	if db := core.Store(); db != nil {
		_ = db.Close()
	}

	_, err := resolvePromptGroupRole(context.Background(), core, groupRepo, shareRepo,
		"00000000-0000-0000-0000-000000000001", "owner@example.com")
	if err == nil {
		t.Fatal("expected error from closed store; got nil")
	}
}

func TestResolvePromptGroupRole_SharingOff(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	groupRepo := store.NewPromptGroupRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())
	ctx := context.Background()

	const ownerEmail = "Owner@Example.com"
	group := &store.PromptGroup{Name: "Off", UserID: ownerEmail}
	if err := groupRepo.Create(ctx, group); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		groupID string
		userID  string
		want    string
	}{
		{"owner_match", group.ID, ownerEmail, "owner"},
		{"owner_case_insensitive", group.ID, "owner@example.com", "owner"},
		{"non_owner_returns_empty", group.ID, "stranger@example.com", ""},
		{"missing_group_returns_empty", "00000000-0000-0000-0000-000000000000", ownerEmail, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePromptGroupRole(ctx, core, groupRepo, shareRepo, tc.groupID, tc.userID)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("role = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPromptSharing_RoleGating_WritePaths(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "Roles", UserID: owner.UserID()}
	if err := groupRepo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	initial := &store.Prompt{
		PromptText: "v1", GroupID: group.ID, UserID: owner.UserID(), Version: 1, Name: &group.Name,
	}
	if err := promptRepo.Create(t.Context(), initial); err != nil {
		t.Fatal(err)
	}

	if _, _, err := shareRepo.BatchShare(t.Context(), group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{
			{UserEmail: authtest.Editor().UserID(), AccessLevel: "resource_editor"},
			{UserEmail: authtest.Viewer().UserID(), AccessLevel: "resource_viewer"},
		}, owner.UserID()); err != nil {
		t.Fatal(err)
	}

	groupURL := "/api/v1/prompts/groups/" + group.ID
	promptsURL := groupURL + "/prompts"

	cases := []struct {
		name         string
		caller       *auth.Subject
		method, path string
		body         string
		want         int
	}{
		{"viewer_PATCH_group_403", authtest.Viewer(), http.MethodPatch, groupURL, `{"name":"renamed"}`, http.StatusForbidden},
		{"editor_PATCH_group_200", authtest.Editor(), http.MethodPatch, groupURL, `{"name":"renamed"}`, http.StatusOK},
		{"viewer_DELETE_group_403", authtest.Viewer(), http.MethodDelete, groupURL, "", http.StatusForbidden},
		{"editor_DELETE_group_403", authtest.Editor(), http.MethodDelete, groupURL, "", http.StatusForbidden},
		{"editor_DELETE_prompt_403", authtest.Editor(), http.MethodDelete, "/api/v1/prompts/" + initial.ID, "", http.StatusForbidden},
		{"viewer_POST_prompt_403", authtest.Viewer(), http.MethodPost, promptsURL, `{"promptText":"v2"}`, http.StatusForbidden},
		{"editor_POST_prompt_201", authtest.Editor(), http.MethodPost, promptsURL, `{"promptText":"v2"}`, http.StatusCreated},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := authtest.With(
				httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body)),
				tc.caller,
			)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			adapter.Mux().ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body = %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestPromptGroupUpdate_CommandOwnerOnly_M1(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	owner := authtest.Owner()
	group := &store.PromptGroup{ID: idgen.New(), Name: "M1", UserID: owner.UserID()}
	if err := groupRepo.Create(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	if _, _, err := shareRepo.BatchShare(t.Context(), group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{{UserEmail: authtest.Editor().UserID(), AccessLevel: "resource_editor"}},
		owner.UserID()); err != nil {
		t.Fatal(err)
	}

	groupURL := "/api/v1/prompts/groups/" + group.ID

	cases := []struct {
		name   string
		caller *auth.Subject
		body   string
		want   int
	}{
		{"editor_PATCH_command_403", authtest.Editor(), `{"command":"hijacked"}`, http.StatusForbidden},
		{"editor_PATCH_name_200", authtest.Editor(), `{"name":"renamed-by-editor"}`, http.StatusOK},
		{"owner_PATCH_command_200", owner, `{"command":"owner-update"}`, http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := authtest.With(
				httptest.NewRequestWithContext(t.Context(), http.MethodPatch, groupURL, strings.NewReader(tc.body)),
				tc.caller,
			)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			adapter.Mux().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body = %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestHandlePromptGroupDelete(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())

	ctx := context.Background()
	group := &store.PromptGroup{Name: "Delete Me", UserID: "sam_dev_user"}
	_ = groupRepo.Create(ctx, group)

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		logger:     adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/prompts/groups/{id}", pgh.handleDelete)

	rec := httptest.NewRecorder()
	req := authtest.With(
		httptest.NewRequest("DELETE", "/api/v1/prompts/groups/"+group.ID, nil),
		auth.NewTestSubject(auth.SubjectFields{UserID: "sam_dev_user", Email: "sam_dev_user@dev.local", Authenticated: true}),
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Error("expected empty body for 204")
	}

	got, err := groupRepo.GetByID(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected prompt group to be deleted (nil from GetByID)")
	}
}

func TestHandlePromptGroupDelete_CascadesToShares(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())
	shareRepo := store.NewResourceAccessShareRepo(core.Store())

	ctx := t.Context()
	group := &store.PromptGroup{Name: "With shares", UserID: "sam_dev_user"}
	if err := groupRepo.Create(ctx, group); err != nil {
		t.Fatal(err)
	}
	if _, _, err := shareRepo.BatchShare(ctx, group.ID, store.ResourceTypePromptGroup,
		[]store.ShareInput{
			{UserEmail: "editor@example.com", AccessLevel: "resource_editor"},
			{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer"},
		}, "sam_dev_user"); err != nil {
		t.Fatal(err)
	}

	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		shareRepo:  shareRepo,
		logger:     adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/prompts/groups/{id}", pgh.handleDelete)

	rec := httptest.NewRecorder()
	req := authtest.With(
		httptest.NewRequest("DELETE", "/api/v1/prompts/groups/"+group.ID, nil),
		auth.NewTestSubject(auth.SubjectFields{UserID: "sam_dev_user", Email: "sam_dev_user@dev.local", Authenticated: true}),
	)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}

	remaining, err := shareRepo.GetCollaborators(ctx, group.ID, store.ResourceTypePromptGroup)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 share rows after cascade, got %d: %+v", len(remaining), remaining)
	}
}

func TestHandlePromptGroupDelete_NoEmailIdentity_SharingOff_Succeeds(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	groupRepo := store.NewPromptGroupRepo(core.Store())
	promptRepo := store.NewPromptRepo(core.Store())

	ctx := t.Context()
	group := &store.PromptGroup{Name: "No email caller", UserID: "sam_dev_user"}
	if err := groupRepo.Create(ctx, group); err != nil {
		t.Fatal(err)
	}

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: promptRepo,
		logger:     adapter.logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/prompts/groups/{id}", pgh.handleDelete)

	rec := httptest.NewRecorder()
	req := authtest.With(
		httptest.NewRequest("DELETE", "/api/v1/prompts/groups/"+group.ID, nil),
		auth.NewTestSubject(auth.SubjectFields{UserID: "sam_dev_user", Email: "", Authenticated: true}),
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 (sharing off → no audit needed), got %d; body: %s", rec.Code, rec.Body.String())
	}
	got, err := groupRepo.GetByID(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected prompt group to be deleted")
	}
}

func TestHandlePromptGroupDelete_SystemPrincipal_SharingOn_Succeeds(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	enablePromptSharing(t, core)
	core.SetPromptSharingService(sharing.NewPromptService(core.Store, nil))

	groupRepo := store.NewPromptGroupRepo(core.Store())
	ctx := t.Context()
	group := &store.PromptGroup{Name: "Key-owned", UserID: "system:ci-deployer"}
	if err := groupRepo.Create(ctx, group); err != nil {
		t.Fatal(err)
	}

	pgh := &promptGroupHandler{
		core:       adapter.core,
		groupRepo:  groupRepo,
		promptRepo: store.NewPromptRepo(core.Store()),
		shareRepo:  store.NewResourceAccessShareRepo(core.Store()),
		logger:     adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v1/prompts/groups/{id}", pgh.handleDelete)

	rec := httptest.NewRecorder()
	req := authtest.With(
		httptest.NewRequest("DELETE", "/api/v1/prompts/groups/"+group.ID, nil),
		auth.NewTestSubject(auth.SubjectFields{UserID: "system:ci-deployer", Authenticated: true, AuthMethod: auth.APIKeyAuthMethod}),
	)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}
	got, err := groupRepo.GetByID(ctx, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected prompt group to be deleted")
	}
}
