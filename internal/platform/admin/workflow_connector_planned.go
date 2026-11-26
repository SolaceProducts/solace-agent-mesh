// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type PlannedConnector struct {
	ID      string
	Name    string
	Type    string
	Subtype string
	Values  map[string]any
}

func plannedRow(ctx context.Context, p PlannedConnector, repo *store.ConnectorRepo) (*store.Connector, error) {
	if p.ID != "" {
		stored, getErr := repo.GetByID(ctx, p.ID)
		if getErr != nil {
			return nil, fmt.Errorf("load connector: %w", getErr)
		}
		if stored != nil {
			return plannedOverStored(stored, p)
		}
	}
	values, err := json.Marshal(p.Values)
	if err != nil {
		return nil, fmt.Errorf("encode planned connector values: %w", err)
	}
	encoded := string(values)
	row := &store.Connector{Name: p.Name, Type: p.Type, Values: &encoded}
	if p.Subtype != "" {
		row.Subtype = &p.Subtype
	}
	return row, nil
}

func plannedOverStored(stored *store.Connector, p PlannedConnector) (*store.Connector, error) {
	values, err := json.Marshal(p.Values)
	if err != nil {
		return nil, fmt.Errorf("encode planned connector values: %w", err)
	}
	encoded := string(values)
	if p.Name != "" {
		stored.Name = p.Name
	}
	stored.Values = &encoded
	return stored, nil
}

func resolveForSave(bindings []store.WorkflowConnectorBinding, planned map[string]PlannedConnector, repo *store.ConnectorRepo) connectorResolver {
	resolve, _ := resolveForSaveTracking(bindings, planned, repo)
	return resolve
}

func resolveForSaveTracking(bindings []store.WorkflowConnectorBinding, planned map[string]PlannedConnector, repo *store.ConnectorRepo) (resolve connectorResolver, resolvedPlanned func() bool) {
	byName := resolveByName(repo)
	throughBindings := resolveThroughBindings(bindings, repo)
	boundIDs := make(map[string]string, len(bindings))
	for _, b := range bindings {
		boundIDs[b.RefName] = b.ConnectorID
	}
	plannedByID := make(map[string]PlannedConnector, len(planned))
	for _, p := range planned {
		if p.ID != "" {
			plannedByID[p.ID] = p
		}
	}
	touched := false
	resolve = func(ctx context.Context, name string) (*store.Connector, error) {
		if id, bound := boundIDs[name]; bound {
			if p, isPlanned := plannedByID[id]; isPlanned {
				touched = true
				return plannedRow(ctx, p, repo)
			}
			return throughBindings(ctx, name)
		}
		if p, isPlanned := planned[name]; isPlanned {
			touched = true
			return plannedRow(ctx, p, repo)
		}
		conn, err := byName(ctx, name)
		if err != nil {
			return nil, err
		}
		if updated, isPlanned := plannedByID[conn.ID]; isPlanned {
			touched = true
			if updated.Name != "" && updated.Name != name {
				return nil, errConnectorNotFound
			}
			return plannedOverStored(conn, updated)
		}
		return conn, nil
	}
	return resolve, func() bool { return touched }
}

type PlannedConnectorChecks struct {
	svc      *WorkflowService
	specs    specCache
	bindings map[string]storedBindingsRead
}

type storedBindingsRead struct {
	bindings []store.WorkflowConnectorBinding
	ok       bool
}

func (s *WorkflowService) NewPlannedChecks() *PlannedConnectorChecks {
	return &PlannedConnectorChecks{svc: s, specs: specCache{}, bindings: map[string]storedBindingsRead{}}
}

func (c *PlannedConnectorChecks) specSource() specSource {
	return specSource{store: c.svc.resourceStore, cache: c.specs, logger: c.svc.log()}
}

func (c *PlannedConnectorChecks) UsesPlannedConnector(ctx context.Context, workflowID string, appConfig map[string]any, planned map[string]PlannedConnector) bool {
	if c.svc.connectorRepo == nil || len(planned) == 0 {
		return false
	}
	nodes, err := connectorToolNodes(workflowBody(appConfig))
	if err != nil || len(nodes) == 0 {
		return false
	}
	bindings, ok := c.storedBindings(ctx, workflowID)
	if !ok {
		return false
	}
	resolve, resolvedPlanned := resolveForSaveTracking(bindings, planned, c.svc.connectorRepo)
	seen := map[string]bool{}
	for _, node := range nodes {
		ref, _ := node["connector"].(string)
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if _, resolveErr := resolve(ctx, ref); resolveErr != nil && !errors.Is(resolveErr, errConnectorNotFound) {
			c.svc.log().Debug("could not read a connector to find out whether a workflow calls one this save writes; the grading pass reports it",
				"connector", ref, "failureClass", failureClass(resolveErr))
		}
		if resolvedPlanned() {
			return true
		}
	}
	return false
}

