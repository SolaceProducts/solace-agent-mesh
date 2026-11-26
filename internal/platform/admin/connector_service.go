// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
)

const maxConnectorResourceSize = 10 << 20

const mcpDiscoverAtSaveTimeout = 10 * time.Second

const errGetConnectorWrap = "get connector: %w"

type ConnectorService struct {
	connectorRepo *store.ConnectorRepo
	resourceStore ResourceStore
	namespace     string
	logger        *slog.Logger
	mcpSvc        *MCPDiscoveryService
	redeploy      redeployDeps
	agentRegistry *platpkg.AgentRegistry
}

type redeployDeps struct {
	db           *store.DB
	agentRepo    *store.AgentRepo
	workflowRepo *store.WorkflowRepo
	outboxRepo   *store.OutboxRepo
}

func NewConnectorService(connectorRepo *store.ConnectorRepo, resourceStore ResourceStore, namespace string, logger *slog.Logger) *ConnectorService {
	return &ConnectorService{connectorRepo: connectorRepo, resourceStore: resourceStore, namespace: namespace, logger: logger}
}

func (s *ConnectorService) SetMCPDiscoveryService(svc *MCPDiscoveryService) {
	s.mcpSvc = svc
}

func (s *ConnectorService) SetAgentRegistry(reg *platpkg.AgentRegistry) {
	s.agentRegistry = reg
}

func (s *ConnectorService) SetRedeployDeps(db *store.DB, agentRepo *store.AgentRepo, workflowRepo *store.WorkflowRepo, outboxRepo *store.OutboxRepo) {
	s.redeploy = redeployDeps{db: db, agentRepo: agentRepo, workflowRepo: workflowRepo, outboxRepo: outboxRepo}
}

type ConnectorUpdateOptions struct {
	ClearDescription bool
	URLSpec          URLSpec
	MCPManifest      []map[string]any
}

func clearedDescriptionStoresEmptyNotNull() *string {
	return strPtrOrEmpty("")
}

func (s *ConnectorService) UpdateWithRedeploy(ctx context.Context, id string, req UpdateConnectorRequest, userID string, opts ConnectorUpdateOptions) (*ConnectorResponse, error) {
	agentIDs := findAgentsByConnector(ctx, s.redeploy.agentRepo, s.logger, id)
	workflowIDs := findWorkflowsByConnector(ctx, s.redeploy.workflowRepo, s.logger, id)

	preflight, err := s.preflightUpdate(ctx, id, req)
	if err != nil {
		return nil, err
	}
	opts.URLSpec = preflight.spec
	opts.MCPManifest = preflight.mcpManifest

	var resp *ConnectorResponse
	if err := s.redeploy.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var inErr error
		resp, inErr = s.UpdateInTx(ctx, tx, id, req, userID, opts)
		if inErr != nil {
			return inErr
		}
		return writeRedeployEventInTx(ctx, s.redeploy.outboxRepo, tx, redeployEvent{"connector.updated", id, userID, agentIDs, workflowIDs})
	}); err != nil {
		return nil, err
	}

	resp.Warnings = preflight.warnings
	return resp, nil
}

func (s *ConnectorService) DeleteWithRedeploy(ctx context.Context, id, userID string) error {
	agentIDs := findAgentsByConnector(ctx, s.redeploy.agentRepo, s.logger, id)
	workflowIDs := findWorkflowsByConnector(ctx, s.redeploy.workflowRepo, s.logger, id)

	return s.redeploy.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := s.DeleteInTx(ctx, tx, id); err != nil {
			return err
		}
		return writeRedeployEventInTx(ctx, s.redeploy.outboxRepo, tx, redeployEvent{"connector.deleted", id, userID, agentIDs, workflowIDs})
	})
}

func findAgentsByConnector(ctx context.Context, agentRepo *store.AgentRepo, logger *slog.Logger, connectorID string) []string {
	if agentRepo == nil {
		return nil
	}
	var agentIDs []string
	page := 1
	const pageSize = 100
	for {
		agents, total, err := agentRepo.ListByConnectorID(ctx, connectorID, page, pageSize)
		if err != nil {
			logWarn(logger, "list agents for connector redeploy failed; bound agents may stay on the old configuration",
				"connectorId", connectorID, "page", page, "error", err)
			break
		}
		if len(agents) == 0 {
			break
		}
		for _, a := range agents {
			agentIDs = append(agentIDs, a.Agent.ID)
		}
		if page*pageSize >= total {
			break
		}
		page++
	}
	return agentIDs
}

func findWorkflowsByConnector(ctx context.Context, workflowRepo *store.WorkflowRepo, logger *slog.Logger, connectorID string) []string {
	if workflowRepo == nil {
		return nil
	}
	workflowIDs, err := workflowRepo.ListIDsByConnectorID(ctx, connectorID)
	if err != nil {
		logWarn(logger, "list workflows for connector redeploy failed; bound workflows may stay on the old configuration",
			"connectorId", connectorID, "error", err)
		return nil
	}
	return workflowIDs
}

type redeployEvent struct {
	eventType   string
	connectorID string
	userID      string
	agentIDs    []string
	workflowIDs []string
}

func writeRedeployEventInTx(ctx context.Context, outboxRepo *store.OutboxRepo, tx *dbutil.Tx, ev redeployEvent) error {
	if outboxRepo == nil || (len(ev.agentIDs) == 0 && len(ev.workflowIDs) == 0) {
		return nil
	}
	payload, err := json.Marshal(redeployPayload{AgentIDs: ev.agentIDs, WorkflowIDs: ev.workflowIDs, UserID: ev.userID})
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", ev.eventType, err)
	}
	if err := outboxRepo.CreateInTx(ctx, tx, &store.OutboxEvent{
		EventType:   ev.eventType,
		AggregateID: ev.connectorID,
		Payload:     string(payload),
	}); err != nil {
		return fmt.Errorf("write %s outbox event: %w", ev.eventType, err)
	}
	return nil
}

func (s *ConnectorService) Create(ctx context.Context, req CreateConnectorRequest, userID string) (*ConnectorResponse, error) {
	return s.CreateWithSpecRead(ctx, req, userID, nil)
}

type URLSpecRead struct {
	Spec     URLSpec
	Warnings []string
}

func (s *ConnectorService) CreateWithSpecRead(ctx context.Context, req CreateConnectorRequest, userID string, read *URLSpecRead) (*ConnectorResponse, error) {
	if err := ValidateConnectorCreate(req); err != nil {
		return nil, err
	}

	if values, ok := req.Values.(map[string]any); ok {
		values = maps.Clone(values)
		dropEmptyHIL(values)
		req.Values = values
	}

	var valuesStr *string
	if req.Values != nil {
		b, marshalErr := json.Marshal(req.Values)
		if marshalErr != nil {
			return nil, fmt.Errorf(errMarshalValuesWrap, marshalErr)
		}
		vs := string(b)
		valuesStr = &vs
	}

	if valuesStr == nil {
		empty := "{}"
		valuesStr = &empty
	}

	warnings, err := s.probeNewConnector(ctx, req, read)
	if err != nil {
		return nil, err
	}

	vs, enriched, discoveryWarnings := s.enrichMCPManifestIfNeeded(ctx, req.Type, req.Values)
	warnings = append(warnings, discoveryWarnings...)
	if enriched {
		req.Values = vs
		b, marshalErr := json.Marshal(vs)
		if marshalErr != nil {
			return nil, fmt.Errorf(errMarshalValuesWrap, marshalErr)
		}
		newVS := string(b)
		valuesStr = &newVS
	}

	connector := &store.Connector{
		Name:        req.Name,
		Description: strPtrOrEmpty(req.Description),
		Type:        req.Type,
		Subtype:     strPtrOrEmpty(req.Subtype),
		Values:      valuesStr,
		CreatedBy:   userID,
		UpdatedBy:   userID,
	}

	if err := s.connectorRepo.Create(ctx, connector); err != nil {
		if dbutil.IsUniqueViolation(err) {
			return nil, &ConflictError{Message: fmt.Sprintf("a connector with name %q already exists", req.Name)}
		}
		return nil, fmt.Errorf("create connector: %w", err)
	}

	resp := s.buildResponse(connector, 0)
	resp.Warnings = warnings
	return resp, nil
}

