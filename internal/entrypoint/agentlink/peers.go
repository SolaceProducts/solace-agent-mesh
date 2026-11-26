// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func (s *agentState) handlePeerCall(ctx context.Context, c *conn, f inFrame) {
	if !s.cfg.allowsPeer(f.Peer) {
		c.sendError(errCodeForbidden, "not a configured peer of this agent", f.SubtaskID)
		return
	}
	if s.isStopped() {
		c.sendError(errCodeForbidden, "SAM has stopped this agent", f.SubtaskID)
		return
	}
	if !validTopicLevel(f.SubtaskID) {
		c.sendError(errCodeBadMessage, "subtaskId must be a single topic level", "")
		return
	}
	if f.Text == "" {
		c.sendError(errCodeBadMessage, "a peer call says what the peer is asked", f.SubtaskID)
		return
	}
	if len(f.CallID) > maxIDBytes {
		c.sendError(errCodeBadMessage, "a callId is at most 128 bytes", f.SubtaskID)
		return
	}
	s.mu.Lock()
	parent, refusal := s.admitPeerCallLocked(c, f)
	if refusal != nil {
		s.mu.Unlock()
		c.sendError(refusal.code, refusal.message, f.SubtaskID)
		return
	}
	identity, request, statusTopic := parent.identity, parent.request, parent.statusTopic
	up := a2a.DecodeUserProperties(parent.props)
	s.mu.Unlock()

	if !s.peerReachable(f.Peer) {
		s.warnPeerOffMeshOnce(f.Peer, f.ParentTaskID)
		c.sendError(errCodePeerUnavailable, "the peer is not on the mesh", f.SubtaskID)
		return
	}
	if !s.a.mayInvoke(ctx, identity, f.Peer) {
		s.a.logger.Warn("agent link refused a peer call", "agentName", s.cfg.Name, "peer", f.Peer, "taskID", f.ParentTaskID)
		c.sendError(errCodeForbidden, "the user may not invoke this peer", f.SubtaskID)
		return
	}
	if up.CallDepth+1 > maxCallDepth {
		c.sendError(errCodeForbidden, "call depth limit reached", f.SubtaskID)
		return
	}
	toolCtx := peerCallContext(f, identity, agent.WithRoot(up, s.cfg.Name), request)
	peerTaskID := idgen.New()
	body, err := json.Marshal(agent.PeerRequest(s.displayName(), f.Peer, peerTaskID, f.Text, toolCtx, nil))
	if err != nil {
		c.sendError(errCodeBadMessage, "the peer call could not be encoded", f.SubtaskID)
		return
	}
	props := agent.PeerRequestProperties(s.ns(), s.cfg.Name, peerTaskID, toolCtx)

	s.mu.Lock()
	_, refusal = s.admitPeerCallLocked(c, f)
	if refusal != nil {
		s.mu.Unlock()
		c.sendError(refusal.code, refusal.message, f.SubtaskID)
		return
	}
	s.subtasks[f.SubtaskID] = &subtaskBinding{parent: f.ParentTaskID, connID: c.id, peer: f.Peer, peerTaskID: peerTaskID}
	answered := s.router.Register(peerTaskID, statusTopic, f.ParentTaskID, s.cfg.Name, f.Peer, f.CallID,
		up.ClientID, up.UserID, up.A2AUserConfig, s.a.cfg.TaskTTL, agent.WithTraceID(up.TraceID), agent.WithAuthToken(up.AuthToken))
	s.mu.Unlock()

	pctx, cancel := context.WithTimeout(ctx, s.a.publishWait)
	defer cancel()
	msg := &broker.Message{Payload: body, ReplyTo: props[a2a.PropReplyTo], UserProperties: props, TTL: s.a.cfg.TaskTTL}
	if err := s.brk().PublishGuaranteed(pctx, a2a.AgentRequestTopic(s.ns(), f.Peer), msg); err != nil {
		s.a.logger.Warn("agent link peer request publish failed", "agentName", s.cfg.Name, "peer", f.Peer, "taskID", f.ParentTaskID, "error", err)
		s.router.Cancel(peerTaskID)
		s.mu.Lock()
		delete(s.subtasks, f.SubtaskID)
		s.mu.Unlock()
		c.sendError(errCodePublishFailed, "the peer request could not be delivered", f.SubtaskID)
		return
	}
	s.goRun(func(ctx context.Context) {
		select {
		case m := <-answered:
			s.finishPeerCall(f.SubtaskID, m)
		case <-ctx.Done():
		}
	})
}

