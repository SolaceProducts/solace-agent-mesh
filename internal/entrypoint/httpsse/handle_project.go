// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
)

const errProjectNotFound = "project not found"

const errProjectNameExists = "a project with this name already exists"

const maxProjectNameLength = 255

const errProjectNameInvalid = "name is required (1-255 characters)"

const errProjectDescriptionTooLongFmt = "description must be %d characters or fewer (got %d)"

const zipArtifactDir = "artifacts/"

const (
	maxPerFileUploadBytes = 50 << 20
	maxZipUploadBytes     = 100 << 20
	maxProjectSizeBytes   = 100 << 20
)

type projectHandler struct {
	core            *entrypoint.GatewayCore
	projectRepo     *store.ProjectRepo
	projectUserRepo *store.ProjectUserRepo
	sessRepo        *store.SessionRepo
	artSvc          *artifact.ScopedService
	logger          *slog.Logger
	indexer         *projectIndexer
}

func countUserText(s string) int {
	return utf8.RuneCountInString(strings.ReplaceAll(s, "\r\n", "\n"))
}

func (h *projectHandler) checkDescriptionLength(description *string) error {
	if description == nil {
		return nil
	}
	limit := h.core.Config().GatewayMaxProjectDescriptionChars
	if limit <= 0 {
		return nil
	}
	if got := countUserText(*description); got > limit {
		return fmt.Errorf(errProjectDescriptionTooLongFmt, limit, got)
	}
	return nil
}

func (h *projectHandler) checkInstructionsLength(systemPrompt *string) error {
	if systemPrompt == nil {
		return nil
	}
	limit := h.core.Config().GatewayMaxProjectInstructionsChars
	if limit <= 0 {
		return nil
	}
	if got := countUserText(*systemPrompt); got > limit {
		return fmt.Errorf("instructions must be %d characters or fewer (got %d)", limit, got)
	}
	return nil
}

type CreateProjectRequest struct {
	Name           string  `json:"name"`
	Description    *string `json:"description"`
	SystemPrompt   *string `json:"systemPrompt"`
	DefaultAgentID *string `json:"defaultAgentId"`
}

type UpdateProjectRequest struct {
	Name           *string `json:"name"`
	Description    *string `json:"description"`
	SystemPrompt   *string `json:"systemPrompt"`
	DefaultAgentID *string `json:"defaultAgentId"`
}

type ProjectResponse struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	UserID         string  `json:"userId"`
	CreatedBy      string  `json:"createdBy"`
	Description    *string `json:"description"`
	SystemPrompt   *string `json:"systemPrompt"`
	DefaultAgentID *string `json:"defaultAgentId"`
	ArtifactCount  *int    `json:"artifactCount"`
	IsPinned       bool    `json:"isPinned"`
	CreatedTime    string  `json:"createdAt"`
	UpdatedTime    *string `json:"updatedAt"`
}