func (s *ConnectorService) GetByID(ctx context.Context, id string) (*ConnectorResponse, error) {
	connector, err := s.connectorRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetConnectorWrap, err)
	}
	if connector == nil {
		return nil, &NotFoundError{Resource: "connector", ID: id}
	}

	counts, _ := s.connectorRepo.GetAgentCounts(ctx, []string{id})
	return s.buildResponse(connector, counts[id]), nil
}

type ConnectorNameLookup struct {
	Exists       bool   `json:"exists"`
	Type         string `json:"type,omitempty"`
	HasResource  bool   `json:"hasResource"`
	HasAllowList bool   `json:"hasAllowList"`
}

func (s *ConnectorService) LookupByName(ctx context.Context, name string) (*ConnectorNameLookup, error) {
	c, err := s.connectorRepo.GetByNormalizedName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("look up connector by name: %w", err)
	}
	if c == nil {
		return &ConnectorNameLookup{}, nil
	}
	return &ConnectorNameLookup{
		Exists:       true,
		Type:         c.Type,
		HasResource:  c.ResourceKey != nil && *c.ResourceKey != "",
		HasAllowList: s.storedAllowListHasOperation(c),
	}, nil
}

func (s *ConnectorService) storedAllowListHasOperation(c *store.Connector) bool {
	if c.Values == nil || *c.Values == "" {
		return false
	}
	var values map[string]any
	if json.Unmarshal([]byte(*c.Values), &values) != nil {
		s.logger.Warn("connector lookup: stored values are not a JSON object", "connectorID", c.ID)
		return false
	}
	allowList, _ := values["allow_list"].(string)
	return len(parseOperationList(allowList)) > 0
}

func (s *ConnectorService) GetValuesCleartext(ctx context.Context, id string) (string, string, map[string]any, error) {
	c, err := s.connectorRepo.GetByID(ctx, id)
	if err != nil {
		return "", "", nil, fmt.Errorf(errGetConnectorWrap, err)
	}
	if c == nil {
		return "", "", nil, &NotFoundError{Resource: "connector", ID: id}
	}
	values := map[string]any{}
	if c.Values != nil && *c.Values != "" {
		if err := json.Unmarshal([]byte(*c.Values), &values); err != nil {
			return "", "", nil, fmt.Errorf("parse connector values: %w", err)
		}
	}
	subtype := ""
	if c.Subtype != nil {
		subtype = *c.Subtype
	}
	return c.Type, subtype, values, nil
}

