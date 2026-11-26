// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	eventSkillCreated       = "skill.created"
	eventSkillUpdated       = "skill.updated"
	eventSkillDeleted       = "skill.deleted"
	eventSkillConfigUpdated = "skill.config.updated"
)

type SkillService struct {
	db               *store.DB
	skillRepo        *store.SkillRepo
	outboxRepo       *store.OutboxRepo
	outboxNotifier   platpkg.OutboxNotifier
	resourceStore    ResourceStore
	namespace        string
	skillsDir        string
	logger           *slog.Logger
	featureRegistry  *features.Registry
	maxZipCompressed int64
}

func NewSkillService(db *store.DB, skillRepo *store.SkillRepo, logger *slog.Logger) *SkillService {
	return &SkillService{
		db:               db,
		skillRepo:        skillRepo,
		logger:           logger,
		maxZipCompressed: platpkg.DefaultMaxUploadBytes,
	}
}

func (s *SkillService) WithOutbox(outboxRepo *store.OutboxRepo) {
	s.outboxRepo = outboxRepo
}

func (s *SkillService) SetFeatureRegistry(reg *features.Registry) {
	s.featureRegistry = reg
}

func (s *SkillService) SetMaxZipCompressed(n int64) {
	s.maxZipCompressed = n
}

func (s *SkillService) SetOutboxNotifier(n platpkg.OutboxNotifier) {
	s.outboxNotifier = n
}

func (s *SkillService) notifyOutbox() {
	if s.outboxNotifier != nil {
		s.outboxNotifier.Notify()
	}
}

func (s *SkillService) WithStorage(rs ResourceStore, namespace string) {
	s.resourceStore = rs
	s.namespace = namespace
}

func (s *SkillService) GetByID(ctx context.Context, id string) (*SkillDetailResponse, error) {
	skill, err := s.skillRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(getSkillErrFmt, err)
	}
	if skill == nil {
		return nil, &NotFoundError{Resource: "skill", ID: id}
	}
	resp := buildSkillResponse(skill)
	resp.ConfigSummary = buildSkillConfigSummaryFromLoaded(skill.Name, skill.BundledTools, skill.DiscoveryStatus, s.loadSkillConfigValues(ctx, skill.ID))
	if agentsBySkillID, err := s.skillRepo.GetAgentsBySkillIDs(ctx, []string{skill.ID}); err == nil {
		resp.Agents = toSkillAgentRefs(agentsBySkillID[skill.ID])
	} else {
		s.logger.Warn("skill: lookup agents for skill failed", "skill", skill.Name, "error", err)
	}
	return resp, nil
}