func (c *PlannedConnectorChecks) storedBindings(ctx context.Context, workflowID string) (bindings []store.WorkflowConnectorBinding, ok bool) {
	if workflowID == "" || c.svc.workflowRepo == nil {
		return nil, true
	}
	if read, done := c.bindings[workflowID]; done {
		return read.bindings, read.ok
	}
	bindings, err := c.svc.workflowRepo.GetConnectorBindings(ctx, workflowID)
	if err != nil {
		c.svc.log().Warn("could not read a workflow's connector bindings to check a save before it is written; the write decides",
			"workflowID", workflowID, "failureClass", failureClass(err))
		c.bindings[workflowID] = storedBindingsRead{}
		return nil, false
	}
	c.bindings[workflowID] = storedBindingsRead{bindings: bindings, ok: true}
	return bindings, true
}

func (s *WorkflowService) log() *slog.Logger {
	if s.logger == nil {
		return slog.Default()
	}
	return s.logger
}

func (c *PlannedConnectorChecks) CheckToolInputs(ctx context.Context, workflowID string, appConfig map[string]any, planned map[string]PlannedConnector) *ConnectorToolInputReport {
	if c.svc.connectorRepo == nil {
		return newConnectorToolInputReport()
	}
	bindings, ok := c.storedBindings(ctx, workflowID)
	if !ok {
		return newConnectorToolInputReport()
	}
	check := c.svc.connectorInputCheck(resolveForSave(bindings, planned, c.svc.connectorRepo), c.svc.logger)
	check.cache = c.specs
	return check.check(ctx, appConfig)
}

func (c *PlannedConnectorChecks) RejectUndeployableConnectorNodes(ctx context.Context, workflowID string, appConfig map[string]any, planned map[string]PlannedConnector) (writeRefusal, deployRefusal error) {
	if c.svc.connectorRepo == nil {
		return nil, nil
	}
	nodes, err := connectorToolNodes(workflowBody(appConfig))
	if err != nil || len(nodes) == 0 {
		return nil, nil
	}
	bindings, ok := c.storedBindings(ctx, workflowID)
	if !ok {
		return nil, nil
	}
	resolve := resolveForSave(bindings, planned, c.svc.connectorRepo)
	selector := newAPIToolSelector(c.specSource()).withoutSpecUpload()
	resolved := map[string]*store.Connector{}
	attempted := map[string]bool{}
	listingReported := map[string]bool{}
	var notFound, undeployable []string
	for _, node := range nodes {
		ref, _ := node["connector"].(string)
		id, _ := node["id"].(string)
		if !attempted[ref] {
			attempted[ref] = true
			conn, resolveErr := resolve(ctx, ref)
			switch {
			case resolveErr == nil:
				resolved[ref] = conn
			case errors.Is(resolveErr, errConnectorNotFound):
				notFound = append(notFound, fmt.Sprintf("node %q: connector %q not found", id, ref))
			default:
				c.svc.warnUnreadableConnector(ref, resolveErr)
			}
		}
		conn := resolved[ref]
		if conn == nil || conn.Type != "api" {
			continue
		}
		sel, _ := node["tool"].(string)
		if _, selErr := selector.selectTool(ctx, ref, conn, sel); selErr != nil {
			if problem, refused := c.svc.undeployableProblem(id, ref, selErr, listingReported); refused {
				undeployable = append(undeployable, problem)
			}
		}
	}
	return refusalOf(notFound), refusalOf(undeployable)
}

func refusalOf(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return &ValidationError{Message: strings.Join(problems, "; "), Field: fieldAppConfig}
}

func (s *WorkflowService) undeployableProblem(nodeID, ref string, selErr error, reported map[string]bool) (string, bool) {
	if _, listing := errors.AsType[*apiToolListError](selErr); listing {
		if reported[ref] {
			return "", false
		}
		reported[ref] = true
		if _, refusal := errors.AsType[*ValidationError](selErr); !refusal {
			s.warnUnreadableConnector(ref, selErr)
			return "", false
		}
	}
	return fmt.Sprintf("node %q: %v", nodeID, selErr), true
}

func (s *WorkflowService) warnUnreadableConnector(ref string, err error) {
	class := failureClass(err)
	level := slog.LevelWarn
	if isExpectedFailure(class) {
		level = slog.LevelDebug
	}
	s.log().Log(context.Background(), level, "could not read a connector to check a workflow's connector nodes before a save; the write decides",
		"connector", ref, "failureClass", class)
}

func InputsNotCheckable(u UncheckedConnector) bool { return u.Reason == reasonNoUploadedSpec }
