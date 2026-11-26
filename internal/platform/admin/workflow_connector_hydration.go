// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func resolveWorkflowConnectorBindings(ctx context.Context, appConfig map[string]any, existing []store.WorkflowConnectorBinding, repo *store.ConnectorRepo) ([]store.WorkflowConnectorBinding, error) {
	nodes, err := validateWorkflowConnectorShape(appConfig)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	if repo == nil {
		return nil, &ValidationError{Message: "connector references require the platform connector store, which is not configured"}
	}

	existingByRef := make(map[string]store.WorkflowConnectorBinding, len(existing))
	for _, b := range existing {
		existingByRef[b.RefName] = b
	}

	byRef := map[string]store.WorkflowConnectorBinding{}
	usedConnector := map[string]string{}
	var refs []string
	for _, node := range nodes {
		name, _ := node["connector"].(string)
		if _, done := byRef[name]; done {
			continue
		}
		binding, ok := existingByRef[name]
		if !ok {
			conn, lookupErr := lookupConnectorByName(ctx, repo, name)
			if lookupErr != nil {
				return nil, &ValidationError{Message: lookupErr.Error()}
			}
			binding = store.WorkflowConnectorBinding{ConnectorID: conn.ID, RefName: name}
		}
		if prior, dup := usedConnector[binding.ConnectorID]; dup {
			return nil, &ValidationError{Message: fmt.Sprintf("connector refs %q and %q resolve to the same connector; use a single name", prior, name)}
		}
		usedConnector[binding.ConnectorID] = name
		byRef[name] = binding
		refs = append(refs, name)
	}

	if err := validateConnectorToolNodeSelectors(ctx, nodes, byRef, repo); err != nil {
		return nil, err
	}

	slices.Sort(refs)
	bindings := make([]store.WorkflowConnectorBinding, 0, len(refs))
	for _, name := range refs {
		bindings = append(bindings, byRef[name])
	}
	return bindings, nil
}

func validateWorkflowConnectorShape(appConfig map[string]any) ([]map[string]any, error) {
	body := workflowBody(appConfig)
	if _, present := body["connector_refs"]; present {
		return nil, &ValidationError{Message: "connector_refs is no longer supported; tool nodes reference connectors directly via their 'connector' field"}
	}
	nodes, err := connectorToolNodes(body)
	if err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}
	if err := validateToolSelectorShape(body); err != nil {
		return nil, err
	}
	if err := schema.WorkflowPlatformConnectorEntryError(appConfig); err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}
	return nodes, nil
}

func validateConnectorToolNodeSelectors(ctx context.Context, nodes []map[string]any, byRef map[string]store.WorkflowConnectorBinding, repo *store.ConnectorRepo) error {
	rows := map[string]*store.Connector{}
	entriesByRef := map[string][]map[string]any{}
	valuesByRef := map[string]map[string]any{}

	for _, node := range nodes {
		name, _ := node["connector"].(string)
		binding, bound := byRef[name]
		if !bound {
			continue
		}
		if _, loaded := rows[name]; loaded {
			continue
		}
		conn, err := repo.GetByID(ctx, binding.ConnectorID)
		if err != nil {
			return &ValidationError{Message: fmt.Sprintf("load connector for ref %q: %v", name, err)}
		}
		if conn == nil {
			return &ValidationError{Message: fmt.Sprintf("connector for ref %q no longer exists", name)}
		}
		cc, convErr := connectorConfigFromRow(conn)
		if convErr != nil {
			return &ValidationError{Message: convErr.Error()}
		}
		rows[name] = conn
		entriesByRef[name] = buildConnectorTools(cc)
		valuesByRef[name] = cc.Values
	}

	mcpB := newMCPBindings(rows, valuesByRef, entriesByRef)
	for _, node := range nodes {
		name, _ := node["connector"].(string)
		conn, bound := rows[name]
		if !bound {
			continue
		}

		if conn.Type == "api" {
			continue
		}

		id, _ := node["id"].(string)
		sel, _ := node["tool"].(string)
		if err := refuseAgentOnlyConnector(conn); err != nil {
			return nodeValidationError(id, err)
		}
		if conn.Type == "mcp" {
			if _, err := mcpB.bind(conn, name, id, sel); err != nil {
				return nodeValidationError(id, err)
			}
			continue
		}
		if _, err := selectConnectorToolName(conn, entriesByRef[name], sel); err != nil {
			return nodeValidationError(id, err)
		}
	}
	if err := mcpB.clashes(); err != nil {
		return &ValidationError{Message: err.Error()}
	}
	return nil
}

