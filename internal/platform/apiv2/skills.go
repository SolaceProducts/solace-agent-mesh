// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillsecret"
)

const (
	skillFileCategoryReference = "reference"
	skillFileCategoryAsset     = "asset"
	skillReferencesPrefix      = "references/"
	skillAssetsPrefix          = "assets/"
)

type Skill struct {
	ID              string             `json:"id" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Type            string             `json:"type" example:"skill"`
	Origin          string             `json:"origin" example:"custom"`
	Name            string             `json:"name" example:"order-lookup"`
	Label           *string            `json:"label" example:"SAM documentation"`
	Description     *string            `json:"description" example:"Look up and explain customer orders."`
	Tags            []string           `json:"tags" example:"[\"orders\",\"support\"]"`
	DiscoveryStatus string             `json:"discoveryStatus" example:"ready"`
	HasReferences   bool               `json:"hasReferences" example:"true"`
	HasAssets       bool               `json:"hasAssets" example:"false"`
	HasTools        bool               `json:"hasTools" example:"true"`
	ResourceCount   int                `json:"resourceCount" example:"3"`
	ReferenceCount  int                `json:"referenceCount" example:"2"`
	AssetCount      int                `json:"assetCount" example:"1"`
	BundledTools    []SkillBundledTool `json:"bundledTools"`
	Toolsets        []string           `json:"toolsets" example:"[\"order_tools\"]"`
	Bundle          *SkillBundle       `json:"bundle"`
	Agents          []SkillAgent       `json:"agents"`
	ConfigSummary   *ConfigSummary     `json:"configSummary"`
	CreatedBy       *string            `json:"createdBy" example:"user@example.com"`
	UpdatedBy       *string            `json:"updatedBy" example:"user@example.com"`
	CreatedTime     Instant            `json:"createdTime"`
	UpdatedTime     Instant            `json:"updatedTime"`
}

type SkillBundle struct {
	ContentHash *string `json:"contentHash" example:"9f2c4e1a7b3d5f60812a4c6e8b0d2f4a6c8e0b2d4f6a8c0e2b4d6f8a0c2e4b6d"`
}

type SkillBundledTool struct {
	ToolName       string                        `json:"toolName" example:"order-lookup__find_order"`
	Description    *string                       `json:"description" example:"Find an order by its number."`
	Instructions   *string                       `json:"instructions" example:"Call this before quoting an order's status."`
	Parameters     any                           `json:"parameters" example:"{\"type\":\"object\",\"properties\":{\"orderNumber\":{\"type\":\"string\"}}}"`
	ArtifactParams map[string]SkillArtifactParam `json:"artifactParams" example:"{\"report\":{\"isList\":false,\"isOptional\":true,\"filenameKey\":null}}"`
	ConfigSchema   any                           `json:"configSchema" example:"[{\"key\":\"api_key\",\"type\":\"string\",\"secret\":true}]"`
}

type SkillArtifactParam struct {
	IsList      bool    `json:"isList" example:"false"`
	IsOptional  bool    `json:"isOptional" example:"true"`
	FilenameKey *string `json:"filenameKey" example:"filename"`
}

type SkillAgent struct {
	ID          string  `json:"id" example:"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d"`
	Name        string  `json:"name" example:"Support agent"`
	Description *string `json:"description" example:"Answers customer questions."`
}

type SkillFile struct {
	Path      string `json:"path" example:"references/guide.md"`
	Category  string `json:"category" example:"reference"`
	SizeBytes int64  `json:"sizeBytes" example:"2048"`
}

type SkillConfiguration struct {
	Values      map[string]map[string]any `json:"values" example:"{\"find_order\":{\"region\":\"us-east-1\",\"api_key\":\"<REDACTED>\"}}"`
	UpdatedBy   *string                   `json:"updatedBy" example:"user@example.com"`
	UpdatedTime Instant                   `json:"updatedTime"`
}

type CreateSkillRequest struct {
	File        UploadedFile `form:"file" json:"-"`
	Description string       `form:"description" json:"-" example:"Look up and explain customer orders."`
}

type UpdateSkillRequest struct {
	ID          string        `path:"id,builtin" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Description Patch[string] `json:"description" example:"Look up and explain customer orders."`
	Name        Patch[string] `json:"name" example:"order-lookup"`
}

