// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

var lateMCPRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}

var bootOnlyPolicyKeys = []string{"hil", "volume_bindings", "network_bindings", tool.EffectConfigKey}

func carriesBootOnlyPolicy(toolMap map[string]any) bool {
	for _, key := range bootOnlyPolicyKeys {
		if _, ok := toolMap[key]; ok {
			return true
		}
	}
	return false
}

func serversWithBootOnlyPolicy(pending []pendingMCPEntry) map[string]bool {
	servers := make(map[string]bool)
	for _, p := range pending {
		if carriesBootOnlyPolicy(p.toolMap) {
			servers[p.cfg.ServerKey()] = true
		}
	}
	return servers
}

func retriesLate(p pendingMCPEntry, connectErr error, policyServers map[string]bool) bool {
	return !mcppkg.IsPermanentConnectError(p.cfg, connectErr) && !policyServers[p.cfg.ServerKey()]
}

type lateMCPEntry struct {
	pendingMCPEntry
	recordIndex int
}

type lateMCPRegistrar struct {
	host    Host
	mgr     *mcppkg.Manager
	onReady func(recordIndex int, rec EntryRecord)
	logger  *slog.Logger

	pending []lateMCPEntry

	cancel context.CancelFunc
	done   sync.WaitGroup
}

func (r *lateMCPRegistrar) Start(ctx context.Context) error {
	if len(r.pending) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	plain := make([]pendingMCPEntry, len(r.pending))
	for i, e := range r.pending {
		plain[i] = e.pendingMCPEntry
	}
	for _, group := range groupByServer(plain) {
		entries := make([]lateMCPEntry, len(group))
		for i, idx := range group {
			entries[i] = r.pending[idx]
		}
		r.done.Go(func() { r.retryUntilRegistered(ctx, entries) })
	}
	return nil
}

var lateMCPStopWait = 5 * time.Second

func (r *lateMCPRegistrar) Stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	finished := make(chan struct{})
	go func() {
		r.done.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(lateMCPStopWait):
		r.logger.Warn("mcp toolset retry still connecting at stop; leaving its client to the MCP manager's shutdown")
	}
	r.pending = nil
}

func (r *lateMCPRegistrar) retryUntilRegistered(ctx context.Context, entries []lateMCPEntry) {
	for attempt := 1; len(entries) > 0; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(lateMCPRetryDelays[min(attempt-1, len(lateMCPRetryDelays)-1)]):
		}
		entries = slices.DeleteFunc(entries, func(e lateMCPEntry) bool {
			return ctx.Err() == nil && r.attempt(ctx, e, attempt)
		})
	}
}

func (r *lateMCPRegistrar) attempt(ctx context.Context, e lateMCPEntry, attempt int) bool {
	logger := r.logger.With("entry", DescribeToolEntry(e.toolMap),
		"index", e.index,
		"server", mcpServerLogID(e.cfg),
		"connectorId", SourceConnectorID(e.toolMap),
		"attempt", attempt)
	err := r.mgr.CanConnect(ctx, e.cfg)
	if ctx.Err() != nil {
		return false
	}
	if err == nil {
		err = r.mgr.AddClient(e.cfg)
		if ctx.Err() != nil {
			return false
		}
	}
	if err != nil {
		if mcppkg.IsPermanentConnectError(e.cfg, err) {
			logger.Error("mcp toolset retry gave up; toolset tools not registered", "error", redactedErrorSummary(err))
			return true
		}
		logger.Warn("mcp toolset still unreachable; retrying", "error", redactedErrorSummary(err))
		return false
	}
	tools, err := r.mgr.DiscoverTools(e.cfg)
	if err != nil {
		logger.Error("mcp tool discovery failed; toolset tools not registered", "error", redactedErrorSummary(err))
		return true
	}
	names := registerMCPTools(r.host, tools, logger)
	if len(tools) > 0 && len(names) == 0 {
		logger.Error("mcp tools discovered but none registered", "discovered", len(tools))
		return true
	}
	rec := EntryRecord{ToolMap: e.toolMap, ToolNames: names, Status: EntryStatusReady}
	if len(names) < len(tools) {
		rec.Error = fmt.Sprintf("%d tools discovered, %d registered", len(tools), len(names))
	}
	logger.Info("registered MCP toolset (late)", "tools", len(names), "protocolVersion", r.mgr.ProtocolVersion(e.cfg))
	r.onReady(e.recordIndex, rec)
	return true
}
