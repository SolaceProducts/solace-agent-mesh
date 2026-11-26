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
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const (
	maxZipUncompressed = 500 << 20
	maxZipEntries      = 10_000
	maxPackageNameLen  = 128

	resourceToolPackage = "tool package"
	errGetToolPackage   = "get tool package: %w"
	errGetToolsetConfig = "get toolset config: %w"
	errToolsetNotReady  = "toolset is not ready for configuration (status=%s)"
	errLoadPackageFiles = "load package files: %w"
)

const ToolsetNotDiscoveredMessage = "The toolset's tools are not discovered yet"

var packageNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

type ToolsetService struct {
	db               *store.DB
	repo             *store.ToolPackageRepo
	storage          *toolsetStorage
	outboxRepo       *store.OutboxRepo
	outboxNotifier   platpkg.OutboxNotifier
	namespace        string
	logger           *slog.Logger
	featureRegistry  *features.Registry
	maxZipCompressed int64
}

func NewToolsetService(db *store.DB, repo *store.ToolPackageRepo, storage *toolsetStorage, outboxRepo *store.OutboxRepo, namespace string, logger *slog.Logger) *ToolsetService {
	return &ToolsetService{
		db:               db,
		repo:             repo,
		storage:          storage,
		outboxRepo:       outboxRepo,
		namespace:        namespace,
		logger:           logger,
		maxZipCompressed: platpkg.DefaultMaxUploadBytes,
	}
}

func (s *ToolsetService) SetOutboxNotifier(n platpkg.OutboxNotifier) {
	s.outboxNotifier = n
}

func (s *ToolsetService) SetFeatureRegistry(reg *features.Registry) {
	s.featureRegistry = reg
}

const (
	fieldPackageName = "name"
	fieldPackageFile = "file"
)

func validatePackageName(name string) error {
	switch {
	case name == "":
		return &ValidationError{Field: fieldPackageName, Message: "name is required"}
	case len(name) > maxPackageNameLen:
		return &ValidationError{Field: fieldPackageName, Message: fmt.Sprintf("name must be at most %d characters", maxPackageNameLen)}
	case !packageNameRe.MatchString(name):
		return &ValidationError{Field: fieldPackageName, Message: "name must start with a letter and contain only letters, digits, hyphens, or underscores"}
	}
	return nil
}

func (s *ToolsetService) SetMaxZipCompressed(n int64) {
	s.maxZipCompressed = n
}

func (s *ToolsetService) Create(ctx context.Context, req CreateToolsetRequest, userID string) (*ToolsetResponse, error) {
	name := strings.TrimSpace(req.Name)
	if err := validatePackageName(name); err != nil {
		return nil, err
	}
	if _, ok := schema.ValidBuiltinGroups[name]; ok {
		return nil, &ValidationError{Field: fieldPackageName, Message: fmt.Sprintf("name %q is reserved for a built-in toolset", name)}
	}

	existing, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("get tool package by name: %w", err)
	}
	if existing != nil {
		return nil, &ConflictError{Message: fmt.Sprintf("tool package with name %q already exists", name)}
	}

	pkg := &store.ToolPackage{
		Name:            name,
		Description:     strPtr(req.Description),
		DiscoveryStatus: "created",
		CreatedBy:       userID,
		UpdatedBy:       userID,
	}
	if err := s.repo.Create(ctx, pkg); err != nil {
		if isDuplicateNameErr(err) {
			return nil, &ConflictError{Message: fmt.Sprintf("tool package with name %q already exists", name)}
		}
		return nil, fmt.Errorf("create tool package: %w", err)
	}

	return buildToolsetResponse(pkg, nil, nil), nil
}

func isDuplicateNameErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") ||
		strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "duplicate key")
}

func (s *ToolsetService) GetByID(ctx context.Context, id string) (*ToolsetResponse, error) {
	pkg, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetToolPackage, err)
	}
	if pkg == nil {
		return nil, &NotFoundError{Resource: resourceToolPackage, ID: id}
	}

	files, _ := s.repo.GetFilesForPackage(ctx, id)
	agents, _ := s.repo.GetAgentsUsingPackage(ctx, pkg.Name)
	resp := buildToolsetResponse(pkg, files, agents)
	resp.ConfigSummary, _ = s.BuildConfigSummary(ctx, pkg.ID, pkg.DiscoveryStatus)
	return resp, nil
}

func (s *ToolsetService) Update(ctx context.Context, id string, req UpdateToolsetRequest, userID string) (*ToolsetResponse, error) {
	name := strings.TrimSpace(req.Name)
	if err := validatePackageName(name); err != nil {
		return nil, err
	}

	pkg, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetToolPackage, err)
	}
	if pkg == nil {
		return nil, &NotFoundError{Resource: resourceToolPackage, ID: id}
	}

	if name != pkg.Name {
		if _, ok := schema.ValidBuiltinGroups[name]; ok {
			return nil, &ValidationError{Field: fieldPackageName, Message: fmt.Sprintf("name %q is reserved for a built-in toolset", name)}
		}
		if pkg.ZipResourceKey != nil && *pkg.ZipResourceKey != "" {
			return nil, &ValidationError{Field: fieldPackageName, Message: "rename unsupported after upload; use re-upload with same name to update, or delete and recreate"}
		}
		existing, err := s.repo.GetByName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("get tool package by name: %w", err)
		}
		if existing != nil {
			return nil, &ConflictError{Message: fmt.Sprintf("tool package with name %q already exists", name)}
		}
	}

	if err := s.repo.UpdatePackage(ctx, id, name, strPtr(req.Description), userID); err != nil {
		if isDuplicateNameErr(err) {
			return nil, &ConflictError{Message: fmt.Sprintf("tool package with name %q already exists", name)}
		}
		return nil, fmt.Errorf("update tool package: %w", err)
	}

	return s.GetByID(ctx, id)
}