func (s *ConnectorService) GetSecretsDiff(ctx context.Context, id string, candidate map[string]any) (map[string]SecretFieldDiffResult, error) {
	connType, subtype, stored, err := s.GetValuesCleartext(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ConnectorSecretFieldsKnown(connType, subtype) {
		return compareSetValues(candidate, stored), nil
	}
	fields := registryToSecretFields(getSecretRegistry().ConnectorSecretFields(connType, subtype))
	return compareSecrets(fields, candidate, stored), nil
}

func (s *ConnectorService) TestEventMeshConnection(ctx context.Context, req TestConnectorConnectionRequest) (*TestConnectionResponse, error) {
	submitted, err := submittedConnectorTestValues(req.Values)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if req.ConnectorID != "" {
		connType, _, stored, err := s.GetValuesCleartext(ctx, req.ConnectorID)
		if err != nil {
			return nil, err
		}
		if connType != "event_mesh" {
			return nil, &ValidationError{Message: "connection testing is only supported for Event Mesh connectors", Field: fieldConnectorID}
		}
		bound, err := bindProbeValues(stored, submitted, connectorEventMeshProbeBinding)
		if err != nil {
			logProbeRefusal(ctx, s.logger, bound.refusedField, "connectorID", req.ConnectorID)
			return nil, err
		}
		maps.Copy(values, bound.mergeable)
		submitted = bound.submitted
	}
	maps.Copy(values, submitted)
	if len(values) == 0 {
		return nil, &ValidationError{Message: "connectorId or values must be provided", Field: fieldValues}
	}

	brokerURL, _ := values["broker_url"].(string)
	vpn, _ := values["broker_vpn"].(string)
	username, _ := values["broker_username"].(string)
	password, _ := values["broker_password"].(string)
	if brokerURL == "" || vpn == "" || username == "" || password == "" {
		return nil, &ValidationError{Message: "Host URI, Message VPN, username, and password are required to test the connection", Field: fieldValues}
	}
	if err := testBrokerConnection(ctx, brokerURL, vpn, username, password, false); err != nil {
		return &TestConnectionResponse{Success: false, Message: classifyConnectionError(err)}, nil
	}
	return &TestConnectionResponse{Success: true}, nil
}

func submittedConnectorTestValues(raw any) (map[string]any, error) {
	submitted := map[string]any{}
	if raw == nil {
		return submitted, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal connector test values: %w", err)
	}
	incoming := map[string]any{}
	if err := json.Unmarshal(encoded, &incoming); err != nil {
		return nil, fmt.Errorf("unmarshal connector test values: %w", err)
	}
	for key, value := range incoming {
		if value != nil && value != "" && value != redactedSentinel {
			submitted[key] = value
		}
	}
	return submitted, nil
}

func (s *ConnectorService) ListPaginated(ctx context.Context, pageNumber, pageSize int, filter store.ConnectorFilter, order store.ConnectorOrder) (*PaginatedResponse[ConnectorResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	connectors, total, err := s.connectorRepo.ListPaginated(ctx, pageNumber, pageSize, filter, order)
	if err != nil {
		return nil, fmt.Errorf("list connectors: %w", err)
	}

	ids := make([]string, len(connectors))
	for i, c := range connectors {
		ids[i] = c.ID
	}
	counts, _ := s.connectorRepo.GetAgentCounts(ctx, ids)

	responses := make([]ConnectorResponse, len(connectors))
	for i, c := range connectors {
		responses[i] = *s.buildResponse(c, counts[c.ID])
	}

	return &PaginatedResponse[ConnectorResponse]{
		Data: responses,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *ConnectorService) Update(ctx context.Context, id string, req UpdateConnectorRequest, userID string) (*ConnectorResponse, error) {
	preflight, err := s.preflightUpdate(ctx, id, req)
	if err != nil {
		return nil, err
	}
	resp, err := s.updateImpl(ctx, id, req, userID, nil, ConnectorUpdateOptions{URLSpec: preflight.spec, MCPManifest: preflight.mcpManifest})
	if err != nil {
		return nil, err
	}
	resp.Warnings = preflight.warnings
	return resp, nil
}

func (s *ConnectorService) UpdateInTx(ctx context.Context, tx *dbutil.Tx, id string, req UpdateConnectorRequest, userID string, opts ConnectorUpdateOptions) (*ConnectorResponse, error) {
	return s.updateImpl(ctx, id, req, userID, tx, opts)
}

func (s *ConnectorService) PreflightUpdate(ctx context.Context, id string, req UpdateConnectorRequest) ([]string, error) {
	preflight, err := s.preflightUpdate(ctx, id, req)
	return preflight.warnings, err
}

type updatePreflight struct {
	warnings    []string
	spec        URLSpec
	mcpManifest []map[string]any
}

func (s *ConnectorService) preflightUpdate(ctx context.Context, id string, req UpdateConnectorRequest) (updatePreflight, error) {
	refresh, err := s.readMCPManifest(ctx, id, req)
	if err != nil {
		return updatePreflight{}, err
	}
	existing, values, prunedWarnings, err := s.validatedUpdateValues(ctx, id, req, refresh.manifest)
	if err != nil {
		return updatePreflight{}, err
	}
	if values == nil {
		return updatePreflight{warnings: refresh.warnings, mcpManifest: refresh.manifest}, nil
	}
	hasResource := existing.ResourceKey != nil && *existing.ResourceKey != ""
	probeWarnings, spec, err := s.probeUpdatedConnector(ctx, existing, req.Values, values, hasResource)
	if err != nil {
		return updatePreflight{}, err
	}
	warnings := slices.Concat(refresh.warnings, prunedWarnings, probeWarnings)
	if merged, ok := values.(map[string]any); ok {
		if err := refuseAuthoredGatesOutsideURLSpec(existing, req.Values, merged, spec); err != nil {
			return updatePreflight{}, err
		}
		warnings = append(warnings, s.uncheckedAPIHILWarnings(ctx, existing, merged, spec)...)
	}
	return updatePreflight{warnings: warnings, spec: spec, mcpManifest: refresh.manifest}, nil
}

const (
	mcpToolListKeptWarning = "The MCP server's tool list could not be read during the save, so the connector keeps the tool list it already had. " +
		"The server may be unreachable or list no tools; save the connector again once it lists them."
	mcpToolListKeptBecause    = "The connector keeps the tool list it already had, because the platform could not list the MCP server's tools during the save: "
	mcpToolListClearedWarning = "The connector's tool list was not refreshed during the save, and the update sends an empty manifest, " +
		"so the connector now stores no tool list and its agents list the server's tools when they start."
	mcpToolListEmptiedWarning = "The update sends an empty manifest, and this MCP server cannot be listed during a save (it uses OAuth, an auth type a save cannot sign in with, or has no server_url), " +
		"so the connector now stores no tool list and its agents may start without its tools. Send the manifest with the update to keep it."
)

const (
	overlongNamesShown   = 5
	overlongNameRunesMax = 80
)

type mcpToolListRefresh struct {
	manifest []map[string]any
	warnings []string
}

type mcpManifestUpdate struct {
	sendsEmpty  bool
	storesTools bool
	selected    []string
}

func (s *ConnectorService) readMCPManifest(ctx context.Context, id string, req UpdateConnectorRequest) (mcpToolListRefresh, error) {
	reqValues, sendsValues := req.Values.(map[string]any)
	if req.Values != nil && !sendsValues {
		return mcpToolListRefresh{}, nil
	}
	if hasNonEmptyManifest(reqValues["manifest"]) {
		return mcpToolListRefresh{warnings: unreadableTLSFileWarnings(reqValues)}, nil
	}
	existing, err := s.connectorRepo.GetByID(ctx, id)
	if err != nil {
		return mcpToolListRefresh{}, fmt.Errorf(errGetConnectorWrap, err)
	}
	if existing == nil || existing.Type != "mcp" {
		return mcpToolListRefresh{}, nil
	}
	credentials := maps.Clone(reqValues)
	update := mcpManifestUpdate{sendsEmpty: reqValues["manifest"] != nil}
	if existing.Values != nil {
		var stored map[string]any
		if json.Unmarshal([]byte(*existing.Values), &stored) != nil {
			return mcpToolListRefresh{}, nil
		}
		update.storesTools = hasNonEmptyManifest(stored["manifest"])
		if !sendsValues {
			credentials = stored
		} else if credentials, _, err = inheritStoredValues(existing, credentials, stored); err != nil {
			return mcpToolListRefresh{}, err
		}
	}
	if !sendsValues && (update.storesTools || credentials == nil) {
		return mcpToolListRefresh{}, nil
	}
	update.selected = parseMCPSelectedTools(credentials)
	discovered, outcome, failure := s.discoverMCPManifest(ctx, s.logger.With("connectorID", id), credentials)
	switch outcome {
	case mcpDiscoverySkipped:
		refresh := update.skipped()
		refresh.warnings = append(refresh.warnings, unreadableTLSFileWarnings(credentials)...)
		return refresh, nil
	case mcpDiscoveryUnread:
		return update.unread(failure), nil
	case mcpDiscoveryListed:
		return update.listed(discovered), nil
	}
	return mcpToolListRefresh{}, fmt.Errorf("mcp tool discovery: unhandled outcome %d", outcome)
}

func (u mcpManifestUpdate) skipped() mcpToolListRefresh {
	if u.sendsEmpty && u.storesTools {
		return mcpToolListRefresh{warnings: []string{mcpToolListEmptiedWarning}}
	}
	return mcpToolListRefresh{}
}

func (u mcpManifestUpdate) unread(failure error) mcpToolListRefresh {
	switch {
	case u.sendsEmpty:
		return mcpToolListRefresh{warnings: []string{mcpToolListClearedWarning}}
	case u.storesTools && failure != nil:
		return mcpToolListRefresh{warnings: []string{mcpToolListKeptBecause + discoveryFailureReason(failure)}}
	case u.storesTools:
		return mcpToolListRefresh{warnings: []string{mcpToolListKeptWarning}}
	}
	return mcpToolListRefresh{warnings: discoveryFailureWarnings(failure)}
}

func (u mcpManifestUpdate) listed(discovered []map[string]any) mcpToolListRefresh {
	manifest, overlong := withoutOverlongToolNames(discovered, u.selected)
	if len(overlong) == 0 {
		return mcpToolListRefresh{manifest: manifest}
	}
	switch {
	case len(manifest) > 0:
		return mcpToolListRefresh{manifest: manifest, warnings: []string{overlongToolNamesWarning(overlong)}}
	case u.sendsEmpty:
		return mcpToolListRefresh{warnings: []string{mcpToolListClearedWarning, overlongToolNamesWarning(overlong)}}
	case u.storesTools:
		return mcpToolListRefresh{warnings: []string{everyToolNameOverlongWarning(overlong)}}
	}
	return mcpToolListRefresh{warnings: []string{overlongToolNamesWarning(overlong)}}
}

func withoutOverlongToolNames(manifest []map[string]any, selected []string) (kept []map[string]any, overlong []string) {
	kept = make([]map[string]any, 0, len(manifest))
	for _, tool := range manifest {
		name, _ := tool["name"].(string)
		if len(name) > maxMCPToolNameLen && !slices.Contains(selected, name) {
			overlong = append(overlong, name)
			continue
		}
		kept = append(kept, tool)
	}
	return kept, overlong
}

func overlongToolNamesWarning(names []string) string {
	return fmt.Sprintf("The MCP server lists %d tool(s) with names over %d characters, which some model providers refuse, so they were left out of the connector's tool list: %s.",
		len(names), maxMCPToolNameLen, shownToolNames(names))
}

func everyToolNameOverlongWarning(names []string) string {
	return fmt.Sprintf("Every tool the MCP server lists has a name over %d characters, which some model providers refuse, so the connector keeps the tool list it already had. "+
		"Shorten these names on the server: %s.", maxMCPToolNameLen, shownToolNames(names))
}

func shownToolNames(names []string) string {
	shown := make([]string, 0, min(len(names), overlongNamesShown))
	for _, name := range names[:min(len(names), overlongNamesShown)] {
		name = strings.Map(withoutControlRune, name)
		if runes := []rune(name); len(runes) > overlongNameRunesMax {
			name = string(runes[:overlongNameRunesMax]) + "…"
		}
		shown = append(shown, name)
	}
	list := strings.Join(shown, ", ")
	if hidden := len(names) - len(shown); hidden > 0 {
		list += fmt.Sprintf(", and %d more", hidden)
	}
	return list
}

func withoutControlRune(r rune) rune {
	if unicode.IsControl(r) {
		return -1
	}
	return r
}

func (s *ConnectorService) probeUpdatedConnector(ctx context.Context, existing *store.Connector, reqValues, merged any, hasResource bool) ([]string, URLSpec, error) {
	if existing.Type == webSearchConnectorType && !requestCarriesFreshWebSearchKey(reqValues) {
		return nil, URLSpec{}, nil
	}
	return s.probeConnector(ctx, existing.Type, derefStr(existing.Subtype), merged, hasResource)
}

func (s *ConnectorService) probeConnector(ctx context.Context, typ, subtype string, values any, hasResource bool) ([]string, URLSpec, error) {
	vmap, ok := values.(map[string]any)
	if typ != "api" || subtype != "openapi" || !ok || vmap == nil {
		warnings, err := runConnectorProbe(ctx, s.logger, typ, subtype, values, hasResource)
		return warnings, URLSpec{}, err
	}
	result, err := probeOpenAPISpec(ctx, s.logger, vmap, hasResource, gatesTools(vmap["hil"]))
	return result.warnings, result.spec, err
}

func (s *ConnectorService) probeNewConnector(ctx context.Context, req CreateConnectorRequest, read *URLSpecRead) ([]string, error) {
	var (
		warnings []string
		spec     URLSpec
	)
	if read != nil {
		warnings, spec = read.Warnings, read.Spec
	} else {
		var err error
		if warnings, spec, err = s.probeConnector(ctx, req.Type, req.Subtype, req.Values, false); err != nil {
			return nil, err
		}
	}
	gateWarnings, err := newConnectorGateWarnings(req, spec)
	if err != nil {
		return nil, err
	}
	return slices.Concat(warnings, gateWarnings), nil
}

func newConnectorGateWarnings(req CreateConnectorRequest, spec URLSpec) ([]string, error) {
	values, ok := req.Values.(map[string]any)
	if !ok || req.Type != "api" || req.Subtype != "openapi" {
		return nil, nil
	}
	if spec.Read {
		return nil, validateHILToolKeys(values["hil"], spec.Tools)
	}
	return uncheckedHILWarnings(values, uncheckedAPIHILReason), nil
}

func (s *ConnectorService) ValidateCreate(ctx context.Context, req CreateConnectorRequest) ([]string, error) {
	warnings, _, err := s.ValidateCreateReading(ctx, req)
	return warnings, err
}

func (s *ConnectorService) ValidateCreateReading(ctx context.Context, req CreateConnectorRequest) ([]string, *URLSpecRead, error) {
	values, ok := req.Values.(map[string]any)
	if !ok || req.Type != "api" || req.Subtype != "openapi" {
		return nil, nil, nil
	}
	var (
		spec URLSpec
		read *URLSpecRead
	)
	if gatesTools(values["hil"]) {
		result, _ := probeOpenAPISpec(ctx, s.logger, values, false, true)
		spec = result.spec
		read = &URLSpecRead{Spec: result.spec, Warnings: result.warnings}
	}
	warnings, err := newConnectorGateWarnings(req, spec)
	return warnings, read, err
}

func (s *ConnectorService) ReadURLSpec(ctx context.Context, id string, req UpdateConnectorRequest) (URLSpec, error) {
	return s.readURLSpec(ctx, id, req, nil)
}

func (s *ConnectorService) readURLSpec(ctx context.Context, id string, req UpdateConnectorRequest, mcpManifest []map[string]any) (URLSpec, error) {
	existing, values, _, err := s.validatedUpdateValues(ctx, id, req, mcpManifest)
	if err != nil || values == nil {
		return URLSpec{}, err
	}
	merged, ok := values.(map[string]any)
	if !ok {
		return URLSpec{}, nil
	}
	return s.readAndCheckURLSpec(ctx, existing, req.Values, merged)
}

func (s *ConnectorService) readAndCheckURLSpec(ctx context.Context, existing *store.Connector, reqValues any, merged map[string]any) (URLSpec, error) {
	hasResource := existing.ResourceKey != nil && *existing.ResourceKey != ""
	if !isOpenAPIConnector(existing) || hasResource || !gatesTools(merged["hil"]) {
		return URLSpec{}, nil
	}
	result, _ := probeOpenAPISpec(ctx, s.logger, merged, false, true)
	return result.spec, refuseAuthoredGatesOutsideURLSpec(existing, reqValues, merged, result.spec)
}

func refuseAuthoredGatesOutsideURLSpec(existing *store.Connector, reqValues any, merged map[string]any, spec URLSpec) error {
	if !spec.Read || !isOpenAPIConnector(existing) {
		return nil
	}
	requested, _ := reqValues.(map[string]any)
	if !hilAuthored(requested["hil"], connectorValuesMap(existing)["hil"]) {
		return nil
	}
	return validateHILToolKeys(merged["hil"], spec.Tools)
}

func (s *ConnectorService) ValidateUpdate(ctx context.Context, id string, req UpdateConnectorRequest) ([]string, error) {
	warnings, _, err := s.ValidateUpdateReading(ctx, id, req)
	return warnings, err
}

type UpdateRead struct {
	Spec        URLSpec
	MCPManifest []map[string]any
	Warnings    []string
}

func (s *ConnectorService) ValidateUpdateReading(ctx context.Context, id string, req UpdateConnectorRequest) ([]string, UpdateRead, error) {
	refresh, err := s.readMCPManifest(ctx, id, req)
	if err != nil {
		return nil, UpdateRead{}, err
	}
	existing, values, prunedWarnings, err := s.validatedUpdateValues(ctx, id, req, refresh.manifest)
	if err != nil {
		return nil, UpdateRead{}, err
	}
	read := UpdateRead{MCPManifest: refresh.manifest, Warnings: refresh.warnings}
	merged, ok := values.(map[string]any)
	if !ok {
		return slices.Concat(refresh.warnings, prunedWarnings), read, nil
	}
	if specErr := s.validateAgainstStoredSpec(ctx, existing, merged); specErr != nil {
		return nil, UpdateRead{}, specErr
	}
	read.Spec, err = s.readAndCheckURLSpec(ctx, existing, req.Values, merged)
	if err != nil {
		return nil, UpdateRead{}, err
	}
	return slices.Concat(refresh.warnings, prunedWarnings, s.uncheckedAPIHILWarnings(ctx, existing, merged, read.Spec)), read, nil
}

func (s *ConnectorService) urlSpecForUpdate(ctx context.Context, id string, req UpdateConnectorRequest, known *URLSpec, mcpManifest []map[string]any) (URLSpec, error) {
	if known == nil {
		return s.readURLSpec(ctx, id, req, mcpManifest)
	}
	existing, values, _, err := s.validatedUpdateValues(ctx, id, req, mcpManifest)
	if err != nil || values == nil {
		return URLSpec{}, err
	}
	merged, ok := values.(map[string]any)
	if !ok {
		return URLSpec{}, nil
	}
	return *known, refuseAuthoredGatesOutsideURLSpec(existing, req.Values, merged, *known)
}

func (s *ConnectorService) validateAgainstStoredSpec(ctx context.Context, existing *store.Connector, values map[string]any) error {
	if !isOpenAPIConnector(existing) {
		return nil
	}
	if err := s.validateAllowListAgainstStoredSpec(ctx, existing, values); err != nil {
		return err
	}
	return s.validateAPIHILAgainstStoredSpec(ctx, existing, values)
}

func (s *ConnectorService) validatedUpdateValues(ctx context.Context, id string, req UpdateConnectorRequest, mcpManifest []map[string]any) (*store.Connector, any, []string, error) {
	if req.Values == nil {
		return nil, nil, nil, nil
	}
	existing, err := s.connectorRepo.GetByID(ctx, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf(errGetConnectorWrap, err)
	}
	if existing == nil {
		return nil, nil, nil, &NotFoundError{Resource: "connector", ID: id}
	}
	values, warnings, err := s.resolveUpdateValuesForProbe(existing, req.Values, mcpManifest)
	if err != nil {
		return nil, nil, nil, err
	}
	return existing, values, warnings, nil
}

func (s *ConnectorService) resolveUpdateValuesForProbe(existing *store.Connector, reqValues any, mcpManifest []map[string]any) (any, []string, error) {
	newValues, ok := reqValues.(map[string]any)
	if ok {
		if err := validateUpdateRequestValues(existing.Type, newValues); err != nil {
			return nil, nil, err
		}
	}
	if !ok || existing.Values == nil {
		if err := validateConnectorValues(existing.Type, derefStr(existing.Subtype), reqValues); err != nil {
			return nil, nil, err
		}
		return reqValues, nil, nil
	}
	var existingValues map[string]any
	if err := json.Unmarshal([]byte(*existing.Values), &existingValues); err != nil {
		if verr := validateConnectorValues(existing.Type, derefStr(existing.Subtype), newValues); verr != nil {
			return nil, nil, verr
		}
		return newValues, nil, nil
	}
	merged, warnings, err := inheritStoredValues(existing, maps.Clone(newValues), existingValues)
	if err != nil {
		return nil, nil, err
	}
	if len(mcpManifest) > 0 {
		merged["manifest"] = mcpManifest
	}
	if err := validateMergedConnectorValues(existing.Type, derefStr(existing.Subtype), merged); err != nil {
		return nil, nil, err
	}
	return merged, warnings, nil
}

func inheritStoredValues(existing *store.Connector, newValues, existingValues map[string]any) (map[string]any, []string, error) {
	connType, subtype := existing.Type, derefStr(existing.Subtype)
	if connType == webSearchConnectorType {
		if err := refuseWebSearchKeyMove(subtype, newValues, existingValues); err != nil {
			return nil, nil, err
		}
	}
	requestedHIL := newValues["hil"]
	merged, err := mergeSecretFields(newValues, existingValues, getSecretRegistry().ConnectorSecretFields(connType, subtype))
	if err != nil {
		return nil, nil, err
	}
	mergeHiddenFields(merged, existingValues, getHiddenRegistry().ConnectorHiddenFields(connType, subtype))
	if connType == "mcp" {
		keepStoredSSLConfig(merged, existingValues)
	}
	var warnings []string
	if !hilAuthored(requestedHIL, existingValues["hil"]) {
		warnings = removedGateWarnings(pruneUnprovidedRoleGates(connType, subtype, merged))
	}
	dropEmptyHIL(merged)
	return merged, warnings, nil
}

func storeMCPManifest(existing *store.Connector, manifest []map[string]any) error {
	stored := map[string]any{}
	if existing.Values != nil {
		if err := json.Unmarshal([]byte(*existing.Values), &stored); err != nil {
			return fmt.Errorf("read stored values: %w", err)
		}
	}
	stored["manifest"] = manifest
	raw, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf(errMarshalValuesWrap, err)
	}
	existing.Values = strPtr(string(raw))
	return nil
}

func (s *ConnectorService) updateImpl(ctx context.Context, id string, req UpdateConnectorRequest, userID string, tx *dbutil.Tx, opts ConnectorUpdateOptions) (*ConnectorResponse, error) {
	existing, err := s.loadConnectorForUpdate(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	err = applyNameDescriptionAndManifest(existing, req, opts)
	if err != nil {
		return nil, err
	}
	var warnings []string
	if req.Values != nil {
		warnings, err = s.applyValuesUpdate(ctx, existing, req.Values, opts)
		if err != nil {
			return nil, err
		}
	}
	existing.UpdatedBy = userID
	err = s.persistConnectorUpdate(ctx, tx, existing)
	if err != nil {
		return nil, err
	}
	resp := s.buildResponse(existing, s.agentCount(ctx, tx, id))
	resp.Warnings = warnings
	return resp, nil
}

func applyNameDescriptionAndManifest(existing *store.Connector, req UpdateConnectorRequest, opts ConnectorUpdateOptions) error {
	if err := ValidateConnectorRename(existing.Name, req.Name); err != nil {
		return err
	}
	applyNameAndDescription(existing, req, opts)
	if req.Values == nil && len(opts.MCPManifest) > 0 {
		return storeMCPManifest(existing, opts.MCPManifest)
	}
	return nil
}

func (s *ConnectorService) loadConnectorForUpdate(ctx context.Context, tx *dbutil.Tx, id string) (*store.Connector, error) {
	var (
		existing *store.Connector
		err      error
	)
	if tx != nil {
		existing, err = s.connectorRepo.GetByIDInTx(ctx, tx, id)
	} else {
		existing, err = s.connectorRepo.GetByID(ctx, id)
	}
	if err != nil {
		return nil, fmt.Errorf(errGetConnectorWrap, err)
	}
	if existing == nil {
		return nil, &NotFoundError{Resource: "connector", ID: id}
	}
	return existing, nil
}

func applyNameAndDescription(existing *store.Connector, req UpdateConnectorRequest, opts ConnectorUpdateOptions) {
	if req.Name != "" {
		existing.Name = req.Name
	}
	switch {
	case opts.ClearDescription:
		existing.Description = clearedDescriptionStoresEmptyNotNull()
	case req.Description != "":
		existing.Description = strPtr(req.Description)
	}
}

func (s *ConnectorService) applyValuesUpdate(ctx context.Context, existing *store.Connector, values any, opts ConnectorUpdateOptions) ([]string, error) {
	var warnings []string
	newValues, isMap := values.(map[string]any)
	if isMap {
		prepared, pruned, err := prepareUpdateValues(existing, newValues, opts.MCPManifest)
		if err != nil {
			return nil, err
		}
		newValues, values, warnings = prepared, prepared, pruned
	}
	if err := validateMergedConnectorValues(existing.Type, derefStr(existing.Subtype), values); err != nil {
		return nil, err
	}
	if isMap {
		unchecked, err := s.checkUpdatedValuesAgainstSpec(ctx, existing, newValues, opts.URLSpec)
		if err != nil {
			return nil, err
		}
		warnings = slices.Concat(warnings, unchecked)
	}
	b, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf(errMarshalValuesWrap, err)
	}
	existing.Values = strPtr(string(b))
	return warnings, nil
}

func prepareUpdateValues(existing *store.Connector, newValues map[string]any, manifest []map[string]any) (map[string]any, []string, error) {
	if err := validateUpdateRequestValues(existing.Type, newValues); err != nil {
		return nil, nil, err
	}
	var pruned []string
	if stored, ok := decodeStoredValues(existing); ok {
		merged, prunedWarnings, err := inheritStoredValues(existing, newValues, stored)
		if err != nil {
			return nil, nil, err
		}
		newValues, pruned = merged, prunedWarnings
	}
	if len(manifest) > 0 {
		newValues["manifest"] = manifest
	}
	dropEmptyHIL(newValues)
	return newValues, pruned, nil
}

func decodeStoredValues(existing *store.Connector) (map[string]any, bool) {
	if existing.Values == nil {
		return nil, false
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(*existing.Values), &stored); err != nil {
		return nil, false
	}
	return stored, true
}

func (s *ConnectorService) checkUpdatedValuesAgainstSpec(ctx context.Context, existing *store.Connector, newValues map[string]any, spec URLSpec) ([]string, error) {
	if err := s.validateAgainstStoredSpec(ctx, existing, newValues); err != nil {
		return nil, err
	}
	warnings := s.uncheckedAPIHILWarnings(ctx, existing, newValues, spec)
	if isOpenAPIConnector(existing) {
		restoreSpecDisplayPath(existing, newValues)
	}
	return warnings, nil
}

func (s *ConnectorService) persistConnectorUpdate(ctx context.Context, tx *dbutil.Tx, existing *store.Connector) error {
	var err error
	if tx != nil {
		err = s.connectorRepo.UpdateInTx(ctx, tx, existing)
	} else {
		err = s.connectorRepo.Update(ctx, existing)
	}
	if err == nil {
		return nil
	}
	if dbutil.IsUniqueViolation(err) {
		return &ConflictError{Message: fmt.Sprintf("a connector with name %q already exists", existing.Name)}
	}
	return fmt.Errorf("update connector: %w", err)
}

func (s *ConnectorService) agentCount(ctx context.Context, tx *dbutil.Tx, id string) int {
	var counts map[string]int
	if tx != nil {
		counts, _ = s.connectorRepo.GetAgentCountsInTx(ctx, tx, []string{id})
	} else {
		counts, _ = s.connectorRepo.GetAgentCounts(ctx, []string{id})
	}
	return counts[id]
}

func (s *ConnectorService) CollectAgentRegistrations(ctx context.Context, connectorID string) (regs []ConnectorAgentRegistration, incomplete bool) {
	if s.redeploy.agentRepo == nil || s.agentRegistry == nil {
		return nil, false
	}
	var out []ConnectorAgentRegistration
	page := 1
	const pageSize = 100
	for {
		agents, total, err := s.redeploy.agentRepo.ListByConnectorID(ctx, connectorID, page, pageSize)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("list agents for connector registration aggregation failed",
					"connectorId", connectorID, "error", err)
			}
			return out, true
		}
		if len(agents) == 0 {
			break
		}
		for _, a := range agents {
			card := s.agentRegistry.Get(AgentInstanceName(a.Agent))
			if card == nil {
				continue
			}
			for _, reg := range platpkg.AgentToolRegistrations(card) {
				if reg.ConnectorID != connectorID {
					continue
				}
				out = append(out, ConnectorAgentRegistration{
					Agent:     ConnectorAgentRef{ID: a.Agent.ID, Name: a.Agent.Name},
					Status:    reg.Status,
					Error:     reg.Error,
					ToolCount: reg.ToolCount,
				})
			}
		}
		if page*pageSize >= total {
			break
		}
		page++
	}
	return out, false
}

