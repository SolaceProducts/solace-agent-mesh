// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

const artifactListMetadataConcurrency = 16

type artifactHandler struct {
	core        *entrypoint.GatewayCore
	artSvc      *artifact.ScopedService
	sessRepo    *store.SessionRepo
	projectRepo *store.ProjectRepo
	allCache    *artifactCache
	logger      *slog.Logger
}

type ArtifactUploadResponse struct {
	URI         string         `json:"uri"`
	SessionID   string         `json:"sessionId"`
	Filename    string         `json:"filename"`
	Size        int            `json:"size"`
	MIMEType    string         `json:"mimeType"`
	Metadata    map[string]any `json:"metadata"`
	CreatedTime string         `json:"createdAt"`
}

type ArtifactInfoResponse struct {
	Filename     string         `json:"filename"`
	MIMEType     *string        `json:"mimeType"`
	Size         int64          `json:"size"`
	LastModified *string        `json:"lastModified"`
	Description  *string        `json:"description"`
	Schema       map[string]any `json:"schema"`
	URI          *string        `json:"uri"`
	Version      *int           `json:"version"`
	VersionCount *int           `json:"versionCount"`
	Source       *string        `json:"source"`
	Tags         []string       `json:"tags,omitempty"`
}

const defaultUploadLimitBytes = 10_000_000

type artifactUpload struct {
	SessionID    string
	Filename     string
	ContentType  string
	Content      []byte
	MetadataJSON string
}

type storedArtifact struct {
	Locator  artifact.Locator
	Version  int
	MimeType string
	Size     int
	Metadata map[string]any
}

func (h *artifactHandler) uploadLimit() int64 {
	if limit := int64(h.core.Config().GatewayArtifactContentLimitBytes); limit > 0 {
		return limit
	}
	return defaultUploadLimitBytes
}

func uploadNameProblem(name string) string {
	switch {
	case name == "":
		return "filename is required"
	case artifact.IsInternalArtifact(name):
		return "filename is reserved for internal use"
	case !artifact.IsFilenameSafe(name):
		return "filename is not allowed"
	}
	return ""
}

func (h *artifactHandler) uploadSession(ctx context.Context, userID, sessionID string) (string, *sessionFailure) {
	if sessionID != "" {
		if !apicontract.IsCanonicalUUID(sessionID) {
			return "", failSession(http.StatusBadRequest, "invalid sessionId: must be a canonical lowercase UUID (8-4-4-4-12 hex digits)")
		}
		return sessionID, nil
	}
	sessionID = idgen.New()
	if h.sessRepo != nil {
		if err := h.sessRepo.Create(ctx, &store.Session{ID: sessionID, UserID: userID}); err != nil {
			h.logger.Warn("auto-create session failed", "error", err)
		}
	}
	return sessionID, nil
}

func parseMetadataObject(text string) map[string]any {
	var fields map[string]any
	if text == "" || jsonUnmarshalString(text, &fields) != nil {
		return nil
	}
	return fields
}

func (h *artifactHandler) storeUpload(ctx context.Context, userID string, up artifactUpload) (storedArtifact, *sessionFailure) {
	if problem := uploadNameProblem(up.Filename); problem != "" {
		return storedArtifact{}, failSession(http.StatusBadRequest, problem)
	}
	sessionID, fail := h.uploadSession(ctx, userID, up.SessionID)
	if fail != nil {
		return storedArtifact{}, fail
	}
	if int64(len(up.Content)) > h.uploadLimit() {
		return storedArtifact{}, failSession(http.StatusRequestEntityTooLarge, "file exceeds maximum upload size")
	}

	mimeType := up.ContentType
	if mimeType == "" {
		mimeType = http.DetectContentType(up.Content)
	}
	mimeType = artifact.ResolveDataMIME(up.Filename, mimeType, up.Content)

	customMeta := parseMetadataObject(up.MetadataJSON)
	opts := artifact.MetadataOpts{
		Custom: customMeta,
		Tags:   []string{a2a.ArtifactTagUserUploaded},
	}
	if !artifact.IsStructuredMIME(mimeType) {
		opts.SuppressSchema = true
	}
	saveResult, err := artifact.SaveWithMetadata(ctx, h.artSvc,
		"", userID, sessionID, up.Filename, up.Content, mimeType, opts, h.logger)
	if err != nil {
		h.logger.Error("save artifact error", "error", err, "filename", up.Filename, "sessionID", sessionID)
		return storedArtifact{}, failSession(http.StatusInternalServerError, "failed to save artifact")
	}

	h.invalidateAllCache(userID)
	return storedArtifact{
		Locator:  h.artSvc.Resolve("", userID, sessionID, up.Filename),
		Version:  saveResult.DataVersion,
		MimeType: mimeType,
		Size:     len(up.Content),
		Metadata: customMeta,
	}, nil
}

