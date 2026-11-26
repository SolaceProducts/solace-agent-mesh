// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
)

const projectSessionPrefix = "project-"

func resolveMimeType(filename, providedMIME string, content []byte) string {
	normalized := strings.ToLower(strings.TrimSpace(strings.SplitN(providedMIME, ";", 2)[0]))
	if normalized != "" && normalized != "application/octet-stream" {
		return normalized
	}
	if filename != "" {
		if ext := strings.ToLower(filepath.Ext(filename)); ext != "" {
			if mapped := mime.TypeByExtension(ext); mapped != "" {
				if idx := strings.Index(mapped, ";"); idx > 0 {
					mapped = strings.TrimSpace(mapped[:idx])
				}
				return mapped
			}
		}
	}
	octetStream := "application/octet-stream"
	if len(content) > 0 {
		detected := strings.SplitN(http.DetectContentType(content), ";", 2)[0]
		detected = strings.TrimSpace(detected)
		if detected != "" && detected != octetStream {
			return detected
		}
	}
	return octetStream
}

type projectArtifactHandler struct {
	core        *entrypoint.GatewayCore
	projectRepo *store.ProjectRepo
	artSvc      *artifact.ScopedService
	logger      *slog.Logger

	allCache *artifactCache

	indexer *projectIndexer
}

func (h *projectArtifactHandler) invalidateAllCache(userID string) {
	if h.allCache != nil {
		h.allCache.invalidate(userID)
	}
}

type UploadedFileInfo struct {
	Filename string `json:"filename"`
	Size     int    `json:"size"`
	MIMEType string `json:"mimeType"`
}

func (h *projectArtifactHandler) requireArtifactOwnership(w http.ResponseWriter, r *http.Request, projectID, userID, action string) (*store.Project, bool) {
	project, err := checkProjectOwnership(r.Context(), h.projectRepo, h.logger, projectID, userID)
	if err != nil {
		writeOwnershipError(w, err, "not authorized to "+action+" this project's artifacts")
		return nil, false
	}
	return project, true
}

func (h *projectArtifactHandler) resolveArtifactContext(w http.ResponseWriter, r *http.Request, action string) (projectID, userID, storageSessionID string, ok bool) {
	projectID = r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID = resolveUserID(r, h.core)
	if _, ok = h.requireArtifactOwnership(w, r, projectID, userID, action); !ok {
		return
	}
	storageSessionID = projectSessionPrefix + projectID
	ok = true
	return
}

func (h *projectArtifactHandler) handleAdd(w http.ResponseWriter, r *http.Request) {
	projectID, userID, storageSessionID, ok := h.resolveArtifactContext(w, r, "upload to")
	if !ok {
		return
	}

	maxSize := int64(h.core.Config().GatewayArtifactContentLimitBytes)
	if maxSize <= 0 {
		maxSize = 10_000_000
	}
	if err := r.ParseMultipartForm(maxSize); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form or file too large")
		return
	}

	var fileMetadata map[string]map[string]any
	if metaStr := r.FormValue("fileMetadata"); metaStr != "" {
		fileMetadata = parseFileMetadata(metaStr, projectID, h.logger)
	}

	var uploaded []UploadedFileInfo
	needsProcessing := false

	if r.MultipartForm != nil && r.MultipartForm.File != nil {
		for fieldName, fileHeaders := range r.MultipartForm.File {
			for _, fh := range fileHeaders {
				f, err := fh.Open()
				if err != nil {
					h.logger.Warn("open uploaded file error", "field", fieldName, "error", err)
					continue
				}

				content, err := io.ReadAll(f)
				_ = f.Close()
				if err != nil {
					h.logger.Warn("read uploaded file error", "field", fieldName, "error", err)
					continue
				}

				filename := fh.Filename
				mimeType := resolveMimeType(filename, fh.Header.Get("Content-Type"), content)

				loc := h.artSvc.Resolve("", userID, storageSessionID, filename)
				version, err := h.artSvc.Inner.Save(r.Context(), loc, content, mimeType)
				if err != nil {
					h.logger.Error("save project artifact error", "filename", filename, "error", err)
					continue
				}

				meta := &artifact.VersionMetadata{
					MIMEType:  mimeType,
					SizeBytes: int64(len(content)),
				}
				if fm, ok := fileMetadata[filename]; ok {
					meta.Custom = fm
					if desc, ok := fm["description"].(string); ok {
						meta.Description = desc
					}
				}
				_ = h.artSvc.Inner.SaveMetadata(r.Context(), loc, version, meta)

				uploaded = append(uploaded, UploadedFileInfo{
					Filename: filename,
					Size:     len(content),
					MIMEType: mimeType,
				})
				if convert.ShouldConvert(filename, mimeType) || convert.IsTextFile(mimeType) {
					needsProcessing = true
				}
			}
		}
	}

	if uploaded == nil {
		uploaded = []UploadedFileInfo{}
	}

	if len(uploaded) > 0 {
		h.invalidateAllCache(userID)
	}

	if needsProcessing {
		if taskID := h.indexer.startIndexRebuild(w, r, projectID, userID); taskID != "" {
			writeJSON(w, http.StatusAccepted, map[string]any{"uploaded": uploaded})
			return
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"uploaded": uploaded,
	})
}