func (s *ConnectorService) Delete(ctx context.Context, id string) error {
	return s.deleteImpl(ctx, id, nil)
}

func (s *ConnectorService) DeleteInTx(ctx context.Context, tx *dbutil.Tx, id string) error {
	return s.deleteImpl(ctx, id, tx)
}

func (s *ConnectorService) deleteImpl(ctx context.Context, id string, tx *dbutil.Tx) error {
	var (
		conn *store.Connector
		err  error
	)
	if tx != nil {
		conn, err = s.connectorRepo.GetByIDInTx(ctx, tx, id)
	} else {
		conn, err = s.connectorRepo.GetByID(ctx, id)
	}
	if err != nil {
		return fmt.Errorf(errGetConnectorWrap, err)
	}
	if conn == nil {
		return &NotFoundError{Resource: "connector", ID: id}
	}

	var deleteErr error
	if tx != nil {
		deleteErr = s.connectorRepo.DeleteInTx(ctx, tx, id)
	} else {
		deleteErr = s.connectorRepo.Delete(ctx, id)
	}
	if deleteErr != nil {
		return fmt.Errorf("delete connector: %w", deleteErr)
	}

	if conn.ResourceKey != nil && *conn.ResourceKey != "" {
		if delErr := s.resourceStore.Delete(ctx, *conn.ResourceKey); delErr != nil {
			s.logger.Warn("failed to delete connector resource", "connectorID", id, "key", *conn.ResourceKey, "error", delErr)
		}
	}
	return nil
}

