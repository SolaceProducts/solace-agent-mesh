// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"gopkg.in/yaml.v3"
)

const toolAuthGraphMaxDepth = 3

type toolAuthPeerFilter struct {
	AllowList []string
	DenyList  []string
}

type toolAuthPeerFilterSource interface {
	PeerFilter(ctx context.Context, agentName string) (filter toolAuthPeerFilter, managed bool, err error)
}

type toolAuthMeshAgent struct {
	RoutingName string
	DisplayName string
}

type toolAuthMeshLister interface {
	MeshAgents() []toolAuthMeshAgent
}

type workflowNodeAgentSource interface {
	WorkflowNodeAgents(ctx context.Context, name string) (agents []workflowNodeAgent, complete bool, err error)
}

type toolAuthGraphNode struct {
	AgentName string
	IsTarget  bool
	AddedBy   string
	NodeIDs   []string
}

type toolAuthGraph struct {
	Agents                []toolAuthGraphNode
	AllowListIsWildcard   bool
	unenumerableDenyLists [][]string
}

func (g toolAuthGraph) AdmitsUnenumerated(agent toolAuthMeshAgent) bool {
	names := []string{agent.RoutingName, agent.DisplayName}
	for _, denyList := range g.unenumerableDenyLists {
		if !matchesAnyPattern(denyList, names) {
			return true
		}
	}
	return false
}

func discoverToolAuthGraph(ctx context.Context, targetAgent string, pinned []toolAuthGraphNode, filters toolAuthPeerFilterSource, mesh toolAuthMeshLister) (toolAuthGraph, error) {
	return discoverToolAuthGraphFrom(ctx, toolAuthGraphSeeds{target: targetAgent, walkTarget: true}, pinned, filters, mesh)
}

type toolAuthGraphSeeds struct {
	target     string
	walkTarget bool
	agents     []toolAuthGraphNode
	incomplete bool
}

func discoverToolAuthGraphFrom(ctx context.Context, seeds toolAuthGraphSeeds, pinned []toolAuthGraphNode, filters toolAuthPeerFilterSource, mesh toolAuthMeshLister) (toolAuthGraph, error) {
	b := &toolAuthGraphBuilder{ctx: ctx, filters: filters, nodeIndex: map[string]int{}}
	if mesh != nil {
		b.live = mesh.MeshAgents()
	}
	if seeds.incomplete {
		b.markUnenumerable(nil)
	}
	b.add(toolAuthGraphNode{AgentName: seeds.target, IsTarget: true})
	var roots []string
	if seeds.walkTarget {
		roots = append(roots, seeds.target)
	}
	for _, agent := range seeds.agents {
		if _, added := b.add(agent); added {
			roots = append(roots, agent.AgentName)
		}
	}
	if err := b.walk(roots); err != nil {
		return toolAuthGraph{}, err
	}
	if err := b.walk(b.addPinned(pinned)); err != nil {
		return toolAuthGraph{}, err
	}
	return b.graph, nil
}

type toolAuthGraphBuilder struct {
	ctx       context.Context
	filters   toolAuthPeerFilterSource
	live      []toolAuthMeshAgent
	graph     toolAuthGraph
	nodeIndex map[string]int
}

func (b *toolAuthGraphBuilder) add(node toolAuthGraphNode) (index int, added bool) {
	key := store.NormalizeName(node.AgentName)
	if index, seen := b.nodeIndex[key]; seen {
		return index, false
	}
	b.nodeIndex[key] = len(b.graph.Agents)
	b.graph.Agents = append(b.graph.Agents, node)
	return len(b.graph.Agents) - 1, true
}

func (b *toolAuthGraphBuilder) markUnenumerable(denyList []string) {
	b.graph.AllowListIsWildcard = true
	b.graph.unenumerableDenyLists = append(b.graph.unenumerableDenyLists, denyList)
}