func (h *projectHandler) requireProjectViewAccess(w http.ResponseWriter, r *http.Request, projectID, userID string) (*store.Project, bool) {
	project, err := h.projectRepo.GetByID(r.Context(), projectID)
	if err != nil {
		h.logger.Error("get project error", "projectID", projectID, "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return nil, false
	}
	if project == nil {
		writeError(w, http.StatusNotFound, errProjectNotFound)
		return nil, false
	}
	if !strings.EqualFold(project.UserID, userID) {
		allowed := false
		if svc := h.core.ProjectSharing(); svc != nil {
			hasAccess, accessErr := svc.HasViewAccess(r.Context(), projectID, userID)
			if accessErr != nil {
				h.logger.Error("check shared access error", "projectID", projectID, "userID", userID, "error", accessErr)
				writeError(w, http.StatusInternalServerError, errInternal)
				return nil, false
			}
			allowed = hasAccess
		}
		if !allowed {
			writeError(w, http.StatusNotFound, errProjectNotFound)
			return nil, false
		}
	}
	return project, true
}

var (
	errOwnerNotFound  = errors.New("project not found")
	errOwnerForbidden = errors.New("forbidden")
)

func checkProjectOwnership(ctx context.Context, repo *store.ProjectRepo, logger *slog.Logger, projectID, userID string) (*store.Project, error) {
	project, err := repo.GetByID(ctx, projectID)
	if err != nil {
		logger.Error("get project error", "projectID", projectID, "error", err)
		return nil, err
	}
	if project == nil {
		return nil, errOwnerNotFound
	}
	if !strings.EqualFold(project.UserID, userID) {
		return nil, errOwnerForbidden
	}
	return project, nil
}

func writeOwnershipError(w http.ResponseWriter, err error, forbiddenMsg string) {
	switch {
	case errors.Is(err, errOwnerNotFound):
		writeError(w, http.StatusNotFound, errProjectNotFound)
	case errors.Is(err, errOwnerForbidden):
		writeError(w, http.StatusForbidden, forbiddenMsg)
	default:
		writeError(w, http.StatusInternalServerError, errInternal)
	}
}

func (h *projectHandler) requireProjectOwnership(w http.ResponseWriter, r *http.Request, projectID, userID, action string) (*store.Project, bool) {
	project, err := checkProjectOwnership(r.Context(), h.projectRepo, h.logger, projectID, userID)
	if err != nil {
		writeOwnershipError(w, err, "not authorized to "+action+" this project")
		return nil, false
	}
	return project, true
}

func (h *projectHandler) createProjectWithOwner(ctx context.Context, project *store.Project) error {
	if err := h.projectRepo.Create(ctx, project); err != nil {
		return err
	}
	pu := &store.ProjectUser{
		ProjectID:     project.ID,
		UserID:        project.UserID,
		Role:          "owner",
		AddedByUserID: project.UserID,
	}
	if err := h.projectUserRepo.AddUser(ctx, pu); err != nil {
		h.logger.Error("add owner association error", "projectID", project.ID, "userID", project.UserID, "error", err)
	}
	return nil
}

func (h *projectHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateProjectRequest
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") || strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			if err2 := r.ParseForm(); err2 != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
		}
		req.Name = r.FormValue("name")
		if desc := r.FormValue("description"); desc != "" {
			req.Description = &desc
		}
		if sp := r.FormValue("systemPrompt"); sp != "" {
			req.SystemPrompt = &sp
		}
		if da := r.FormValue("defaultAgentId"); da != "" {
			req.DefaultAgentID = &da
		}
	} else {
		if err := decodeJSON(r, &req); err != nil {
			badRequestFromDecode(w, err)
			return
		}
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || countUserText(req.Name) > maxProjectNameLength {
		writeError(w, http.StatusBadRequest, errProjectNameInvalid)
		return
	}
	if err := h.checkDescriptionLength(req.Description); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.checkInstructionsLength(req.SystemPrompt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	userID := resolveUserID(r, h.core)

	exists, err := h.projectRepo.NameExistsForUser(r.Context(), req.Name, userID, "")
	if err != nil {
		h.logger.Error("check project name uniqueness error", "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if exists {
		writeError(w, http.StatusConflict, errProjectNameExists)
		return
	}

	project := &store.Project{
		Name:           req.Name,
		UserID:         userID,
		Description:    req.Description,
		SystemPrompt:   req.SystemPrompt,
		DefaultAgentID: req.DefaultAgentID,
	}

	if err := h.createProjectWithOwner(r.Context(), project); err != nil {
		h.logger.Error("create project error", "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	writeJSON(w, http.StatusCreated, projectToResponse(project))
}

func (h *projectHandler) handleList(w http.ResponseWriter, r *http.Request) {
	userID := resolveUserID(r, h.core)

	var projects []*store.Project
	var err error

	if svc := h.core.ProjectSharing(); svc != nil {
		sharedIDs, sharedErr := svc.GetSharedProjectIDs(r.Context(), userID)
		if sharedErr != nil {
			h.logger.Error("get shared project IDs error", "userID", userID, "error", sharedErr)
			projects, err = h.projectRepo.ListByUser(r.Context(), userID)
		} else {
			projects, err = h.projectRepo.ListAccessible(r.Context(), userID, sharedIDs)
		}
	} else {
		projects, err = h.projectRepo.ListByUser(r.Context(), userID)
	}
	if err != nil {
		h.logger.Error("list projects error", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	expand := parseExpand(r)
	includeArtifactCount := expand["artifactCount"]

	result := make([]ProjectResponse, 0, len(projects))
	for _, p := range projects {
		resp := projectToResponse(p)
		if includeArtifactCount && h.artSvc != nil {
			count := countProjectArtifacts(r.Context(), h.artSvc, userID, p.UserID, p.ID)
			resp.ArtifactCount = &count
		}
		result = append(result, resp)
	}

	writeJSON(w, http.StatusOK, unpagedResponse(result))
}

func (h *projectHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	project, ok := h.requireProjectViewAccess(w, r, projectID, userID)
	if !ok {
		return
	}

	resp := projectToResponse(project)
	if h.artSvc != nil {
		count := countProjectArtifacts(r.Context(), h.artSvc, userID, project.UserID, project.ID)
		resp.ArtifactCount = &count
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *projectHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	project, ok := h.requireProjectOwnership(w, r, projectID, userID, "update")
	if !ok {
		return
	}

	var req UpdateProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}

	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" || countUserText(trimmed) > maxProjectNameLength {
			writeError(w, http.StatusBadRequest, errProjectNameInvalid)
			return
		}
		*req.Name = trimmed
	}

	if err := h.checkDescriptionLength(req.Description); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.checkInstructionsLength(req.SystemPrompt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Name != nil {
		exists, err := h.projectRepo.NameExistsForUser(r.Context(), *req.Name, userID, projectID)
		if err != nil {
			h.logger.Error("check project name uniqueness error", "projectID", projectID, "userID", userID, "error", err)
			writeError(w, http.StatusInternalServerError, errInternal)
			return
		}
		if exists {
			writeError(w, http.StatusConflict, errProjectNameExists)
			return
		}
	}

	if req.Name != nil {
		project.Name = *req.Name
	}
	if req.Description != nil {
		project.Description = req.Description
	}
	if req.SystemPrompt != nil {
		project.SystemPrompt = req.SystemPrompt
	}
	if req.DefaultAgentID != nil {
		project.DefaultAgentID = req.DefaultAgentID
	}

	if err := h.projectRepo.Update(r.Context(), project); err != nil {
		h.logger.Error("update project error", "projectID", projectID, "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	writeJSON(w, http.StatusOK, projectToResponse(project))
}

func (h *projectHandler) handlePin(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	project, ok := h.requireProjectOwnership(w, r, projectID, userID, "pin")
	if !ok {
		return
	}

	project.IsPinned = !project.IsPinned
	if err := h.projectRepo.Update(r.Context(), project); err != nil {
		h.logger.Error("toggle project pin error", "projectID", projectID, "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	writeJSON(w, http.StatusOK, projectToResponse(project))
}

func (h *projectHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	if _, ok := h.requireProjectOwnership(w, r, projectID, userID, "delete"); !ok {
		return
	}

	if err := h.projectRepo.SoftDelete(r.Context(), projectID, userID); err != nil {
		h.logger.Error("soft delete project error", "projectID", projectID, "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	if err := h.projectRepo.SoftDeleteSessionsByProject(r.Context(), projectID, userID); err != nil {
		h.logger.Error("cascade delete sessions error", "projectID", projectID, "error", err)
	}

	if svc := h.core.ProjectSharing(); svc != nil {
		if err := svc.DeleteAllShares(r.Context(), projectID); err != nil {
			h.logger.Error("cascade delete shares error", "projectID", projectID, "error", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *projectHandler) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	project, ok := h.requireProjectViewAccess(w, r, projectID, userID)
	if !ok {
		return
	}

	if h.artSvc == nil {
		writeJSON(w, http.StatusOK, unpagedResponse([]ArtifactInfoResponse{}))
		return
	}

	sessionID := projectSessionPrefix + projectID
	result, err := listScopeArtifactInfos(r.Context(), h.artSvc, project.UserID, sessionID)
	if err != nil {
		h.logger.Error("list project artifact keys error", "projectID", projectID, "userID", userID, "error", err)
		writeJSON(w, http.StatusOK, unpagedResponse([]ArtifactInfoResponse{}))
		return
	}

	writeJSON(w, http.StatusOK, unpagedResponse(admittedInfos(result, userID, project.UserID)))
}

type projectExportData struct {
	Version    string                  `json:"version"`
	ExportedAt int64                   `json:"exportedAt"`
	Project    projectExportMeta       `json:"project"`
	Artifacts  []artifactManifestEntry `json:"artifacts"`
}

type projectExportMeta struct {
	Name           string                   `json:"name"`
	Description    *string                  `json:"description"`
	SystemPrompt   *string                  `json:"systemPrompt"`
	DefaultAgentID *string                  `json:"defaultAgentId"`
	Metadata       projectExportMetaSummary `json:"metadata"`
}

type projectExportMetaSummary struct {
	OriginalCreatedAt int64 `json:"originalCreatedAt"`
	ArtifactCount     int   `json:"artifactCount"`
	TotalSizeBytes    int64 `json:"totalSizeBytes"`
}

type artifactManifestEntry struct {
	Filename string         `json:"filename"`
	MIMEType string         `json:"mimeType"`
	Size     int64          `json:"size"`
	Metadata map[string]any `json:"metadata"`
	Tags     []string       `json:"tags,omitempty"`
}

func (h *projectHandler) handleExport(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if err := validatePathUUID(projectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := resolveUserID(r, h.core)

	project, ok := h.requireProjectOwnership(w, r, projectID, userID, "export")
	if !ok {
		return
	}

	ctx := r.Context()
	storageSessionID := projectSessionPrefix + projectID

	var infos []ArtifactInfoResponse
	if h.artSvc != nil {
		var err error
		infos, err = listScopeArtifactInfos(ctx, h.artSvc, userID, storageSessionID)
		if err != nil {
			h.logger.Error("list project artifacts for export error", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	manifest := make([]artifactManifestEntry, 0, len(infos))
	var totalSize int64
	for _, info := range infos {
		entry := artifactManifestEntry{
			Filename: info.Filename,
			MIMEType: "application/octet-stream",
			Size:     info.Size,
			Metadata: map[string]any{},
		}
		if info.MIMEType != nil && *info.MIMEType != "" {
			entry.MIMEType = *info.MIMEType
		}
		if info.Description != nil {
			entry.Metadata["description"] = *info.Description
		}
		if info.Source != nil {
			entry.Metadata["source"] = *info.Source
		}
		entry.Tags = info.Tags
		manifest = append(manifest, entry)
		totalSize += info.Size
	}

	exportData := projectExportData{
		Version:    "1.0",
		ExportedAt: time.Now().UnixMilli(),
		Project: projectExportMeta{
			Name:           project.Name,
			Description:    project.Description,
			SystemPrompt:   project.SystemPrompt,
			DefaultAgentID: project.DefaultAgentID,
			Metadata: projectExportMetaSummary{
				OriginalCreatedAt: project.CreatedAt,
				ArtifactCount:     len(manifest),
				TotalSizeBytes:    totalSize,
			},
		},
		Artifacts: manifest,
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	jsonData, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		h.logger.Error("marshal project export error", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	pw, err := zw.Create("project.json")
	if err != nil {
		h.logger.Error("create zip entry error", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}
	if _, err := pw.Write(jsonData); err != nil {
		h.logger.Error("write zip entry error", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	if h.artSvc != nil {
		for _, entry := range manifest {
			loc := h.artSvc.Resolve("", userID, storageSessionID, entry.Filename)
			content, _, err := h.artSvc.Inner.Load(ctx, loc, nil)
			if err != nil {
				h.logger.Warn("load artifact for export error", "key", entry.Filename, "error", err)
				continue
			}
			aw, err := zw.Create(zipArtifactDir + entry.Filename)
			if err != nil {
				h.logger.Warn("create zip artifact entry error", "key", entry.Filename, "error", err)
				continue
			}
			_, _ = aw.Write(content)
		}
	}

	if err := zw.Close(); err != nil {
		h.logger.Error("close zip error", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	safeName := strings.ReplaceAll(project.Name, " ", "_")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="project_%s.zip"`, safeName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (h *projectHandler) handleImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxZipUploadBytes)
	if err := r.ParseMultipartForm(maxZipUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid or oversized multipart form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer func() { _ = file.Close() }()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read file")
		return
	}
	if int64(len(fileBytes)) > maxZipUploadBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("ZIP file exceeds maximum allowed size of %d MB", maxZipUploadBytes>>20))
		return
	}

	zr, err := zip.NewReader(bytes.NewReader(fileBytes), int64(len(fileBytes)))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ZIP file")
		return
	}

	var exportData projectExportData
	foundProjectJSON := false
	for _, f := range zr.File {
		if f.Name == "project.json" {
			rc, err := f.Open()
			if err != nil {
				writeError(w, http.StatusBadRequest, "cannot read project.json")
				return
			}
			data, _ := io.ReadAll(rc)
			_ = rc.Close()
			if err := json.Unmarshal(data, &exportData); err != nil {
				writeError(w, http.StatusBadRequest, "invalid project.json format")
				return
			}
			foundProjectJSON = true
			break
		}
	}
	if !foundProjectJSON {
		writeError(w, http.StatusBadRequest, "ZIP must contain project.json")
		return
	}
	if exportData.Version != "1.0" {
		writeError(w, http.StatusBadRequest, "unsupported export version: "+exportData.Version)
		return
	}

	ctx := r.Context()
	userID := resolveUserID(r, h.core)

	projectName := exportData.Project.Name
	if optStr := r.FormValue("options"); optStr != "" {
		var opts struct {
			PreserveName bool   `json:"preserveName"`
			CustomName   string `json:"customName"`
		}
		dec := json.NewDecoder(strings.NewReader(optStr))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&opts); err != nil {
			writeError(w, http.StatusBadRequest, "invalid options: "+err.Error())
			return
		}
		if name := strings.TrimSpace(opts.CustomName); name != "" {
			projectName = name
		}
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" || countUserText(projectName) > maxProjectNameLength {
		writeError(w, http.StatusBadRequest, errProjectNameInvalid)
		return
	}
	if err := h.checkDescriptionLength(exportData.Project.Description); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.checkInstructionsLength(exportData.Project.SystemPrompt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var totalArtifactBytes int64
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, zipArtifactDir) || f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > maxPerFileUploadBytes {
			continue
		}
		totalArtifactBytes += int64(f.UncompressedSize64)
	}
	if totalArtifactBytes > maxProjectSizeBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("project size exceeds maximum allowed of %d MB", maxProjectSizeBytes>>20))
		return
	}
	project := &store.Project{
		Name:           projectName,
		UserID:         userID,
		Description:    exportData.Project.Description,
		SystemPrompt:   exportData.Project.SystemPrompt,
		DefaultAgentID: exportData.Project.DefaultAgentID,
	}
	if err := h.createProjectWithOwner(ctx, project); err != nil {
		h.logger.Error("create imported project error", "error", err)
		writeError(w, http.StatusInternalServerError, errInternal)
		return
	}

	pu := &store.ProjectUser{
		ProjectID:     project.ID,
		UserID:        userID,
		Role:          "owner",
		AddedByUserID: userID,
	}
	if err := h.projectUserRepo.AddUser(ctx, pu); err != nil {
		h.logger.Warn("add owner to imported project", "error", err, "projectID", project.ID, "userID", userID)
	}

	manifestByName := make(map[string]artifactManifestEntry, len(exportData.Artifacts))
	for _, entry := range exportData.Artifacts {
		manifestByName[entry.Filename] = entry
	}

	artifactsImported, needsProcessing := h.importArtifacts(ctx, zr, manifestByName, project.ID, userID)

	resStatus := http.StatusCreated
	if needsProcessing && h.indexer != nil {
		if taskID := h.indexer.startIndexRebuild(w, r, project.ID, userID); taskID != "" {
			resStatus = http.StatusAccepted
		}
	}

	writeJSON(w, resStatus, map[string]any{
		"projectId":         project.ID,
		"name":              project.Name,
		"artifactsImported": artifactsImported,
	})
}

func (h *projectHandler) importArtifacts(ctx context.Context, zr *zip.Reader, manifestByName map[string]artifactManifestEntry, projectID, userID string) (artifactsImported int, needsProcessing bool) {
	if h.artSvc == nil {
		return 0, false
	}
	storageSessionID := projectSessionPrefix + projectID
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, zipArtifactDir) || f.FileInfo().IsDir() {
			continue
		}
		filename := strings.TrimPrefix(f.Name, zipArtifactDir)
		if !artifact.IsFilenameSafe(filename) {
			h.logger.Warn("skipping project-import entry with unsafe filename", "filename", filename)
			continue
		}
		if artifact.IsInternalArtifact(filename) {
			h.logger.Warn("skipping project-import entry with reserved internal-artifact filename",
				"filename", filename)
			continue
		}
		if f.UncompressedSize64 > maxPerFileUploadBytes {
			h.logger.Warn("skipping oversized project-import artifact", "filename", filename, "size", f.UncompressedSize64)
			continue
		}

		rc, err := f.Open()
		if err != nil {
			h.logger.Warn("open project-import entry error", "filename", filename, "error", err)
			continue
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxPerFileUploadBytes))
		_ = rc.Close()
		if err != nil {
			h.logger.Warn("read project-import entry error", "filename", filename, "error", err)
			continue
		}

		entry, inManifest := manifestByName[filename]
		mimeType := entry.MIMEType
		if mimeType == "" {
			mimeType = resolveMimeType(filename, "", content)
		}

		opts := artifact.MetadataOpts{}
		if inManifest {
			opts.Tags = entry.Tags
			if desc, ok := entry.Metadata["description"].(string); ok {
				opts.Description = desc
			}
			if src, ok := entry.Metadata["source"].(string); ok && src != "" {
				opts.Source = src
				if src == "project" {
					opts.Custom = map[string]any{"source_project_id": projectID}
				}
			}
		}

		if _, err := artifact.SaveWithMetadata(ctx, h.artSvc, "", userID, storageSessionID,
			filename, content, mimeType, opts, h.logger); err != nil {
			h.logger.Warn("import artifact error", "filename", filename, "error", err)
			continue
		}
		artifactsImported++
		if convert.ShouldConvert(filename, mimeType) || convert.IsTextFile(mimeType) {
			needsProcessing = true
		}
	}
	return artifactsImported, needsProcessing
}

func projectToResponse(p *store.Project) ProjectResponse {
	return ProjectResponse{
		ID:             p.ID,
		Name:           p.Name,
		UserID:         p.UserID,
		CreatedBy:      p.UserID,
		Description:    p.Description,
		SystemPrompt:   p.SystemPrompt,
		DefaultAgentID: p.DefaultAgentID,
		ArtifactCount:  nil,
		IsPinned:       p.IsPinned,
		CreatedTime:    epochMsToISO8601(p.CreatedAt),
		UpdatedTime:    optionalISO8601(p.UpdatedAt),
	}
}