func buildConfigSummaryFromLoaded(files []store.ToolPackageFile, tsCfg store.ToolsetConfig, discoveryStatus string) *ConfigSummary {
	if discoveryStatus != "ready" {
		return nil
	}
	schemaByTool := loadConfigSchemaByTool(files)

	var required []string
	for toolName, schema := range schemaByTool {
		for key, entry := range schema {
			if entry.isRequired() {
				required = append(required, toolName+"/"+key)
			}
		}
	}

	var covered []string
	if tsCfg.ConfigValues != nil && *tsCfg.ConfigValues != "" {
		var stored map[string]any
		if json.Unmarshal([]byte(*tsCfg.ConfigValues), &stored) == nil {
			for _, qualifiedKey := range required {
				toolName, key, _ := strings.Cut(qualifiedKey, "/")
				if toolValsRaw, ok := stored[toolName]; ok {
					if toolVals, ok := toolValsRaw.(map[string]any); ok {
						if v, ok := toolVals[key]; ok && v != nil {
							covered = append(covered, qualifiedKey)
						}
					}
				}
			}
		}
	}
	slices.Sort(required)
	slices.Sort(covered)
	return &ConfigSummary{
		RequiredFields:            required,
		RequiredCoveredByDefaults: covered,
		RequiresAgentConfig:       len(covered) < len(required),
	}
}

func (s *ToolsetService) ListPaginated(ctx context.Context, pageNumber, pageSize int, typeFilter string, discoveryStatus *string) (*PaginatedResponse[ToolsetResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	includeBuiltins := typeFilter != "custom" && discoveryStatus == nil
	includeCustom := typeFilter != "builtin"

	var builtins []ToolsetResponse
	if includeBuiltins {
		builtins = visibleBuiltinToolsets(s.featureRegistry)
	}
	builtinCount := len(builtins)

	windowStart := (pageNumber - 1) * pageSize
	windowEnd := windowStart + pageSize

	var pagedBuiltins []ToolsetResponse
	if includeBuiltins {
		bStart := min(windowStart, builtinCount)
		bEnd := min(windowEnd, builtinCount)
		pagedBuiltins = builtins[bStart:bEnd]

		if len(pagedBuiltins) > 0 {
			s.attachBuiltinToolsetAgents(ctx, pagedBuiltins)
		}
	}

	var customTotal int
	var responses []ToolsetResponse

	if includeCustom {
		customOffset := max(0, windowStart-builtinCount)
		customLimit := max(0, windowEnd-max(windowStart, builtinCount))

		packages, total, err := s.repo.ListWithOffset(ctx, customOffset, customLimit, discoveryStatus)
		if err != nil {
			return nil, fmt.Errorf("list tool packages: %w", err)
		}
		customTotal = total

		packageIDs := make([]string, len(packages))
		packageNames := make([]string, len(packages))
		for i := range packages {
			packageIDs[i] = packages[i].ID
			packageNames[i] = packages[i].Name
		}
		filesMap, fErr := s.repo.GetFilesForPackages(ctx, packageIDs)
		if fErr != nil {
			s.logger.Warn("toolset: list files for packages failed", "error", fErr)
			filesMap = nil
		}
		agentsMap, aErr := s.repo.GetAgentsByPackageNames(ctx, packageNames)
		if aErr != nil {
			s.logger.Warn("toolset: list agents for packages failed", "error", aErr)
			agentsMap = nil
		}

		tsCfgMap, tsCfgErr := s.repo.GetToolsetConfigsForPackages(ctx, packageIDs)
		if tsCfgErr != nil {
			s.logger.Warn("toolset: list toolset configs for packages failed", "error", tsCfgErr)
			tsCfgMap = nil
		}

		for i := range packages {
			files := filesMap[packages[i].ID]
			agents := agentsMap[packages[i].Name]
			r := buildToolsetResponse(&packages[i], files, agents)
			r.ConfigSummary = buildConfigSummaryFromLoaded(files, tsCfgMap[packages[i].ID], packages[i].DiscoveryStatus)
			responses = append(responses, *r)
		}
	}

	data := make([]ToolsetResponse, 0, len(pagedBuiltins)+len(responses))
	data = append(data, pagedBuiltins...)
	data = append(data, responses...)

	totalCount := builtinCount + customTotal

	return &PaginatedResponse[ToolsetResponse]{
		Data: data,
		Meta: NewPaginationMeta(pageNumber, pageSize, totalCount),
	}, nil
}

var alwaysOnBuiltinToolsets = map[string]string{
	"builtin_artifact_tools": "autoInjectArtifactTools",
	"data_analysis":          "autoInjectDataAnalysisTools",
}

func (s *ToolsetService) attachBuiltinToolsetAgents(ctx context.Context, pagedBuiltins []ToolsetResponse) {
	builtinNames := make([]string, len(pagedBuiltins))
	anyAlwaysOn := false
	for i, b := range pagedBuiltins {
		builtinNames[i] = b.ID
		if _, ok := alwaysOnBuiltinToolsets[b.ID]; ok {
			anyAlwaysOn = true
		}
	}

	explicitByToolset, baErr := s.repo.GetAgentsByPackageNames(ctx, builtinNames)
	if baErr != nil {
		s.logger.Warn("toolset: list agents for builtin toolsets failed", "error", baErr)
		explicitByToolset = nil
	}

	var autoInjectRefs []store.AgentAutoInjectRef
	if anyAlwaysOn {
		var aiErr error
		autoInjectRefs, aiErr = s.repo.ListAgentAutoInjectRefs(ctx)
		if aiErr != nil {
			s.logger.Warn("toolset: list agents for auto-injected builtin usage failed", "error", aiErr)
			autoInjectRefs = nil
		}
	}

	for i := range pagedBuiltins {
		explicit := explicitByToolset[pagedBuiltins[i].ID]
		if optOutKey, ok := alwaysOnBuiltinToolsets[pagedBuiltins[i].ID]; ok && autoInjectRefs != nil {
			pagedBuiltins[i].Agents = agentsReceivingAutoInjected(autoInjectRefs, optOutKey, explicit)
			continue
		}
		pagedBuiltins[i].Agents = toToolsetAgentRefs(explicit)
	}
}

func agentsReceivingAutoInjected(refs []store.AgentAutoInjectRef, optOutKey string, explicit []store.AgentToolsetRef) []ToolsetAgentRef {
	explicitIDs := make(map[string]struct{}, len(explicit))
	for _, a := range explicit {
		explicitIDs[a.ID] = struct{}{}
	}
	out := make([]ToolsetAgentRef, 0, len(refs))
	for _, a := range refs {
		if _, declared := explicitIDs[a.ID]; declared || !autoInjectOptedOut(a.AdditionalConfigurations, optOutKey) {
			out = append(out, ToolsetAgentRef{ID: a.ID, Name: a.Name, Description: a.Description})
		}
	}
	return out
}

func autoInjectOptedOut(additionalConfigurations []byte, optOutKey string) bool {
	var cfg map[string]any
	if len(additionalConfigurations) == 0 {
		return true
	}
	if err := json.Unmarshal(additionalConfigurations, &cfg); err != nil {
		return false
	}
	v, ok := cfg[optOutKey].(bool)
	return !ok || !v
}

func toToolsetAgentRefs(agents []store.AgentToolsetRef) []ToolsetAgentRef {
	if len(agents) == 0 {
		return nil
	}
	refs := make([]ToolsetAgentRef, len(agents))
	for i, a := range agents {
		refs[i] = ToolsetAgentRef{ID: a.ID, Name: a.Name, Description: a.Description}
	}
	return refs
}

func (s *ToolsetService) Delete(ctx context.Context, id string) error {
	pkg, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf(errGetToolPackage, err)
	}
	if pkg == nil {
		return &NotFoundError{Resource: resourceToolPackage, ID: id}
	}

	agentIDs := s.agentIDsUsingPackage(ctx, pkg.Name)
	workflowIDs := s.workflowIDsUsingPackage(ctx, pkg.Name)

	if s.storage != nil {
		key := packageKey(s.namespace, pkg.Name)
		if delErr := s.storage.delete(ctx, key); delErr != nil {
			s.logger.Warn("failed to delete tool package", "key", key, "error", delErr)
		}
	}

	if err := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := s.repo.RemoveFromAllAgentsInTx(ctx, tx, pkg.Name); err != nil {
			return fmt.Errorf("detach toolset from agents: %w", err)
		}
		if err := s.repo.DeleteInTx(ctx, tx, id); err != nil {
			return fmt.Errorf("delete tool package: %w", err)
		}
		return s.writeToolOutboxEventInTx(ctx, tx, toolOutboxEvent{eventType: "tool.deleted", pkgID: pkg.ID, pkgName: pkg.Name, agentIDs: agentIDs, workflowIDs: workflowIDs})
	}); err != nil {
		return err
	}
	s.notifyOutbox()
	return nil
}

