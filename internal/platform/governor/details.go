// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const (
	detailsTimeout = 2 * time.Second

	nameTTL = 5 * time.Minute

	maxCachedNames = 4096
)

const (
	gapOverload        = "overloaded"
	gapWriteFailed     = "write failed"
	gapNamesUnresolved = "names unresolved"
)

type detailWriter struct {
	names *nameCache
	gaps  *detailGaps
}

func (g *Governor) writeDetails(ctx context.Context, batch []pendingEvent, models map[ModelRef]Model) {
	if len(batch) == 0 || ctx.Err() != nil {
		return
	}
	now := g.now()
	if len(g.pending) > cap(g.pending)/2 {
		g.details.gaps.record(gapOverload, len(batch), nil, g.logger, now)
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, detailsTimeout)
	defer cancel()
	names, resolveErr := g.details.names.resolve(writeCtx, g.cfg.Ledger, batch, now)
	details := make([]store.SpendLedgerDetail, len(batch))
	unresolved := 0
	for i, p := range batch {
		details[i] = detailFor(p.event, models[modelRefOf(p.event)], names)
		if names.missedAny(p.event) {
			unresolved++
		}
	}
	_, writeErr := g.cfg.Ledger.InsertDetails(writeCtx, details)
	if ctx.Err() != nil {
		return
	}
	if resolveErr != nil {
		g.details.gaps.record(gapNamesUnresolved, unresolved, resolveErr, g.logger, now)
	}
	if writeErr != nil {
		g.details.gaps.record(gapWriteFailed, len(details), writeErr, g.logger, now)
	}
}

func detailFor(e spend.Event, m Model, names resolvedNames) store.SpendLedgerDetail {
	d := store.SpendLedgerDetail{
		ID:                     e.ID,
		ModelKey:               optional(e.ModelKey),
		SentModelName:          optional(e.ModelName),
		ToolName:               optional(e.ToolName),
		WorkflowNodeID:         optional(e.WorkflowNodeID),
		ParentTaskID:           optional(e.ParentTaskID),
		SessionID:              optional(e.SessionID),
		ResolvedModelName:      optional(m.Name),
		ResolvedModelProvider:  optional(m.Provider),
		ResolvedAgentName:      names.of(nameKey{store.ComponentAgents, e.AgentID}),
		ResolvedWorkflowName:   names.of(nameKey{store.ComponentWorkflows, e.WorkflowID}),
		ResolvedEntrypointName: names.of(nameKey{store.ComponentGateways, e.EntrypointID}),
		CacheWritesReported:    e.CacheWritesReported,
	}
	if e.AttemptCount > 0 {
		d.AttemptCount = &e.AttemptCount
	}
	if table, ok := rootTable(e.RootKind); ok {
		d.ResolvedRootName = names.of(nameKey{table, e.RootID})
	}
	return d
}

func rootTable(kind spend.RootKind) (store.ComponentTable, bool) {
	switch kind {
	case spend.RootAgent:
		return store.ComponentAgents, true
	case spend.RootWorkflow:
		return store.ComponentWorkflows, true
	}
	return "", false
}

type nameKey struct {
	table store.ComponentTable
	id    string
}

func componentRefs(e spend.Event) []nameKey {
	refs := make([]nameKey, 0, 4)
	add := func(table store.ComponentTable, id string) {
		if id != "" {
			refs = append(refs, nameKey{table, id})
		}
	}
	add(store.ComponentAgents, e.AgentID)
	add(store.ComponentWorkflows, e.WorkflowID)
	add(store.ComponentGateways, e.EntrypointID)
	if table, ok := rootTable(e.RootKind); ok {
		add(table, e.RootID)
	}
	return refs
}

type resolvedNames struct {
	names  map[nameKey]string
	failed map[store.ComponentTable]bool
}

func (r resolvedNames) of(key nameKey) *string {
	name, ok := r.names[key]
	if !ok {
		return nil
	}
	return &name
}

func (r resolvedNames) missedAny(e spend.Event) bool {
	for _, ref := range componentRefs(e) {
		if r.failed[ref.table] {
			return true
		}
	}
	return false
}

type cachedName struct {
	name    string
	found   bool
	expires time.Time
}

type nameCache struct {
	entries map[nameKey]cachedName
}

func newNameCache() *nameCache {
	return &nameCache{entries: make(map[nameKey]cachedName)}
}

func (c *nameCache) resolve(ctx context.Context, ledger Ledger, batch []pendingEvent, now time.Time) (resolvedNames, error) {
	resolved := resolvedNames{names: make(map[nameKey]string), failed: make(map[store.ComponentTable]bool)}
	wanted := make(map[store.ComponentTable][]string)
	seen := make(map[nameKey]bool)
	for _, p := range batch {
		for _, key := range componentRefs(p.event) {
			if seen[key] {
				continue
			}
			seen[key] = true
			if hit, ok := c.entries[key]; ok && now.Before(hit.expires) {
				if hit.found {
					resolved.names[key] = hit.name
				}
				continue
			}
			wanted[key.table] = append(wanted[key.table], key.id)
		}
	}
	var errs []error
	for table, ids := range wanted {
		found, err := ledger.ResolveNames(ctx, table, ids)
		if err != nil {
			resolved.failed[table] = true
			errs = append(errs, err)
			continue
		}
		for _, id := range ids {
			key := nameKey{table, id}
			name, ok := found[id]
			c.put(key, cachedName{name: name, found: ok, expires: now.Add(nameTTL)}, now)
			if ok {
				resolved.names[key] = name
			}
		}
	}
	return resolved, errors.Join(errs...)
}

func (c *nameCache) put(key nameKey, entry cachedName, now time.Time) {
	if _, ok := c.entries[key]; !ok && len(c.entries) >= maxCachedNames {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= maxCachedNames {
			clear(c.entries)
		}
	}
	c.entries[key] = entry
}

type detailGaps struct {
	throttle *logThrottle
	missing  map[string]int
}

func newDetailGaps(interval time.Duration) *detailGaps {
	return &detailGaps{throttle: newLogThrottle(interval), missing: make(map[string]int)}
}

func (d *detailGaps) record(reason string, events int, err error, logger *slog.Logger, now time.Time) {
	if events == 0 {
		return
	}
	d.missing[reason] += events
	if !d.throttle.allow(reason, now) {
		return
	}
	fields := []any{"reason", reason, "events", d.missing[reason]}
	if err != nil {
		fields = append(fields, "error", err)
	}
	logger.Warn("spend details not written in full; the spend itself is filed", fields...)
	delete(d.missing, reason)
}