func (s *ConnectorService) UploadResource(ctx context.Context, id, filename string, r io.Reader) (*ConnectorResponse, error) {
	connector, err := s.connectorRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf(errGetConnectorWrap, err)
	}
	if connector == nil {
		return nil, &NotFoundError{Resource: "connector", ID: id}
	}

	data, err := io.ReadAll(io.LimitReader(r, maxConnectorResourceSize+1))
	if err != nil {
		return nil, fmt.Errorf("read resource: %w", err)
	}
	if len(data) > maxConnectorResourceSize {
		return nil, &ValidationError{Message: fmt.Sprintf("resource exceeds the %d-byte limit", maxConnectorResourceSize)}
	}
	sum := sha256.Sum256(data)
	contentHash := hex.EncodeToString(sum[:])

	key := connectorResourceKey(s.namespace, id, filename)
	if err = s.resourceStore.Put(ctx, key, bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("store resource: %w", err)
	}

	var prune func(map[string]any) []string
	if isOpenAPIConnector(connector) {
		prune = s.pruneGatesMissingFromSpec(ctx, connector, data)
	}
	removedGates, err := s.commitUploadedResource(ctx, connector, uploadedResource{key: key, hash: contentHash, prune: prune})
	if err != nil {
		return nil, err
	}
	var uncheckedWarnings []string
	if isOpenAPIConnector(connector) && prune == nil {
		uncheckedWarnings = uncheckedHILWarnings(connectorValuesMap(connector), unparseableSpecHILReason)
	}

	var updated *store.Connector
	updated, err = s.connectorRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get updated connector: %w", err)
	}

	if isOpenAPIConnector(updated) {
		var values map[string]any
		if updated.Values != nil {
			_ = json.Unmarshal([]byte(*updated.Values), &values)
		}
		if valErr := s.validateAllowListAgainstStoredSpec(ctx, updated, values); valErr != nil {
			return nil, valErr
		}
	}

	counts, _ := s.connectorRepo.GetAgentCounts(ctx, []string{id})
	resp := s.buildResponse(updated, counts[id])
	resp.Warnings = slices.Concat(removedGateWarnings(removedGates), uncheckedWarnings)
	return resp, nil
}

