// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type errorInjectingVolumeService struct {
	volume.Service
	createErr    error
	getErr       error
	usageErr     error
	listErr      error
	failGetAfter bool
	getCalls     int
}

func (e *errorInjectingVolumeService) Create(ctx context.Context, opts volume.CreateOptions) (string, error) {
	if e.createErr != nil {
		return "", e.createErr
	}
	return e.Service.Create(ctx, opts)
}

func (e *errorInjectingVolumeService) Get(ctx context.Context, id string) (*volume.Volume, error) {
	e.getCalls++
	if e.getErr != nil && (!e.failGetAfter || e.getCalls > 1) {
		return nil, e.getErr
	}
	return e.Service.Get(ctx, id)
}

func (e *errorInjectingVolumeService) UserUsage(ctx context.Context, userID string) (int64, error) {
	if e.usageErr != nil {
		return 0, e.usageErr
	}
	return e.Service.UserUsage(ctx, userID)
}

func (e *errorInjectingVolumeService) List(ctx context.Context, userID string, filter volume.ListFilter) ([]*volume.Volume, error) {
	if e.listErr != nil {
		return nil, e.listErr
	}
	return e.Service.List(ctx, userID, filter)
}

func setupVolumeHandler(t *testing.T) (*volumeHandler, *http.ServeMux) {
	t.Helper()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	volSvc := volume.NewMemoryStore()
	h := &volumeHandler{core: core, volSvc: volSvc, logger: adapter.logger}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/volumes", h.handleCreate)
	mux.HandleFunc("GET /api/v1/volumes", h.handleList)
	mux.HandleFunc("GET /api/v1/volumes/{id}", h.handleGet)
	mux.HandleFunc("DELETE /api/v1/volumes/{id}", h.handleDelete)

	return h, mux
}