func (b *toolAuthGraphBuilder) addPinned(pinned []toolAuthGraphNode) []string {
	var roots []string
	for _, node := range pinned {
		index, added := b.add(node)
		switch {
		case added:
			roots = append(roots, node.AgentName)
		case !b.graph.Agents[index].IsTarget:
			b.graph.Agents[index].AddedBy = node.AddedBy
		}
	}
	return roots
}

func (b *toolAuthGraphBuilder) walk(frontier []string) error {
	if b.filters == nil {
		return nil
	}
	for depth := 0; len(frontier) > 0; depth++ {
		detectOnly := depth == toolAuthGraphMaxDepth
		var next []string
		for _, agentName := range frontier {
			added, err := b.visit(agentName, detectOnly)
			if err != nil {
				return err
			}
			next = append(next, added...)
		}
		if detectOnly {
			return nil
		}
		frontier = next
	}
	return nil
}

func (b *toolAuthGraphBuilder) visit(agentName string, detectOnly bool) ([]string, error) {
	filter, managed, err := b.filters.PeerFilter(b.ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("read peer allow list for %q: %w", agentName, err)
	}
	if !managed {
		b.markUnenumerable(nil)
		return nil, nil
	}
	if isWildcardAllowList(filter.AllowList) {
		b.markUnenumerable(filter.DenyList)
		return nil, nil
	}
	var added []string
	for _, peer := range expandPeerAllowList(filter, b.live) {
		if detectOnly {
			if _, seen := b.nodeIndex[store.NormalizeName(peer)]; !seen {
				b.markUnenumerable(nil)
			}
			continue
		}
		if _, isNew := b.add(toolAuthGraphNode{AgentName: peer}); isNew {
			added = append(added, peer)
		}
	}
	return added, nil
}

func isWildcardAllowList(allowList []string) bool {
	if allowList == nil {
		return true
	}
	for _, pattern := range allowList {
		if strings.TrimSpace(pattern) == "*" {
			return true
		}
	}
	return false
}

func expandPeerAllowList(filter toolAuthPeerFilter, live []toolAuthMeshAgent) []string {
	var peers []string
	for _, pattern := range filter.AllowList {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		matchedLive := false
		for _, agent := range live {
			candidates := []string{agent.RoutingName, agent.DisplayName}
			if !matchesPattern(pattern, candidates) {
				continue
			}
			matchedLive = true
			if !matchesAnyPattern(filter.DenyList, candidates) {
				peers = append(peers, agent.DisplayName)
			}
		}
		if !matchedLive && !isGlobPattern(pattern) && !matchesAnyPattern(filter.DenyList, []string{pattern}) {
			peers = append(peers, pattern)
		}
	}
	return peers
}

func matchesAnyPattern(patterns, names []string) bool {
	for _, pattern := range patterns {
		if matchesPattern(strings.TrimSpace(pattern), names) {
			return true
		}
	}
	return false
}

