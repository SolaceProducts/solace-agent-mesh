// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

type (
	ConnectorToolInputProblem = tools.ConnectorToolInputProblem
	UncheckedConnector        = tools.UncheckedConnector
	ConnectorToolInputReport  = tools.ConnectorToolInputReport
)

func newConnectorToolInputReport() *ConnectorToolInputReport {
	return &ConnectorToolInputReport{Problems: []ConnectorToolInputProblem{}, Unchecked: []UncheckedConnector{}}
}

func addUnchecked(r *ConnectorToolInputReport, connector, reason string) {
	entry := UncheckedConnector{Connector: connector, Reason: reason}
	if !slices.Contains(r.Unchecked, entry) {
		r.Unchecked = append(r.Unchecked, entry)
	}
}

const (
	reasonConnectorNotResolved  = "it could not be resolved to one connector"
	reasonCheckInterrupted      = "the check was interrupted before it could read it"
	reasonNoSpecOrNames         = "it has no allow_list and no uploaded spec, so its tool names cannot be listed before the workflow runs"
	reasonNoUploadedSpec        = "it has no uploaded spec, so its operations' parameters are not known"
	reasonToolNamesUnreadable   = "its tool names could not be read"
	reasonSpecHasNoOperations   = "its uploaded spec declares no operations"
	reasonSpecUnreadable        = "its uploaded spec could not be read"
	reasonSpecTooLarge          = "its uploaded spec is larger than the platform reads"
	reasonSpecUnparseable       = "its uploaded spec could not be parsed for parameters"
	reasonOperationNotSettled   = "the spec does not settle the parameters of operation %q (request-body media types that disagree, or a tool name another operation also produces)"
	reasonCheckFailedUnexpected = "the check failed unexpectedly"
)

const (
	failureNotFound    = "not_found"
	failureAmbiguous   = "ambiguous"
	failureCanceled    = "canceled"
	failureTimeout     = "timeout"
	failureUnparseable = "unparseable"
	failureTooLarge    = "too_large"
	failureReadFailed  = "read_failed"
)

var (
	errConnectorNotFound  = errors.New("connector not found")
	errConnectorNotBound  = errors.New("connector is not bound to this workflow")
	errConnectorAmbiguous = errors.New("connector name is ambiguous")
)

func failureClass(err error) string {
	if _, tooLarge := errors.AsType[*specTooLargeError](err); tooLarge {
		return failureTooLarge
	}
	switch {
	case errors.Is(err, context.Canceled):
		return failureCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return failureTimeout
	case errors.Is(err, errConnectorNotFound), errors.Is(err, errConnectorNotBound):
		return failureNotFound
	case errors.Is(err, errConnectorAmbiguous):
		return failureAmbiguous
	default:
		return failureReadFailed
	}
}

func isExpectedFailure(class string) bool {
	return class != failureTimeout && class != failureReadFailed
}

func ClassifyReadFailure(err error) (class string, interruption bool) {
	class = failureClass(err)
	return class, interrupted(class)
}

func interrupted(class string) bool {
	return class == failureCanceled || class == failureTimeout
}

type connectorResolver func(ctx context.Context, name string) (*store.Connector, error)

func resolveThroughBindings(bindings []store.WorkflowConnectorBinding, repo *store.ConnectorRepo) connectorResolver {
	idByRef := make(map[string]string, len(bindings))
	for _, b := range bindings {
		idByRef[b.RefName] = b.ConnectorID
	}
	return func(ctx context.Context, name string) (*store.Connector, error) {
		id, bound := idByRef[name]
		if !bound {
			return nil, errConnectorNotBound
		}
		conn, err := repo.GetByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load connector: %w", err)
		}
		if conn == nil {
			return nil, errConnectorNotFound
		}
		return conn, nil
	}
}

func resolveByName(repo *store.ConnectorRepo) connectorResolver {
	return func(ctx context.Context, name string) (*store.Connector, error) {
		conns, err := repo.GetAllByName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("look up connector: %w", err)
		}
		switch len(conns) {
		case 0:
			return nil, errConnectorNotFound
		case 1:
			return conns[0], nil
		default:
			return nil, errConnectorAmbiguous
		}
	}
}

type connectorInputCheck struct {
	resolve connectorResolver
	specs   ResourceStore
	cache   specCache
	logger  *slog.Logger
}

func (c *connectorInputCheck) specSource() specSource {
	return specSource{store: c.specs, cache: c.cache, logger: c.logger}
}

func (c *connectorInputCheck) log() *slog.Logger {
	if c.logger == nil {
		return slog.Default()
	}
	return c.logger
}