type peerCallRefusal struct{ code, message string }

func (s *agentState) admitPeerCallLocked(c *conn, f inFrame) (*taskBinding, *peerCallRefusal) {
	parent, refusal := s.openTaskLocked(c, typePeerCall, f.ParentTaskID)
	if parent == nil {
		if refusal == msgNotAnOpenTask {
			refusal = "the parent task is not an open task of this agent"
		}
		return nil, &peerCallRefusal{errCodeUnknownTask, refusal}
	}
	if parent.cancelled {
		return nil, &peerCallRefusal{errCodeForbidden, "the parent task was cancelled"}
	}
	if problem, code := s.subtaskRoomLocked(f.SubtaskID); problem != "" {
		return nil, &peerCallRefusal{code, problem}
	}
	return parent, nil
}

func (s *agentState) subtaskRoomLocked(subtaskID string) (string, string) {
	if _, open := s.subtasks[subtaskID]; open {
		return "a peer call with this subtaskId is open", errCodeBadMessage
	}
	if len(s.subtasks)+s.heldLocked(typePeerResult) >= maxOpenSubtasks {
		return "too many open peer calls", errCodeLimit
	}
	return "", ""
}

func peerCallContext(f inFrame, identity *auth.Identity, up a2a.UserProperties, request requestFacts) *tool.Context {
	if identity != nil && identity.AuthToken == "" {
		withToken := *identity
		withToken.AuthToken = up.AuthToken
		identity = &withToken
	}
	return &tool.Context{
		Identity:                identity,
		UserID:                  up.UserID,
		SessionID:               request.message.ContextID,
		A2AUserConfig:           up.A2AUserConfig,
		ParentTaskID:            f.ParentTaskID,
		ClientID:                up.ClientID,
		TraceID:                 up.TraceID,
		Trigger:                 up.Trigger,
		RootKind:                up.RootKind,
		RootID:                  up.RootID,
		RootNodeID:              up.RootNodeID,
		Origin:                  up.Origin,
		DisplaysTemplateSignals: request.displaysTemplates,
		CallDepth:               up.CallDepth,
		ToolCallID:              f.CallID,
	}
}

func (s *agentState) handlePeerCancel(c *conn, f inFrame) {
	s.mu.Lock()
	b := s.subtasks[f.SubtaskID]
	if b == nil {
		s.mu.Unlock()
		c.sendError(errCodeUnknownTask, "not an open peer call of this agent", f.SubtaskID)
		return
	}
	b.cancelled = true
	peerTaskID := b.peerTaskID
	s.mu.Unlock()
	s.router.CancelWithPropagation(peerTaskID, s.ns())
}

func (s *agentState) finishPeerCall(subtaskID string, answer *broker.Message) {
	s.mu.Lock()
	b := s.subtasks[subtaskID]
	if b == nil {
		s.mu.Unlock()
		return
	}
	delete(s.subtasks, subtaskID)
	s.peerResultsInFlight++
	target := s.askersConnLocked(b.connID, b.parent)
	s.mu.Unlock()
	result := peerOutcome(b, answer)
	result.Type, result.SubtaskID = typePeerResult, subtaskID
	s.sendOrHold(target, b.parent, result)
	s.mu.Lock()
	s.peerResultsInFlight--
	s.mu.Unlock()
	s.signalQuiet()
}

func (s *agentState) liveConnLocked(connID string) *conn {
	if c := s.conns[connID]; c != nil && !c.isClosed() {
		return c
	}
	for _, c := range s.conns {
		if !c.isClosed() {
			return c
		}
	}
	return nil
}

func peerOutcome(b *subtaskBinding, answer *broker.Message) outFrame {
	outcome := func(state, text string) outFrame {
		redacted, cut := redactedTextReportingCut(text)
		return outFrame{State: state, Text: &redacted, Truncated: cut}
	}
	if answer == nil {
		if b.cancelled {
			return outcome(stateCanceled, "peer request cancelled")
		}
		return outcome(stateFailed, fmt.Sprintf("peer agent %q request timed out", b.peer))
	}
	var resp a2a.Response
	if err := json.Unmarshal(answer.Payload, &resp); err != nil {
		return outcome(stateFailed, "failed to parse peer response")
	}
	if resp.Error != nil {
		out := outcome(stateFailed, resp.Error.Message)
		out.Error = &peerError{Code: resp.Error.Code, Message: *out.Text}
		return out
	}
	task, ok := resp.Result.(map[string]any)
	if !ok {
		return outcome(stateCompleted, "")
	}
	text, structuredErr := agent.DelegatedTaskReply(task)
	out := outcome(peerState(task), cmp.Or(text, structuredErr))
	data, cut := replyData(task)
	out.Data, out.Truncated = data, out.Truncated || cut
	return out
}