func (h *artifactHandler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(h.uploadLimit()); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form or file too large")
		return
	}

	file, header, err := r.FormFile("uploadFile")
	if err != nil {
		writeError(w, http.StatusBadRequest, "uploadFile is required")
		return
	}
	defer func() { _ = file.Close() }()

	filename := r.FormValue("filename")
	if filename == "" {
		filename = header.Filename
	}
	if problem := uploadNameProblem(filename); problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read uploaded file")
		return
	}

	stored, fail := h.storeUpload(r.Context(), resolveUserID(r, h.core), artifactUpload{
		SessionID:    r.FormValue("sessionId"),
		Filename:     filename,
		ContentType:  header.Header.Get(headerContentType),
		Content:      content,
		MetadataJSON: r.FormValue("metadataJson"),
	})
	if fail != nil {
		fail.write(w)
		return
	}

	writeJSON(w, http.StatusCreated, ArtifactUploadResponse{
		URI:         buildArtifactURI(stored.Locator, stored.Version),
		SessionID:   stored.Locator.SessionID,
		Filename:    stored.Locator.Filename,
		Size:        stored.Size,
		MIMEType:    stored.MimeType,
		Metadata:    stored.Metadata,
		CreatedTime: time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"),
	})
}

const (
	artifactVisibilityVisible = "visible"
	artifactVisibilityAll     = "all"
)

func (h *artifactHandler) handleList(w http.ResponseWriter, r *http.Request) {
	includeWorking, ok := parseArtifactVisibility(strings.TrimSpace(r.URL.Query().Get("visibility")))
	if !ok {
		errID := idgen.New()
		h.logger.Warn("artifact list rejected: invalid visibility", "errorId", errID)
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Message:           "validation error",
			ErrorID:           errID,
			ValidationDetails: map[string][]string{"visibility": {"must be one of: visible, all"}},
		})
		return
	}

	scope, ok := h.resolveReadScope(w, r)
	if !ok {
		return
	}
	infos, gone := h.listScopeFiles(r.Context(), scope, includeWorking)
	if gone {
		return
	}
	writeJSON(w, http.StatusOK, unpagedResponse(infos))
}

func parseArtifactVisibility(visibility string) (includeWorking, ok bool) {
	switch visibility {
	case "", artifactVisibilityVisible:
		return false, true
	case artifactVisibilityAll:
		return true, true
	}
	return false, false
}

func (h *artifactHandler) listScopeFiles(ctx context.Context, scope artifactReadScope, includeWorking bool) (infos []ArtifactInfoResponse, gone bool) {
	list := listScopeArtifactInfos
	if includeWorking {
		list = scopeArtifactInfos
	}
	listed, err := list(ctx, h.artSvc, scope.storageUserID, scope.sessionID)
	if err != nil {
		if clientGone(err) {
			h.logger.Debug("list artifacts canceled by client", "sessionID", scope.sessionID)
			return nil, true
		}
		h.logger.Error("list artifact keys error", "error", err)
		return []ArtifactInfoResponse{}, false
	}
	return admittedInfos(listed, scope.requester, scope.storageUserID), false
}

func (h *artifactHandler) handleGetLatest(w http.ResponseWriter, r *http.Request) {
	loc, ok := h.resolveReadLocator(w, r)
	if !ok {
		return
	}
	h.serveArtifact(w, r, loc, nil)
}

func (h *artifactHandler) handleListVersions(w http.ResponseWriter, r *http.Request) {
	loc, ok := h.resolveReadLocator(w, r)
	if !ok {
		return
	}
	versions, fail := h.versionsOf(r.Context(), loc)
	if fail != nil {
		fail.write(w)
		return
	}
	writeJSON(w, http.StatusOK, unpagedResponse(versions))
}

func (h *artifactHandler) versionsOf(ctx context.Context, loc artifact.Locator) ([]int, *sessionFailure) {
	versions, err := h.artSvc.Inner.ListVersions(ctx, loc)
	if err != nil {
		h.logger.Error("list versions error", "error", err)
		return nil, failSession(http.StatusInternalServerError, "failed to list versions")
	}
	if versions == nil {
		return []int{}, nil
	}
	return versions, nil
}

