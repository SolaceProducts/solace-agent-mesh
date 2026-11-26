// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/timeutil"
)

const (
	NodeTypeTimer = "timer"
	NodeTypeWait  = "wait"
)

var signalNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func isDurableWaitNode(n *NodeConfig) bool {
	return n != nil && (n.Type == NodeTypeTimer || n.Type == NodeTypeWait)
}

func parseTimerNode(node *NodeConfig, raw map[string]any) error {
	value := getString(raw, "duration")
	if value == "" {
		return fmt.Errorf("timer node %q missing 'duration'", node.ID)
	}
	d, err := timeutil.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("timer node %q: invalid duration %q: %w", node.ID, value, err)
	}
	if d <= 0 {
		return fmt.Errorf("timer node %q: duration %q must be positive", node.ID, value)
	}
	if getString(raw, "timeout") != "" {
		return fmt.Errorf("timer node %q: 'timeout' does not apply; the timer ends after its 'duration'", node.ID)
	}
	node.WaitDuration = d
	node.Timeout = 0
	return nil
}

func parseWaitNode(node *NodeConfig, raw map[string]any) error {
	node.Signal = getString(raw, "signal")
	if node.Signal == "" {
		return fmt.Errorf("wait node %q missing 'signal'", node.ID)
	}
	if !signalNamePattern.MatchString(node.Signal) {
		return fmt.Errorf("wait node %q: signal %q must be 1 to 64 letters, digits, '_' or '-'", node.ID, node.Signal)
	}
	if getString(raw, "timeout") == "" {
		node.Timeout = 0
	}
	if node.Timeout < 0 {
		return fmt.Errorf("wait node %q: timeout must not be negative", node.ID)
	}
	return nil
}

func validateDurableWaits(cfg *WorkflowConfig) error {
	exitHandlers := exitHandlerNodeIDs(cfg.OnExit)
	signals := map[string]string{}
	for _, n := range cfg.Nodes {
		if !isDurableWaitNode(n) {
			continue
		}
		if slices.Contains(exitHandlers, n.ID) {
			return fmt.Errorf("%s node %q cannot be an on_exit handler; exit handlers run to completion without waiting", n.Type, n.ID)
		}
		if n.Retry != nil {
			return fmt.Errorf("%s node %q: retry_strategy does not apply to a node that only waits", n.Type, n.ID)
		}
		if n.Type != NodeTypeWait {
			continue
		}
		if other, dup := signals[n.Signal]; dup {
			return fmt.Errorf("wait nodes %q and %q both wait for signal %q; each signal can be awaited by one node", other, n.ID, n.Signal)
		}
		signals[n.Signal] = n.ID
	}
	return nil
}

func (cfg *WorkflowConfig) hasDurableWaits() bool {
	return slices.ContainsFunc(cfg.Nodes, isDurableWaitNode)
}

func (w *WorkflowInstance) requireDurableWaitSupport(svc runtime.Services) error {
	flagOn := svc != nil && svc.FeatureRegistry() != nil && svc.FeatureRegistry().IsEnabled(features.KeyWorkflowDurableWaits)
	w.durableWaits = flagOn && w.durableWaitStoresOpen()
	if !w.cfg.hasDurableWaits() {
		return nil
	}
	if !flagOn {
		return fmt.Errorf("workflow %q has timer or wait nodes, which need the %s feature flag", w.cfg.Name, features.KeyWorkflowDurableWaits)
	}
	if !w.workflowHILEnabled() {
		return fmt.Errorf("workflow %q has timer or wait nodes, which park runs in the workflow store: they need the workflow_hil_pause_resume feature flag and a session_service database", w.cfg.Name)
	}
	if w.checkpointStore() == nil {
		return fmt.Errorf("workflow %q has timer or wait nodes, which resume a run from its stored steps after an AWE stops: they need the %s feature flag", w.cfg.Name, features.KeyWorkflowCheckpointResume)
	}
	return nil
}

func (w *WorkflowInstance) durableWaitStoresOpen() bool {
	return w.workflowHILEnabled() && w.checkpointStore() != nil
}