func hydrateWorkflowConnectors(ctx context.Context, appConfig map[string]any, bindings []store.WorkflowConnectorBinding, repo *store.ConnectorRepo, specs ResourceStore, logger *slog.Logger) error {
	wfBody, _ := appConfig["workflow"].(map[string]any)
	if wfBody == nil {
		return nil
	}
	if _, present := wfBody["connector_refs"]; present {
		return fmt.Errorf("connector_refs is no longer supported; re-save the workflow (tool nodes reference connectors directly via their 'connector' field)")
	}

	dropRestatedConnectorEntriesBeforeNodeRewrite(appConfig, logger)

	nodes, err := connectorToolNodes(wfBody)
	if err != nil {
		return err
	}
	if len(nodes) == 0 && len(bindings) == 0 {
		return nil
	}
	if repo == nil {
		return fmt.Errorf("connector references require the platform connector store, which is not configured")
	}

	existing, err := existingToolNames(appConfig)
	if err != nil {
		return err
	}

	var emitted []any
	entriesByRef := make(map[string][]map[string]any, len(bindings))
	rowsByRef := make(map[string]*store.Connector, len(bindings))
	valuesByRef := make(map[string]map[string]any, len(bindings))
	for _, binding := range bindings {
		conn, getErr := repo.GetByID(ctx, binding.ConnectorID)
		if getErr != nil {
			return fmt.Errorf("load connector for ref %q: %w", binding.RefName, getErr)
		}
		if conn == nil {
			return fmt.Errorf("connector for ref %q no longer exists", binding.RefName)
		}
		cc, convErr := connectorConfigFromRow(conn)
		if convErr != nil {
			return convErr
		}
		entries := buildConnectorTools(cc)
		if len(entries) == 0 {
			return fmt.Errorf("connector %q has unsupported type %q", binding.RefName, conn.Type)
		}
		for _, entry := range entries {
			if toolName, _ := entry["tool_name"].(string); toolName != "" && entry["tool_type"] != "mcp" {
				if src, dup := existing[toolName]; dup {
					return fmt.Errorf("connector %q emits tool %q which collides with %s", binding.RefName, toolName, src)
				}
				existing[toolName] = fmt.Sprintf("a tool emitted by connector %q", binding.RefName)
			}
			emitted = append(emitted, entry)
		}
		entriesByRef[binding.RefName] = entries
		rowsByRef[binding.RefName] = conn
		valuesByRef[binding.RefName] = cc.Values
	}

	apiTools := newAPIToolSelector(specSource{store: specs, logger: logger})
	mcpB := newMCPBindings(rowsByRef, valuesByRef, entriesByRef)
	for _, node := range nodes {
		name, _ := node["connector"].(string)
		id, _ := node["id"].(string)
		conn, bound := rowsByRef[name]
		if !bound {
			return fmt.Errorf("node %q: connector %q is not bound to this workflow (it may have been deleted); re-save the workflow", id, name)
		}
		sel, _ := node["tool"].(string)
		if err := refuseAgentOnlyConnector(conn); err != nil {
			return fmt.Errorf("node %q: %w", id, err)
		}
		var toolName string
		var selErr error
		switch conn.Type {
		case "mcp":
			toolName, selErr = mcpB.bind(conn, name, id, sel)
		case "api":
			toolName, selErr = apiTools.selectTool(ctx, name, conn, sel)
		default:
			toolName, selErr = selectConnectorToolName(conn, entriesByRef[name], sel)
		}
		if selErr != nil {
			return fmt.Errorf("node %q: %w", id, selErr)
		}
		node["tool_name"] = toolName
		delete(node, "connector")
		delete(node, "tool")
	}

	if err := mcpB.clashes(); err != nil {
		return err
	}

	if len(emitted) > 0 {
		tools, _ := appConfig["tools"].([]any)
		appConfig["tools"] = append(tools, emitted...)
	}
	return nil
}

