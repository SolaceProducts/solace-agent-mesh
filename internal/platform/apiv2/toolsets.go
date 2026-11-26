// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	originCustom  = "custom"
	originBuiltin = "builtin"
)

var origins = []string{originCustom, originBuiltin}

type Toolset struct {
	ID              string         `json:"id" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Type            string         `json:"type" example:"toolset"`
	Origin          string         `json:"origin" example:"custom"`
	Name            string         `json:"name" example:"order_tools"`
	Description     *string        `json:"description" example:"Look up and update customer orders."`
	Runtime         *string        `json:"runtime" example:"python"`
	DiscoveryStatus *string        `json:"discoveryStatus" example:"ready"`
	DiscoveryErrors []string       `json:"discoveryErrors" example:"[]"`
	Bundle          *ToolsetBundle `json:"bundle"`
	Tools           []ToolsetTool  `json:"tools"`
	Agents          []ToolsetAgent `json:"agents"`
	ConfigSummary   *ConfigSummary `json:"configSummary"`
	Capabilities    map[string]any `json:"capabilities" example:"{\"profileProvider\":true}"`
	Default         bool           `json:"default" example:"false"`
	Aliases         []string       `json:"aliases" example:"[\"web_tools\"]"`
	CreatedBy       *string        `json:"createdBy" example:"user@example.com"`
	UpdatedBy       *string        `json:"updatedBy" example:"user@example.com"`
	CreatedTime     Instant        `json:"createdTime"`
	UpdatedTime     Instant        `json:"updatedTime"`
}

type ToolsetBundle struct {
	Filename    string  `json:"filename" example:"order_tools.zip"`
	SizeBytes   *int64  `json:"sizeBytes" example:"48213"`
	ContentHash *string `json:"contentHash" example:"9f2c4e1a7b3d5f60812a4c6e8b0d2f4a6c8e0b2d4f6a8c0e2b4d6f8a0c2e4b6d"`
}

type ToolsetTool struct {
	ToolName       string  `json:"toolName" example:"lookup_order"`
	Description    *string `json:"description" example:"Find an order by its number."`
	TimeoutSeconds *int    `json:"timeoutSeconds" example:"30"`
	SandboxProfile *string `json:"sandboxProfile" example:"default"`
	Parameters     any     `json:"parameters" example:"{\"type\":\"object\",\"properties\":{\"orderNumber\":{\"type\":\"string\"}}}"`
	ConfigSchema   any     `json:"configSchema" example:"[{\"key\":\"region\",\"type\":\"string\",\"required\":true}]"`
}

type ToolsetAgent struct {
	ID          string  `json:"id" example:"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d"`
	Name        string  `json:"name" example:"Support agent"`
	Description *string `json:"description" example:"Answers customer questions."`
}

type ConfigSummary struct {
	RequiredFields            []string `json:"requiredFields" example:"[\"region\",\"api_key\"]"`
	RequiredCoveredByDefaults []string `json:"requiredCoveredByDefaults" example:"[\"region\"]"`
	RequiresAgentConfig       bool     `json:"requiresAgentConfig" example:"true"`
}

type ToolsetConfiguration struct {
	Values      map[string]any `json:"values" example:"{\"lookup_order\":{\"region\":\"us-east-1\",\"api_key\":\"<REDACTED>\"}}"`
	UpdatedBy   *string        `json:"updatedBy" example:"user@example.com"`
	UpdatedTime Instant        `json:"updatedTime"`
}

type CreateToolsetRequest struct {
	Name        string `json:"name" validate:"required" example:"order_tools"`
	Description string `json:"description" example:"Look up and update customer orders."`
}

type UpdateToolsetRequest struct {
	ID          string        `path:"id,builtin" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Name        Patch[string] `json:"name" example:"order_tools"`
	Description Patch[string] `json:"description" clearable:"true" example:"Look up and update customer orders."`
}

type UploadToolsetBundleRequest struct {
	ID   string       `path:"id,builtin" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	File UploadedFile `form:"file" json:"-"`
}

type UpdateToolsetConfigurationRequest struct {
	ID     string                `path:"id,builtin" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Values Patch[map[string]any] `json:"values" example:"{\"lookup_order\":{\"region\":\"eu-west-1\"}}"`
}

