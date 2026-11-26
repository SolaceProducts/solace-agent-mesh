// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

const cardRepublishInterval = config.DefaultCardPublishIntervalSecs * time.Second

const describePublishInterval = 10 * time.Second

const originAgentLink = "agent-link"

type describedCard struct {
	card           a2a.AgentCard
	body           []byte
	healthSince    map[string]time.Time
	requiredScopes []string
	toolScopes     []string
}

const maxToolScopes = 64

func namedToolScopes(named, scopes []string) []string {
	out := slices.Clone(named)
	for _, scope := range scopes {
		if !slices.Contains(out, scope) {
			out = append(out, scope)
		}
	}
	return out
}

func distinctToolScopes(tools []describedTool) []string {
	var scopes []string
	for _, t := range tools {
		for _, scope := range t.RequiredScopes {
			if scope != "" && !slices.Contains(scopes, scope) {
				scopes = append(scopes, scope)
			}
		}
	}
	return scopes
}

func (s *agentState) handleDescribe(ctx context.Context, c *conn, f inFrame) {
	if len(f.Agent) > maxCardBytes {
		s.refuseDescribe(c, errCodeBadMessage, "card too large")
		return
	}
	var d describedAgent
	if err := json.Unmarshal(f.Agent, &d); err != nil {
		s.refuseDescribe(c, errCodeBadMessage, "agent must be a JSON object")
		return
	}
	if over := overlongPeerText(d); over != "" {
		s.refuseDescribe(c, errCodeLimit, over)
		return
	}
	d.DisplayName = cmp.Or(s.cfg.DisplayName, s.cfg.Name)
	toolScopes := distinctToolScopes(d.Tools)
	s.mu.Lock()
	named := namedToolScopes(s.toolScopesNamed, toolScopes)
	if len(named) > maxToolScopes {
		s.mu.Unlock()
		s.refuseDescribe(c, errCodeLimit, "the agent's tools have named more than 64 distinct scopes")
		return
	}
	s.toolScopesNamed = named
	var since map[string]time.Time
	if s.described != nil {
		since = s.described.healthSince
	}
	s.mu.Unlock()
	problems, healthSince, ok := s.describedHealth(d.Health, since)
	if !ok {
		s.refuseDescribe(c, errCodeBadMessage, "a health problem's severity is degraded or fatal")
		return
	}
	card := s.buildCard(d, problems)
	body, err := json.Marshal(card)
	if err != nil || len(body) > maxCardBytes {
		s.refuseDescribe(c, errCodeBadMessage, "card too large")
		return
	}

	s.mu.Lock()
	wasListed, sameCard := s.listedLocked(), bytes.Equal(body, s.publishedBody)
	if current := s.described; current == nil || !bytes.Equal(body, current.body) ||
		!slices.Equal(d.RequiredScopes, current.requiredScopes) || !slices.Equal(toolScopes, current.toolScopes) {
		s.described = &describedCard{card: card, body: body, healthSince: healthSince,
			requiredScopes: d.RequiredScopes, toolScopes: toolScopes}
	}
	s.withdrawn = false
	c.retired = false
	resume := c.describeRefused && c.consuming
	c.describeRefused = false
	unchanged := sameCard && wasListed && s.listedLocked()
	s.mu.Unlock()
	if resume {
		s.ensureDispatch()
		s.signalCapacity()
	}
	if unchanged {
		return
	}
	if err := s.publishDescribed(ctx); err != nil {
		c.refuseFrame(typeDescribe, errCodePublishFailed, "the card could not be published")
	}
}

func (s *agentState) refuseDescribe(c *conn, code, message string) {
	s.mu.Lock()
	c.describeRefused = true
	s.mu.Unlock()
	c.refuseFrame(typeDescribe, code, message)
}

func (s *agentState) publishDescribed(ctx context.Context) error {
	s.mu.Lock()
	now := s.a.now()
	wait := s.lastDescribePublish.Add(s.a.describeInterval).Sub(now)
	if wait > 0 {
		scheduled := s.describePending
		s.describePending = true
		s.mu.Unlock()
		if !scheduled {
			s.goRun(func(ctx context.Context) {
				if !sleepCtx(ctx, wait) {
					return
				}
				s.mu.Lock()
				s.describePending, s.lastDescribePublish = false, s.a.now()
				s.mu.Unlock()
				_ = s.syncCard(ctx, false)
			})
		}
		return nil
	}
	s.lastDescribePublish = now
	s.mu.Unlock()
	err := s.syncCard(ctx, false)
	if err != nil {
		s.mu.Lock()
		s.lastDescribePublish = time.Time{}
		s.mu.Unlock()
	}
	return err
}