func dropRestatedConnectorEntriesBeforeNodeRewrite(appConfig map[string]any, logger *slog.Logger) {
	if dropped := schema.DropWorkflowPlatformConnectorEntries(appConfig); len(dropped) > 0 && logger != nil {
		logger.Warn("dropped workflow-level connectors entries restating bound platform connectors",
			"connectors", dropped)
	}
}

func workflowLogger(logger *slog.Logger, workflowID string) *slog.Logger {
	if logger == nil {
		return nil
	}
	return logger.With("workflowID", workflowID)
}

func workflowBody(appConfig map[string]any) map[string]any {
	if body, ok := appConfig["workflow"].(map[string]any); ok {
		return body
	}
	return appConfig
}

func validateToolSelectorShape(body map[string]any) error {
	rawNodes, _ := body["nodes"].([]any)
	for i, raw := range rawNodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasTool := node["tool"]; hasTool {
			if _, hasConnector := node["connector"]; !hasConnector {
				id, _ := node["id"].(string)
				if id == "" {
					id = fmt.Sprintf("#%d", i)
				}
				return &ValidationError{Message: fmt.Sprintf("node %q: 'tool' selects a connector tool and requires 'connector'; use 'tool_name' to invoke a tool declared in tools:", id)}
			}
		}
	}
	return nil
}

func connectorToolNodes(wfBody map[string]any) ([]map[string]any, error) {
	rawNodes, _ := wfBody["nodes"].([]any)
	var out []map[string]any
	for i, raw := range rawNodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rawConn, present := node["connector"]
		if !present {
			continue
		}
		id, _ := node["id"].(string)
		if id == "" {
			id = fmt.Sprintf("#%d", i)
		}
		name, ok := rawConn.(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("node %q: connector must be a non-empty connector name", id)
		}
		if nodeType, _ := node["type"].(string); nodeType != "tool" {
			return nil, fmt.Errorf("node %q: connector is only valid on tool nodes, got type %q", id, nodeType)
		}
		out = append(out, node)
	}
	return out, nil
}

func existingToolNames(appConfig map[string]any) (map[string]string, error) {
	out := map[string]string{}
	rawTools, present := appConfig["tools"]
	if !present {
		return out, nil
	}
	list, ok := rawTools.([]any)
	if !ok {
		return nil, fmt.Errorf("tools must be a list, got %T", rawTools)
	}
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := entry["tool_name"].(string); name != "" {
			out[name] = fmt.Sprintf("the existing tools[%d] entry", i)
		}
	}
	return out, nil
}

func lookupConnectorByName(ctx context.Context, repo *store.ConnectorRepo, name string) (*store.Connector, error) {
	conns, err := repo.GetAllByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("look up connector %q: %w", name, err)
	}
	switch len(conns) {
	case 0:
		return nil, fmt.Errorf("connector %q not found", name)
	case 1:
		return conns[0], nil
	default:
		return nil, fmt.Errorf("connector name %q is ambiguous (%d connectors share it); rename so the reference is unique", name, len(conns))
	}
}

func connectorConfigFromRow(conn *store.Connector) (ConnectorConfig, error) {
	cc := ConnectorConfig{ID: conn.ID, Name: conn.Name, Type: conn.Type}
	if conn.Subtype != nil {
		cc.Subtype = *conn.Subtype
	}
	if conn.ResourceKey != nil {
		cc.ResourceKey = *conn.ResourceKey
	}
	if conn.Values != nil && *conn.Values != "" {
		if err := json.Unmarshal([]byte(*conn.Values), &cc.Values); err != nil {
			return cc, fmt.Errorf("connector %q: malformed values JSON: %w", conn.Name, err)
		}
	}
	return cc, nil
}