type uploadedResource struct {
	key, hash string
	prune     func(map[string]any) []string
}

func (s *ConnectorService) commitUploadedResource(ctx context.Context, conn *store.Connector, up uploadedResource) ([]string, error) {
	if s.redeploy.db == nil {
		return s.writeUploadedResource(ctx, nil, conn, up)
	}
	specChanged := derefStr(conn.ContentHash) != up.hash || derefStr(conn.ResourceKey) != up.key
	event := redeployEvent{
		eventType:   "connector.updated",
		connectorID: conn.ID,
		userID:      userIDFromContext(ctx),
		agentIDs:    findAgentsByConnector(ctx, s.redeploy.agentRepo, s.logger, conn.ID),
		workflowIDs: findWorkflowsByConnector(ctx, s.redeploy.workflowRepo, s.logger, conn.ID),
	}
	var removed []string
	err := s.redeploy.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var inErr error
		removed, inErr = s.writeUploadedResource(ctx, tx, conn, up)
		if inErr != nil || (!specChanged && len(removed) == 0) {
			return inErr
		}
		return writeRedeployEventInTx(ctx, s.redeploy.outboxRepo, tx, event)
	})
	return removed, err
}

func (s *ConnectorService) writeUploadedResource(ctx context.Context, tx *dbutil.Tx, conn *store.Connector, up uploadedResource) ([]string, error) {
	var err error
	if tx != nil {
		err = s.connectorRepo.UpdateResourceKeyInTx(ctx, tx, conn.ID, up.key, up.hash)
	} else {
		err = s.connectorRepo.UpdateResourceKey(ctx, conn.ID, up.key, up.hash)
	}
	if err != nil {
		return nil, fmt.Errorf("update resource key: %w", err)
	}
	if !isOpenAPIConnector(conn) {
		return nil, nil
	}
	return s.persistSpecURL(ctx, tx, conn, up.prune)
}