type ToolsetSecretsDiffRequest struct {
	ID     string         `path:"id,builtin" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Values map[string]any `json:"values" validate:"required" example:"{\"api_key\":\"<REDACTED>\"}"`
}

type listToolsetsRequest struct {
	Page            Page   `json:"-"`
	Origin          string `query:"origin" json:"-" example:"custom"`
	DiscoveryStatus string `query:"discoveryStatus" json:"-" example:"ready"`
}

type toolsetIDRequest struct {
	ID string `path:"id,builtin" json:"-" example:"builtin_artifact_tools"`
}

type toolsetHandler struct {
	svc       *entplatform.ToolsetService
	analytics *platpkg.ResourceReporter
	logger    *slog.Logger
}

const toolsetPath = "/toolsets/{id}"

var toolsetDiscoveryStatuses = []string{"pending", "ready", "failed", "removed"}

func toolsetRoutes(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, logger *slog.Logger) []Route {
	if deps.ToolsetSvc == nil {
		return nil
	}
	h := &toolsetHandler{svc: deps.ToolsetSvc, analytics: core.ResourceAnalytics(), logger: logger}
	return []Route{
		{Method: http.MethodGet, Path: "/toolsets", Scope: rbac.ScopeToolsetRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listToolsets", Summary: "Get a list of toolsets",
				Text: "Returns one page of toolsets, the built-in ones first, optionally only custom or built-in ones, or custom ones in one discovery status.", Tag: toolsetsTag},
			Handler: Handle(h.list, "Unable to list toolsets")},
		{Method: http.MethodPost, Path: "/toolsets", Scope: rbac.ScopeToolsetCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createToolset", Summary: "Create a toolset",
				Text: "Creates a toolset with no tools. Upload its bundle with PUT /toolsets/{id}/bundle, or in chunks with POST /toolsetUploads.", Tag: toolsetsTag},
			Handler: Handle(h.create, "Unable to create toolset")},
		{Method: http.MethodGet, Path: toolsetPath, Scope: rbac.ScopeToolsetRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getToolset", Summary: "Get a toolset",
				Text: "Returns one toolset, by its UUID or, for a built-in toolset, its id.", Tag: toolsetsTag},
			Handler: Handle(h.get, "Unable to get toolset")},
		{Method: http.MethodPatch, Path: toolsetPath, Scope: rbac.ScopeToolsetUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateToolset", Summary: "Update a toolset",
				Text: "Changes the fields sent and keeps the rest. Null clears the description. The name cannot change after a bundle is uploaded. For a built-in toolset, which cannot be changed, the request fails with 409.", Tag: toolsetsTag},
			Handler: Handle(h.update, "Unable to update toolset")},
		{Method: http.MethodDelete, Path: toolsetPath, Scope: rbac.ScopeToolsetDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteToolset", Summary: "Delete a toolset",
				Text: "Deletes the toolset and its bundle, and removes it from every agent that uses it. Those agents are redeployed. For a built-in toolset, which cannot be deleted, the request fails with 409.", Tag: toolsetsTag},
			Handler: Handle(h.delete, "Unable to delete toolset")},
		{Method: http.MethodGet, Path: "/toolsets/{id}/bundle", Scope: rbac.ScopeToolsetRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getToolsetBundle", FileTypes: []string{"application/zip"}, Summary: "Get a toolset's bundle",
				Text: "Downloads the toolset's ZIP as application/zip. For a toolset with no bundle, and for a built-in toolset, the request fails with 409.", Tag: toolsetsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.getBundle, "Unable to download toolset bundle")},
		{Method: http.MethodPut, Path: "/toolsets/{id}/bundle", Scope: rbac.ScopeToolsetUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceToolsetBundle", Summary: "Replace a toolset's bundle",
				Text: "Uploads the toolset's ZIP as the file part of a multipart/form-data body and returns the toolset. Its discoveryStatus is pending until the tool runtime has found its tools. Agents that use the toolset are redeployed. If the body is over 100 MB, the request fails with 413. Upload a larger bundle with POST /toolsetUploads.", Tag: toolsetsTag,
				Errors: []int{http.StatusRequestEntityTooLarge}},
			Handler: Handle(h.putBundle, "Unable to upload toolset bundle")},
		{Method: http.MethodGet, Path: "/toolsets/{id}/config", Scope: rbac.ScopeToolsetRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getToolsetConfiguration", Summary: "Get a toolset's configuration",
				Text: "Returns the settings the toolset gives every agent that uses it. Secret values are redacted. The request fails with 409 until the toolset's discoveryStatus is ready.", Tag: toolsetsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.getConfig, "Unable to get toolset configuration")},
		{Method: http.MethodPatch, Path: "/toolsets/{id}/config", Scope: rbac.ScopeToolsetUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateToolsetConfiguration", Summary: "Update a toolset's configuration",
				Text: "Changes the settings sent and keeps the rest, then redeploys the agents that use the toolset. For a built-in toolset, and until the toolset's discoveryStatus is ready, the request fails with 409.", Tag: toolsetsTag},
			Handler: Handle(h.updateConfig, "Unable to update toolset configuration")},
		{Method: http.MethodPut, Path: "/toolsets/{id}/secretsDiff", Scope: rbac.ScopeToolsetUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "compareToolsetSecrets", Summary: "Validate settings against a toolset's stored secrets",
				Text: "Returns, for each secret setting of the toolset, whether saving the settings sent would keep the stored secret. Secret values are never returned. A built-in toolset has no secrets. The request fails with 409 until the toolset's discoveryStatus is ready.", Tag: toolsetsTag},
			Handler: Handle(h.secretsDiff, "Unable to compare toolset secrets")},
	}
}

func (h *toolsetHandler) list(ctx context.Context, req listToolsetsRequest) (List[Toolset], error) {
	details := map[string][]string{}
	CheckOneOfQuery(details, "origin", req.Origin, origins)
	CheckOneOfQuery(details, "discoveryStatus", req.DiscoveryStatus, toolsetDiscoveryStatuses)
	var discoveryStatus *string
	if req.DiscoveryStatus != "" {
		discoveryStatus = &req.DiscoveryStatus
	}
	if len(details) > 0 {
		return List[Toolset]{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgInvalidQueryParam, Details: details}
	}
	resp, err := h.svc.ListPaginated(ctx, req.Page.Number, req.Page.Size, req.Origin, discoveryStatus)
	if err != nil {
		return List[Toolset]{}, err
	}
	return ListOf(resp, toToolset), nil
}

func (h *toolsetHandler) create(ctx context.Context, req CreateToolsetRequest) (Toolset, error) {
	resp, err := h.svc.Create(ctx, entplatform.CreateToolsetRequest{Name: req.Name, Description: req.Description}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Toolset{}, err
	}
	h.analytics.ToolsetCreated(ctx)
	return toToolset(resp), nil
}

func (h *toolsetHandler) get(ctx context.Context, req toolsetIDRequest) (Toolset, error) {
	resp, err := h.find(ctx, req.ID)
	if err != nil {
		return Toolset{}, err
	}
	return toToolset(resp), nil
}

func (h *toolsetHandler) find(ctx context.Context, id string) (*entplatform.ToolsetResponse, error) {
	if IsCanonicalUUID(id) {
		return h.svc.GetByID(ctx, id)
	}
	return h.svc.GetBuiltin(ctx, id)
}

func (h *toolsetHandler) refuseBuiltin(ctx context.Context, id, message string) error {
	if IsCanonicalUUID(id) {
		return nil
	}
	if _, err := h.svc.GetBuiltin(ctx, id); err != nil {
		return err
	}
	return &CallerError{Status: http.StatusConflict, Message: message}
}

const msgBuiltinToolsetFixed = "Built-in toolsets cannot be changed"

func (h *toolsetHandler) update(ctx context.Context, req UpdateToolsetRequest) (Toolset, error) {
	if err := h.refuseBuiltin(ctx, req.ID, msgBuiltinToolsetFixed); err != nil {
		return Toolset{}, err
	}
	current, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Toolset{}, err
	}
	update, err := req.toUpdateToolsetRequest(current)
	if err != nil {
		return Toolset{}, err
	}
	resp, err := h.svc.Update(ctx, req.ID, update, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Toolset{}, err
	}
	return toToolset(resp), nil
}

func (req UpdateToolsetRequest) toUpdateToolsetRequest(current *entplatform.ToolsetResponse) (entplatform.UpdateToolsetRequest, error) {
	update := entplatform.UpdateToolsetRequest{Name: current.Name, Description: current.Description}
	if req.Name.Set {
		if req.Name.Null || req.Name.Value == "" {
			return update, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
				Details: map[string][]string{"name": {"cannot be empty"}}}
		}
		update.Name = req.Name.Value
	}
	if req.Description.Set {
		update.Description = req.Description.Value
	}
	return update, nil
}

func (h *toolsetHandler) delete(ctx context.Context, req toolsetIDRequest) (struct{}, error) {
	if err := h.refuseBuiltin(ctx, req.ID, "Built-in toolsets cannot be deleted"); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, h.svc.Delete(ctx, req.ID)
}

func (h *toolsetHandler) getBundle(ctx context.Context, req toolsetIDRequest) (FileResponse, error) {
	if err := h.refuseBuiltin(ctx, req.ID, "Built-in toolsets have no bundle"); err != nil {
		return FileResponse{}, err
	}
	current, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return FileResponse{}, err
	}
	if current.ZipFilename == nil || *current.ZipFilename == "" {
		return FileResponse{}, &CallerError{Status: http.StatusConflict, Message: "The toolset has no bundle yet; upload one first"}
	}
	body, filename, size, err := h.svc.Download(ctx, req.ID)
	if err != nil {
		return FileResponse{}, err
	}
	return FileResponse{ContentType: "application/zip", Filename: filename, Size: size, Body: body}, nil
}

func (h *toolsetHandler) putBundle(ctx context.Context, req UploadToolsetBundleRequest) (Toolset, error) {
	if err := h.refuseBuiltin(ctx, req.ID, msgBuiltinToolsetFixed); err != nil {
		return Toolset{}, err
	}
	if err := CheckZipName("file", req.File.Name); err != nil {
		return Toolset{}, err
	}
	if err := h.svc.Upload(ctx, req.ID, req.File.Name, req.File.Data, entplatform.UserIDFromContext(ctx)); err != nil {
		return Toolset{}, err
	}
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Toolset{}, err
	}
	return toToolset(resp), nil
}

func (h *toolsetHandler) getConfig(ctx context.Context, req toolsetIDRequest) (ToolsetConfiguration, error) {
	if !IsCanonicalUUID(req.ID) {
		if _, err := h.svc.GetBuiltin(ctx, req.ID); err != nil {
			return ToolsetConfiguration{}, err
		}
		return ToolsetConfiguration{Values: map[string]any{}}, nil
	}
	resp, err := h.svc.GetConfig(ctx, req.ID)
	if err != nil {
		return ToolsetConfiguration{}, notReadyIs409(err, msgToolsetNotReady)
	}
	return toToolsetConfiguration(resp), nil
}

const msgToolsetNotReady = entplatform.ToolsetNotDiscoveredMessage + "; wait until its discoveryStatus is ready"

func (h *toolsetHandler) updateConfig(ctx context.Context, req UpdateToolsetConfigurationRequest) (ToolsetConfiguration, error) {
	if err := h.refuseBuiltin(ctx, req.ID, msgBuiltinToolsetFixed); err != nil {
		return ToolsetConfiguration{}, err
	}
	switch {
	case req.Values.Null:
		return ToolsetConfiguration{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
			Details: map[string][]string{"values": {"cannot be null"}}}
	case !req.Values.Set:
		return h.getConfig(ctx, toolsetIDRequest{ID: req.ID})
	}
	resp, err := h.svc.PatchConfig(ctx, req.ID, entplatform.ToolsetConfigPatchRequest{Values: req.Values.Value}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return ToolsetConfiguration{}, notReadyIs409(err, msgToolsetNotReady)
	}
	return toToolsetConfiguration(resp), nil
}

func (h *toolsetHandler) secretsDiff(ctx context.Context, req ToolsetSecretsDiffRequest) (SecretsDiffResult, error) {
	if !IsCanonicalUUID(req.ID) {
		if _, err := h.svc.GetBuiltin(ctx, req.ID); err != nil {
			return SecretsDiffResult{}, err
		}
		return SecretsDiffResult{Secrets: map[string]SecretMatch{}}, nil
	}
	diff, err := h.svc.GetSecretsDiff(ctx, req.ID, req.Values)
	if err != nil {
		return SecretsDiffResult{}, notReadyIs409(err, msgToolsetNotReady)
	}
	entplatform.LogSecretsDiff(ctx, h.logger, "toolset", req.ID, diff)
	return toSecretsDiffResult(diff), nil
}

func notReadyIs409(err error, message string) error {
	if errors.Is(err, entplatform.ErrDiscoveryNotReady) {
		return &CallerError{Status: http.StatusConflict, Message: message, Cause: err}
	}
	return err
}

func toToolset(resp *entplatform.ToolsetResponse) Toolset {
	t := Toolset{
		ID:              resp.ID,
		Type:            "toolset",
		Origin:          resp.Type,
		Name:            resp.Name,
		Description:     NullIfZero(resp.Description),
		Runtime:         resp.Runtime,
		DiscoveryStatus: NullIfZero(resp.DiscoveryStatus),
		DiscoveryErrors: nonNilList(resp.DiscoveryErrors),
		Tools:           make([]ToolsetTool, len(resp.Tools)),
		Agents:          make([]ToolsetAgent, len(resp.Agents)),
		ConfigSummary:   toConfigSummary(resp.ConfigSummary),
		Capabilities:    resp.Capabilities,
		Default:         resp.Default,
		Aliases:         nonNilList(resp.Aliases),
		CreatedBy:       NullIfZero(resp.CreatedBy),
		UpdatedBy:       NullIfZero(resp.UpdatedBy),
		CreatedTime:     Instant(resp.CreatedTime),
		UpdatedTime:     Instant(resp.UpdatedTime),
	}
	if t.Capabilities == nil {
		t.Capabilities = map[string]any{}
	}
	if resp.ZipFilename != nil && *resp.ZipFilename != "" {
		t.Bundle = &ToolsetBundle{Filename: *resp.ZipFilename, SizeBytes: resp.ZipFileSize, ContentHash: NullIfZero(resp.ContentHash)}
	}
	for i, tool := range resp.Tools {
		t.Tools[i] = ToolsetTool{
			ToolName:       tool.ToolName,
			Description:    NullIfZero(tool.ToolDescription),
			TimeoutSeconds: NullIfZero(tool.TimeoutSeconds),
			SandboxProfile: NullIfZero(tool.SandboxProfile),
			Parameters:     tool.Parameters,
			ConfigSchema:   tool.ConfigSchema,
		}
	}
	for i, a := range resp.Agents {
		t.Agents[i] = ToolsetAgent{ID: a.ID, Name: a.Name, Description: NullIfZero(a.Description)}
	}
	return t
}

func toConfigSummary(c *entplatform.ConfigSummary) *ConfigSummary {
	if c == nil {
		return nil
	}
	return &ConfigSummary{
		RequiredFields:            nonNilList(c.RequiredFields),
		RequiredCoveredByDefaults: nonNilList(c.RequiredCoveredByDefaults),
		RequiresAgentConfig:       c.RequiresAgentConfig,
	}
}

func toToolsetConfiguration(resp *entplatform.ToolsetConfigResponse) ToolsetConfiguration {
	values := resp.Values
	if values == nil {
		values = map[string]any{}
	}
	return ToolsetConfiguration{Values: values, UpdatedBy: NullIfZero(resp.UpdatedBy), UpdatedTime: Instant(resp.UpdatedAt)}
}