const connectorSpecMaxBytes = 10 << 20

const specURLOnlyRefusalFormat = "connector %q (type api) fetches its spec from specification_url at runtime, so its tool names cannot be enumerated at deploy time; set allow_list on the connector%s"

const vendorSpecRemedy = " or vendor the spec via specification_file"

const specNotEnumerableFormat = "connector %q stored spec is not valid OpenAPI or references external documents, so its tool names cannot be enumerated at deploy time; upload a valid spec that does not reference external documents, or set allow_list"

const specTooLargeFormat = "connector %q stored spec is larger than the %d MB the platform reads, so its tool names cannot be enumerated at deploy time; upload a smaller spec or set allow_list"

type specTooLargeError struct{ *ValidationError }

func (e *specTooLargeError) Unwrap() error { return e.ValidationError }

func apiConnectorToolNames(ctx context.Context, conn *store.Connector, src specSource, remedy string) ([]string, error) {
	cc, err := connectorConfigFromRow(conn)
	if err != nil {
		return nil, err
	}
	if names, listed := apiAllowListToolNames(cc.Values); listed {
		return names, nil
	}
	if cc.ResourceKey != "" {
		spec := src.load(ctx, conn, cc.ResourceKey)
		if spec.readErr != nil {
			return nil, spec.readErr
		}
		names, enumErr := spec.toolNames(ctx)
		if enumErr != nil {
			src.logUnenumerable(conn)
			return nil, &ValidationError{Message: fmt.Sprintf(specNotEnumerableFormat, conn.Name)}
		}
		if len(names) == 0 {
			return nil, &ValidationError{Message: fmt.Sprintf("connector %q stored spec declares no operations", conn.Name)}
		}
		return names, nil
	}
	return nil, &ValidationError{Message: fmt.Sprintf(specURLOnlyRefusalFormat, conn.Name, remedy)}
}

func apiAllowListToolNames(values map[string]any) ([]string, bool) {
	allowList, _ := values["allow_list"].(string)
	if allowList == "" {
		return nil, false
	}
	ops := parseOperationList(allowList)
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		names = append(names, openapi.OperationToolName(op))
	}
	slices.Sort(names)
	return slices.Compact(names), true
}

func readStoredSpec(ctx context.Context, conn *store.Connector, resourceKey string, specs ResourceStore) ([]byte, error) {
	if specs == nil {
		return nil, fmt.Errorf("connector %q has a stored spec but the platform resource store is not configured", conn.Name)
	}
	rc, getErr := specs.Get(ctx, resourceKey)
	if getErr != nil {
		return nil, fmt.Errorf("read stored spec for connector %q: %w", conn.Name, getErr)
	}
	defer func() { _ = rc.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(rc, connectorSpecMaxBytes+1))
	if readErr != nil {
		return nil, fmt.Errorf("read stored spec for connector %q: %w", conn.Name, readErr)
	}
	if len(data) > connectorSpecMaxBytes {
		return nil, &specTooLargeError{&ValidationError{Message: fmt.Sprintf(specTooLargeFormat, conn.Name, connectorSpecMaxBytes>>20)}}
	}
	return data, nil
}

type specSource struct {
	store  ResourceStore
	cache  specCache
	logger *slog.Logger
}

type specCache map[string]*storedSpec

type storedSpec struct {
	data    []byte
	readErr error

	names     []string
	namesErr  error
	namesDone bool

	operations     []openapi.OperationSignature
	operationsErr  error
	operationsDone bool
}

func (src specSource) load(ctx context.Context, conn *store.Connector, resourceKey string) *storedSpec {
	if spec, cached := src.cache[resourceKey]; cached {
		return spec
	}
	spec := &storedSpec{}
	spec.data, spec.readErr = readStoredSpec(ctx, conn, resourceKey, src.store)
	if src.cache != nil {
		src.cache[resourceKey] = spec
	}
	return spec
}

func (s *storedSpec) toolNames(ctx context.Context) ([]string, error) {
	if !s.namesDone {
		s.names, s.namesErr = openapi.EnumerateToolNames(ctx, s.data, nil, nil)
		s.namesDone = true
	}
	return s.names, s.namesErr
}