type ReplaceSkillBundleRequest struct {
	ID   string       `path:"id,builtin" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	File UploadedFile `form:"file" json:"-"`
}

type UpdateSkillConfigurationRequest struct {
	ID     string                           `path:"id,builtin" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Values Patch[map[string]map[string]any] `json:"values" example:"{\"find_order\":{\"region\":\"eu-west-1\"}}"`
}

type SkillSecretsDiffRequest struct {
	ID     string         `path:"id,builtin" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Values map[string]any `json:"values" validate:"required" example:"{\"find_order\":{\"api_key\":\"<REDACTED>\"}}"`
}

type listSkillsRequest struct {
	Page   Page   `json:"-"`
	Origin string `query:"origin" json:"-" example:"custom"`
}

type skillIDRequest struct {
	ID string `path:"id,builtin" json:"-" example:"sam-docs"`
}

type listSkillFilesRequest struct {
	ID       string `path:"id,builtin" json:"-" example:"sam-docs"`
	Page     Page   `json:"-"`
	Category string `query:"category" json:"-" example:"reference"`
}

type getSkillFileContentRequest struct {
	ID   string `path:"id,builtin" json:"-" example:"sam-docs"`
	Path string `query:"path" json:"-" example:"references/guide.md"`
}

type skillHandler struct {
	svc    *entplatform.SkillService
	logger *slog.Logger
}

const skillPath = "/skills/{id}"

var skillFileCategories = []string{skillFileCategoryReference, skillFileCategoryAsset}

const skillStatusCreated = "created"

const (
	msgBuiltinSkillFixed = "Built-in skills cannot be changed"
	msgSkillNotReady     = "The skill's tools are not discovered yet; wait until its discoveryStatus is ready"
)

func skillRoutes(deps *entplatform.RouteDeps, _ *platpkg.PlatformCore, logger *slog.Logger) []Route {
	if deps.SkillSvc == nil {
		return nil
	}
	h := &skillHandler{svc: deps.SkillSvc, logger: logger}
	return []Route{
		{Method: http.MethodGet, Path: "/skills", Scope: rbac.ScopeSkillRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listSkills", Summary: "Get a list of skills",
				Text: "Returns one page of skills, the built-in ones first, optionally only custom or built-in ones.", Tag: skillsTag},
			Handler: Handle(h.list, "Unable to list skills")},
		{Method: http.MethodPost, Path: "/skills", Scope: rbac.ScopeSkillCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createSkill", Summary: "Create a skill",
				Text: "Creates a skill from a ZIP sent as the file part of a multipart/form-data body, with an optional description part. The skill takes its name from the ZIP's SKILL.md. Its discoveryStatus is pending until the tool runtime has read the bundle. If a skill with that name exists, the request fails with 409.", Tag: skillsTag,
				Errors: []int{http.StatusRequestEntityTooLarge}},
			Handler: Handle(h.create, "Unable to create skill")},
		{Method: http.MethodGet, Path: skillPath, Scope: rbac.ScopeSkillRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getSkill", Summary: "Get a skill",
				Text: "Returns one skill, by its UUID or, for a built-in skill, its name.", Tag: skillsTag},
			Handler: Handle(h.get, "Unable to get skill")},
		{Method: http.MethodPatch, Path: skillPath, Scope: rbac.ScopeSkillUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateSkill", Summary: "Update a skill",
				Text: "Changes the description. The name comes from the bundle's SKILL.md and cannot be changed. For a built-in skill, which cannot be changed, the request fails with 409.", Tag: skillsTag},
			Handler: Handle(h.update, "Unable to update skill")},
		{Method: http.MethodDelete, Path: skillPath, Scope: rbac.ScopeSkillDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteSkill", Summary: "Delete a skill",
				Text: "Deletes the skill and its bundle, and removes it from every agent that uses it. For a built-in skill, which cannot be deleted, the request fails with 409.", Tag: skillsTag},
			Handler: Handle(h.delete, "Unable to delete skill")},
		{Method: http.MethodGet, Path: "/skills/{id}/bundle", Scope: rbac.ScopeSkillRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getSkillBundle", FileTypes: []string{"application/zip"}, Summary: "Get a skill's bundle",
				Text: "Downloads the skill's ZIP as application/zip, named after the skill. For a skill with no bundle, and for a built-in skill, the request fails with 409.", Tag: skillsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.getBundle, "Unable to download skill bundle")},
		{Method: http.MethodPut, Path: "/skills/{id}/bundle", Scope: rbac.ScopeSkillUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceSkillBundle", Summary: "Replace a skill's bundle",
				Text: "Uploads the skill's ZIP as the file part of a multipart/form-data body and returns the skill. Its discoveryStatus is pending until the tool runtime has read the bundle. The ZIP's SKILL.md must name the skill. If the body is over 100 MB, the request fails with 413.", Tag: skillsTag,
				Errors: []int{http.StatusRequestEntityTooLarge}},
			Handler: Handle(h.putBundle, "Unable to upload skill bundle")},
		{Method: http.MethodGet, Path: "/skills/{id}/files", Scope: rbac.ScopeSkillRead, AllowAPIKey: false,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listSkillFiles", Summary: "Get a list of a skill's files",
				Text: "Returns one page of the reference and asset files in the skill's bundle, references first, optionally only one category. The list is empty for a skill with no bundle, and for a built-in skill.", Tag: skillsTag},
			Handler: Handle(h.listFiles, "Unable to list skill files")},
		{Method: http.MethodGet, Path: "/skills/{id}/fileContent", Scope: rbac.ScopeSkillRead, AllowAPIKey: false,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getSkillFileContent", FileTypes: []string{anyFileType}, Summary: "Get a file from a skill's bundle",
				Text: "Returns the bytes of SKILL.md or of a file under references/ or assets/, for showing inline. For any other path, and for a file over 1 MB, the request fails with 422. If the bundle has no such file, and for a built-in skill, the request fails with 404.", Tag: skillsTag},
			Handler: Handle(h.getFileContent, "Unable to get skill file")},
		{Method: http.MethodGet, Path: "/skills/{id}/config", Scope: rbac.ScopeSkillRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getSkillConfiguration", Summary: "Get a skill's configuration",
				Text: "Returns the settings the skill gives every agent that uses it. Secret values are redacted. A built-in skill has no settings. The request fails with 409 until the skill's discoveryStatus is ready.", Tag: skillsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.getConfig, "Unable to get skill configuration")},
		{Method: http.MethodPatch, Path: "/skills/{id}/config", Scope: rbac.ScopeSkillUpdate, AllowAPIKey: false,
			Stability: StabilityDraft,
			Doc: Doc{ID: "updateSkillConfiguration", Summary: "Update a skill's configuration",
				Text: "Changes the settings sent and keeps the rest, then redeploys the agents that use the skill. For a built-in skill, and until the skill's discoveryStatus is ready, the request fails with 409.", Tag: skillsTag},
			Handler: Handle(h.updateConfig, "Unable to update skill configuration")},
		{Method: http.MethodPut, Path: "/skills/{id}/secretsDiff", Scope: rbac.ScopeSkillUpdate, AllowAPIKey: false,
			Stability: StabilityDraft,
			Doc: Doc{ID: "compareSkillSecrets", Summary: "Validate settings against a skill's stored secrets",
				Text: "Returns, for each secret setting of the skill's tools, keyed tool.setting, whether saving the settings sent would keep the stored secret. Secret values are never returned. A built-in skill has no secrets. The request fails with 409 until the skill's discoveryStatus is ready.", Tag: skillsTag},
			Handler: Handle(h.secretsDiff, "Unable to compare skill secrets")},
	}
}

func (h *skillHandler) list(ctx context.Context, req listSkillsRequest) (List[Skill], error) {
	if err := OneOfQueryError("origin", req.Origin, origins); err != nil {
		return List[Skill]{}, err
	}
	resp, err := h.svc.ListWithBuiltins(ctx, req.Page.Number, req.Page.Size, req.Origin)
	if err != nil {
		return List[Skill]{}, err
	}
	return ListOf(resp, toSkill), nil
}

func (h *skillHandler) create(ctx context.Context, req CreateSkillRequest) (Skill, error) {
	if err := CheckZipName("file", req.File.Name); err != nil {
		return Skill{}, err
	}
	resp, err := h.svc.CreateWithBundle(ctx, req.Description, req.File.Data, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Skill{}, err
	}
	return toSkill(resp), nil
}

func (h *skillHandler) get(ctx context.Context, req skillIDRequest) (Skill, error) {
	resp, err := h.find(ctx, req.ID)
	if err != nil {
		return Skill{}, err
	}
	return toSkill(resp), nil
}

func (h *skillHandler) find(ctx context.Context, id string) (*entplatform.SkillDetailResponse, error) {
	if IsCanonicalUUID(id) {
		return h.svc.GetByID(ctx, id)
	}
	return h.svc.GetBuiltin(ctx, id)
}

func (h *skillHandler) refuseBuiltin(ctx context.Context, id, message string) error {
	if IsCanonicalUUID(id) {
		return nil
	}
	if _, err := h.svc.GetBuiltin(ctx, id); err != nil {
		return err
	}
	return &CallerError{Status: http.StatusConflict, Message: message}
}

func (h *skillHandler) update(ctx context.Context, req UpdateSkillRequest) (Skill, error) {
	if err := h.refuseBuiltin(ctx, req.ID, msgBuiltinSkillFixed); err != nil {
		return Skill{}, err
	}
	if err := req.validate(); err != nil {
		return Skill{}, err
	}
	if req.Description.Set {
		if _, err := h.svc.Update(ctx, req.ID, entplatform.UpdateSkillRequest{Description: req.Description.Value}, entplatform.UserIDFromContext(ctx)); err != nil {
			return Skill{}, err
		}
	}
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Skill{}, err
	}
	return toSkill(resp), nil
}

func (req UpdateSkillRequest) validate() error {
	details := map[string][]string{}
	if req.Description.Set && (req.Description.Null || req.Description.Value == "") {
		details["description"] = []string{"cannot be empty"}
	}
	if req.Name.Set {
		details["name"] = []string{"cannot be changed"}
	}
	if len(details) == 0 {
		return nil
	}
	return &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
}

func (h *skillHandler) delete(ctx context.Context, req skillIDRequest) (struct{}, error) {
	if err := h.refuseBuiltin(ctx, req.ID, "Built-in skills cannot be deleted"); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, h.svc.DeleteWithCleanup(ctx, req.ID)
}

func (h *skillHandler) getBundle(ctx context.Context, req skillIDRequest) (FileResponse, error) {
	if err := h.refuseBuiltin(ctx, req.ID, "Built-in skills have no bundle"); err != nil {
		return FileResponse{}, err
	}
	current, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return FileResponse{}, err
	}
	if current.DiscoveryStatus == skillStatusCreated {
		return FileResponse{}, skillNoBundleError(nil)
	}
	body, filename, err := h.svc.Download(ctx, req.ID)
	if nf, ok := errors.AsType[*entplatform.NotFoundError](err); ok && nf.Resource == entplatform.SkillBundleResource {
		return FileResponse{}, skillNoBundleError(err)
	}
	if ve, ok := errors.AsType[*entplatform.ValidationError](err); ok && ve.Message == skillsecret.RedactionRefusedMessage {
		return FileResponse{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: ve.Message, Cause: err}
	}
	if err != nil {
		return FileResponse{}, err
	}
	return FileResponse{ContentType: "application/zip", Filename: filename, Body: body}, nil
}

func skillNoBundleError(cause error) error {
	return &CallerError{Status: http.StatusConflict, Message: entplatform.SkillNoBundleMessage + "; upload one first", Cause: cause}
}

func (h *skillHandler) putBundle(ctx context.Context, req ReplaceSkillBundleRequest) (Skill, error) {
	if err := h.refuseBuiltin(ctx, req.ID, msgBuiltinSkillFixed); err != nil {
		return Skill{}, err
	}
	if err := CheckZipName("file", req.File.Name); err != nil {
		return Skill{}, err
	}
	current, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Skill{}, err
	}
	err = h.svc.UploadZIP(ctx, req.ID, current.Name, req.File.Data, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Skill{}, err
	}
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Skill{}, err
	}
	return toSkill(resp), nil
}

func (h *skillHandler) listFiles(ctx context.Context, req listSkillFilesRequest) (List[SkillFile], error) {
	if err := OneOfQueryError("category", req.Category, skillFileCategories); err != nil {
		return List[SkillFile]{}, err
	}
	if !IsCanonicalUUID(req.ID) {
		if _, err := h.svc.GetBuiltin(ctx, req.ID); err != nil {
			return List[SkillFile]{}, err
		}
		return PageOf([]SkillFile{}, req.Page), nil
	}
	resp, err := h.svc.SkillFiles(ctx, req.ID)
	if err != nil {
		return List[SkillFile]{}, err
	}
	files := make([]SkillFile, 0, len(resp.References)+len(resp.Assets))
	if req.Category != skillFileCategoryAsset {
		files = appendSkillFiles(files, resp.References, skillReferencesPrefix, skillFileCategoryReference)
	}
	if req.Category != skillFileCategoryReference {
		files = appendSkillFiles(files, resp.Assets, skillAssetsPrefix, skillFileCategoryAsset)
	}
	return PageOf(files, req.Page), nil
}

func appendSkillFiles(files []SkillFile, refs []entplatform.SkillFileRef, dir, category string) []SkillFile {
	for _, ref := range refs {
		files = append(files, SkillFile{Path: dir + ref.Path, Category: category, SizeBytes: ref.SizeBytes})
	}
	return files
}

func (h *skillHandler) getFileContent(ctx context.Context, req getSkillFileContentRequest) (FileResponse, error) {
	if !IsCanonicalUUID(req.ID) {
		if _, err := h.svc.GetBuiltin(ctx, req.ID); err != nil {
			return FileResponse{}, err
		}
		return FileResponse{}, &entplatform.NotFoundError{Resource: "skill file", ID: req.Path}
	}
	content, contentType, err := h.svc.SkillFileContent(ctx, req.ID, req.Path)
	if err != nil {
		return FileResponse{}, err
	}
	return FileResponse{
		ContentType: contentType,
		Filename:    path.Base(req.Path),
		Size:        int64(len(content)),
		Inline:      true,
		Body:        io.NopCloser(bytes.NewReader(content)),
	}, nil
}

func (h *skillHandler) getConfig(ctx context.Context, req skillIDRequest) (SkillConfiguration, error) {
	if !IsCanonicalUUID(req.ID) {
		if _, err := h.svc.GetBuiltin(ctx, req.ID); err != nil {
			return SkillConfiguration{}, err
		}
		return SkillConfiguration{Values: map[string]map[string]any{}}, nil
	}
	resp, err := h.svc.GetConfig(ctx, req.ID)
	if err != nil {
		return SkillConfiguration{}, notReadyIs409(err, msgSkillNotReady)
	}
	return toSkillConfiguration(resp), nil
}

func (h *skillHandler) updateConfig(ctx context.Context, req UpdateSkillConfigurationRequest) (SkillConfiguration, error) {
	if err := h.refuseBuiltin(ctx, req.ID, msgBuiltinSkillFixed); err != nil {
		return SkillConfiguration{}, err
	}
	switch {
	case req.Values.Null:
		return SkillConfiguration{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
			Details: map[string][]string{"values": {"cannot be null"}}}
	case !req.Values.Set:
		return h.getConfig(ctx, skillIDRequest{ID: req.ID})
	}
	resp, err := h.svc.PatchConfig(ctx, req.ID, entplatform.SkillConfigPatchRequest{Values: req.Values.Value}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return SkillConfiguration{}, notReadyIs409(err, msgSkillNotReady)
	}
	return toSkillConfiguration(resp), nil
}

func (h *skillHandler) secretsDiff(ctx context.Context, req SkillSecretsDiffRequest) (SecretsDiffResult, error) {
	if !IsCanonicalUUID(req.ID) {
		if _, err := h.svc.GetBuiltin(ctx, req.ID); err != nil {
			return SecretsDiffResult{}, err
		}
		return SecretsDiffResult{Secrets: map[string]SecretMatch{}}, nil
	}
	diff, err := h.svc.GetSecretsDiff(ctx, req.ID, req.Values)
	if err != nil {
		return SecretsDiffResult{}, notReadyIs409(err, msgSkillNotReady)
	}
	entplatform.LogSecretsDiff(ctx, h.logger, "skill", req.ID, diff)
	return toSecretsDiffResult(diff), nil
}

func toSkill(resp *entplatform.SkillDetailResponse) Skill {
	s := Skill{
		ID:              resp.ID,
		Type:            "skill",
		Origin:          originCustom,
		Name:            resp.Name,
		Label:           NullIfZero(resp.Label),
		Description:     NullIfZero(resp.Description),
		Tags:            nonNilList(resp.Tags),
		DiscoveryStatus: resp.DiscoveryStatus,
		HasReferences:   resp.HasReferences,
		HasAssets:       resp.HasAssets,
		HasTools:        resp.HasTools,
		ResourceCount:   resp.ResourceCount,
		ReferenceCount:  resp.ReferenceCount,
		AssetCount:      resp.AssetCount,
		BundledTools:    toSkillBundledTools(resp.BundledTools),
		Toolsets:        nonNilList(resp.Toolsets),
		Bundle:          toSkillBundle(resp),
		Agents:          make([]SkillAgent, len(resp.Agents)),
		ConfigSummary:   toConfigSummary(resp.ConfigSummary),
		CreatedBy:       NullIfZero(resp.CreatedBy),
		UpdatedBy:       NullIfZero(resp.UpdatedBy),
		CreatedTime:     Instant(resp.CreatedTime),
		UpdatedTime:     Instant(resp.UpdatedTime),
	}
	if resp.Origin == originBuiltin {
		s.Origin = originBuiltin
	}
	for i, a := range resp.Agents {
		s.Agents[i] = SkillAgent{ID: a.ID, Name: a.Name, Description: NullIfZero(a.Description)}
	}
	return s
}

func toSkillBundle(resp *entplatform.SkillDetailResponse) *SkillBundle {
	if resp.Origin == originBuiltin || resp.DiscoveryStatus == skillStatusCreated {
		return nil
	}
	return &SkillBundle{ContentHash: NullIfZero(resp.ContentHash)}
}

func toSkillBundledTools(entries []any) []SkillBundledTool {
	tools := make([]SkillBundledTool, 0, len(entries))
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		var tool remotetool.SkillBundledTool
		if err := json.Unmarshal(raw, &tool); err != nil {
			continue
		}
		tools = append(tools, toSkillBundledTool(tool))
	}
	return tools
}

func toSkillBundledTool(tool remotetool.SkillBundledTool) SkillBundledTool {
	t := SkillBundledTool{
		ToolName:       tool.ToolName,
		Description:    NullIfZero(tool.Description),
		Instructions:   NullIfZero(tool.Instructions),
		ArtifactParams: make(map[string]SkillArtifactParam, len(tool.ArtifactParams)),
		Parameters:     tool.Parameters,
		ConfigSchema:   tool.ConfigSchema,
	}
	for name, p := range tool.ArtifactParams {
		t.ArtifactParams[name] = SkillArtifactParam{IsList: p.IsList, IsOptional: p.IsOptional, FilenameKey: NullIfZero(p.FilenameKey)}
	}
	return t
}

func toSkillConfiguration(resp *entplatform.SkillConfigResponse) SkillConfiguration {
	values := resp.Values
	if values == nil {
		values = map[string]map[string]any{}
	}
	return SkillConfiguration{Values: values, UpdatedBy: NullIfZero(resp.UpdatedBy), UpdatedTime: Instant(resp.UpdatedAt)}
}