func connectorValuesMap(conn *store.Connector) map[string]any {
	values := map[string]any{}
	if conn.Values != nil {
		_ = json.Unmarshal([]byte(*conn.Values), &values)
	}
	return values
}

func (s *ConnectorService) DownloadResource(ctx context.Context, id string) (io.ReadCloser, string, error) {
	connector, err := s.connectorRepo.GetByID(ctx, id)
	if err != nil {
		return nil, "", fmt.Errorf(errGetConnectorWrap, err)
	}
	if connector == nil {
		return nil, "", &NotFoundError{Resource: "connector", ID: id}
	}
	if connector.ResourceKey == nil || *connector.ResourceKey == "" {
		return nil, "", &NotFoundError{Resource: "connector resource", ID: id}
	}

	rc, err := s.resourceStore.Get(ctx, *connector.ResourceKey)
	if err != nil {
		return nil, "", fmt.Errorf("get resource: %w", err)
	}
	return rc, *connector.ResourceKey, nil
}

func (s *ConnectorService) ExternalSpecificationURL(ctx context.Context, id string) (string, error) {
	connector, err := s.connectorRepo.GetByID(ctx, id)
	if err != nil {
		return "", fmt.Errorf(errGetConnectorWrap, err)
	}
	if connector == nil {
		return "", &NotFoundError{Resource: "connector", ID: id}
	}
	noURL := &NotFoundError{Resource: "connector specification URL", ID: id}
	hasResource := connector.ResourceKey != nil && *connector.ResourceKey != ""
	if !isOpenAPIConnector(connector) || hasResource || connector.Values == nil {
		return "", noURL
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(*connector.Values), &values); err != nil {
		return "", fmt.Errorf("parse connector values: %w", err)
	}
	specURL, _ := values["specification_url"].(string)
	if !isExternalSpecURL(specURL) {
		return "", noURL
	}
	return specURL, nil
}

func connectorResourceKey(namespace, connectorID, filename string) string {
	return fmt.Sprintf("%s/connectors/%s/%s", namespace, connectorID, filename)
}

func isOpenAPIConnector(c *store.Connector) bool {
	return c.Type == "api" && (c.Subtype != nil && *c.Subtype == "openapi")
}

func restoreSpecDisplayPath(existing *store.Connector, values map[string]any) {
	if existing.ResourceKey == nil || *existing.ResourceKey == "" {
		return
	}
	if u, _ := values["specification_url"].(string); u == "" {
		values["specification_url"] = SpecResourcePath(existing.ID)
	}
}

func SpecResourcePath(connectorID string) string {
	return platpkg.APIV2Prefix + "/connectors/" + connectorID + specResourceSuffix
}

var specDisplayPathPrefixes = [...]string{"/api/v1/platform/connectors/", "/api/v2/platform/connectors/"}

const specResourceSuffix = "/resource"

func (s *ConnectorService) validateAllowListAgainstStoredSpec(ctx context.Context, conn *store.Connector, values map[string]any) error {
	allowList, ok := values["allow_list"].(string)
	if !ok || allowList == "" {
		return nil
	}
	operations := parseOperationList(allowList)
	if len(operations) == 0 {
		return nil
	}
	body, ok := s.storedSpec(ctx, conn)
	if !ok {
		return nil
	}
	spec := parseOpenAPISpec(body)
	if spec == nil {
		s.logger.Warn("skipping spec-dependent allow-list validation: spec parse failed",
			"connectorId", conn.ID)
		return nil
	}
	return validateAllowListAgainstSpec(operations, spec)
}

func (s *ConnectorService) validateAPIHILAgainstStoredSpec(ctx context.Context, conn *store.Connector, values map[string]any) error {
	if !gatesTools(values["hil"]) {
		return nil
	}
	body, ok := s.storedSpec(ctx, conn)
	if !ok {
		return nil
	}
	everySpecOperation, ok := s.specToolNames(ctx, conn.ID, body)
	if !ok {
		return nil
	}
	return validateHILToolKeys(values["hil"], everySpecOperation)
}

func (s *ConnectorService) uncheckedAPIHILWarnings(ctx context.Context, conn *store.Connector, values map[string]any, spec URLSpec) []string {
	if !isOpenAPIConnector(conn) || !gatesTools(values["hil"]) {
		return nil
	}
	if conn.ResourceKey == nil || *conn.ResourceKey == "" {
		if spec.Read {
			return missingFromURLSpecWarnings(values, spec)
		}
		return uncheckedHILWarnings(values, uncheckedAPIHILReason)
	}
	body, ok := s.storedSpec(ctx, conn)
	if !ok {
		return uncheckedHILWarnings(values, unreadableSpecHILReason)
	}
	if _, ok := s.specToolNames(ctx, conn.ID, body); !ok {
		return uncheckedHILWarnings(values, unparseableSpecHILReason)
	}
	return nil
}

func (s *ConnectorService) pruneGatesMissingFromSpec(ctx context.Context, conn *store.Connector, spec []byte) func(map[string]any) []string {
	known, ok := s.specToolNames(ctx, conn.ID, spec)
	if !ok {
		return nil
	}
	return func(values map[string]any) []string { return pruneHILToolKeys(values, known) }
}

func (s *ConnectorService) specToolNames(ctx context.Context, connectorID string, spec []byte) ([]string, bool) {
	known, err := openapi.EnumerateToolNames(ctx, spec, nil, nil)
	if err != nil {
		s.logger.Warn("skipping spec-dependent hil validation: spec parse failed", "connectorId", connectorID)
		return nil, false
	}
	return known, true
}

func gatesTools(rawHIL any) bool {
	parsed, _ := parseConnectorHIL(rawHIL)
	_, gated := parsed["tools"]
	return gated
}

func (s *ConnectorService) storedSpec(ctx context.Context, conn *store.Connector) ([]byte, bool) {
	if conn.ResourceKey == nil || *conn.ResourceKey == "" {
		return nil, false
	}
	rc, err := s.resourceStore.Get(ctx, *conn.ResourceKey)
	if err != nil {
		s.logger.Warn("skipping spec-dependent validation: resource read failed",
			"connectorId", conn.ID, "error", err)
		return nil, false
	}
	defer func() { _ = rc.Close() }()

	body, err := io.ReadAll(io.LimitReader(rc, 10<<20))
	if err != nil {
		s.logger.Warn("skipping spec-dependent validation: spec read failed",
			"connectorId", conn.ID, "error", err)
		return nil, false
	}
	return body, true
}

func (s *ConnectorService) persistSpecURL(ctx context.Context, tx *dbutil.Tx, conn *store.Connector, prune func(map[string]any) []string) ([]string, error) {
	values := map[string]any{}
	if conn.Values != nil && *conn.Values != "" {
		if err := json.Unmarshal([]byte(*conn.Values), &values); err != nil {
			return nil, fmt.Errorf("unmarshal connector values: %w", err)
		}
	}
	values["specification_url"] = SpecResourcePath(conn.ID)
	var removed []string
	if prune != nil {
		removed = prune(values)
	}

	b, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal connector values: %w", err)
	}
	if tx != nil {
		err = s.connectorRepo.UpdateValuesInTx(ctx, tx, conn.ID, string(b))
	} else {
		err = s.connectorRepo.UpdateValues(ctx, conn.ID, string(b))
	}
	if err != nil {
		return nil, fmt.Errorf("update specification_url: %w", err)
	}
	return removed, nil
}

func (s *ConnectorService) enrichMCPManifestIfNeeded(ctx context.Context, connType string, raw any) (map[string]any, bool, []string) {
	if s.mcpSvc == nil || connType != "mcp" {
		return nil, false, nil
	}
	values, ok := raw.(map[string]any)
	if !ok || values == nil {
		return nil, false, nil
	}
	if hasNonEmptyManifest(values["manifest"]) {
		return nil, false, unreadableTLSFileWarnings(values)
	}
	manifest, outcome, failure := s.discoverMCPManifest(ctx, s.logger, values)
	if outcome == mcpDiscoverySkipped {
		return nil, false, unreadableTLSFileWarnings(values)
	}
	if outcome != mcpDiscoveryListed {
		return nil, false, discoveryFailureWarnings(failure)
	}
	values["manifest"] = manifest
	return values, true, nil
}