func (s *agentState) buildCard(d describedAgent, problems []health.Problem) a2a.AgentCard {
	cfg := &agent.AgentConfig{
		Name:              s.cfg.Name,
		Namespace:         s.ns(),
		Description:       d.Description,
		Version:           d.Version,
		DisplayName:       d.DisplayName,
		InputSchema:       d.InputSchema,
		OutputSchema:      d.OutputSchema,
		RequiredScopes:    d.RequiredScopes,
		SupportsStreaming: d.Streaming,
		Skills:            []a2a.AgentSkill{},
	}
	for _, skill := range d.Skills {
		cfg.Skills = append(cfg.Skills, a2a.AgentSkill{ID: skill.ID, Name: skill.Name, Description: skill.Description, Tags: []string{}})
	}
	if d.WelcomeMessage != "" || len(d.Suggestions) > 0 {
		cfg.Welcome = &agent.WelcomeConfig{Message: d.WelcomeMessage}
		for _, sug := range d.Suggestions {
			cfg.Welcome.Suggestions = append(cfg.Welcome.Suggestions, agent.WelcomeSuggestion{Label: sug.Title, Prompt: sug.Prompt, AutoSend: sug.AutoSend})
		}
	}
	in := agent.CardInputs{PeerNames: s.declaredPeers(d.Peers), Health: problems}
	for _, t := range d.Tools {
		in.Tools = append(in.Tools, agent.CardTool{Name: t.Name, Description: t.Description, RequiredScopes: t.RequiredScopes})
	}
	card := agent.BuildCard(cfg, in)
	card.Capabilities.Extensions = append(card.Capabilities.Extensions,
		a2a.AgentExtension{URI: a2a.ExtensionURIArtifactAccess, Params: map[string]any{"artifact_store": false}},
		provenanceExtension(d.Declared))
	card.Capabilities.Extensions = append(card.Capabilities.Extensions, linkExtensions(s.cfg)...)
	return card
}

func (s *agentState) declaredPeers(declared []string) []string {
	var peers []string
	for _, name := range declared {
		if s.cfg.allowsPeer(name) && !slices.Contains(peers, name) {
			peers = append(peers, name)
		}
	}
	return peers
}

func (s *agentState) describedHealth(described []describedProblem, since map[string]time.Time) ([]health.Problem, map[string]time.Time, bool) {
	now := s.a.now()
	problems := make([]health.Problem, 0, len(described))
	for _, p := range described {
		var severity health.Severity
		switch p.Severity {
		case health.Degraded.String():
			severity = health.Degraded
		case health.Fatal.String():
			severity = health.Fatal
		default:
			return nil, nil, false
		}
		first, seen := since[p.ID]
		if !seen {
			first = now
		}
		problems = append(problems, health.Problem{ID: p.ID, Severity: severity, Detail: p.Detail, Since: first})
	}
	if len(problems) == 0 {
		return nil, nil, true
	}
	bounded := a2a.HealthProblemsFromCard(a2a.AgentCard{Capabilities: a2a.AgentCapabilities{
		Extensions: []a2a.AgentExtension{a2a.HealthProblemsExtension(problems)},
	}})
	kept := make(map[string]time.Time, len(bounded))
	for _, p := range bounded {
		kept[p.ID] = p.Since
	}
	return bounded, kept, true
}

func provenanceExtension(d *declaredFacts) a2a.AgentExtension {
	if d == nil {
		d = &declaredFacts{}
	}
	params := map[string]any{"origin": originAgentLink}
	if d.SDK.Name != "" {
		params["origin"] = d.SDK.Name
	}
	for key, value := range map[string]string{"sdk_version": d.SDK.Version, "framework": d.Framework, "owner": d.Owner, "team": d.Team} {
		if value != "" {
			params[key] = value
		}
	}
	if len(d.DataAccess) > 0 {
		access := make([]any, 0, len(d.DataAccess))
		for _, a := range d.DataAccess {
			access = append(access, map[string]any{"system": a.System, "classification": a.Classification, "access": a.Access})
		}
		params["data_access"] = access
	}
	if len(d.MCPServers) > 0 {
		servers := make([]any, 0, len(d.MCPServers))
		for _, m := range d.MCPServers {
			servers = append(servers, map[string]any{"name": m.Name, "url": m.URL})
		}
		params["mcp_servers"] = servers
	}
	return a2a.AgentExtension{URI: a2a.ExtensionURIProvenance, Params: params}
}

func (s *agentState) listedLocked() bool {
	return s.described != nil && !s.withdrawn && !s.stopped && (s.stayingLocked() || s.cfg.Wake != nil)
}

func (s *agentState) stayingLocked() bool {
	for _, c := range s.conns {
		if !c.retired && !c.isClosed() {
			return true
		}
	}
	return false
}