func (s *ToolsetService) Upload(ctx context.Context, id string, filename string, data []byte, userID string) error {
	pkg, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf(errGetToolPackage, err)
	}
	if pkg == nil {
		return &NotFoundError{Resource: resourceToolPackage, ID: id}
	}

	if int64(len(data)) > s.maxZipCompressed {
		return &ValidationError{Field: fieldPackageFile, Message: fmt.Sprintf("ZIP file exceeds %d MB compressed limit", s.maxZipCompressed>>20)}
	}

	runtime, err := validateAndDetectRuntime(data)
	if err != nil {
		return zipRejection(err)
	}

	key := packageKey(s.namespace, pkg.Name)
	if err := s.storage.put(ctx, key, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("store tool package: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			if delErr := s.storage.delete(context.Background(), key); delErr != nil {
				s.logger.Warn("toolset: orphan package cleanup failed", "key", key, "error", delErr)
			}
		}
	}()

	agentIDs := s.agentIDsUsingPackage(ctx, pkg.Name)
	workflowIDs := s.workflowIDsUsingPackage(ctx, pkg.Name)

	sum := sha256.Sum256(data)
	contentHash := hex.EncodeToString(sum[:])

	if err := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := s.repo.UpdateZipInfoInTx(ctx, tx, id, runtime, key, filename, int64(len(data)), userID); err != nil {
			return fmt.Errorf("update zip info: %w", err)
		}
		if err := s.repo.SetContentHashInTx(ctx, tx, id, contentHash); err != nil {
			return fmt.Errorf("set content hash: %w", err)
		}
		return s.writeToolOutboxEventInTx(ctx, tx, toolOutboxEvent{eventType: "tool.uploaded", pkgID: pkg.ID, pkgName: pkg.Name, agentIDs: agentIDs, workflowIDs: workflowIDs, userID: userID})
	}); err != nil {
		return err
	}
	committed = true
	s.notifyOutbox()

	return nil
}

func (s *ToolsetService) agentIDsUsingPackage(ctx context.Context, pkgName string) []string {
	refs, err := s.repo.GetAgentsUsingPackage(ctx, pkgName)
	if err != nil {
		s.logger.Warn("toolset: lookup agents using package failed", "package", pkgName, "error", err)
		return nil
	}
	ids := make([]string, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.ID)
	}
	return ids
}

func (s *ToolsetService) workflowIDsUsingPackage(ctx context.Context, pkgName string) []string {
	ids, err := s.repo.GetWorkflowIDsUsingPackage(ctx, pkgName)
	if err != nil {
		s.logger.Warn("toolset: lookup workflows using package failed", "package", pkgName, "error", err)
		return nil
	}
	return ids
}

type toolOutboxEvent struct {
	eventType   string
	pkgID       string
	pkgName     string
	agentIDs    []string
	workflowIDs []string
	userID      string
}

func (s *ToolsetService) writeToolOutboxEventInTx(ctx context.Context, tx *dbutil.Tx, ev toolOutboxEvent) error {
	if s.outboxRepo == nil {
		return nil
	}
	payload, err := json.Marshal(redeployPayload{AgentIDs: ev.agentIDs, WorkflowIDs: ev.workflowIDs, Name: ev.pkgName, UserID: ev.userID})
	if err != nil {
		s.logger.Error("toolset: marshal outbox payload", "eventType", ev.eventType, "package", ev.pkgName, "error", err)
		return nil
	}
	if err := s.outboxRepo.CreateInTx(ctx, tx, &store.OutboxEvent{
		EventType:   ev.eventType,
		AggregateID: ev.pkgID,
		Payload:     string(payload),
	}); err != nil {
		return fmt.Errorf("write outbox event %s: %w", ev.eventType, err)
	}
	return nil
}

func (s *ToolsetService) notifyOutbox() {
	if s.outboxNotifier != nil {
		s.outboxNotifier.Notify()
	}
}

type configSchemaField struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Default     any    `json:"default,omitempty"`
}

func (f configSchemaField) isRequired() bool {
	if f.Required {
		return true
	}
	return f.Secret && f.Default == nil
}