func (h *artifactHandler) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	loc, ok := h.resolveReadLocator(w, r)
	if !ok {
		return
	}
	versionStr := r.PathValue("version")

	var versionPtr *int
	if versionStr != "latest" {
		v, err := strconv.Atoi(versionStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid version number")
			return
		}
		versionPtr = &v
	}

	h.serveArtifact(w, r, loc, versionPtr)
}

const (
	artifactNotFoundMsg   = "artifact not found"
	artifactLoadFailedMsg = "failed to load artifact"
)

func mayReferenceArtifact(ctx context.Context, projects *store.ProjectRepo, sharing entrypoint.ProjectSharingService, requester string, loc artifact.Locator) (bool, error) {
	if refusesForeignUserScopedName(requester, loc.UserID, loc.Filename) {
		return false, nil
	}
	if artifact.SameOwner(loc.UserID, requester) {
		return true, nil
	}
	projectID, isProjectScope := projectIDOfSession(loc.SessionID)
	if !isProjectScope {
		return false, nil
	}
	ownerID, allowed, err := resolveProjectOwner(ctx, projects, sharing, projectID, requester)
	if err != nil || !allowed {
		return false, err
	}
	return ownerID == loc.UserID, nil
}

func projectIDOfSession(sessionID string) (projectID string, ok bool) {
	projectID, hasPrefix := strings.CutPrefix(sessionID, projectSessionPrefix)
	if !hasPrefix {
		return "", false
	}
	parsed, err := uuid.Parse(projectID)
	if err != nil || parsed.String() != projectID {
		return "", false
	}
	return projectID, true
}

func (h *artifactHandler) handleByURI(w http.ResponseWriter, r *http.Request) {
	loc, version, fail := h.byURILocator(r.Context(), resolveUserID(r, h.core), r.URL.Query().Get("uri"))
	if fail != nil {
		if r.Context().Err() == nil {
			fail.write(w)
		}
		return
	}
	maxBytes, ok := parseMaxBytes(w, r)
	if !ok {
		return
	}
	h.serveArtifactStreaming(w, r, h.artSvc.Inner, loc, &version, maxBytes)
}

func (h *artifactHandler) byURILocator(ctx context.Context, requester, uriStr string) (artifact.Locator, int, *sessionFailure) {
	if uriStr == "" {
		return artifact.Locator{}, 0, failSession(http.StatusBadRequest, "uri parameter is required")
	}
	if !strings.HasPrefix(uriStr, "artifact://") {
		return artifact.Locator{}, 0, failSession(http.StatusBadRequest, "uri must start with artifact://")
	}
	parsed, err := url.Parse(uriStr)
	if err != nil {
		return artifact.Locator{}, 0, failSession(http.StatusBadRequest, "invalid URI format")
	}

	appName, hostErr := url.QueryUnescape(parsed.Host)
	if hostErr != nil {
		appName = parsed.Host
	}
	pathParts := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 3)
	if len(pathParts) != 3 {
		return artifact.Locator{}, 0, failSession(http.StatusBadRequest, "URI path must have format: user_id/session_id/filename")
	}

	versionStr := parsed.Query().Get("version")
	if versionStr == "" {
		return artifact.Locator{}, 0, failSession(http.StatusBadRequest, "version query parameter is required")
	}
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return artifact.Locator{}, 0, failSession(http.StatusBadRequest, "invalid version number")
	}

	loc := artifact.Locator{
		AppName:   appName,
		UserID:    decodeURIPathSegment(pathParts[0]),
		SessionID: decodeURIPathSegment(pathParts[1]),
		Filename:  decodeURIPathSegment(pathParts[2]),
	}
	allowed, accessErr := mayReferenceArtifact(ctx, h.projectRepo, h.core.ProjectSharing(), requester, loc)
	if accessErr != nil {
		projectID, _ := projectIDOfSession(loc.SessionID)
		if ctx.Err() != nil {
			h.logger.Debug("by-uri shared access check: client gone", "error", accessErr, "projectID", projectID)
			return artifact.Locator{}, 0, failSession(http.StatusServiceUnavailable, "request canceled")
		}
		h.logger.Error("by-uri shared access check", "error", accessErr, "projectID", projectID)
		return artifact.Locator{}, 0, failSession(http.StatusInternalServerError, "failed to check artifact access")
	}
	if !allowed {
		return artifact.Locator{}, 0, failSession(http.StatusNotFound, artifactNotFoundMsg)
	}
	return loc, version, nil
}