func (s *SkillService) ListPaginated(ctx context.Context, pageNumber, pageSize int, typeFilter string) (*PaginatedResponse[SkillDetailResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	if typeFilter == "builtin" {
		page := paginateBuiltinSkills(visibleBuiltinSkills(s.featureRegistry), pageNumber, pageSize)
		s.populateBuiltinSkillAgents(ctx, page.Data)
		return page, nil
	}

	skills, total, err := s.skillRepo.ListPaginated(ctx, pageNumber, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}

	responses := s.buildSkillResponses(ctx, skills)
	return &PaginatedResponse[SkillDetailResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *SkillService) buildSkillResponses(ctx context.Context, skills []store.Skill) []SkillDetailResponse {
	skillIDs := make([]string, len(skills))
	for i := range skills {
		skillIDs[i] = skills[i].ID
	}
	cfgBySkillID, _ := s.skillRepo.GetSkillConfigsForSkills(ctx, skillIDs)

	agentsBySkillID, err := s.skillRepo.GetAgentsBySkillIDs(ctx, skillIDs)
	if err != nil {
		s.logger.Warn("skill: list agents for skills failed", "error", err)
		agentsBySkillID = nil
	}

	responses := make([]SkillDetailResponse, len(skills))
	for i, sk := range skills {
		responses[i] = *buildSkillResponse(&sk)
		var stored map[string]map[string]any
		if cfg, ok := cfgBySkillID[sk.ID]; ok && cfg.ConfigValues != nil && *cfg.ConfigValues != "" {
			_ = json.Unmarshal([]byte(*cfg.ConfigValues), &stored)
		}
		responses[i].ConfigSummary = buildSkillConfigSummaryFromLoaded(sk.Name, sk.BundledTools, sk.DiscoveryStatus, stored)
		responses[i].Agents = toSkillAgentRefs(agentsBySkillID[sk.ID])
	}
	return responses
}

func (s *SkillService) Create(ctx context.Context, req CreateSkillRequest, userID string) (*SkillDetailResponse, error) {
	name := strings.TrimSpace(req.Name)
	if err := validatePackageName(name); err != nil {
		return nil, err
	}
	if strings.HasPrefix(name, builtinSkillNamePrefix) {
		return nil, &ValidationError{Field: fieldPackageName, Message: fmt.Sprintf("name must not start with %q — that prefix is reserved for cluster built-in skills", builtinSkillNamePrefix)}
	}

	existing, err := s.skillRepo.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("get skill by name: %w", err)
	}
	if existing != nil {
		return nil, &ConflictError{Message: fmt.Sprintf("skill with name %q already exists", name)}
	}

	skill := &store.Skill{
		Name:            name,
		Description:     strPtr(req.Description),
		Origin:          "platform",
		DiscoveryStatus: "created",
		CreatedBy:       userID,
		UpdatedBy:       userID,
	}

	if txErr := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if createErr := s.skillRepo.CreateInTx(ctx, tx, skill); createErr != nil {
			return createErr
		}
		return s.writeSkillOutboxEventInTx(ctx, tx, eventSkillCreated, skill.ID, skill.Name, userID, nil)
	}); txErr != nil {
		if isDuplicateNameErr(txErr) {
			return nil, &ConflictError{Message: fmt.Sprintf("skill with name %q already exists", name)}
		}
		return nil, fmt.Errorf("create skill: %w", txErr)
	}
	s.notifyOutbox()

	saved, err := s.skillRepo.GetByID(ctx, skill.ID)
	if err != nil {
		return nil, fmt.Errorf("reload skill: %w", err)
	}
	return buildSkillResponse(saved), nil
}

func (s *SkillService) CreateWithBundle(ctx context.Context, description string, data []byte, userID string) (*SkillDetailResponse, error) {
	extractedName, err := validateSkillZIP(data, "")
	if err != nil {
		return nil, zipRejection(err)
	}

	created, err := s.Create(ctx, CreateSkillRequest{Name: extractedName, Description: description}, userID)
	if valErr, ok := errors.AsType[*ValidationError](err); ok && valErr.Field == fieldPackageName {
		valErr.Field = fieldPackageFile
	}
	if err != nil {
		return nil, err
	}

	if uploadErr := s.UploadZIP(ctx, created.ID, created.Name, data, userID); uploadErr != nil {
		if delErr := s.Delete(ctx, created.ID); delErr != nil {
			s.logger.Warn("rollback after skill upload failure failed", "skillID", created.ID, "error", delErr)
		}
		return nil, uploadErr
	}

	return s.GetByID(ctx, created.ID)
}

func (s *SkillService) Update(ctx context.Context, id string, req UpdateSkillRequest, userID string) (*SkillDetailResponse, error) {
	skill, err := s.skillRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(getSkillErrFmt, err)
	}
	if skill == nil {
		return nil, &NotFoundError{Resource: "skill", ID: id}
	}

	if req.Description != "" {
		skill.Description = strPtr(req.Description)
	}
	skill.UpdatedBy = userID

	if txErr := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if updateErr := s.skillRepo.UpdateInTx(ctx, tx, skill); updateErr != nil {
			return updateErr
		}
		return s.writeSkillOutboxEventInTx(ctx, tx, eventSkillUpdated, skill.ID, skill.Name, userID, nil)
	}); txErr != nil {
		return nil, fmt.Errorf("update skill: %w", txErr)
	}
	s.notifyOutbox()

	saved, err := s.skillRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reload skill: %w", err)
	}
	return buildSkillResponse(saved), nil
}

func (s *SkillService) Delete(ctx context.Context, id string) error {
	skill, err := s.skillRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get skill for delete: %w", err)
	}
	if skill == nil {
		return &NotFoundError{Resource: "skill", ID: id}
	}

	agentIDs := s.affectedAgentIDsForSkill(ctx, skill.ID, skill.Name)

	if txErr := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if delErr := s.skillRepo.DeleteInTx(ctx, tx, id); delErr != nil {
			return delErr
		}
		return s.writeSkillOutboxEventInTx(ctx, tx, eventSkillDeleted, skill.ID, skill.Name, "", agentIDs)
	}); txErr != nil {
		if errors.Is(txErr, store.ErrSkillNotFound) {
			return &NotFoundError{Resource: "skill", ID: id}
		}
		return fmt.Errorf("delete skill: %w", txErr)
	}
	s.notifyOutbox()
	return nil
}