func loadConfigSchemaByTool(files []store.ToolPackageFile) map[string]map[string]configSchemaField {
	out := make(map[string]map[string]configSchemaField)
	for _, f := range files {
		if f.ConfigSchema == nil || *f.ConfigSchema == "" {
			continue
		}
		var entries []configSchemaField
		if err := json.Unmarshal([]byte(*f.ConfigSchema), &entries); err != nil {
			continue
		}
		toolSchema := make(map[string]configSchemaField, len(entries))
		for _, entry := range entries {
			if entry.Key == "" {
				continue
			}
			toolSchema[entry.Key] = entry
		}
		if len(toolSchema) > 0 {
			out[f.ToolName] = toolSchema
		}
	}
	return out
}

func (s *ToolsetService) loadSecretFieldsByTool(ctx context.Context, packageID string) (map[string]map[string]secretFieldInfo, error) {
	files, err := s.repo.GetFilesForPackage(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf(errLoadPackageFiles, err)
	}
	schemaByTool := loadConfigSchemaByTool(files)
	out := make(map[string]map[string]secretFieldInfo, len(schemaByTool))
	for toolName, schema := range schemaByTool {
		secrets := make(map[string]secretFieldInfo)
		for key, entry := range schema {
			if entry.Secret {
				secrets[key] = secretFieldInfo{Required: entry.isRequired()}
			}
		}
		if len(secrets) > 0 {
			out[toolName] = secrets
		}
	}
	return out, nil
}

func (s *ToolsetService) requireReadyPackage(ctx context.Context, packageID string) (*store.ToolPackage, error) {
	pkg, err := s.repo.GetByID(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf(errGetToolPackage, err)
	}
	if pkg == nil {
		return nil, &NotFoundError{Resource: resourceToolPackage, ID: packageID}
	}
	if pkg.DiscoveryStatus != "ready" {
		return nil, errNotReady(fmt.Sprintf(errToolsetNotReady, pkg.DiscoveryStatus))
	}
	return pkg, nil
}

func (s *ToolsetService) requireConfigAccess(ctx context.Context, packageID string) (*store.ToolPackage, error) {
	pkg, err := s.repo.GetByID(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf(errGetToolPackage, err)
	}
	if pkg == nil {
		return nil, &NotFoundError{Resource: resourceToolPackage, ID: packageID}
	}
	if pkg.DiscoveryStatus == "ready" {
		return pkg, nil
	}
	recoverable, err := s.blockedOnlyByOrphanedScopeGate(ctx, packageID)
	if err != nil {
		return nil, err
	}
	if !recoverable {
		return nil, errNotReady(fmt.Sprintf(errToolsetNotReady, pkg.DiscoveryStatus))
	}
	return pkg, nil
}

func (s *ToolsetService) blockedOnlyByOrphanedScopeGate(ctx context.Context, packageID string) (bool, error) {
	files, err := s.repo.GetFilesForPackage(ctx, packageID)
	if err != nil {
		return false, fmt.Errorf(errLoadPackageFiles, err)
	}
	if len(files) == 0 {
		return false, nil
	}
	orphans, err := s.orphanedRequiredScopeGates(ctx, packageID, toolNamesFromFiles(files))
	if err != nil {
		return false, err
	}
	return len(orphans) > 0, nil
}

func (s *ToolsetService) reflipReadyIfScopesResolved(ctx context.Context, packageID string, files []store.ToolPackageFile) {
	orphans, err := s.orphanedRequiredScopeGates(ctx, packageID, toolNamesFromFiles(files))
	if err != nil {
		s.logger.Warn("toolset: skip ready re-flip after config patch; failed to recheck required_scopes gates",
			"packageID", packageID, "error", err)
		return
	}
	if len(orphans) > 0 {
		return
	}
	if err := s.repo.UpdateDiscoveryStatus(ctx, packageID, "ready", nil); err != nil {
		s.logger.Error("toolset: flip ready after required_scopes recovery", "packageID", packageID, "error", err)
	}
}

func (s *ToolsetService) GetConfig(ctx context.Context, packageID string) (*ToolsetConfigResponse, error) {
	if _, err := s.requireConfigAccess(ctx, packageID); err != nil {
		return nil, err
	}
	return s.readConfigResponse(ctx, packageID)
}

func (s *ToolsetService) readConfigResponse(ctx context.Context, packageID string) (*ToolsetConfigResponse, error) {
	secretFieldsByTool, err := s.loadSecretFieldsByTool(ctx, packageID)
	if err != nil {
		return nil, err
	}

	cfg, err := s.repo.GetToolsetConfig(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf(errGetToolsetConfig, err)
	}
	resp := &ToolsetConfigResponse{Values: map[string]any{}}
	if cfg == nil {
		return resp, nil
	}
	resp.UpdatedBy = cfg.UpdatedBy
	resp.UpdatedAt = Timestamp(cfg.UpdatedTime)
	if cfg.ConfigValues == nil || *cfg.ConfigValues == "" {
		return resp, nil
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(*cfg.ConfigValues), &stored); err != nil {
		s.logger.Warn("toolset config: stored values not valid JSON", "packageID", packageID, "error", err)
		return resp, nil
	}
	for toolName, toolValsRaw := range stored {
		if toolName == reservedAuthConfigKey {
			if sanitized := sanitizeDeployerAuthValue(toolValsRaw); sanitized != nil {
				stored[toolName] = sanitized
			} else {
				delete(stored, toolName)
			}
			continue
		}
		toolVals, ok := toolValsRaw.(map[string]any)
		if !ok {
			continue
		}
		if secrets, hasTool := secretFieldsByTool[toolName]; hasTool {
			stored[toolName] = redactSecretFields(toolVals, secrets)
		}
	}
	resp.Values = stored
	return resp, nil
}

func (s *ToolsetService) GetSecretsDiff(ctx context.Context, packageID string, candidate map[string]any) (map[string]SecretFieldDiffResult, error) {
	if _, err := s.requireReadyPackage(ctx, packageID); err != nil {
		return nil, err
	}

	secretFieldsByTool, err := s.loadSecretFieldsByTool(ctx, packageID)
	if err != nil {
		return nil, err
	}
	stored, err := s.storedConfigCleartext(ctx, packageID)
	if err != nil {
		return nil, err
	}

	out := map[string]SecretFieldDiffResult{}
	for toolName, secrets := range secretFieldsByTool {
		fields := make([]secretField, 0, len(secrets))
		for name, info := range secrets {
			fields = append(fields, secretField{Name: name, Required: info.Required})
		}
		toolStored, _ := stored[toolName].(map[string]any)
		for name, res := range compareSecrets(fields, toolSecretsCandidate(candidate, toolName), toolStored) {
			if prev, seen := out[name]; seen {
				out[name] = SecretFieldDiffResult{Matches: prev.Matches && res.Matches}
			} else {
				out[name] = res
			}
		}
	}
	maps.Copy(out, authCredentialSecretsDiff(candidate, stored))
	return out, nil
}