func TestVolumeCreate(t *testing.T) {
	_, mux := setupVolumeHandler(t)

	body := `{"name":"test-vol","description":"a test volume","ttl":"48h"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp VolumeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "test-vol" {
		t.Errorf("Name = %q, want %q", resp.Name, "test-vol")
	}
	if resp.Description != "a test volume" {
		t.Errorf("Description = %q", resp.Description)
	}
	if resp.TTL == nil || *resp.TTL != (48*time.Hour).String() {
		t.Errorf("TTL = %v", resp.TTL)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID")
	}
	if resp.CreatorType != "human" {
		t.Errorf("CreatorType = %q, want %q", resp.CreatorType, "human")
	}
}

func TestVolumeCreateMissingName(t *testing.T) {
	_, mux := setupVolumeHandler(t)

	body := `{"description":"no name"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeList(t *testing.T) {
	h, mux := setupVolumeHandler(t)

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	_, _ = h.volSvc.Create(ctx, volume.CreateOptions{Name: "v1", UserID: "sam_dev_user", CreatorType: volume.CallerHuman})
	_, _ = h.volSvc.Create(ctx, volume.CreateOptions{Name: "v2", UserID: "sam_dev_user", CreatorType: volume.CallerAgent})
	_, _ = h.volSvc.Create(ctx, volume.CreateOptions{Name: "v3", UserID: "other_user", CreatorType: volume.CallerHuman})

	req := httptest.NewRequest("GET", "/api/v1/volumes", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[VolumeResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("got %d volumes, want 2", len(resp.Data))
	}
}

func TestVolumeListWithCreatorTypeFilter(t *testing.T) {
	h, mux := setupVolumeHandler(t)

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	_, _ = h.volSvc.Create(ctx, volume.CreateOptions{Name: "v1", UserID: "sam_dev_user", CreatorType: volume.CallerHuman})
	_, _ = h.volSvc.Create(ctx, volume.CreateOptions{Name: "v2", UserID: "sam_dev_user", CreatorType: volume.CallerAgent})

	req := httptest.NewRequest("GET", "/api/v1/volumes?creatorType=agent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp PaginatedResponse[VolumeResponse]
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Data) != 1 {
		t.Errorf("got %d volumes, want 1", len(resp.Data))
	}
	if resp.Data[0].CreatorType != "agent" {
		t.Errorf("CreatorType = %q, want %q", resp.Data[0].CreatorType, "agent")
	}
}

func TestVolumeGetByID(t *testing.T) {
	h, mux := setupVolumeHandler(t)

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	id, _ := h.volSvc.Create(ctx, volume.CreateOptions{Name: "v1", UserID: "sam_dev_user", CreatorType: volume.CallerHuman})

	req := httptest.NewRequest("GET", "/api/v1/volumes/"+id, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp VolumeResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.ID != id {
		t.Errorf("ID = %q, want %q", resp.ID, id)
	}
}

func TestVolumeGetNotFound(t *testing.T) {
	_, mux := setupVolumeHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/volumes/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestVolumeDelete(t *testing.T) {
	h, mux := setupVolumeHandler(t)

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	id, _ := h.volSvc.Create(ctx, volume.CreateOptions{Name: "v1", UserID: "sam_dev_user", CreatorType: volume.CallerHuman})

	req := httptest.NewRequest("DELETE", "/api/v1/volumes/"+id, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d; body: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/api/v1/volumes/"+id, nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rec.Code)
	}
}

func TestVolumeDeleteLocked(t *testing.T) {
	h, mux := setupVolumeHandler(t)

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	id, _ := h.volSvc.Create(ctx, volume.CreateOptions{Name: "v1", UserID: "sam_dev_user", CreatorType: volume.CallerHuman})
	_ = h.volSvc.AcquireWriteLock(ctx, id, "agent-a", 5*time.Minute)

	req := httptest.NewRequest("DELETE", "/api/v1/volumes/"+id, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeDelete_CrossUserReturns404(t *testing.T) {
	h, mux := setupVolumeHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()

	id, _ := h.volSvc.Create(ctx, volume.CreateOptions{
		Name: "v1", UserID: "other_user", CreatorType: volume.CallerHuman,
	})

	req := httptest.NewRequest("DELETE", "/api/v1/volumes/"+id, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (no leak), got %d", rec.Code)
	}

	if _, err := h.volSvc.Get(ctx, id); err != nil {
		t.Fatalf("other user's volume was deleted! err=%v", err)
	}
}

func TestVolumeGet_CrossUserReturns404(t *testing.T) {
	h, mux := setupVolumeHandler(t)
	ctx := httptest.NewRequest("GET", "/", nil).Context()

	id, _ := h.volSvc.Create(ctx, volume.CreateOptions{
		Name: "v1", UserID: "other_user", CreatorType: volume.CallerHuman,
	})

	req := httptest.NewRequest("GET", "/api/v1/volumes/"+id, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (no leak), got %d; body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "v1") {
		t.Errorf("response leaked the volume name: %s", rec.Body.String())
	}
}

func TestVolumeCreate_LinkedPath_RejectedWhenNotAllowed(t *testing.T) {
	_, mux := setupVolumeHandler(t)
	body := `{"name":"x","linkedPath":"/tmp/anywhere"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (host-path rejected by default), got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeCreate_CopyFromPath_RejectedWhenNotAllowed(t *testing.T) {
	_, mux := setupVolumeHandler(t)
	body := `{"name":"x","copyFromPath":"/tmp/anywhere"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (host-path rejected by default), got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "host-path") {
		t.Errorf("expected error to mention host-path, got %q", rec.Body.String())
	}
}

func TestVolumeCreate_QuotaExceeded_RealStore(t *testing.T) {
	fsStore, ferr := volume.NewFilesystemStore(t.TempDir(), nil)
	if ferr != nil {
		t.Fatalf("NewFilesystemStore: %v", ferr)
	}
	t.Cleanup(func() { _ = fsStore.Close() })
	fsStore.SetPerUserQuotaBytes(100)

	h, mux := setupVolumeHandlerWithService(t, fsStore)
	h.core.Config().AllowHostPathVolumes = true

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "big.bin"), bytes.Repeat([]byte{0x42}, 1024), 0o644); err != nil {
		t.Fatal(err)
	}

	body := `{"name":"oversized","copyFromPath":"` + srcDir + `"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 (quota exceeded), got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeCreate_CopyFromPath_AcceptedWhenAllowed(t *testing.T) {
	fsStore, ferr := volume.NewFilesystemStore(t.TempDir(), nil)
	if ferr != nil {
		t.Fatalf("NewFilesystemStore: %v", ferr)
	}
	t.Cleanup(func() { _ = fsStore.Close() })
	h, mux := setupVolumeHandlerWithService(t, fsStore)
	h.core.Config().AllowHostPathVolumes = true

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "seed.txt"), []byte("seeded"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := `{"name":"seeded","copyFromPath":"` + srcDir + `"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp VolumeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Kind != "managed" {
		t.Errorf("copyFromPath should produce a managed volume, got kind=%q", resp.Kind)
	}
}

func TestVolumeCreate_LinkedAndCopyMutex(t *testing.T) {
	h, mux := setupVolumeHandler(t)
	h.core.Config().AllowHostPathVolumes = true

	body := `{"name":"x","linkedPath":"/tmp/a","copyFromPath":"/tmp/b"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 mutual-exclusion, got %d", rec.Code)
	}
}

func TestVolumeCreate_LinkedKindInResponse(t *testing.T) {
	h, mux := setupVolumeHandler(t)
	h.core.Config().AllowHostPathVolumes = true

	body := `{"name":"work","linkedPath":"/abs/somewhere"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp VolumeResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Kind != "linked" {
		t.Errorf("Kind = %q, want %q", resp.Kind, "linked")
	}
	if resp.LinkedPath != "/abs/somewhere" {
		t.Errorf("LinkedPath = %q", resp.LinkedPath)
	}
	if resp.SizeBytes != 0 {
		t.Errorf("linked volume reported non-zero size: %d", resp.SizeBytes)
	}
}

func TestVolumeUserUsage(t *testing.T) {
	h, mux := setupVolumeHandler(t)
	mux.HandleFunc("GET /api/v1/user/volumes/usage", h.handleUserUsage)

	ctx := httptest.NewRequest("GET", "/", nil).Context()
	_, _ = h.volSvc.Create(ctx, volume.CreateOptions{Name: "m1", UserID: "sam_dev_user", CreatorType: volume.CallerHuman})
	_, _ = h.volSvc.Create(ctx, volume.CreateOptions{Name: "l1", UserID: "sam_dev_user", CreatorType: volume.CallerHuman, ExternalPath: "/abs/host"})
	_, _ = h.volSvc.Create(ctx, volume.CreateOptions{Name: "x", UserID: "other_user", CreatorType: volume.CallerHuman})

	req := httptest.NewRequest("GET", "/api/v1/user/volumes/usage", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp UserVolumeUsageResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.VolumeCount != 2 {
		t.Errorf("VolumeCount = %d, want 2 (cross-user volume excluded)", resp.VolumeCount)
	}
	if resp.ManagedCount != 1 || resp.LinkedCount != 1 {
		t.Errorf("managed/linked split: %d/%d", resp.ManagedCount, resp.LinkedCount)
	}
	if resp.UsedBytes != 0 {
		t.Errorf("UsedBytes = %d; want 0 (memory store has no real bytes)", resp.UsedBytes)
	}
}

func setupVolumeHandlerWithService(t *testing.T, svc volume.Service) (*volumeHandler, *http.ServeMux) {
	t.Helper()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	h := &volumeHandler{core: core, volSvc: svc, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/volumes", h.handleCreate)
	mux.HandleFunc("GET /api/v1/volumes", h.handleList)
	mux.HandleFunc("GET /api/v1/volumes/{id}", h.handleGet)
	mux.HandleFunc("DELETE /api/v1/volumes/{id}", h.handleDelete)
	mux.HandleFunc("GET /api/v1/user/volumes/usage", h.handleUserUsage)
	return h, mux
}

func TestVolumeCreate_MalformedBodyIs400(t *testing.T) {
	_, mux := setupVolumeHandler(t)
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeCreate_InvalidTTLIs400(t *testing.T) {
	_, mux := setupVolumeHandler(t)
	body := `{"name":"x","ttl":"not-a-duration"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeCreate_ServiceErrInvalidOptionsIs400(t *testing.T) {
	h, mux := setupVolumeHandler(t)
	h.core.Config().AllowHostPathVolumes = true

	body := `{"name":"x","linkedPath":"relative-path"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (ErrInvalidOptions), got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeCreate_QuotaExceededIs413(t *testing.T) {
	svc := &errorInjectingVolumeService{
		Service:   volume.NewMemoryStore(),
		createErr: volume.ErrQuotaExceeded,
	}
	_, mux := setupVolumeHandlerWithService(t, svc)

	body := `{"name":"big"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeList_RejectsUnknownCreatorType(t *testing.T) {
	_, mux := setupVolumeHandler(t)
	req := httptest.NewRequest("GET", "/api/v1/volumes?creatorType=bogus", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeUserUsage_UsageErrorIs500(t *testing.T) {
	svc := &errorInjectingVolumeService{
		Service:  volume.NewMemoryStore(),
		usageErr: context.DeadlineExceeded,
	}
	_, mux := setupVolumeHandlerWithService(t, svc)
	req := httptest.NewRequest("GET", "/api/v1/user/volumes/usage", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeUserUsage_ListErrorIs500(t *testing.T) {
	svc := &errorInjectingVolumeService{
		Service: volume.NewMemoryStore(),
		listErr: context.DeadlineExceeded,
	}
	_, mux := setupVolumeHandlerWithService(t, svc)
	req := httptest.NewRequest("GET", "/api/v1/user/volumes/usage", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeDelete_PrecheckGetErrorIs500(t *testing.T) {
	mem := volume.NewMemoryStore()
	id, _ := mem.Create(context.Background(), volume.CreateOptions{
		Name: "v", UserID: "sam_dev_user", CreatorType: volume.CallerHuman,
	})
	svc := &errorInjectingVolumeService{
		Service: mem,
		getErr:  context.DeadlineExceeded,
	}
	_, mux := setupVolumeHandlerWithService(t, svc)

	req := httptest.NewRequest("DELETE", "/api/v1/volumes/"+id, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeGet_ServiceErrorIs500(t *testing.T) {
	svc := &errorInjectingVolumeService{
		Service: volume.NewMemoryStore(),
		getErr:  context.DeadlineExceeded,
	}
	_, mux := setupVolumeHandlerWithService(t, svc)
	req := httptest.NewRequest("GET", "/api/v1/volumes/vol-anything", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestVolumeCreate_LinkedPath_OutsideScope_NoContainerPath(t *testing.T) {
	h, mux := setupVolumeHandler(t)
	h.core.Config().AllowHostPathVolumes = true

	body := `{"name":"work","linkedPath":"/no/such/scope/project"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp VolumeResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	v, err := h.volSvc.Get(req.Context(), resp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.ExternalPathContainer != "" {
		t.Errorf("expected empty ExternalPathContainer for out-of-scope path, got %q", v.ExternalPathContainer)
	}
}

func TestVolumeCreate_LinkedPath_OutsideScope_RequiresContainerMount_400(t *testing.T) {
	h, mux := setupVolumeHandler(t)
	h.core.Config().AllowHostPathVolumes = true
	h.core.Config().RequireContainerMountForLinked = true

	body := `{"name":"work","linkedPath":"/no/such/scope/project"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "scope") {
		t.Errorf("expected error to mention scope, got %q", rec.Body.String())
	}
	vols, _ := h.volSvc.List(req.Context(), "anon-avery.grant@example.org", volume.ListFilter{})
	if len(vols) != 0 {
		t.Errorf("expected 0 volumes after rejected create, got %d", len(vols))
	}
}

func TestVolumeCreate_LinkedPath_ScopeUnset_RequiresContainerMount_400(t *testing.T) {
	const envKey = "SAM_CONTAINER_HOST_MOUNT_SCOPE"
	prev := os.Getenv(envKey)
	if err := os.Setenv(envKey, "none"); err != nil { //nolint:usetesting // parallel via setupVolumeHandler; known race, see comment above
		t.Fatalf("setenv: %v", err)
	}
	t.Cleanup(func() {
		if prev == "" {
			_ = os.Unsetenv(envKey)
		} else {
			_ = os.Setenv(envKey, prev) //nolint:usetesting // restore for the os.Setenv above
		}
	})

	h, mux := setupVolumeHandler(t)
	h.core.Config().AllowHostPathVolumes = true
	h.core.Config().RequireContainerMountForLinked = true

	body := `{"name":"work","linkedPath":"/Users/avery.grant/foo"}`
	req := httptest.NewRequest("POST", "/api/v1/volumes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "host_mount_scope") {
		t.Errorf("expected error to mention host_mount_scope, got %q", rec.Body.String())
	}
}