func (s *SkillService) SetDiscoveryStatus(ctx context.Context, name, status string) {
	if err := s.skillRepo.SetDiscoveryStatus(ctx, name, status); err != nil {
		s.logger.Warn("set skill discovery status", "skill", name, "discoveryStatus", status, "error", err)
	}
}

func (s *SkillService) UpsertFromInit(ctx context.Context, params remotetool.SkillInitParams) {
	existing, err := s.skillRepo.GetByName(ctx, params.SkillName)
	if err != nil {
		s.logger.Warn("load skill before reconcile", "skill", params.SkillName, "error", err)
		return
	}
	if existing == nil {
		return
	}

	var tagsJSON *string
	if len(params.Tags) > 0 {
		if b, mErr := json.Marshal(params.Tags); mErr == nil {
			t := string(b)
			tagsJSON = &t
		}
	}

	var bundledToolsJSON *string
	if len(params.BundledTools) > 0 {
		if b, mErr := json.Marshal(params.BundledTools); mErr == nil {
			t := string(b)
			bundledToolsJSON = &t
		}
	}

	var toolsetsJSON *string
	if len(params.Toolsets) > 0 {
		if b, mErr := json.Marshal(params.Toolsets); mErr == nil {
			t := string(b)
			toolsetsJSON = &t
		}
	}

	discoveryStatus := "ready"
	if params.DiscoveryFailed {
		discoveryStatus = "failed"
	}

	desc := params.Description
	skill := &store.Skill{
		Name:            params.SkillName,
		Description:     &desc,
		Tags:            tagsJSON,
		Origin:          existing.Origin,
		DiscoveryStatus: discoveryStatus,
		HasReferences:   params.HasReferences,
		HasAssets:       params.HasAssets,
		HasTools:        len(params.BundledTools) > 0,
		ResourceCount:   params.ResourceCount,
		ReferenceCount:  params.ReferenceCount,
		AssetCount:      params.AssetCount,
		BundledTools:    bundledToolsJSON,
		Toolsets:        toolsetsJSON,
		CreatedBy:       existing.CreatedBy,
		UpdatedBy:       "system",
	}

	if skillContentEqual(existing, skill) {
		return
	}

	agentIDs, err := s.persistReadySkillFromInit(ctx, existing, skill)
	if err != nil {
		s.logger.Warn("upsert skill from init", "skill", params.SkillName, "error", err)
		return
	}
	s.notifyOutbox()
	s.logger.Info("skill reconciled from init", "skill", params.SkillName, "discoveryStatus", discoveryStatus, "boundAgents", len(agentIDs))
}

func (s *SkillService) persistReadySkillFromInit(ctx context.Context, existing, skill *store.Skill) ([]string, error) {
	agentIDsBeforeTx := s.affectedAgentIDsForSkill(ctx, existing.ID, existing.Name)
	err := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if upsertErr := s.skillRepo.UpsertByNameInTx(ctx, tx, skill); upsertErr != nil {
			return upsertErr
		}
		return s.writeSkillOutboxEventInTx(ctx, tx, eventSkillUpdated, existing.ID, existing.Name, "system", agentIDsBeforeTx)
	})
	return agentIDsBeforeTx, err
}

func skillContentEqual(a, b *store.Skill) bool {
	return strPtrEqual(a.Description, b.Description) &&
		strPtrEqual(a.Tags, b.Tags) &&
		bundledToolsJSONEqual(a.BundledTools, b.BundledTools) &&
		strPtrEqual(a.Toolsets, b.Toolsets) &&
		a.DiscoveryStatus == b.DiscoveryStatus &&
		a.HasReferences == b.HasReferences &&
		a.HasAssets == b.HasAssets &&
		a.HasTools == b.HasTools &&
		a.ResourceCount == b.ResourceCount &&
		a.ReferenceCount == b.ReferenceCount &&
		a.AssetCount == b.AssetCount
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func bundledToolsJSONEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	if *a == *b {
		return true
	}
	indexByToolName := func(raw string) (map[string]string, bool) {
		var entries []remotetool.SkillBundledTool
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return nil, false
		}
		out := make(map[string]string, len(entries))
		for _, e := range entries {
			if e.ToolName == "" {
				return nil, false
			}
			canon, err := json.Marshal(e)
			if err != nil {
				return nil, false
			}
			out[e.ToolName] = string(canon)
		}
		if len(out) != len(entries) {
			return nil, false
		}
		return out, true
	}
	ai, aok := indexByToolName(*a)
	bi, bok := indexByToolName(*b)
	if !aok || !bok {
		return false
	}
	if len(ai) != len(bi) {
		return false
	}
	for name, entry := range ai {
		if bi[name] != entry {
			return false
		}
	}
	return true
}