func matchesPattern(pattern string, names []string) bool {
	for _, name := range names {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func isGlobPattern(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

type agentPeerFilterAdapter struct {
	repo        *store.AgentRepo
	deployments *store.AgentDeploymentRepo
	logger      *slog.Logger
}

func (a *agentPeerFilterAdapter) PeerFilter(ctx context.Context, agentName string) (toolAuthPeerFilter, bool, error) {
	if a == nil || a.repo == nil {
		return toolAuthPeerFilter{}, false, nil
	}
	agent, err := a.lookup(ctx, agentName)
	if err != nil {
		return toolAuthPeerFilter{}, false, err
	}
	if agent == nil {
		return toolAuthPeerFilter{}, false, nil
	}
	deployed, ok, err := a.deployedPeerFilter(ctx, agent)
	if err != nil {
		return toolAuthPeerFilter{}, false, err
	}
	if ok {
		return deployed, true, nil
	}
	filter := toolAuthPeerFilter{AllowList: []string{}}
	if len(agent.AdditionalConfigurations) == 0 {
		return filter, true, nil
	}
	var blob struct {
		InterAgentCommunication struct {
			AllowList []string `json:"allowList"`
			DenyList  []string `json:"denyList"`
		} `json:"interAgentCommunication"`
	}
	if err := json.Unmarshal(agent.AdditionalConfigurations, &blob); err != nil {
		if a.logger != nil {
			a.logger.Warn("ignoring unparseable agent additional configurations while reading peer allow list",
				"agentName", agentName, "error", err)
		}
		return filter, true, nil
	}
	if blob.InterAgentCommunication.AllowList != nil {
		filter.AllowList = blob.InterAgentCommunication.AllowList
	}
	filter.DenyList = blob.InterAgentCommunication.DenyList
	return filter, true, nil
}

func (a *agentPeerFilterAdapter) deployedPeerFilter(ctx context.Context, agent *store.Agent) (toolAuthPeerFilter, bool, error) {
	if a.deployments == nil {
		return toolAuthPeerFilter{}, false, nil
	}
	deploys, err := a.deployments.GetLatestSuccessfulDeploys(ctx, []string{agent.ID})
	if err != nil {
		return toolAuthPeerFilter{}, false, fmt.Errorf("read latest successful deploy for %q: %w", agent.Name, err)
	}
	latest := deploys[agent.ID]
	if latest == nil || latest.AgentConfigSnapshot == nil {
		return toolAuthPeerFilter{}, false, nil
	}
	undeployed, err := a.deployments.GetLatestSuccessfulUndeploy(ctx, agent.ID)
	if err != nil {
		return toolAuthPeerFilter{}, false, fmt.Errorf("read latest successful undeploy for %q: %w", agent.Name, err)
	}
	if undeployed != nil && isNewerAgentDeploymentRow(undeployed, latest) {
		return toolAuthPeerFilter{}, false, nil
	}
	var snapshot struct {
		Apps []struct {
			AppConfig struct {
				InterAgentCommunication *struct {
					AllowList []string `yaml:"allow_list"`
					DenyList  []string `yaml:"deny_list"`
				} `yaml:"inter_agent_communication"`
			} `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err := yaml.Unmarshal([]byte(*latest.AgentConfigSnapshot), &snapshot); err != nil {
		if a.logger != nil {
			a.logger.Warn("ignoring unparseable agent deploy snapshot while reading peer allow list",
				"agentName", agent.Name, "error", err)
		}
		return toolAuthPeerFilter{}, false, nil
	}
	if len(snapshot.Apps) == 0 || snapshot.Apps[0].AppConfig.InterAgentCommunication == nil {
		return toolAuthPeerFilter{}, false, nil
	}
	peers := snapshot.Apps[0].AppConfig.InterAgentCommunication
	return toolAuthPeerFilter{AllowList: peers.AllowList, DenyList: peers.DenyList}, true, nil
}

func (a *agentPeerFilterAdapter) lookup(ctx context.Context, agentName string) (*store.Agent, error) {
	agent, _, _, _, _, err := a.repo.GetByName(ctx, agentName)
	if err != nil {
		return nil, err
	}
	if agent != nil {
		return agent, nil
	}
	id, ok := agentIDFromInstanceName(agentName)
	if !ok {
		return nil, nil
	}
	agent, _, _, _, _, err = a.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return agent, nil
}

type agentRegistryMeshLister struct {
	registry *platpkg.AgentRegistry
}

func (l *agentRegistryMeshLister) MeshAgents() []toolAuthMeshAgent {
	if l == nil || l.registry == nil {
		return nil
	}
	cards := l.registry.List()
	agents := make([]toolAuthMeshAgent, 0, len(cards))
	for i := range cards {
		agents = append(agents, toolAuthMeshAgent{
			RoutingName: cards[i].Name,
			DisplayName: platpkg.DisplayName(&cards[i]),
		})
	}
	return agents
}