func authCredentialSecretsDiff(candidate, stored map[string]any) map[string]SecretFieldDiffResult {
	fields := make([]secretField, 0, len(authCredentialSecretKeys))
	for name := range authCredentialSecretKeys {
		fields = append(fields, secretField{Name: name})
	}
	compared := compareSecrets(fields, authCredentialLeaves(candidate), authCredentialLeaves(stored))
	out := make(map[string]SecretFieldDiffResult, len(compared))
	for name, res := range compared {
		out[authCredentialResultKey(name)] = res
	}
	return out
}

func authCredentialResultKey(leaf string) string {
	return reservedAuthConfigKey + ".credential." + leaf
}

func authCredentialLeaves(config map[string]any) map[string]any {
	auth, _ := config[reservedAuthConfigKey].(map[string]any)
	cred, _ := auth["credential"].(map[string]any)
	return cred
}

func toolSecretsCandidate(candidate map[string]any, toolName string) map[string]any {
	perTool, ok := candidate[toolName].(map[string]any)
	if !ok {
		return candidate
	}
	merged := maps.Clone(candidate)
	maps.Copy(merged, perTool)
	return merged
}

func (s *ToolsetService) storedConfigCleartext(ctx context.Context, packageID string) (map[string]any, error) {
	cfg, err := s.repo.GetToolsetConfig(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf(errGetToolsetConfig, err)
	}
	if cfg == nil || cfg.ConfigValues == nil || *cfg.ConfigValues == "" {
		return map[string]any{}, nil
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(*cfg.ConfigValues), &stored); err != nil {
		s.logger.Warn("toolset config: stored values not valid JSON", "packageID", packageID, "error", err)
		return map[string]any{}, nil
	}
	return stored, nil
}

func (s *ToolsetService) PatchConfig(ctx context.Context, packageID string, req ToolsetConfigPatchRequest, userID string) (*ToolsetConfigResponse, error) {
	pkg, err := s.requireConfigAccess(ctx, packageID)
	if err != nil {
		return nil, err
	}

	files, err := s.repo.GetFilesForPackage(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf(errLoadPackageFiles, err)
	}
	schemaByTool := loadConfigSchemaByTool(files)

	if verr := validatePatchConfigValues(req.Values, files, schemaByTool); verr != nil {
		return nil, verr
	}

	existingNested, err := s.loadExistingNestedConfig(ctx, packageID)
	if err != nil {
		return nil, err
	}

	merged, err := s.composeMergedConfig(req.Values, schemaByTool, existingNested, packageID)
	if err != nil {
		return nil, err
	}

	var serialized *string
	if len(merged) > 0 {
		b, err := json.Marshal(merged)
		if err != nil {
			return nil, fmt.Errorf("marshal merged config values: %w", err)
		}
		raw := string(b)
		serialized = &raw
	}

	agentIDs := s.agentIDsUsingPackage(ctx, pkg.Name)
	workflowIDs := s.workflowIDsUsingPackage(ctx, pkg.Name)

	if err := s.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := s.repo.SetToolsetConfigInTx(ctx, tx, packageID, serialized, userID); err != nil {
			return fmt.Errorf("persist toolset config: %w", err)
		}
		return s.writeToolOutboxEventInTx(ctx, tx, toolOutboxEvent{eventType: "tool.config.updated", pkgID: pkg.ID, pkgName: pkg.Name, agentIDs: agentIDs, workflowIDs: workflowIDs, userID: userID})
	}); err != nil {
		return nil, err
	}
	s.notifyOutbox()

	if failedOnOrphanedScopeGate(pkg) {
		s.reflipReadyIfScopesResolved(ctx, packageID, files)
	}

	return s.readConfigResponse(ctx, packageID)
}

var reservedToolsetConfigBlocks = map[string]func(val any, toolNames map[string]struct{}) map[string][]string{
	reservedAuthConfigKey: func(val any, _ map[string]struct{}) map[string][]string {
		return validateDeployerAuthBlock(val)
	},
	reservedHILConfigKey:            validateToolsetHIL,
	reservedRequiredScopesConfigKey: validateToolsetRequiredScopes,
}

func IsReservedToolsetConfigKey(key string) bool {
	_, ok := reservedToolsetConfigBlocks[key]
	return ok
}

func validatePatchConfigValues(reqValues map[string]any, files []store.ToolPackageFile, schemaByTool map[string]map[string]configSchemaField) error {
	details := map[string][]string{}
	for toolName, toolValsRaw := range reqValues {
		if validateBlock, reserved := reservedToolsetConfigBlocks[toolName]; reserved {
			for k, msgs := range validateBlock(toolValsRaw, toolNamesFromFiles(files)) {
				details[k] = append(details[k], msgs...)
			}
			continue
		}
		toolSchema, knownTool := schemaByTool[toolName]
		if !knownTool {
			details[toolName] = append(details[toolName], "unknown tool name for this toolset")
			continue
		}
		toolVals, ok := toolValsRaw.(map[string]any)
		if !ok {
			details[toolName] = append(details[toolName], "config values must be an object")
			continue
		}
		for key, val := range toolVals {
			qualifiedKey := toolName + "." + key
			entry, knownKey := toolSchema[key]
			if !knownKey {
				details[qualifiedKey] = append(details[qualifiedKey], "unknown configuration field for this tool")
				continue
			}
			if msg := validateConfigValueType(entry, val); msg != "" {
				details[qualifiedKey] = append(details[qualifiedKey], msg)
			}
		}
	}
	if len(details) > 0 {
		return &ValidationError{Message: "invalid toolset configuration values", Details: details}
	}
	return nil
}