func (s *SkillService) FailStalePending(ctx context.Context, cutoffMs int64) error {
	stale, err := s.skillRepo.ListPendingOlderThan(ctx, cutoffMs)
	if err != nil {
		return fmt.Errorf("list stale pending skills: %w", err)
	}

	for _, sk := range stale {
		failed, markErr := s.skillRepo.FailStalePending(ctx, sk.ID, cutoffMs)
		switch {
		case markErr != nil:
			s.logger.Error("failed to mark skill as failed", "skill", sk.Name, "error", markErr)
		case failed:
			s.logger.Info("marked stale pending skill as failed", "skill", sk.Name)
		}
	}
	return nil
}

func (s *SkillService) HandleToolFailed(ctx context.Context, prefixedToolName, errMsg string) {
	skillName, _, ok := splitToolName(prefixedToolName)
	if !ok {
		return
	}
	skill, err := s.skillRepo.GetByName(ctx, skillName)
	if err != nil {
		s.logger.Warn("skill: get by name for failed discovery", "skill", skillName, "error", err)
		return
	}
	if skill == nil {
		s.logger.Debug("skill: ignoring failed-discovery for unknown skill", "skill", skillName)
		return
	}
	s.logger.Warn("skill: bundled tool failed discovery; marking skill failed",
		"skill", skillName, "tool", prefixedToolName, "error", errMsg)
	if setErr := s.skillRepo.SetDiscoveryStatus(ctx, skillName, "failed"); setErr != nil {
		s.logger.Error("skill: set failed discovery", "skill", skillName, "error", setErr)
	}
}

func (s *SkillService) MarkUnavailable(ctx context.Context, skillName string) {
	if err := s.skillRepo.SetDiscoveryStatus(ctx, skillName, "failed"); err != nil {
		s.logger.Error("mark skill unavailable", "skill", skillName, "error", err)
	} else {
		s.logger.Info("skill marked unavailable", "skill", skillName)
	}
}

func toSkillAgentRefs(refs []store.SkillAgentRef) []SkillAgentRef {
	out := make([]SkillAgentRef, len(refs))
	for i, r := range refs {
		out[i] = SkillAgentRef{ID: r.ID, Name: r.Name, Description: r.Description}
	}
	return out
}

func (s *SkillService) populateBuiltinSkillAgents(ctx context.Context, skills []SkillDetailResponse) {
	if len(skills) == 0 {
		return
	}
	names := make([]string, len(skills))
	for i := range skills {
		names[i] = skills[i].Name
	}
	agentsByName, err := s.skillRepo.GetAgentsBySkillRefNames(ctx, names)
	if err != nil {
		s.logger.Warn("skill: lookup agents for built-in skills failed", "error", err)
		return
	}
	for i := range skills {
		skills[i].Agents = toSkillAgentRefs(agentsByName[skills[i].Name])
	}
}

func buildSkillResponse(sk *store.Skill) *SkillDetailResponse {
	resp := &SkillDetailResponse{
		ID:              sk.ID,
		Name:            sk.Name,
		Description:     derefStr(sk.Description),
		Origin:          sk.Origin,
		DiscoveryStatus: sk.DiscoveryStatus,
		HasReferences:   sk.HasReferences,
		HasAssets:       sk.HasAssets,
		HasTools:        sk.HasTools,
		ResourceCount:   sk.ResourceCount,
		ReferenceCount:  sk.ReferenceCount,
		AssetCount:      sk.AssetCount,
		ContentHash:     derefStr(sk.ContentHash),
		Agents:          []SkillAgentRef{},
		CreatedBy:       sk.CreatedBy,
		UpdatedBy:       sk.UpdatedBy,
		CreatedTime:     Timestamp(sk.CreatedTime),
		UpdatedTime:     Timestamp(sk.UpdatedTime),
	}

	if sk.Tags != nil {
		var tags []string
		if err := json.Unmarshal([]byte(*sk.Tags), &tags); err == nil {
			resp.Tags = tags
		}
	}
	if sk.BundledTools != nil {
		var tools []any
		if err := json.Unmarshal([]byte(*sk.BundledTools), &tools); err == nil {
			resp.BundledTools = tools
		}
	}
	if sk.Toolsets != nil {
		var ts []string
		if err := json.Unmarshal([]byte(*sk.Toolsets), &ts); err == nil {
			resp.Toolsets = ts
		}
	}

	return resp
}