func (c *connectorInputCheck) logUngraded(class string, attrs ...any) {
	level := slog.LevelWarn
	if isExpectedFailure(class) {
		level = slog.LevelDebug
	}
	c.log().Log(context.Background(), level, "could not read a connector to check a workflow's tool-node inputs; its nodes go unchecked",
		append(attrs, "failureClass", class)...)
}

type apiToolSurface struct {
	conn       *store.Connector
	names      []string
	signatures map[string]openapi.OperationSignature
	declared   map[string]bool
	offered    []string
	allowList  []string
}

const maxOfferedOperations = 20

func (c *connectorInputCheck) loadAPIToolSurface(ctx context.Context, conn *store.Connector) (surface *apiToolSurface, reason, failure string) {
	if conn.ResourceKey == nil || *conn.ResourceKey == "" {
		return c.surfaceWithoutSpec(ctx, conn)
	}
	spec := c.specSource().load(ctx, conn, *conn.ResourceKey)
	if spec.readErr != nil {
		failure = failureClass(spec.readErr)
		if failure == failureTooLarge {
			return nil, reasonSpecTooLarge, failure
		}
		return nil, reasonSpecUnreadable, failure
	}
	operations, err := spec.operationSignatures(ctx)
	if err != nil {
		return nil, reasonSpecUnparseable, failureUnparseable
	}
	allNames, err := spec.toolNames(ctx)
	if err != nil {
		return nil, reasonSpecUnparseable, failureUnparseable
	}
	if len(allNames) == 0 {
		return nil, reasonSpecHasNoOperations, ""
	}
	names, err := apiConnectorToolNames(ctx, conn, c.specSource(), vendorSpecRemedy)
	if err != nil {
		return nil, reasonToolNamesUnreadable, failureClass(err)
	}
	signatures := make(map[string]openapi.OperationSignature, len(operations))
	for _, op := range operations {
		signatures[op.ToolName] = op
	}
	declared := make(map[string]bool, len(allNames))
	for _, name := range allNames {
		declared[name] = true
	}
	return &apiToolSurface{
		conn: conn, names: names, signatures: signatures, declared: declared, offered: allNames,
		allowList: allowListEntries(conn),
	}, "", ""
}

func (c *connectorInputCheck) surfaceWithoutSpec(ctx context.Context, conn *store.Connector) (*apiToolSurface, string, string) {
	_, err := apiConnectorToolNames(ctx, conn, c.specSource(), vendorSpecRemedy)
	if err == nil {
		return nil, reasonNoUploadedSpec, ""
	}
	if _, expected := errors.AsType[*ValidationError](err); expected {
		return nil, reasonNoSpecOrNames, ""
	}
	return nil, reasonToolNamesUnreadable, failureClass(err)
}

func allowListEntries(conn *store.Connector) []string {
	cc, err := connectorConfigFromRow(conn)
	if err != nil {
		return nil
	}
	raw, _ := cc.Values["allow_list"].(string)
	return parseOperationList(raw)
}

func (s *apiToolSurface) missingOperationProblem(node map[string]any, toolName string, stops *workflow.RunStopAnalysis) *workflow.ToolInputFinding {
	id, _ := node["id"].(string)
	reference, _ := node["connector"].(string)
	entry := toolName
	for _, authored := range s.allowList {
		if openapi.OperationToolName(authored) == toolName {
			entry = authored
			break
		}
	}
	offered := s.offered
	more := ""
	if len(offered) > maxOfferedOperations {
		more = fmt.Sprintf(" and %d more", len(offered)-maxOfferedOperations)
		offered = offered[:maxOfferedOperations]
	}
	return &workflow.ToolInputFinding{
		StaticProblem: stops.FailingNodeProblem(id, toolName,
			fmt.Sprintf("tool node %q calls %q of connector %q, but the connector's allow_list entry %q is not an operation of its uploaded spec, so every run that reaches the node fails with \"references unknown tool\"; the spec's operations are: %s%s",
				id, toolName, reference, entry, strings.Join(offered, ", "), more)),
	}
}

func (s *apiToolSurface) problemFor(node map[string]any, stops *workflow.RunStopAnalysis) (finding *workflow.ToolInputFinding, unchecked string) {
	selector, _ := node["tool"].(string)
	toolName, err := selectAPIConnectorToolName(s.conn, s.names, selector)
	if err != nil {
		return nil, ""
	}
	if !s.declared[toolName] {
		return s.missingOperationProblem(node, toolName, stops), ""
	}
	input, _ := node["input"].(map[string]any)
	if input == nil {
		return nil, ""
	}
	signature, settled := s.signatures[toolName]
	if !settled {
		return nil, fmt.Sprintf(reasonOperationNotSettled, toolName)
	}
	id, _ := node["id"].(string)
	reference, _ := node["connector"].(string)
	params := make([]workflow.ToolParam, len(signature.Params))
	for i, p := range signature.Params {
		params[i] = workflow.ToolParam{Name: p.Name, Required: p.Required}
	}
	return workflow.ToolInputProblem(workflow.ToolInputTarget{
		NodeID:    id,
		Connector: reference,
		ToolName:  toolName,
		Params:    params,
		RunStops:  stops,
	}, input), ""
}