func (s *ToolsetService) loadExistingNestedConfig(ctx context.Context, packageID string) (map[string]map[string]any, error) {
	existingNested := map[string]map[string]any{}
	existingRow, err := s.repo.GetToolsetConfig(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf(errGetToolsetConfig, err)
	}
	if existingRow == nil || existingRow.ConfigValues == nil || *existingRow.ConfigValues == "" {
		return existingNested, nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(*existingRow.ConfigValues), &raw); err != nil {
		s.logger.Warn("toolset config: existing values not valid JSON, treating as empty", "packageID", packageID, "error", err)
		return existingNested, nil
	}
	for toolName, v := range raw {
		if toolMap, ok := v.(map[string]any); ok {
			existingNested[toolName] = toolMap
		}
	}
	return existingNested, nil
}

func (s *ToolsetService) composeMergedConfig(reqValues map[string]any, schemaByTool map[string]map[string]configSchemaField, existingNested map[string]map[string]any, packageID string) (map[string]any, error) {
	merged := make(map[string]any)
	for toolName, toolSchema := range schemaByTool {
		secrets := make(map[string]secretFieldInfo)
		for key, entry := range toolSchema {
			if entry.Secret {
				secrets[key] = secretFieldInfo{Required: entry.isRequired()}
			}
		}

		incoming := map[string]any{}
		if toolValsRaw, ok := reqValues[toolName]; ok {
			if tv, ok := toolValsRaw.(map[string]any); ok {
				maps.Copy(incoming, tv)
			}
		}

		existing := existingNested[toolName]
		if existing == nil {
			existing = map[string]any{}
		}

		if _, err := mergeSecretFields(incoming, existing, secrets); err != nil {
			return nil, err
		}

		toolMerged := make(map[string]any, len(existing)+len(incoming))
		maps.Copy(toolMerged, existing)
		for k, v := range incoming {
			if v == nil {
				delete(toolMerged, k)
			} else {
				toolMerged[k] = v
			}
		}

		for k := range toolMerged {
			if _, ok := toolSchema[k]; !ok {
				s.logger.Debug("toolset config: dropping orphan key", "packageID", packageID, "tool", toolName, "key", k)
				delete(toolMerged, k)
			}
		}

		if len(toolMerged) > 0 {
			merged[toolName] = toolMerged
		}
	}

	if authVal, ok := reqValues[reservedAuthConfigKey]; ok {
		merged[reservedAuthConfigKey] = mergeDeployerAuthBlock(authVal, existingNested[reservedAuthConfigKey])
	} else if existingAuth, ok := existingNested[reservedAuthConfigKey]; ok {
		merged[reservedAuthConfigKey] = existingAuth
	}

	if hilVal, ok := reqValues[reservedHILConfigKey]; ok {
		merged[reservedHILConfigKey] = hilVal
	} else if existingHIL, ok := existingNested[reservedHILConfigKey]; ok {
		merged[reservedHILConfigKey] = existingHIL
	}

	if scopesVal, ok := reqValues[reservedRequiredScopesConfigKey]; ok {
		merged[reservedRequiredScopesConfigKey] = scopesVal
	} else if existingScopes, ok := existingNested[reservedRequiredScopesConfigKey]; ok {
		merged[reservedRequiredScopesConfigKey] = existingScopes
	}

	return merged, nil
}

func validateConfigValueType(entry configSchemaField, val any) string {
	if val == nil {
		return ""
	}
	if s, ok := val.(string); ok && s == redactedSentinel {
		return ""
	}
	switch entry.Type {
	case "string", "":
		if _, ok := val.(string); !ok {
			return fmt.Sprintf("expected string, got %T", val)
		}
	case "integer":
		switch v := val.(type) {
		case float64:
			if v != float64(int64(v)) {
				return "expected integer, got fractional number"
			}
		case int, int32, int64:
		default:
			return fmt.Sprintf("expected integer, got %T", val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Sprintf("expected boolean, got %T", val)
		}
	}
	return ""
}

func (s *ToolsetService) BuildConfigSummary(ctx context.Context, packageID, discoveryStatus string) (*ConfigSummary, error) {
	if discoveryStatus != "ready" {
		return nil, nil
	}
	files, err := s.repo.GetFilesForPackage(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf("load package files for summary: %w", err)
	}
	schemaByTool := loadConfigSchemaByTool(files)

	var required []string
	for toolName, schema := range schemaByTool {
		for key, entry := range schema {
			if entry.isRequired() {
				required = append(required, toolName+"/"+key)
			}
		}
	}

	var covered []string
	if cfg, err := s.repo.GetToolsetConfig(ctx, packageID); err == nil && cfg != nil &&
		cfg.ConfigValues != nil && *cfg.ConfigValues != "" {
		var stored map[string]any
		if json.Unmarshal([]byte(*cfg.ConfigValues), &stored) == nil {
			for _, qualifiedKey := range required {
				toolName, key, _ := strings.Cut(qualifiedKey, "/")
				if toolValsRaw, ok := stored[toolName]; ok {
					if toolVals, ok := toolValsRaw.(map[string]any); ok {
						if v, ok := toolVals[key]; ok && v != nil {
							covered = append(covered, qualifiedKey)
						}
					}
				}
			}
		}
	}

	slices.Sort(required)
	slices.Sort(covered)

	return &ConfigSummary{
		RequiredFields:            required,
		RequiredCoveredByDefaults: covered,
		RequiresAgentConfig:       len(covered) < len(required),
	}, nil
}

func (s *ToolsetService) Download(ctx context.Context, id string) (io.ReadCloser, string, int64, error) {
	pkg, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, "", 0, fmt.Errorf(errGetToolPackage, err)
	}
	if pkg == nil {
		return nil, "", 0, &NotFoundError{Resource: resourceToolPackage, ID: id}
	}

	if pkg.ZipResourceKey == nil || *pkg.ZipResourceKey == "" {
		return nil, "", 0, &ValidationError{Message: "no ZIP file uploaded for this package"}
	}

	rc, err := s.storage.get(ctx, *pkg.ZipResourceKey)
	if err != nil {
		return nil, "", 0, fmt.Errorf("get tool package: %w", err)
	}

	filename := "package.zip"
	if pkg.ZipFilename != nil && *pkg.ZipFilename != "" {
		filename = *pkg.ZipFilename
	}
	var size int64
	if pkg.ZipFileSize != nil {
		size = *pkg.ZipFileSize
	}

	return rc, filename, size, nil
}

type ToolInitMeta struct {
	Params           *string
	ConfigSchema     *string
	Capabilities     *string
	Auth             *string
	PackageToolNames []string
	TimeoutSeconds   int
}