func (s *SkillService) writeSkillOutboxEventInTx(ctx context.Context, tx *dbutil.Tx, eventType, skillID, skillName string, userID string, agentIDs []string) error {
	if s.outboxRepo == nil {
		return nil
	}
	payload, err := json.Marshal(skillOutboxPayload{AgentIDs: agentIDs, Name: skillName, UserID: userID})
	if err != nil {
		s.logger.Error("skill: marshal outbox payload", "eventType", eventType, "skill", skillName, "error", err)
		return nil
	}
	if err := s.outboxRepo.CreateInTx(ctx, tx, &store.OutboxEvent{
		EventType:   eventType,
		AggregateID: skillID,
		Payload:     string(payload),
	}); err != nil {
		return fmt.Errorf("write outbox event %s: %w", eventType, err)
	}
	return nil
}

func (s *SkillService) affectedAgentIDsForSkill(ctx context.Context, skillID, skillName string) []string {
	ids, err := s.skillRepo.GetAgentIDsBySkillID(ctx, skillID)
	if err != nil {
		s.logger.Warn("skill: lookup affected agents failed", "skill", skillName, "error", err)
		return nil
	}
	return ids
}

const SkillBundleResource = "skill bundle"

const SkillNoBundleMessage = "The skill has no bundle yet"

func (s *SkillService) Download(ctx context.Context, id string) (io.ReadCloser, string, error) {
	skill, data, err := s.readSkillZIP(ctx, id)
	if errors.Is(err, errSkillBundleMissing) {
		return nil, "", &NotFoundError{Resource: SkillBundleResource, ID: id}
	}
	if err != nil {
		return nil, "", err
	}
	redacted, err := rewriteSkillManifests(data, func(_ string, content []byte, topLevel bool) ([]byte, error) {
		return redactSkillMD(content, topLevel)
	})
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(redacted)), skill.Name + ".zip", nil
}

func (s *SkillService) UploadZIP(ctx context.Context, skillID, skillName string, data []byte, userID string) error {
	if structErr := precheckSkillZIPStructure(data); structErr != nil {
		return zipRejection(structErr)
	}
	data, restoreErr := s.restoreSkillSecrets(ctx, skillName, data)
	if restoreErr != nil {
		return restoreErr
	}
	if _, err := validateSkillZIP(data, skillName); err != nil {
		return zipRejection(err)
	}

	if s.resourceStore == nil {
		return fmt.Errorf("skill ZIP storage is not configured")
	}
	key := skillRuntimeKey(s.namespace, skillName)
	if putErr := s.resourceStore.Put(ctx, key, bytes.NewReader(data)); putErr != nil {
		return fmt.Errorf("store skill ZIP: %w", putErr)
	}

	var noRedeployAgentIDs []string
	sum := sha256.Sum256(data)
	contentHash := hex.EncodeToString(sum[:])
	if err := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if statusErr := s.skillRepo.SetDiscoveryStatusInTx(ctx, tx, skillName, "pending"); statusErr != nil {
			return statusErr
		}
		if hashErr := s.skillRepo.SetContentHashInTx(ctx, tx, skillName, contentHash); hashErr != nil {
			return hashErr
		}
		return s.writeSkillOutboxEventInTx(ctx, tx, eventSkillUpdated, skillID, skillName, userID, noRedeployAgentIDs)
	}); err != nil {
		return fmt.Errorf("commit skill upload: %w", err)
	}
	s.notifyOutbox()
	return nil
}

func precheckSkillZIPStructure(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil
	}
	_, err = checkSkillZIPStructure(zr)
	return err
}

type skillOutboxPayload struct {
	AgentIDs []string `json:"agentIds,omitempty"`
	Name     string   `json:"name,omitempty"`
	UserID   string   `json:"userId"`
}

const (
	getSkillErrFmt = "get skill: %w"
)