func decodeURIPathSegment(s string) string {
	decoded, err := url.PathUnescape(s)
	if err != nil {
		return s
	}
	return decoded
}

func (h *artifactHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if fail := h.removeArtifact(r.Context(), resolveUserID(r, h.core), r.PathValue("id"), r.PathValue("filename")); fail != nil {
		fail.write(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *artifactHandler) removeArtifact(ctx context.Context, userID, sessionID, filename string) *sessionFailure {
	sess, fail := h.sessionRowOf(ctx, sessionID, "get session error")
	if fail != nil {
		return fail
	}
	if sess != nil && sess.UserID != userID {
		return failSession(http.StatusForbidden, "not authorized to delete artifacts in this session")
	}

	loc := h.artSvc.Resolve("", userID, sessionID, filename)
	if err := h.artSvc.Inner.Delete(ctx, loc); err != nil {
		h.logger.Error("delete artifact error", "error", err)
		return failSession(http.StatusInternalServerError, "failed to delete artifact")
	}

	h.invalidateAllCache(userID)
	return nil
}

func (h *artifactHandler) sessionRowOf(ctx context.Context, sessionID, logMsg string) (*store.Session, *sessionFailure) {
	if h.sessRepo == nil {
		return nil, nil
	}
	sess, err := h.sessRepo.GetByID(ctx, sessionID)
	if err != nil {
		h.logger.Error(logMsg, "error", err)
		return nil, failSession(http.StatusInternalServerError, errInternal)
	}
	return sess, nil
}

func (h *artifactHandler) handleCopy(w http.ResponseWriter, r *http.Request) {
	destSessionID := r.PathValue("id")
	userID := resolveUserID(r, h.core)

	var req struct {
		SourceSessionID string   `json:"sourceSessionId"`
		Filenames       []string `json:"filenames"`
	}
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if req.SourceSessionID == "" {
		writeError(w, http.StatusBadRequest, "sourceSessionId is required")
		return
	}
	if len(req.Filenames) == 0 {
		writeError(w, http.StatusBadRequest, "filenames is required and must not be empty")
		return
	}

	copied, failed, fail := h.copySessionArtifactsBetween(r.Context(), userID, req.SourceSessionID, destSessionID, req.Filenames)
	if fail != nil {
		fail.write(w)
		return
	}

	resp := map[string]any{
		"copied": copied,
		"failed": failed,
	}
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *artifactHandler) copySessionArtifactsBetween(ctx context.Context, userID, sourceSessionID, destSessionID string, filenames []string) ([]string, []string, *sessionFailure) {
	if h.sessRepo != nil {
		source, fail := h.sessionRowOf(ctx, sourceSessionID, "get source session error")
		if fail != nil {
			return nil, nil, fail
		}
		if source == nil {
			return nil, nil, failSession(http.StatusNotFound, "source session not found")
		}
		if source.UserID != userID {
			return nil, nil, failSession(http.StatusForbidden, "not authorized to access source session")
		}

		if dest, _ := h.sessRepo.GetByID(ctx, destSessionID); dest == nil {
			if err := h.sessRepo.Create(ctx, &store.Session{ID: destSessionID, UserID: userID}); err != nil {
				h.logger.Warn("auto-create destination session failed", "error", err)
			}
		}
	}

	var copied, failed []string
	for _, filename := range filenames {
		if strings.HasPrefix(filename, "user:") {
			copied = append(copied, filename)
			continue
		}

		from := h.artSvc.Resolve("", userID, sourceSessionID, filename)
		to := h.artSvc.Resolve("", userID, destSessionID, filename)
		if err := artifact.CopyLatest(ctx, h.artSvc.Inner, from, to); err != nil {
			h.logger.Warn("copy artifact failed", "filename", filename, "error", err)
			failed = append(failed, filename)
			continue
		}
		copied = append(copied, filename)
	}
	return copied, failed, nil
}

const maxBytesLimit = 1_048_576

const headerContentType = "Content-Type"

func (h *artifactHandler) serveArtifact(w http.ResponseWriter, r *http.Request, loc artifact.Locator, version *int) {
	maxBytes, ok := parseMaxBytes(w, r)
	if !ok {
		return
	}
	h.serveArtifactStreaming(w, r, h.artSvc.Inner, loc, version, maxBytes)
}

func parseMaxBytes(w http.ResponseWriter, r *http.Request) (int, bool) {
	mbStr := r.URL.Query().Get("maxBytes")
	if mbStr == "" {
		return 0, true
	}
	mb, err := strconv.Atoi(mbStr)
	if err != nil || mb < 1 || mb > maxBytesLimit {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("maxBytes must be an integer between 1 and %d", maxBytesLimit))
		return 0, false
	}
	return mb, true
}

func setArtifactDownloadHeaders(w http.ResponseWriter, mimeType, filename string) {
	w.Header().Set(headerContentType, mimeType)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(filename)))
}

