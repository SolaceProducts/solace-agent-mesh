// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	toolsetUploadType = "toolsetUpload"

	buildTargetSourceSTRInit  = "str_init"
	buildTargetSourceFallback = "fallback"
)

type ToolsetUpload struct {
	ID             string  `json:"id" example:"0190c7d8-4e2b-7f6a-9b1c-5d3e7f9a1b2c"`
	Type           string  `json:"type" example:"toolsetUpload"`
	ToolsetID      string  `json:"toolsetId" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Filename       string  `json:"filename" example:"order_tools.zip"`
	TotalSize      int64   `json:"totalSize" example:"9437184"`
	ChunkSize      int64   `json:"chunkSize" example:"4194304"`
	ReceivedBytes  int64   `json:"receivedBytes" example:"4194304"`
	ReceivedChunks []int   `json:"receivedChunks" example:"[0]"`
	CreatedTime    Instant `json:"createdTime"`
	ExpiresTime    Instant `json:"expiresTime"`
}

type ToolsetBuildTarget struct {
	OS            string  `json:"os" example:"linux"`
	Arch          string  `json:"arch" example:"amd64"`
	Source        string  `json:"source" example:"str_init"`
	PythonVersion *string `json:"pythonVersion" example:"3.12"`
}

type CreateToolsetUploadRequest struct {
	ToolsetID string `json:"toolsetId" validate:"required,uuid" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Filename  string `json:"filename" validate:"required,zipname" example:"order_tools.zip"`
	TotalSize int64  `json:"totalSize" validate:"required,min=1" example:"9437184"`
}

type ReplaceToolsetUploadChunkRequest struct {
	ToolsetUploadID string  `path:"toolsetUploadId" json:"-"`
	ID              int     `path:"id" json:"-"`
	Chunk           RawBody `json:"-"`
}

type toolsetUploadHandler struct {
	svc      *entplatform.ToolsetService
	uploads  *entplatform.ChunkedUploadManager
	registry *entplatform.STRRuntimeRegistry
}

func toolsetUploadRoutes(deps *entplatform.RouteDeps, _ *platpkg.PlatformCore, _ *slog.Logger) []Route {
	var routes []Route
	h := &toolsetUploadHandler{svc: deps.ToolsetSvc, uploads: deps.ChunkedUploadMgr, registry: deps.STRRuntimeRegistry}
	if deps.ToolsetSvc != nil && deps.ChunkedUploadMgr != nil {
		routes = append(routes,
			Route{Method: http.MethodPost, Path: "/toolsetUploads", Scope: rbac.ScopeToolsetUpdate, AllowAPIKey: true, Published: true,
				Stability: StabilityStable,
				Doc: Doc{ID: "createToolsetUpload", Summary: "Initiate a chunked upload of a toolset's bundle",
					Text: "Starts an upload of a toolset's ZIP in chunks, for a bundle too large for PUT /toolsets/{id}/bundle. The response gives the chunkSize to split the bundle at. PUT each chunk to /toolsetUploads/{toolsetUploadId}/chunks/{id}, then complete the upload. An upload is discarded 1 hour after it is created.", Tag: toolsetUploadsTag},
				Handler: Handle(h.create, "Unable to create toolset upload")},
			Route{Method: http.MethodGet, Path: "/toolsetUploads/{id}", Scope: rbac.ScopeToolsetUpdate, AllowAPIKey: true, Published: true,
				Stability: StabilityStable,
				Doc: Doc{ID: "getToolsetUpload", Summary: "Get a chunked toolset upload",
					Text: "Returns the upload's progress, to resume after an interruption: send the chunks missing from receivedChunks. After the upload is completed or has expired, the request fails with 404.", Tag: toolsetUploadsTag},
				Handler: Handle(h.get, "Unable to get toolset upload")},
			Route{Method: http.MethodPut, Path: "/toolsetUploads/{toolsetUploadId}/chunks/{id}", Scope: rbac.ScopeToolsetUpdate, AllowAPIKey: true, Published: true,
				Stability: StabilityStable,
				Doc: Doc{ID: "replaceToolsetUploadChunk", Summary: "Replace a chunk of a toolset upload",
					Text: "Stores the raw request body as chunk {id} of the upload, replacing any chunk already sent at that index. Every chunk but the last must be exactly chunkSize bytes. If the index is past the last chunk or the chunk is the wrong size, the request fails with 422. If the chunks received would exceed totalSize, the request fails with 413.", Tag: toolsetUploadsTag,
					Errors: []int{http.StatusRequestEntityTooLarge}},
				Handler: Handle(h.putChunk, "Unable to store toolset upload chunk")},
			Route{Method: http.MethodPut, Path: "/toolsetUploads/{id}/complete", Scope: rbac.ScopeToolsetUpdate, AllowAPIKey: true, Published: true,
				Stability: StabilityStable,
				Doc: Doc{ID: "completeToolsetUpload", Summary: "Update a toolset upload to complete it",
					Text: "Assembles the chunks, stores them as the toolset's bundle, and returns the toolset. Its discoveryStatus is pending until the tool runtime has found its tools. Agents that use the toolset are redeployed. If a chunk is missing, the request fails with 422. After the chunks are assembled, the upload is discarded.", Tag: toolsetUploadsTag},
				Handler: Handle(h.complete, "Unable to complete toolset upload")},
		)
	}
	return append(routes,
		Route{Method: http.MethodGet, Path: "/toolsetBuildTarget", Scope: rbac.ScopeToolsetRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getToolsetBuildTarget", Summary: "Get the build target for toolset bundles",
				Text: "Returns the platform and Python version to build a toolset's bundle for. If tool runtimes on different platforms are active, the request fails with 409 and lists the targets in validationDetails.", Tag: toolsetBuildTargetTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.buildTarget, "Unable to get toolset build target")},
	)
}