func (s *ToolsetService) HandleToolInit(ctx context.Context, prefixedToolName, description string, meta ToolInitMeta) {
	pkgName, toolName, ok := splitToolName(prefixedToolName)
	if !ok {
		return
	}

	pkg, err := s.repo.GetByName(ctx, pkgName)
	if err != nil {
		s.logger.Error("toolset: get package by name for tool init", "package", pkgName, "tool", toolName, "error", err)
		return
	}
	if pkg == nil {
		s.logger.Debug("toolset: ignoring tool init for unknown package", "package", pkgName, "tool", toolName)
		return
	}

	if !packageNameRe.MatchString(toolName) {
		errMsg := fmt.Sprintf("tool name %q rejected: must match %s", toolName, packageNameRe.String())
		s.logger.Warn("toolset: invalid tool name from STR", "package", pkgName, "tool", toolName)
		if appendErr := s.repo.AppendDiscoveryError(ctx, pkg.ID, errMsg); appendErr != nil {
			s.logger.Warn("toolset: append invalid-name discovery error", "package", pkgName, "toolName", toolName, "error", appendErr)
		}
		return
	}

	if errMsg := rejectReservedConfigKey(meta.ConfigSchema); errMsg != "" {
		msg := fmt.Sprintf("tool %q: %s", toolName, errMsg)
		s.logger.Warn("toolset: rejecting tool with reserved config field", "package", pkgName, "tool", toolName, "error", msg)
		if appendErr := s.repo.AppendDiscoveryError(ctx, pkg.ID, msg); appendErr != nil {
			s.logger.Warn("toolset: append reserved-key discovery error", "package", pkgName, "toolName", toolName, "error", appendErr)
		}
		return
	}

	if strings.TrimSpace(description) == "" {
		msg := fmt.Sprintf("tool %q rejected: empty description; every tool must describe what it does", toolName)
		s.logger.Warn("toolset: rejecting tool with empty description", "package", pkgName, "tool", toolName)
		if appendErr := s.repo.AppendDiscoveryError(ctx, pkg.ID, msg); appendErr != nil {
			s.logger.Warn("toolset: append empty-description discovery error", "package", pkgName, "toolName", toolName, "error", appendErr)
		}
		return
	}

	f := &store.ToolPackageFile{
		PackageID:       pkg.ID,
		ToolName:        toolName,
		ToolDescription: strPtr(description),
		TimeoutSeconds:  meta.TimeoutSeconds,
		SandboxProfile:  "standard",
		Parameters:      meta.Params,
		ConfigSchema:    meta.ConfigSchema,
		Auth:            meta.Auth,
	}

	writeFile := true
	if existingFiles, filesErr := s.repo.GetFilesForPackage(ctx, pkg.ID); filesErr == nil {
		writeFile = !toolFileUnchanged(existingFiles, f)
	}

	if writeFile {
		if upsertErr := s.repo.UpsertToolFile(ctx, f); upsertErr != nil {
			s.logger.Warn("failed to upsert tool file", "package", pkgName, "tool", toolName, "error", upsertErr)
			return
		}
	}

	if meta.Capabilities != nil {
		if capsErr := s.repo.UpdateCapabilities(ctx, pkg.ID, *meta.Capabilities); capsErr != nil {
			s.logger.Warn("toolset: update capabilities", "package", pkgName, "error", capsErr)
		}
	}

	if pkg.DiscoveryStatus != "ready" {
		ready := true
		if len(meta.PackageToolNames) > 0 {
			ready, err = s.allPackageToolsRegistered(ctx, pkg.ID, meta.PackageToolNames)
			if err != nil {
				s.logger.Warn("toolset: skip ready transition; failed to read package files",
					"package", pkgName, "error", err)
				return
			}
		}
		if ready {
			if s.orphanScopeGateBlocksReady(ctx, pkg.ID, pkgName, meta.PackageToolNames) {
				return
			}
			expected := meta.PackageToolNames
			if len(expected) == 0 {
				expected = []string{toolName}
			}
			if err := s.repo.MarkReadyIfToolsRegistered(ctx, pkg.ID, expected); err != nil {
				s.logger.Error("toolset: flip status to ready on tool init", "package", pkgName, "error", err)
			}
		}
	}
}

const orphanScopeErrMarker = "no longer registers"

func failedOnOrphanedScopeGate(pkg *store.ToolPackage) bool {
	return pkg.DiscoveryStatus == "failed" &&
		pkg.DiscoveryErrors != nil &&
		strings.Contains(*pkg.DiscoveryErrors, orphanScopeErrMarker)
}

func (s *ToolsetService) orphanScopeGateBlocksReady(ctx context.Context, packageID, pkgName string, packageToolNames []string) bool {
	if len(packageToolNames) == 0 {
		return false
	}
	current := make(map[string]struct{}, len(packageToolNames))
	for _, n := range packageToolNames {
		current[n] = struct{}{}
	}
	orphans, err := s.orphanedRequiredScopeGates(ctx, packageID, current)
	if err != nil {
		const failMsg = "could not verify required_scopes gates; discovery will retry"
		if failErr := s.repo.SetFailedDiscovery(ctx, packageID, failMsg); failErr != nil {
			s.logger.Error("toolset: record required_scopes check failure", "package", pkgName, "error", failErr)
		}
		s.logger.Warn("toolset: skip ready transition; failed to check orphaned required_scopes gates",
			"package", pkgName, "error", err)
		return true
	}
	if len(orphans) == 0 {
		return false
	}
	msg := fmt.Sprintf("required_scopes gates %s, which this package version %s; clear or re-gate that entry in the toolset config (it stays editable) — the package returns to ready once no gate names a missing tool", strings.Join(orphans, ", "), orphanScopeErrMarker)
	if failErr := s.repo.SetFailedDiscovery(ctx, packageID, msg); failErr != nil {
		s.logger.Error("toolset: mark discovery failed on orphaned required_scopes gate", "package", pkgName, "error", failErr)
	}
	return true
}