type artifactBody struct {
	mimeType string
	size     int64
	headers  map[string]string
	reader   io.ReadCloser
}

func (h *artifactHandler) serveArtifactStreaming(w http.ResponseWriter, r *http.Request, ro artifact.ReaderOpener, loc artifact.Locator, version *int, maxBytes int) {
	body, fail := h.openArtifactBody(r.Context(), ro, loc, version, maxBytes)
	if fail != nil {
		fail.write(w)
		return
	}
	if body == nil {
		writeError(w, http.StatusNotFound, artifactNotFoundMsg)
		return
	}
	defer func() { _ = body.reader.Close() }()
	setArtifactDownloadHeaders(w, body.mimeType, loc.Filename)
	for name, value := range body.headers {
		w.Header().Set(name, value)
	}
	if body.size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(body.size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, body.reader); err != nil {
		h.logger.Warn("artifact stream interrupted", "filename", loc.Filename, "error", err)
	}
}

func (h *artifactHandler) openArtifactBody(ctx context.Context, ro artifact.ReaderOpener, loc artifact.Locator, version *int, maxBytes int) (*artifactBody, *sessionFailure) {
	rc, mimeType, size, err := ro.OpenReader(ctx, loc, version)
	if err != nil {
		h.logger.Error("load artifact error", "error", err, "filename", loc.Filename, "sessionID", loc.SessionID)
		return nil, failSession(http.StatusInternalServerError, artifactLoadFailedMsg)
	}
	if rc == nil {
		return nil, nil
	}

	isText := embed.IsTextBasedMIME(mimeType)

	if maxBytes > 0 && size > int64(maxBytes) {
		defer func() { _ = rc.Close() }()
		buf := make([]byte, maxBytes)
		n, rerr := io.ReadFull(rc, buf)
		if rerr != nil && rerr != io.ErrUnexpectedEOF {
			h.logger.Error("read artifact preview error", "error", rerr)
			return nil, failSession(http.StatusInternalServerError, "failed to read artifact")
		}
		buf = buf[:n]
		if isText {
			buf = sanitizeUTF8Truncation(buf)
		}
		h.logger.Debug("truncating artifact for preview",
			"filename", loc.Filename, "originalSize", size, "max_bytes", maxBytes)
		return &artifactBody{
			mimeType: mimeType,
			headers:  map[string]string{"X-Truncated": "true", "X-Original-Size": strconv.FormatInt(size, 10)},
			reader:   io.NopCloser(bytes.NewReader(buf)),
		}, nil
	}

	cfg := h.core.Config()
	if cfg.EnableEmbedResolution && isText && !artifact.IsSAMConfigMIME(mimeType) &&
		size > 0 && size <= int64(cfg.GatewayArtifactContentLimitBytes) {
		defer func() { _ = rc.Close() }()
		content, rerr := io.ReadAll(rc)
		if rerr != nil {
			h.logger.Error("read artifact error", "error", rerr)
			return nil, failSession(http.StatusInternalServerError, "failed to read artifact")
		}
		rctx := &embed.ResolveContext{
			Artifacts: h.artSvc,
			AgentName: "",
			UserID:    loc.UserID,
			SessionID: loc.SessionID,
			Config: embed.ResolveConfig{
				MaxDepth:             12,
				MaxArtifactSizeBytes: cfg.GatewayArtifactContentLimitBytes,
				MaxTotalSizeBytes:    embed.DefaultMaxTotalSizeBytes,
			},
			Logger: h.logger,
		}
		if resolved, _, resErr := embed.ResolveEmbedsInString(ctx, string(content), embed.ModeA2AMessageToUser, rctx); resErr != nil {
			h.logger.Warn("embed resolution failed for artifact; serving unresolved source",
				"filename", loc.Filename, "error", resErr)
		} else {
			content = []byte(resolved)
		}
		return &artifactBody{mimeType: mimeType, reader: io.NopCloser(bytes.NewReader(content))}, nil
	}

	return &artifactBody{mimeType: mimeType, size: size, reader: rc}, nil
}

