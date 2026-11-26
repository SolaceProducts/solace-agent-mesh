// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func refStringField(m map[string]any, key string) (string, error) {
	raw, present := m[key]
	if !present {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string, got %T", key, raw)
	}
	return s, nil
}

func displayRefID(name string, index int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("#%d", index)
}

type agentWorkflowLookups struct {
	agents             map[string]struct{}
	platformLinkAgents func() (map[string]struct{}, error)
	workflows          map[string]struct{}
}

func (l *agentWorkflowLookups) validateAgent(name string) error {
	if _, ok := l.agents[name]; ok {
		return nil
	}
	linkAgents, err := l.platformLinkAgents()
	if err != nil {
		return fmt.Errorf("%w; its link agents could not be checked: %w", agentNotFound(name), err)
	}
	if _, ok := linkAgents[name]; ok {
		return nil
	}
	return agentNotFound(name)
}

func agentNotFound(name string) error {
	return fmt.Errorf("agent %q not found on platform and not declared in the desired manifest", name)
}

func (l *agentWorkflowLookups) validateWorkflow(name string) error {
	if _, ok := l.workflows[name]; ok {
		return nil
	}
	return fmt.Errorf("workflow %q not found on platform and not declared in the desired manifest", name)
}

func buildAgentWorkflowLookups(
	ctx context.Context,
	c *Client,
	desiredWorkflows []resolve.ResolvedResource,
	actualWorkflows []wire.WorkflowResponse,
	desiredAgentNames []string,
) (*agentWorkflowLookups, error) {
	agents, err := c.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agents for reference validation: %w", err)
	}

	known := map[string]struct{}{}
	for _, a := range agents {
		addNonEmptyNames(known, a.Name, a.InstanceName)
	}
	addNonEmptyNames(known, desiredAgentNames...)

	return &agentWorkflowLookups{
		agents:             known,
		platformLinkAgents: platformLinkAgentNames(ctx, c),
		workflows:          knownWorkflowNames(desiredWorkflows, actualWorkflows),
	}, nil
}

func platformLinkAgentNames(ctx context.Context, c *Client) func() (map[string]struct{}, error) {
	return sync.OnceValues(func() (map[string]struct{}, error) {
		linkAgents, err := c.ListLinkAgents(ctx)
		if platformHasNoLinkAgentRoute(err) {
			return map[string]struct{}{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("list link agents for reference validation: %w", err)
		}
		names := make(map[string]struct{}, 2*len(linkAgents))
		for _, a := range linkAgents {
			addNonEmptyNames(names, a.Name, a.InstanceName)
		}
		return names, nil
	})
}

func platformHasNoLinkAgentRoute(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

func addNonEmptyNames(set map[string]struct{}, names ...string) {
	for _, n := range names {
		if n != "" {
			set[n] = struct{}{}
		}
	}
}

func knownWorkflowNames(desiredWorkflows []resolve.ResolvedResource, actualWorkflows []wire.WorkflowResponse) map[string]struct{} {
	known := map[string]struct{}{}
	for _, w := range actualWorkflows {
		if w.Name != "" {
			known[w.Name] = struct{}{}
		}
		if w.ID != "" {
			known[a2a.InstanceName(a2a.KindWorkflow, w.ID)] = struct{}{}
		}
	}
	for _, w := range desiredWorkflows {
		if w.Name != "" {
			known[w.Name] = struct{}{}
		}
	}
	return known
}
