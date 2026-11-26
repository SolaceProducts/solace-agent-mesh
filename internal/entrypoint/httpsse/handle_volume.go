// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

const errVolumeNotFound = "volume not found"

type volumeHandler struct {
	core   *entrypoint.GatewayCore
	volSvc volume.Service
	logger *slog.Logger
}

type CreateVolumeRequest struct {
	Name         string  `json:"name"`
	Description  string  `json:"description,omitempty"`
	TTL          *string `json:"ttl,omitempty"`
	LinkedPath   string  `json:"linkedPath,omitempty"`
	CopyFromPath string  `json:"copyFromPath,omitempty"`
}

type VolumeResponse struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	UserID      string             `json:"userId"`
	CreatedBy   string             `json:"createdBy"`
	UpdatedBy   string             `json:"updatedBy"`
	CreatorType string             `json:"creatorType"`
	CreatorName string             `json:"creatorName"`
	CreatedTime string             `json:"createdAt"`
	UpdatedTime string             `json:"updatedAt"`
	LastAccess  string             `json:"lastAccess"`
	TTL         *string            `json:"ttl,omitempty"`
	ExpiresAt   *string            `json:"expiresAt,omitempty"`
	SizeBytes   int64              `json:"sizeBytes"`
	WriteLock   *WriteLockResponse `json:"writeLock"`
	Kind        string             `json:"kind"`
	LinkedPath  string             `json:"linkedPath,omitempty"`
}

type UserVolumeUsageResponse struct {
	UsedBytes    int64 `json:"usedBytes"`
	LimitBytes   int64 `json:"limitBytes"`
	VolumeCount  int   `json:"volumeCount"`
	ManagedCount int   `json:"managedCount"`
	LinkedCount  int   `json:"linkedCount"`
}

type WriteLockResponse struct {
	Holder     string `json:"holder"`
	AcquiredAt string `json:"acquiredAt"`
	MaxExpiry  string `json:"maxExpiry"`
}