func sanitizeUTF8Truncation(b []byte) []byte {

	for i := len(b) - 1; i >= 0 && i >= len(b)-3; i-- {
		if b[i]&0xC0 != 0x80 {

			var expectedLen int
			switch {
			case b[i]&0x80 == 0:
				expectedLen = 1
			case b[i]&0xE0 == 0xC0:
				expectedLen = 2
			case b[i]&0xF0 == 0xE0:
				expectedLen = 3
			case b[i]&0xF8 == 0xF0:
				expectedLen = 4
			default:

				return b[:i]
			}
			if i+expectedLen <= len(b) {

				return b
			}

			return b[:i]
		}
	}
	return b
}

func (h *artifactHandler) getArtifactInfo(ctx context.Context, loc artifact.Locator) ArtifactInfoResponse {
	versions, err := h.artSvc.Inner.ListVersions(ctx, loc)
	if err != nil {
		versions = nil
	}
	return h.getArtifactInfoWithVersions(ctx, loc, versions, nil)
}

func (h *artifactHandler) getArtifactInfoWithVersions(ctx context.Context, loc artifact.Locator, versions []int, companionVersion *int) ArtifactInfoResponse {
	return getArtifactInfoFromServiceWithVersions(ctx, h.artSvc, loc, versions, companionVersion)
}

func latestCompanionVersion(versionsByName map[string][]int, name string) *int {
	cv := versionsByName[name+artifact.MetadataSuffix]
	if len(cv) == 0 {
		return nil
	}
	return new(cv[len(cv)-1])
}

const maxArtifactDecoratorDepth = 16

func unwrapArtifactService(s artifact.Service) artifact.Service {
	for range maxArtifactDecoratorDepth {
		u, ok := s.(artifact.Unwrapper)
		if !ok || u.Inner() == nil {
			return s
		}
		s = u.Inner()
	}
	return s
}

func scopeArtifactVersions(ctx context.Context, artSvc *artifact.ScopedService, scope artifact.KeyScope) (map[string][]int, error) {
	if vl, ok := unwrapArtifactService(artSvc.Inner).(scopeArtifactVersionLister); ok {
		return vl.ListArtifactVersions(ctx, scope)
	}

	keys, err := artSvc.Inner.ListKeys(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]int, len(keys))
	for _, key := range keys {
		loc := artSvc.Resolve("", scope.UserID, scope.SessionID, key)
		versions, err := artSvc.Inner.ListVersions(ctx, loc)
		if err != nil {
			return nil, err
		}
		out[key] = versions
	}
	return out, nil
}

func isWorkingArtifact(info ArtifactInfoResponse) bool {
	return slices.Contains(info.Tags, a2a.ArtifactTagWorking)
}

func isWorkingByCompanion(ctx context.Context, artSvc *artifact.ScopedService, userID, sessionID, key string) bool {
	meta := artifact.LoadCompanionMetadata(ctx, artSvc, "", userID, sessionID, key, nil)
	tags, ok := meta["tags"].([]any)
	if !ok {
		return false
	}
	for _, t := range tags {
		if s, ok := t.(string); ok && s == a2a.ArtifactTagWorking {
			return true
		}
	}
	return false
}

func listScopeArtifactInfos(ctx context.Context, artSvc *artifact.ScopedService, userID, sessionID string) ([]ArtifactInfoResponse, error) {
	infos, err := scopeArtifactInfos(ctx, artSvc, userID, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]ArtifactInfoResponse, 0, len(infos))
	for _, info := range infos {
		if !isWorkingArtifact(info) {
			out = append(out, info)
		}
	}
	return out, nil
}