func (h *projectArtifactHandler) handleUpdateMetadata(w http.ResponseWriter, r *http.Request) {
	_, userID, storageSessionID, ok := h.resolveArtifactContext(w, r, "modify")
	if !ok {
		return
	}
	filename := r.PathValue("filename")

	var req struct {
		Description *string `json:"description"`
	}
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}

	loc := h.artSvc.Resolve("", userID, storageSessionID, filename)

	meta, err := h.artSvc.Inner.GetVersionMetadata(r.Context(), loc, nil)
	if err != nil || meta == nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}

	if req.Description != nil {
		meta.Description = *req.Description
	}

	versions, err := h.artSvc.Inner.ListVersions(r.Context(), loc)
	if err != nil || len(versions) == 0 {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	latestVersion := versions[len(versions)-1]

	if err := h.artSvc.Inner.SaveMetadata(r.Context(), loc, latestVersion, meta); err != nil {
		h.logger.Error("save metadata error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update metadata")
		return
	}

	h.invalidateAllCache(userID)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Artifact metadata updated successfully",
	})
}

func (h *projectArtifactHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	projectID, userID, storageSessionID, ok := h.resolveArtifactContext(w, r, "delete from")
	if !ok {
		return
	}
	filename := r.PathValue("filename")
	loc := h.artSvc.Resolve("", userID, storageSessionID, filename)

	versions, listErr := h.artSvc.Inner.ListVersions(r.Context(), loc)
	if listErr != nil {
		h.logger.Warn("list versions for delete gate; assuming file existed",
			"projectID", projectID, "filename", filename, "error", listErr)
	}
	existed := listErr != nil || len(versions) > 0

	if err := h.artSvc.Inner.Delete(r.Context(), loc); err != nil {
		h.logger.Error("delete project artifact error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete artifact")
		return
	}
	fileSuffix := ".converted.txt"
	if !strings.HasSuffix(filename, fileSuffix) {
		convertedLoc := h.artSvc.Resolve("", userID, storageSessionID, filename+fileSuffix)
		_ = h.artSvc.Inner.Delete(r.Context(), convertedLoc)
	}

	if existed {
		h.invalidateAllCache(userID)
	}

	if existed {
		if taskID := h.indexer.startIndexRebuild(w, r, projectID, userID); taskID != "" {
			writeJSON(w, http.StatusAccepted, map[string]any{"deleted": true, "filename": filename})
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func parseFileMetadata(metaStr, projectID string, logger *slog.Logger) map[string]map[string]any {
	var raw map[string]any
	if err := json.Unmarshal([]byte(metaStr), &raw); err != nil {
		logger.Warn("unmarshal project artifact fileMetadata", "projectID", projectID, "error", err)
		return nil
	}
	result := make(map[string]map[string]any, len(raw))
	for k, v := range raw {
		switch val := v.(type) {
		case string:
			result[k] = map[string]any{"description": val}
		case map[string]any:
			result[k] = val
		default:
			logger.Warn("unexpected fileMetadata value type", "key", k)
		}
	}
	return result
}

func countProjectArtifacts(ctx context.Context, artSvc *artifact.ScopedService, requester, ownerID, projectID string) int {
	storageSessionID := projectSessionPrefix + projectID
	scope := artifact.KeyScope{
		AppName:   artSvc.Namespace,
		UserID:    ownerID,
		SessionID: storageSessionID,
	}

	keys, err := artSvc.Inner.ListKeys(ctx, scope)
	if err != nil {
		return 0
	}

	count := 0
	for _, key := range keys {
		if artifact.IsInternalArtifact(key) || refusesForeignUserScopedName(requester, ownerID, key) {
			continue
		}
		if isWorkingByCompanion(ctx, artSvc, ownerID, storageSessionID, key) {
			continue
		}
		count++
	}
	return count
}