func peerState(task map[string]any) string {
	status, _ := task["status"].(map[string]any)
	switch state, _ := status["state"].(string); a2a.TaskState(state) {
	case a2a.TaskStateFailed:
		return stateFailed
	case a2a.TaskStateCancelled:
		return stateCanceled
	case a2a.TaskStateRejected:
		return stateRejected
	}
	return stateCompleted
}

func replyData(task map[string]any) (json.RawMessage, bool) {
	status, _ := task["status"].(map[string]any)
	msg, _ := status["message"].(map[string]any)
	parts, _ := msg["parts"].([]any)
	for _, p := range parts {
		part, _ := p.(map[string]any)
		if part["kind"] != "data" || part["data"] == nil || isSAMNotice(part) {
			continue
		}
		redacted, cut := hilsurface.RedactValueReportingCut(part["data"])
		if data, err := json.Marshal(redacted); err == nil {
			return data, cut
		}
	}
	return nil, false
}

func (s *agentState) peerList() []peerEntry {
	peers := []peerEntry{}
	for _, card := range s.a.core.AgentRegistry().List() {
		if !s.cfg.allowsPeer(card.Name) {
			continue
		}
		entry := peerEntry{
			Name:        card.Name,
			DisplayName: a2a.DisplayNameFromCard(card),
			Description: card.Description,
			Skills:      []describedSkill{},
			OnDemand:    a2a.IsOnDemandCard(card),
		}
		for _, skill := range card.Skills {
			entry.Skills = append(entry.Skills, describedSkill{ID: skill.ID, Name: skill.Name, Description: skill.Description})
		}
		peers = append(peers, entry)
	}
	slices.SortFunc(peers, func(a, b peerEntry) int { return cmp.Compare(a.Name, b.Name) })
	return peers
}

func (s *agentState) sendPeers(c *conn) {
	peers := s.peerList()
	encoded, err := json.Marshal(peers)
	if err != nil {
		return
	}
	s.mu.Lock()
	unchanged := c.peers == string(encoded)
	c.peers = string(encoded)
	s.mu.Unlock()
	if !unchanged {
		c.send(outFrame{Type: typePeers, Peers: &peers})
	}
}

func (s *agentState) refreshPeers(c *conn) {
	t := time.NewTicker(peersRefresh)
	defer t.Stop()
	for {
		s.sendPeers(c)
		select {
		case <-c.done:
			return
		case <-t.C:
		}
	}
}

func (s *agentState) pushPeers() {
	for _, c := range s.connections() {
		s.sendPeers(c)
	}
}

func isSAMNotice(part map[string]any) bool {
	if data, ok := part["data"].(map[string]any); ok {
		if _, signal := data["a2a_signal_type"]; signal {
			return true
		}
	}
	meta, _ := part["metadata"].(map[string]any)
	return meta["tool_name"] == artifactSaveTool
}

const artifactSaveTool = "_notify_artifact_save"

func redactedTextReportingCut(text string) (string, bool) {
	redacted, cut := hilsurface.RedactValueReportingCut(text)
	s, _ := redacted.(string)
	return s, cut
}

func (s *agentState) peerReachable(peer string) bool {
	if s.a.core.AgentRegistry().Get(peer) != nil {
		s.mu.Lock()
		delete(s.peersWarnedOffMesh, peer)
		s.mu.Unlock()
		return true
	}
	hosted := s.a.agent(peer)
	return hosted != nil && hosted.wakeable()
}

func (s *agentState) warnPeerOffMeshOnce(peer, taskID string) {
	s.mu.Lock()
	warned := s.peersWarnedOffMesh[peer]
	s.peersWarnedOffMesh[peer] = true
	s.mu.Unlock()
	if !warned {
		s.a.logger.Warn("agent link refused a peer call to a peer not on the mesh", "agentName", s.cfg.Name, "peer", peer, "taskID", taskID)
	}
}