func (s *agentState) syncCard(ctx context.Context, remove bool) error {
	s.cardMu.Lock()
	defer s.cardMu.Unlock()
	s.resumeRepublishing()
	s.mu.Lock()
	listed, described := s.listedLocked(), s.described
	s.mu.Unlock()
	switch {
	case listed:
		return s.publishCard(ctx, described.card)
	case remove:
		return s.publishRemovalCard(ctx)
	}
	return nil
}

func (s *agentState) resumeRepublishing() {
	s.mu.Lock()
	start := s.listedLocked() && !s.republishing
	if start {
		s.republishing = true
	}
	s.mu.Unlock()
	if start && !s.goRun(s.republishCard) {
		s.mu.Lock()
		s.republishing = false
		s.mu.Unlock()
	}
}

func (s *agentState) relistOnConnect(ctx context.Context) {
	s.mu.Lock()
	listed := s.listedLocked()
	s.mu.Unlock()
	if !listed {
		return
	}
	s.cardMu.Lock()
	up := s.cardUp && s.a.now().Sub(s.lastCardPublish) < s.a.cardRepublish
	s.cardMu.Unlock()
	if up {
		s.resumeRepublishing()
		return
	}
	_ = s.publishDescribed(ctx)
}

func (s *agentState) publishCard(ctx context.Context, card a2a.AgentCard) error {
	if !s.a.serves() {
		return nil
	}
	body, err := json.Marshal(card)
	if err == nil {
		err = s.publish(ctx, a2a.DiscoveryAgentCardsTopic(s.ns()), body, map[string]string{"instance_id": s.instanceID()}, false)
	}
	s.cardUp = true
	if err != nil {
		s.a.logger.Warn("agent link card publish failed", "agentName", s.cfg.Name, "error", err)
		return err
	}
	s.lastCardPublish = s.a.now()
	s.mu.Lock()
	s.publishedBody = body
	s.mu.Unlock()
	return nil
}

func (s *agentState) republishCard(ctx context.Context) {
	t := time.NewTicker(s.a.cardRepublish)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.republishing = false
			s.mu.Unlock()
			return
		case <-t.C:
		}
		if !s.republishOnce(ctx) {
			return
		}
	}
}

func (s *agentState) republishOnce(ctx context.Context) bool {
	s.cardMu.Lock()
	defer s.cardMu.Unlock()
	s.mu.Lock()
	if !s.listedLocked() {
		s.republishing = false
		s.mu.Unlock()
		return false
	}
	card := s.described.card
	s.mu.Unlock()
	_ = s.publishCard(ctx, card)
	return true
}

func (s *agentState) publishRemovalCard(ctx context.Context) error {
	if !s.a.serves() {
		return nil
	}
	body, err := json.Marshal(map[string]any{"name": s.cfg.Name, "url": "solace:" + s.requestTopic()})
	if err == nil {
		err = s.publish(ctx, a2a.DiscoveryAgentCardsTopic(s.ns()), body,
			map[string]string{"instance_id": s.instanceID(), "removed": "true"}, false)
	}
	if err != nil {
		s.a.logger.Warn("agent link removal card publish failed", "agentName", s.cfg.Name, "error", err)
	}
	s.cardUp = false
	s.mu.Lock()
	s.publishedBody = nil
	s.mu.Unlock()
	return err
}

func (s *agentState) handleRetire(ctx context.Context, c *conn) {
	s.mu.Lock()
	c.retired = true
	withdraw := !s.stayingLocked() && !s.withdrawn
	if withdraw {
		s.withdrawn = true
	}
	s.mu.Unlock()
	if withdraw {
		s.withdrawCard(ctx)
	}
}

func (s *agentState) withdrawCard(ctx context.Context) {
	s.cardMu.Lock()
	defer s.cardMu.Unlock()
	s.mu.Lock()
	listed := s.listedLocked()
	s.mu.Unlock()
	if !listed && s.cardUp {
		_ = s.publishRemovalCard(ctx)
	}
}

func (s *agentState) displayName() string {
	return cmp.Or(s.cfg.DisplayName, s.cfg.Name)
}

func overlongPeerText(d describedAgent) string {
	if len(d.Description) > maxDescriptionBytes {
		return fmt.Sprintf("the description is over %d bytes", maxDescriptionBytes)
	}
	if len(d.Skills) > maxSkills {
		return fmt.Sprintf("an agent describes at most %d skills", maxSkills)
	}
	for _, sk := range d.Skills {
		if len(sk.Name) > maxNameBytes || len(sk.Description) > maxSkillDescriptionBytes {
			return fmt.Sprintf("a skill's name is at most %d bytes and its description at most %d", maxNameBytes, maxSkillDescriptionBytes)
		}
	}
	return ""
}