func scopeArtifactInfos(ctx context.Context, artSvc *artifact.ScopedService, userID, sessionID string) ([]ArtifactInfoResponse, error) {
	scope := artifact.KeyScope{
		AppName:   artSvc.Namespace,
		UserID:    userID,
		SessionID: sessionID,
	}
	versionsByName, err := scopeArtifactVersions(ctx, artSvc, scope)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(versionsByName))
	for name := range versionsByName {
		if artifact.IsInternalArtifact(name) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)

	infos := make([]ArtifactInfoResponse, len(names))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(artifactListMetadataConcurrency)
	for i, name := range names {
		g.Go(func() error {
			loc := artSvc.Resolve("", userID, sessionID, name)
			infos[i] = getArtifactInfoFromServiceWithVersions(gctx, artSvc, loc, versionsByName[name], latestCompanionVersion(versionsByName, name))
			return nil
		})
	}
	_ = g.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return infos, nil
}

func buildArtifactURI(loc artifact.Locator, version int) string {
	return fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		loc.AppName, loc.UserID, loc.SessionID, loc.Filename, version)
}

func buildArtifactPathURI(loc artifact.Locator) string {
	return fmt.Sprintf("artifact://%s/%s/%s/%s",
		loc.AppName, loc.UserID, loc.SessionID, loc.Filename)
}

func getArtifactInfoFromService(ctx context.Context, artSvc *artifact.ScopedService, loc artifact.Locator) ArtifactInfoResponse {
	versions, err := artSvc.Inner.ListVersions(ctx, loc)
	if err != nil {
		versions = nil
	}
	return getArtifactInfoFromServiceWithVersions(ctx, artSvc, loc, versions, nil)
}

func getArtifactInfoFromServiceWithVersions(ctx context.Context, artSvc *artifact.ScopedService, loc artifact.Locator, versions []int, companionVersion *int) ArtifactInfoResponse {
	info := ArtifactInfoResponse{
		Filename: loc.Filename,
	}

	if len(versions) > 0 {
		latestVersion := versions[len(versions)-1]
		info.Version = &latestVersion
		vc := len(versions)
		info.VersionCount = &vc

		meta, err := artSvc.Inner.GetVersionMetadata(ctx, loc, &latestVersion)
		if err == nil && meta != nil {
			info.MIMEType = &meta.MIMEType
			info.Size = meta.SizeBytes
			if meta.Description != "" {
				info.Description = &meta.Description
			}
			info.Schema = meta.Schema
			if !meta.ModifiedAt.IsZero() {
				s := meta.ModifiedAt.UTC().Format(time.RFC3339Nano)
				info.LastModified = &s
			}
		}
	}

	companionMeta := artifact.LoadCompanionMetadata(ctx, artSvc, "", loc.UserID, loc.SessionID, loc.Filename, companionVersion)
	if companionMeta != nil {
		info.Tags = extractStringSlice(companionMeta, "tags")
		if src, ok := companionMeta["source"].(string); ok && src != "" {
			info.Source = &src
		}
	}

	uri := buildArtifactPathURI(loc)
	info.URI = &uri

	return info
}

func (h *artifactHandler) projectScopeOf(ctx context.Context, projectID, userID string) (sessionID, storageUserID string, status int, err error) {
	if projectID == "" {
		return "", "", 0, nil
	}
	if _, parseErr := uuid.Parse(projectID); parseErr != nil {
		return "", "", http.StatusBadRequest, errors.New("invalid projectId: must be a UUID")
	}
	if h.projectRepo == nil {

		return projectSessionPrefix + projectID, userID, 0, nil
	}
	project, lookupErr := h.projectRepo.GetByID(ctx, projectID)
	if lookupErr != nil {
		h.logger.Error("get project error", "error", lookupErr, "projectID", projectID)
		return "", "", http.StatusInternalServerError, errors.New(errInternal)
	}
	if project == nil {
		return "", "", http.StatusNotFound, errors.New("project not found")
	}
	if !artifact.SameOwner(project.UserID, userID) {
		allowed := false
		if svc := h.core.ProjectSharing(); svc != nil {
			hasAccess, accessErr := svc.HasViewAccess(ctx, projectID, userID)
			if accessErr != nil {
				h.logger.Error("check shared access error", "error", accessErr)
				return "", "", http.StatusInternalServerError, errors.New(errInternal)
			}
			allowed = hasAccess
		}
		if !allowed {
			return "", "", http.StatusForbidden, errors.New("access denied")
		}
	}
	return projectSessionPrefix + projectID, project.UserID, 0, nil
}

func extractStringSlice(m map[string]any, key string) []string {
	val, ok := m[key]
	if !ok || val == nil {
		return nil
	}
	switch v := val.(type) {
	case []string:
		return v
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	}
	return nil
}

func jsonUnmarshalString(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}