type mcpDiscoveryOutcome int

const (
	mcpDiscoverySkipped mcpDiscoveryOutcome = iota
	mcpDiscoveryUnread
	mcpDiscoveryListed
)

func (s *ConnectorService) discoverMCPManifest(ctx context.Context, logger *slog.Logger, values map[string]any) ([]map[string]any, mcpDiscoveryOutcome, error) {
	if s.mcpSvc == nil {
		return nil, mcpDiscoverySkipped, nil
	}
	req, ok := mcpDiscoverRequestFromValues(values)
	if !ok {
		return nil, mcpDiscoverySkipped, nil
	}

	dctx, cancel := context.WithTimeout(ctx, mcpDiscoverAtSaveTimeout)
	defer cancel()

	tools, err := s.mcpSvc.DiscoverTools(dctx, req)
	if err != nil {
		logger.Warn("MCP discovery at save failed; manifest not written", "error", withLoggableCause(err))
		return nil, mcpDiscoveryUnread, err
	}
	if len(tools) == 0 {
		logger.Debug("MCP tool list read at save is empty", "serverURL", logging.RedactURLQueries(req.ServerURL))
		return nil, mcpDiscoveryUnread, nil
	}

	manifest := buildMCPManifestEntries(tools)
	logger.Debug("MCP tool list read at save", "serverURL", logging.RedactURLQueries(req.ServerURL), "tools", len(manifest))
	return manifest, mcpDiscoveryListed, nil
}

const (
	warningSavedWithoutTools       = "Saved without tools: "
	warningToolsUnlistedNoSentence = "the platform could not list the MCP server's tools."
	warningTLSFilesUnreadable      = "The platform could not read the connector's TLS files, and its agents read the same paths: "
)

func unreadableTLSFileWarnings(values map[string]any) []string {
	ssl := mcpSSLConfigFromValues(values)
	if ssl == nil || (ssl.CABundle == "" && ssl.ClientCert == "" && ssl.ClientKey == "") {
		return nil
	}
	if _, err := buildMCPDiscoveryTLSConfig(ssl); err != nil {
		return []string{warningTLSFilesUnreadable + err.Error()}
	}
	return nil
}

func discoveryFailureWarnings(failure error) []string {
	if failure == nil {
		return nil
	}
	return []string{savedWithoutToolsWarning(failure)}
}

func savedWithoutToolsWarning(err error) string {
	return warningSavedWithoutTools + discoveryFailureReason(err)
}

func discoveryFailureReason(err error) string {
	if failure, ok := errors.AsType[*ExternalServiceError](err); ok && failure.Message != "" {
		return failure.Message
	}
	if invalid, ok := errors.AsType[*ValidationError](err); ok && invalid.Message != "" {
		return invalid.Message
	}
	return warningToolsUnlistedNoSentence
}

func buildMCPManifestEntries(tools []MCPDiscoveredTool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		entry := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		}
		if t.OutputSchema != nil {
			entry["outputSchema"] = t.OutputSchema
		}
		if hints := manifestEffectHints(t.Annotations); hints != nil {
			entry["annotations"] = hints
		}
		out = append(out, entry)
	}
	return out
}

func manifestEffectHints(a *MCPToolAnnotations) map[string]any {
	if a == nil {
		return nil
	}
	hints := make(map[string]any, 3)
	if a.ReadOnlyHint != nil {
		hints["readOnlyHint"] = *a.ReadOnlyHint
	}
	if a.DestructiveHint != nil {
		hints["destructiveHint"] = *a.DestructiveHint
	}
	if a.OpenWorldHint != nil {
		hints["openWorldHint"] = *a.OpenWorldHint
	}
	if len(hints) == 0 {
		return nil
	}
	return hints
}

func hasNonEmptyManifest(v any) bool {
	switch m := v.(type) {
	case []any:
		return len(m) > 0
	case []map[string]any:
		return len(m) > 0
	case string:
		return strings.TrimSpace(m) != "" && strings.TrimSpace(m) != "[]"
	}
	return false
}

func mcpSSLConfigFromValues(values map[string]any) *MCPSSLConfig {
	raw, ok := values["ssl_config"].(map[string]any)
	if !ok {
		return nil
	}
	ssl := &MCPSSLConfig{}
	ssl.CABundle, _ = raw["ca_bundle"].(string)
	ssl.ClientCert, _ = raw["client_cert"].(string)
	ssl.ClientKey, _ = raw["client_key"].(string)
	ssl.ServerName, _ = raw["server_name"].(string)
	return ssl
}

func mcpDiscoverRequestFromValues(values map[string]any) (MCPDiscoverRequest, bool) {
	serverURL, _ := values["server_url"].(string)
	if serverURL == "" {
		return MCPDiscoverRequest{}, false
	}
	connType, _ := values["connection_type"].(string)
	if connType == "" {
		connType = "streamable-http"
	}

	req := MCPDiscoverRequest{
		ServerURL:      serverURL,
		ConnectionType: connType,
		SSLConfig:      mcpSSLConfigFromValues(values),
	}

	if headers, ok := values["custom_headers"].(map[string]any); ok && len(headers) > 0 {
		req.CustomHeaders = make(map[string]string, len(headers))
		for k, v := range headers {
			if s, ok := v.(string); ok {
				req.CustomHeaders[k] = s
			}
		}
	}

	authType, _ := values["auth_type"].(string)
	switch authType {
	case "", "none":
		req.Auth = &MCPAuthInfo{Type: "none"}
	case "http":
		scheme, _ := values["auth_http_scheme"].(string)
		http := &MCPHTTPAuth{Scheme: scheme}
		switch scheme {
		case "bearer":
			http.Token, _ = values["auth_http_bearer_token"].(string)
		case "basic":
			http.Username, _ = values["auth_http_basic_username"].(string)
			http.Password, _ = values["auth_http_basic_password"].(string)
		}
		req.Auth = &MCPAuthInfo{Type: "http", HTTP: http}
	case "apikey":
		in, _ := values["auth_apikey_location"].(string)
		name, _ := values["auth_apikey_name"].(string)
		val, _ := values["auth_apikey_value"].(string)
		req.Auth = &MCPAuthInfo{Type: "apikey", APIKey: &MCPAPIKeyAuth{In: in, Name: name, Value: val}}
	case "oauth":
		return MCPDiscoverRequest{}, false
	default:
		return MCPDiscoverRequest{}, false
	}

	return req, true
}

func (s *ConnectorService) buildResponse(c *store.Connector, agentCount int) *ConnectorResponse {
	resp := &ConnectorResponse{
		ID:          c.ID,
		Name:        c.Name,
		Description: derefStr(c.Description),
		Type:        c.Type,
		Subtype:     derefStr(c.Subtype),
		AgentCount:  agentCount,
		HasResource: c.ResourceKey != nil && *c.ResourceKey != "",
		ContentHash: derefStr(c.ContentHash),
		CreatedBy:   c.CreatedBy,
		UpdatedBy:   c.UpdatedBy,
		CreatedTime: Timestamp(c.CreatedTime),
		UpdatedTime: Timestamp(c.UpdatedTime),
	}

	if c.Values != nil {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(*c.Values), &parsed); err == nil {
			secretFields := getSecretRegistry().ConnectorSecretFields(c.Type, derefStr(c.Subtype))
			resp.Values = redactSecretFields(parsed, secretFields)
		}
	}

	return resp
}