func (s *storedSpec) operationSignatures(ctx context.Context) ([]openapi.OperationSignature, error) {
	if !s.operationsDone {
		s.operations, s.operationsErr = openapi.EnumerateOperations(ctx, s.data, nil, nil)
		s.operationsDone = true
	}
	return s.operations, s.operationsErr
}

func (src specSource) logUnenumerable(conn *store.Connector) {
	if src.logger == nil {
		return
	}
	src.logger.Debug("a connector's stored spec could not be enumerated for its tool names",
		"connectorID", conn.ID, "failureClass", failureUnparseable)
}

type apiToolSelector struct {
	src    specSource
	remedy string
	listed map[string]apiToolListing
}

type apiToolListing struct {
	names []string
	err   error
}

func newAPIToolSelector(src specSource) *apiToolSelector {
	return &apiToolSelector{src: src, remedy: vendorSpecRemedy, listed: map[string]apiToolListing{}}
}

func (a *apiToolSelector) withoutSpecUpload() *apiToolSelector {
	a.remedy = ""
	return a
}

type apiToolListError struct{ err error }

func (e *apiToolListError) Error() string { return e.err.Error() }
func (e *apiToolListError) Unwrap() error { return e.err }

func (a *apiToolSelector) selectTool(ctx context.Context, ref string, conn *store.Connector, sel string) (string, error) {
	listing, done := a.listed[ref]
	if !done {
		listing.names, listing.err = apiConnectorToolNames(ctx, conn, a.src, a.remedy)
		a.listed[ref] = listing
	}
	if listing.err != nil {
		return "", &apiToolListError{err: listing.err}
	}
	return selectAPIConnectorToolName(conn, listing.names, sel)
}

func selectAPIConnectorToolName(conn *store.Connector, names []string, sel string) (string, error) {
	if sel == "" {
		if len(names) == 1 {
			return names[0], nil
		}
		return "", fmt.Errorf("connector %q emits %d tools; set 'tool' to one of: %s", conn.Name, len(names), strings.Join(names, ", "))
	}
	want := openapi.OperationToolName(sel)
	for _, name := range names {
		if name == sel || name == want {
			return name, nil
		}
	}
	return "", fmt.Errorf("connector %q emits no tool named %q; available: %s", conn.Name, sel, strings.Join(names, ", "))
}

func connectorIsAgentOnly(connType string) bool {
	return connType == webSearchConnectorType
}

func refuseAgentOnlyConnector(conn *store.Connector) error {
	if !connectorIsAgentOnly(conn.Type) {
		return nil
	}
	return fmt.Errorf("connector %q is a Web Search connector, which is %s", conn.Name, reasonAgentNodeOnly)
}

func nodeValidationError(nodeID string, err error) *ValidationError {
	return &ValidationError{Message: fmt.Sprintf("node %q: %v", nodeID, err)}
}

func selectConnectorToolName(conn *store.Connector, entries []map[string]any, sel string) (string, error) {
	suffix := toolNameSuffix(conn.Type, conn.ID)
	var names []string
	for _, entry := range entries {
		if name, _ := entry["tool_name"].(string); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("connector %q (type %s) emits no tool this build can bind to a workflow tool node", conn.Name, conn.Type)
	}
	if sel == "" {
		if len(names) == 1 {
			return names[0], nil
		}
		return "", fmt.Errorf("connector %q emits %d tools; set 'tool' to one of: %s", conn.Name, len(names), strings.Join(unsuffixedToolNames(names, suffix), ", "))
	}
	for _, name := range names {
		if name == sel || name == sel+"_"+suffix {
			return name, nil
		}
	}
	return "", fmt.Errorf("connector %q emits no tool named %q; available: %s", conn.Name, sel, strings.Join(unsuffixedToolNames(names, suffix), ", "))
}

func unsuffixedToolNames(names []string, suffix string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = strings.TrimSuffix(name, "_"+suffix)
	}
	return out
}
