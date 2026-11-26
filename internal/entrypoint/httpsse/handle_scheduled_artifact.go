// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type scheduledArtifact struct {
	filename    string
	content     []byte
	contentType string

	inline bool

	sandboxed bool
}

func (h *scheduledTaskHandler) handleGetScheduledArtifact(w http.ResponseWriter, r *http.Request) {
	file, fail := h.openScheduledArtifact(r.Context(), r.PathValue("id"), r.PathValue("filename"),
		r.URL.Query().Get("download") == "true")
	if fail != nil {
		fail.write(w)
		return
	}

	disposition := "inline"
	if !file.inline {
		disposition = "attachment"
	}
	if file.sandboxed {
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", file.contentType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("%s; filename*=UTF-8''%s", disposition, url.PathEscape(file.filename)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.content)
}

func (h *scheduledTaskHandler) openScheduledArtifact(ctx context.Context, sessionID, rawFilename string,
	wantAttachment bool,
) (scheduledArtifact, *sessionFailure) {
	if h.artSvc == nil || h.sessKeyRepo == nil {
		return scheduledArtifact{}, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}

	if err := validatePathUUID(sessionID); err != nil {
		return scheduledArtifact{}, failSession(http.StatusBadRequest, err.Error())
	}

	filename, fail := scheduledArtifactFilename(rawFilename)
	if fail != nil {
		return scheduledArtifact{}, fail
	}

	userID := resolveUserIDCtx(ctx, h.core)
	task, exec, fail := h.ownedScheduledTaskOfSession(ctx, sessionID, userID)
	if fail != nil {
		return scheduledArtifact{}, fail
	}

	if _, userScoped := artifact.ResolveFilename(filename); userScoped && exec.RanAsSystemUser() {
		return scheduledArtifact{}, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}
	loc := h.artSvc.Resolve("", exec.ArtifactUserID(task), sessionID, filename)

	content, mimeType, err := h.artSvc.Inner.Load(ctx, loc, nil)
	if err != nil {
		h.logger.Error("scheduled artifact: load", "error", err, "sessionID", sessionID, "filename", filename)
		return scheduledArtifact{}, failSession(http.StatusInternalServerError, artifactLoadFailedMsg)
	}
	if content == nil {
		return scheduledArtifact{}, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}

	contentType, safe := artifact.InlineContentType(mimeType)
	if !safe {
		contentType = mimeType
	}
	return scheduledArtifact{
		filename:    filename,
		content:     content,
		contentType: contentType,
		inline:      safe && !wantAttachment,
		sandboxed:   !safe,
	}, nil
}

func scheduledArtifactFilename(raw string) (string, *sessionFailure) {
	invalid := failSession(http.StatusBadRequest, "invalid filename")
	decoded, err := url.PathUnescape(raw)
	if err != nil || decoded == "" {
		return "", invalid
	}

	if strings.ContainsAny(decoded, `/\`) || decoded != filepath.Base(decoded) ||
		decoded == "." || decoded == ".." {
		return "", invalid
	}
	if artifact.IsInternalArtifact(decoded) {
		return "", invalid
	}
	return decoded, nil
}

func (h *scheduledTaskHandler) ownedScheduledTaskOfSession(ctx context.Context, sessionID, userID string,
) (*store.ScheduledTask, *store.ScheduledTaskExecution, *sessionFailure) {
	keys, err := h.sessKeyRepo.GetByGatewayAndSessionIDs(ctx,
		store.GatewayTypeScheduled, []string{sessionID})
	if err != nil {
		h.logger.Error("scheduled artifact: session key lookup", "error", err, "sessionID", sessionID, "userID", userID)
		return nil, nil, failSession(http.StatusInternalServerError, artifactLoadFailedMsg)
	}
	execID, ok := keys[sessionID]
	if !ok {
		return nil, nil, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}

	exec, err := h.repo.GetExecutionByID(ctx, execID)
	if err != nil {
		h.logger.Error("scheduled artifact: get execution", "error", err, "execID", execID, "userID", userID)
		return nil, nil, failSession(http.StatusInternalServerError, artifactLoadFailedMsg)
	}
	if exec == nil {
		return nil, nil, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}

	task, err := h.repo.GetByID(ctx, exec.ScheduledTaskID)
	if err != nil {
		h.logger.Error("scheduled artifact: get task", "error", err, "taskID", exec.ScheduledTaskID, "userID", userID)
		return nil, nil, failSession(http.StatusInternalServerError, artifactLoadFailedMsg)
	}
	if task == nil || task.DeletedAt != nil {
		return nil, nil, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}

	if task.Namespace != h.core.Namespace() {
		return nil, nil, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}
	if !isScheduledTaskCreator(task, userID) {
		return nil, nil, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}
	return task, exec, nil
}

func isScheduledTaskCreator(task *store.ScheduledTask, userID string) bool {
	if userID == "" {
		return false
	}
	return task.CreatedBy == userID
}