func (h *toolsetUploadHandler) create(ctx context.Context, req CreateToolsetUploadRequest) (ToolsetUpload, error) {
	if _, err := h.svc.GetByID(ctx, req.ToolsetID); err != nil {
		return ToolsetUpload{}, err
	}
	id, _, err := h.uploads.InitFixed(ctx, req.ToolsetID, req.Filename, req.TotalSize)
	if err != nil {
		return ToolsetUpload{}, err
	}
	return h.get(ctx, IDRequest{ID: id})
}

func (h *toolsetUploadHandler) get(ctx context.Context, req IDRequest) (ToolsetUpload, error) {
	status, err := h.uploads.Status(ctx, req.ID)
	if err != nil {
		return ToolsetUpload{}, err
	}
	return ToolsetUpload{
		ID:             req.ID,
		Type:           toolsetUploadType,
		ToolsetID:      status.ToolsetID,
		Filename:       status.Filename,
		TotalSize:      status.TotalSize,
		ChunkSize:      status.ChunkSize,
		ReceivedBytes:  status.ReceivedBytes,
		ReceivedChunks: nonNilList(status.Received),
		CreatedTime:    Instant(status.CreatedAt.UnixMilli()),
		ExpiresTime:    Instant(status.ExpiresAt.UnixMilli()),
	}, nil
}

func (h *toolsetUploadHandler) putChunk(ctx context.Context, req ReplaceToolsetUploadChunkRequest) (struct{}, error) {
	if len(req.Chunk) == 0 {
		return struct{}{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
			Details: map[string][]string{"body": {"cannot be empty"}}}
	}
	err := h.uploads.PutChunk(ctx, req.ToolsetUploadID, req.ID, req.Chunk)
	switch {
	case errors.Is(err, entplatform.ErrChunkIndexOutOfRange):
		return struct{}{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Cause: err,
			Details: map[string][]string{"id": {"is past the last chunk of the upload's totalSize"}}}
	case errors.Is(err, entplatform.ErrUploadExceedsDeclaredSize):
		return struct{}{}, &CallerError{Status: http.StatusRequestEntityTooLarge, Message: "The chunks exceed the upload's declared totalSize", Cause: err}
	}
	if mismatch, ok := errors.AsType[*entplatform.ChunkSizeMismatchError](err); ok {
		return struct{}{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Cause: err,
			Details: map[string][]string{"body": {fmt.Sprintf("must be %d bytes for chunk %d", mismatch.Want, mismatch.Index)}}}
	}
	return struct{}{}, err
}

func (h *toolsetUploadHandler) complete(ctx context.Context, req IDRequest) (Toolset, error) {
	data, session, err := h.uploads.Assemble(ctx, req.ID)
	if err != nil {
		return Toolset{}, err
	}
	defer h.uploads.Cleanup(ctx, req.ID)

	err = h.svc.Upload(ctx, session.ToolsetID, session.Filename, data, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Toolset{}, err
	}
	resp, err := h.svc.GetByID(ctx, session.ToolsetID)
	if err != nil {
		return Toolset{}, err
	}
	return toToolset(resp), nil
}

func (h *toolsetUploadHandler) buildTarget(context.Context, struct{}) (ToolsetBuildTarget, error) {
	resp, err := h.registry.BuildTarget()
	if multi, ok := errors.AsType[*entplatform.MultipleBuildTargetsError](err); ok {
		return ToolsetBuildTarget{}, &CallerError{Status: http.StatusConflict, Cause: err,
			Message: "Tool runtimes on different platforms are active, so no single build target applies; set SAM_TOOL_TARGET_OS and SAM_TOOL_TARGET_ARCH to choose one",
			Details: map[string][]string{"targets": multi.Targets}}
	}
	if err != nil {
		return ToolsetBuildTarget{}, err
	}
	source := buildTargetSourceFallback
	if resp.Source == entplatform.BuildTargetSourceSTRInit {
		source = buildTargetSourceSTRInit
	}
	return ToolsetBuildTarget{OS: resp.OS, Arch: resp.Arch, Source: source, PythonVersion: NullIfZero(resp.PythonVersion)}, nil
}