func (h *volumeHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	cfg := h.core.Config()
	userID := auth.UserIDFromRequest(r, cfg.DefaultUserIdentity)

	var req CreateVolumeRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if req.LinkedPath != "" && req.CopyFromPath != "" {
		writeError(w, http.StatusBadRequest, "linkedPath and copyFromPath are mutually exclusive")
		return
	}
	if (req.LinkedPath != "" || req.CopyFromPath != "") && !cfg.AllowHostPathVolumes {
		writeError(w, http.StatusBadRequest,
			"host-path volumes are not enabled on this deployment (requires AllowHostPathVolumes in config)")
		return
	}

	opts := volume.CreateOptions{
		Name:         req.Name,
		Description:  req.Description,
		UserID:       userID,
		CreatorType:  volume.CallerHuman,
		CreatorName:  userID,
		ExternalPath: req.LinkedPath,
		CopyFromPath: req.CopyFromPath,
	}

	if req.LinkedPath != "" {
		scope := volume.DefaultHostMountScope()
		switch {
		case scope == "" && cfg.RequireContainerMountForLinked:
			writeError(w, http.StatusBadRequest,
				"linked volumes require container.host_mount_scope to be set when the STR runs in a container; "+
					"set SAM_CONTAINER_HOST_MOUNT_SCOPE to a directory containing the linked path, or use a managed (copy-from-dir) volume instead")
			return
		case scope != "":
			containerPath, mapErr := volume.MapHostToContainer(scope, req.LinkedPath)
			switch {
			case mapErr == nil:
				opts.ExternalPathContainer = containerPath
			case cfg.RequireContainerMountForLinked:
				writeError(w, http.StatusBadRequest,
					"linked path "+req.LinkedPath+" is not under the container host mount scope "+scope+
						" — pick a path under the scope, set SAM_CONTAINER_HOST_MOUNT_SCOPE to a parent directory, or use a managed (copy-from-dir) volume instead")
				return
			}
		}
	}

	if req.TTL != nil {
		ttl, err := time.ParseDuration(*req.TTL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid ttl: "+err.Error())
			return
		}
		opts.TTL = &ttl
	}

	id, err := h.volSvc.Create(r.Context(), opts)
	if err != nil {
		switch {
		case errors.Is(err, volume.ErrInvalidOptions):
			writeError(w, http.StatusBadRequest, err.Error())
			return
		case errors.Is(err, volume.ErrQuotaExceeded):
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		h.logger.Error("create volume", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create volume")
		return
	}

	v, err := h.volSvc.Get(r.Context(), id)
	if err != nil {
		h.logger.Error("get created volume", "error", err)
		writeError(w, http.StatusInternalServerError, "volume created but failed to retrieve")
		return
	}

	writeJSON(w, http.StatusCreated, volumeToResponse(v))
}

func (h *volumeHandler) handleUserUsage(w http.ResponseWriter, r *http.Request) {
	cfg := h.core.Config()
	userID := auth.UserIDFromRequest(r, cfg.DefaultUserIdentity)

	used, err := h.volSvc.UserUsage(r.Context(), userID)
	if err != nil {
		h.logger.Error("user usage", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute usage")
		return
	}
	vols, err := h.volSvc.List(r.Context(), userID, volume.ListFilter{})
	if err != nil {
		h.logger.Error("user usage list", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list volumes")
		return
	}
	var managed, linked int
	for _, v := range vols {
		if v.Linked {
			linked++
		} else {
			managed++
		}
	}
	writeJSON(w, http.StatusOK, UserVolumeUsageResponse{
		UsedBytes:    used,
		LimitBytes:   h.volSvc.PerUserQuotaBytes(),
		VolumeCount:  managed + linked,
		ManagedCount: managed,
		LinkedCount:  linked,
	})
}

func (h *volumeHandler) handleList(w http.ResponseWriter, r *http.Request) {
	cfg := h.core.Config()
	userID := auth.UserIDFromRequest(r, cfg.DefaultUserIdentity)

	var filter volume.ListFilter
	if ct := r.URL.Query().Get("creatorType"); ct != "" {
		callerType := volume.CallerType(ct)
		if callerType != volume.CallerHuman && callerType != volume.CallerAgent {
			writeError(w, http.StatusBadRequest,
				"creatorType must be 'human' or 'agent'")
			return
		}
		filter.CreatorType = &callerType
	}
	if sid := r.URL.Query().Get("sessionId"); sid != "" {
		filter.SessionID = &sid
	}

	volumes, err := h.volSvc.List(r.Context(), userID, filter)
	if err != nil {
		h.logger.Error("list volumes", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list volumes")
		return
	}

	page, pageSize, pageErr := parsePagination(r)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}

	total := len(volumes)

	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pageItems := volumes[start:end]

	items := make([]VolumeResponse, 0, len(pageItems))
	for _, v := range pageItems {
		items = append(items, volumeToResponse(v))
	}

	writeJSON(w, http.StatusOK, PaginatedResponse[VolumeResponse]{
		Data: items,
		Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
	})
}

func (h *volumeHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	cfg := h.core.Config()
	userID := auth.UserIDFromRequest(r, cfg.DefaultUserIdentity)
	volumeID := r.PathValue("id")

	v, err := h.volSvc.Get(r.Context(), volumeID)
	if err != nil {
		if errors.Is(err, volume.ErrNotFound) {
			writeError(w, http.StatusNotFound, errVolumeNotFound)
			return
		}
		h.logger.Error("get volume", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get volume")
		return
	}

	if v.UserID != userID {
		writeError(w, http.StatusNotFound, errVolumeNotFound)
		return
	}

	writeJSON(w, http.StatusOK, volumeToResponse(v))
}

func (h *volumeHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	cfg := h.core.Config()
	userID := auth.UserIDFromRequest(r, cfg.DefaultUserIdentity)
	volumeID := r.PathValue("id")

	v, getErr := h.volSvc.Get(r.Context(), volumeID)
	if getErr != nil {
		if errors.Is(getErr, volume.ErrNotFound) {
			writeError(w, http.StatusNotFound, errVolumeNotFound)
			return
		}
		h.logger.Error("delete volume: pre-check get", "error", getErr)
		writeError(w, http.StatusInternalServerError, "failed to delete volume")
		return
	}
	if v.UserID != userID {
		writeError(w, http.StatusNotFound, errVolumeNotFound)
		return
	}

	err := h.volSvc.Delete(r.Context(), volumeID, volume.CallerHuman)
	if err != nil {
		var lockErr *volume.LockError
		switch {
		case errors.Is(err, volume.ErrNotFound):
			writeError(w, http.StatusNotFound, errVolumeNotFound)
		case errors.Is(err, volume.ErrPermission):
			writeError(w, http.StatusForbidden, "insufficient permissions")
		case errors.As(err, &lockErr):
			writeError(w, http.StatusConflict, "volume is locked: "+err.Error())
		default:
			h.logger.Error("delete volume", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to delete volume")
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func volumeToResponse(v *volume.Volume) VolumeResponse {
	createdTime := v.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000+00:00")
	resp := VolumeResponse{
		ID:          v.ID,
		Name:        v.Name,
		Description: v.Description,
		UserID:      v.UserID,
		CreatedBy:   v.UserID,
		UpdatedBy:   v.UserID,
		CreatorType: string(v.CreatorType),
		CreatorName: v.CreatorName,
		CreatedTime: createdTime,
		UpdatedTime: createdTime,
		LastAccess:  v.LastAccess.UTC().Format("2006-01-02T15:04:05.000000+00:00"),
		SizeBytes:   v.SizeBytes,
	}

	if v.Linked {
		resp.Kind = "linked"
		resp.LinkedPath = v.ExternalPath
		resp.SizeBytes = 0
	} else {
		resp.Kind = "managed"
	}

	if v.TTL != nil {
		s := v.TTL.String()
		resp.TTL = &s
	}
	if v.ExpiresAt != nil {
		s := v.ExpiresAt.UTC().Format("2006-01-02T15:04:05.000000+00:00")
		resp.ExpiresAt = &s
	}
	if v.WriteLock != nil {
		resp.WriteLock = &WriteLockResponse{
			Holder:     v.WriteLock.Holder,
			AcquiredAt: v.WriteLock.AcquiredAt.UTC().Format("2006-01-02T15:04:05.000000+00:00"),
			MaxExpiry:  v.WriteLock.MaxExpiry.UTC().Format("2006-01-02T15:04:05.000000+00:00"),
		}
	}

	return resp
}