func (s *ToolsetService) orphanedRequiredScopeGates(ctx context.Context, packageID string, current map[string]struct{}) ([]string, error) {
	seen := map[string]struct{}{}
	var orphans []string
	check := func(cfgJSON *string) {
		for _, tool := range requiredScopeToolKeys(cfgJSON) {
			if _, ok := current[tool]; ok {
				continue
			}
			if _, dup := seen[tool]; dup {
				continue
			}
			seen[tool] = struct{}{}
			orphans = append(orphans, tool)
		}
	}
	tsCfg, err := s.repo.GetToolsetConfig(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf("load toolset config: %w", err)
	}
	if tsCfg != nil {
		check(tsCfg.ConfigValues)
	}
	agentCfgs, err := s.repo.GetAgentToolsetConfigsForPackage(ctx, packageID)
	if err != nil {
		return nil, fmt.Errorf("load agent toolset configs: %w", err)
	}
	for i := range agentCfgs {
		check(agentCfgs[i].ConfigValues)
	}
	slices.Sort(orphans)
	return orphans, nil
}

func toolFileUnchanged(files []store.ToolPackageFile, incoming *store.ToolPackageFile) bool {
	for i := range files {
		f := &files[i]
		if f.ToolName != incoming.ToolName {
			continue
		}
		return strPtrEqual(f.ToolDescription, incoming.ToolDescription) &&
			f.TimeoutSeconds == incoming.TimeoutSeconds &&
			f.SandboxProfile == incoming.SandboxProfile &&
			strPtrEqual(f.Parameters, incoming.Parameters) &&
			strPtrEqual(f.ConfigSchema, incoming.ConfigSchema) &&
			strPtrEqual(f.Auth, incoming.Auth)
	}
	return false
}

func (s *ToolsetService) allPackageToolsRegistered(ctx context.Context, packageID string, expected []string) (bool, error) {
	files, err := s.repo.GetFilesForPackage(ctx, packageID)
	if err != nil {
		return false, fmt.Errorf("get files for package %s: %w", packageID, err)
	}
	registered := make(map[string]struct{}, len(files))
	for _, f := range files {
		registered[f.ToolName] = struct{}{}
	}
	for _, name := range expected {
		if _, ok := registered[name]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func (s *ToolsetService) HandleToolRemoved(ctx context.Context, prefixedToolName string) {
	pkgName, toolName, ok := splitToolName(prefixedToolName)
	if !ok {
		return
	}

	pkg, err := s.repo.GetByName(ctx, pkgName)
	if err != nil {
		s.logger.Error("toolset: get package by name for tool removal", "package", pkgName, "tool", toolName, "error", err)
		return
	}
	if pkg == nil {
		s.logger.Debug("toolset: ignoring tool removal for unknown package", "package", pkgName, "tool", toolName)
		return
	}

	if err := s.repo.RemoveToolFile(ctx, pkg.ID, toolName); err != nil {
		s.logger.Error("failed to remove tool file", "package", pkgName, "tool", toolName, "error", err)
	}
}

func (s *ToolsetService) HandleToolFailed(ctx context.Context, prefixedToolName, errMsg string) {
	pkgName, _, ok := splitToolName(prefixedToolName)
	if !ok {
		return
	}

	pkg, err := s.repo.GetByName(ctx, pkgName)
	if err != nil {
		s.logger.Error("toolset: get package by name for failed discovery", "package", pkgName, "error", err)
		return
	}
	if pkg == nil {
		s.logger.Debug("toolset: ignoring failed-discovery for unknown package", "package", pkgName)
		return
	}

	if setErr := s.repo.SetFailedDiscovery(ctx, pkg.ID, errMsg); setErr != nil {
		s.logger.Error("toolset: set failed discovery", "package", pkgName, "error", setErr)
	}
}

func (s *ToolsetService) FailStalePending(ctx context.Context, cutoffMs int64) error {
	stale, err := s.repo.ListPendingOlderThan(ctx, cutoffMs)
	if err != nil {
		return fmt.Errorf("list stale pending: %w", err)
	}

	for _, pkg := range stale {
		errMsg := "discovery timed out — no tool init events received within the timeout period"
		errs, _ := json.Marshal([]string{errMsg})
		errsStr := string(errs)
		failed, updateErr := s.repo.FailStalePending(ctx, pkg.ID, cutoffMs, &errsStr)
		switch {
		case updateErr != nil:
			s.logger.Error("failed to mark package as failed", "package", pkg.Name, "error", updateErr)
		case failed:
			s.logger.Info("marked stale pending package as failed", "package", pkg.Name)
		}
	}
	return nil
}

func splitToolName(prefixed string) (pkgName, toolName string, ok bool) {
	parts := strings.SplitN(prefixed, "__", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func validateAndDetectRuntime(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", newReaderFailure(errInvalidZIP, "invalid ZIP file", err)
	}

	if len(zr.File) > maxZipEntries {
		return "", fmt.Errorf("ZIP contains too many entries (%d > %d)", len(zr.File), maxZipEntries)
	}

	var totalUncompressed uint64
	hasPython := false
	hasNativeBinary := false

	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			continue
		}

		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			return "", fmt.Errorf("ZIP contains unsafe path: %s", name)
		}
		cleaned := path.Clean(strings.ReplaceAll(name, "\\", "/"))
		if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/../") {
			return "", fmt.Errorf("ZIP contains unsafe Windows path: %s", name)
		}

		if f.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("ZIP contains symlink: %s", name)
		}

		totalUncompressed += f.UncompressedSize64
		if totalUncompressed > uint64(maxZipUncompressed) {
			return "", fmt.Errorf("ZIP uncompressed size exceeds %d MB limit", maxZipUncompressed>>20)
		}

		if strings.HasSuffix(name, ".py") {
			hasPython = true
		}

		if !hasNativeBinary && looksLikeNativeBinary(f) {
			hasNativeBinary = true
		}
	}

	if hasNativeBinary {
		return "go", nil
	}
	if hasPython {
		return "python", nil
	}
	return "unknown", nil
}

func looksLikeNativeBinary(f *zip.File) bool {
	rc, err := f.Open()
	if err != nil {
		return false
	}
	defer func() { _ = rc.Close() }()

	header := make([]byte, 4)
	if _, err := io.ReadFull(rc, header); err != nil {
		return false
	}

	if header[0] == 0x7f && header[1] == 'E' && header[2] == 'L' && header[3] == 'F' {
		return true
	}

	magic := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
	switch magic {
	case 0xfeedface, 0xfeedfacf, 0xbebafeca, 0xcffaedfe, 0xcefaedfe:
		return true
	}

	return false
}