func (c *connectorInputCheck) check(ctx context.Context, appConfig map[string]any) *ConnectorToolInputReport {
	return c.run(ctx, appConfig, false)
}

func (c *connectorInputCheck) run(ctx context.Context, appConfig map[string]any, logUndeclared bool) (report *ConnectorToolInputReport) {
	report = newConnectorToolInputReport()
	body := workflowBody(appConfig)
	nodes, err := connectorToolNodes(body)
	if err != nil {
		return report
	}
	graded := 0
	defer func() {
		if r := recover(); r != nil {
			c.reportPanic(report, nodes[graded:], r)
		}
	}()
	stops := workflow.AnalyzeRunStops(body)

	surfaces := map[string]*apiToolSurface{}
	attempted := map[string]bool{}
	for _, node := range nodes {
		name, _ := node["connector"].(string)
		if !attempted[name] {
			attempted[name] = true
			surfaces[name] = c.surfaceFor(ctx, name, report)
		}
		if surface := surfaces[name]; surface != nil {
			finding, unchecked := surface.problemFor(node, stops)
			if unchecked != "" {
				addUnchecked(report, name, unchecked)
			}
			if finding != nil {
				report.Problems = append(report.Problems, ConnectorToolInputProblem{
					NodeID: finding.NodeID, Connector: name, Message: finding.Message, Fatal: finding.Fatal,
				})
				if logUndeclared {
					c.logNotRefused(name, finding)
				}
			}
		}
		graded++
	}
	return report
}

func (c *connectorInputCheck) logNotRefused(connector string, finding *workflow.ToolInputFinding) {
	switch {
	case finding.Downgraded():
		c.log().Warn(msgToolNodeCannotStopRun, "nodeID", finding.NodeID, "toolName", finding.ToolName, "connector", connector, "why", finding.NotFatalBecause, "undeclaredKeys", finding.Undeclared)
	case !finding.Fatal:
		c.log().Warn("workflow tool node passes input keys its OpenAPI operation does not declare",
			"nodeID", finding.NodeID, "connector", connector, "undeclaredKeys", finding.Undeclared)
	}
}

func (c *connectorInputCheck) reportPanic(report *ConnectorToolInputReport, ungraded []map[string]any, panicked any) {
	var connectors []string
	for _, node := range ungraded {
		name, _ := node["connector"].(string)
		addUnchecked(report, name, reasonCheckFailedUnexpected)
		if !slices.Contains(connectors, name) {
			connectors = append(connectors, name)
		}
	}
	c.log().Error("workflow connector tool-input check panicked; its nodes go unchecked",
		"connectors", connectors, "panicType", fmt.Sprintf("%T", panicked), "stack", string(debug.Stack()))
}

func (c *connectorInputCheck) surfaceFor(ctx context.Context, name string, report *ConnectorToolInputReport) *apiToolSurface {
	conn, err := c.resolve(ctx, name)
	if err != nil {
		class := failureClass(err)
		reason := reasonConnectorNotResolved
		if interrupted(class) {
			reason = reasonCheckInterrupted
		}
		addUnchecked(report, name, reason)
		c.logUngraded(class, "connector", name)
		return nil
	}
	if conn.Type != "api" {
		return nil
	}
	surface, reason, failure := c.loadAPIToolSurface(ctx, conn)
	if surface == nil {
		if interrupted(failure) {
			reason = reasonCheckInterrupted
		}
		addUnchecked(report, name, reason)
		if failure != "" {
			c.logUngraded(failure, "connectorID", conn.ID, "reason", reason)
		}
	}
	return surface
}

func (c *connectorInputCheck) reject(ctx context.Context, appConfig map[string]any) error {
	report := c.run(ctx, appConfig, true)
	var fatal []string
	for _, p := range report.Problems {
		if p.Fatal {
			fatal = append(fatal, p.Message)
		}
	}
	for _, u := range report.Unchecked {
		c.log().Debug("workflow tool-node inputs not checked against a connector", "connector", u.Connector, "reason", u.Reason)
	}
	if len(fatal) == 0 {
		return nil
	}
	return &ValidationError{Message: "workflow tool nodes would fail whenever they run, and a failure of them can stop a run: " + strings.Join(fatal, "; ")}
}
