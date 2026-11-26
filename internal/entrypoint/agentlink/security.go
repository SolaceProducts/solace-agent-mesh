// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func (a *Adapter) authenticate(ctx context.Context, props a2a.UserProperties, taskID string) (*auth.Identity, error) {
	authn := a.core.Services().Authenticator()
	if props.AuthToken == "" {
		if _, allows := authn.(auth.EmptyTokenAuthenticator); allows {
			return nil, nil
		}
		a.core.Services().AuditLogger().LogAuth(ctx, auth.AuthEvent{UserID: props.UserID, TaskID: taskID, Reason: errMissingToken.Error()})
		return nil, errMissingToken
	}
	var identity *auth.Identity
	var err error
	if pa, ok := authn.(auth.PeerAuthenticator); ok && props.IsPeerCall() {
		identity, err = pa.AuthenticateWithSoftTaskBinding(ctx, props.AuthToken, "")
	} else {
		identity, err = authn.Authenticate(ctx, props.AuthToken, taskID)
	}
	event := auth.AuthEvent{UserID: props.UserID, TaskID: taskID, Success: err == nil}
	if err != nil {
		event.Reason = "invalid auth token"
	} else if identity != nil {
		event.UserID, event.CredentialID = identity.UserID, identity.CredentialID
	}
	a.core.Services().AuditLogger().LogAuth(ctx, event)
	if err == nil {
		auth.ApplySoftSubsetAssert(identity, props.A2AUserConfig, auth.SoftSubsetAssertFields{
			TaskID: taskID, UserID: props.UserID, DelegatingAgent: props.DelegatingAgentName, Origin: adapterType,
		}, a.logger)
	}
	return identity, err
}

func (a *Adapter) mayInvoke(ctx context.Context, identity *auth.Identity, peer string) bool {
	return a.core.Services().AgentAuthorizer().AuthorizeAgent(ctx, identity, peer) == nil
}

func (s *agentState) attribute(ev *spend.Event, msg a2a.Message, up a2a.UserProperties, identity *auth.Identity, deliveredAt time.Time) {
	ev.AgentID, ev.AgentName = agent.ComponentIdentity(s.cfg.Name)
	origin := agent.SpendOriginOf(msg, up)
	ev.WorkflowID, ev.WorkflowName, ev.WorkflowNodeID, ev.ParentTaskID = origin.WorkflowID, origin.WorkflowName, origin.WorkflowNodeID, origin.ParentTaskID
	ev.Surface = spend.SurfaceAgent
	ev.TraceID = up.TraceID
	ev.UserID, ev.UserSub = spend.SpendUser(up, identity)
	up = agent.WithRoot(up, s.cfg.Name)
	ev.RootKind = spend.RootKind(up.RootKind)
	ev.RootID, ev.RootName = agent.RootIdentity(up.RootID)
	ev.RootNodeID = up.RootNodeID
	ev.EntrypointID, ev.EntrypointName = spend.StampedRowOr(up.EntrypointUUID, up.ResolveEntrypointName())
	ev.Channel = spend.Channel(up.Channel)
	trigger, triggerID := agent.SpendTrigger(up, origin)
	ev.TriggerKind, ev.TriggerID = trigger, triggerID
	now := s.a.now()
	if completed := time.UnixMilli(ev.CompletedTime); completed.Before(deliveredAt) || completed.After(now) {
		ev.CompletedTime = now.UnixMilli()
	}
}

const (
	msgControlNotSigned = "agent link refused an agent control command the platform did not sign"
	msgControlMalformed = "agent link refused a malformed agent control command"
)

func (s *agentState) onControl(ctx context.Context, m *broker.Message) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(m.Payload, &body); err != nil || body.Enabled == nil {
		s.a.logger.Warn(msgControlMalformed, "agentName", s.cfg.Name, "reason", "enabled is required")
		s.refuseControl(m, control.ErrorResponse(control.ErrInvalidRequest, "enabled is required"))
		return
	}
	state := "running"
	if !*body.Enabled {
		state = "stopped"
	}
	if verified, class := s.verifyControlStatement(ctx, m.UserProperties[a2a.PropAuthToken], state); !verified {
		s.a.logger.Warn(msgControlNotSigned, "agentName", s.cfg.Name, "reason", class, "state", state)
		s.refuseControl(m, control.ErrorResponse(control.ErrAuthDenied, "agent control command not signed by the platform"))
		return
	}
	s.setStopped(!*body.Enabled)
	reason := "released_by_sam"
	if !*body.Enabled {
		reason = "stopped_by_sam"
	}
	resp := control.SuccessResponse(map[string]any{"name": s.cfg.Name, "enabled": *body.Enabled, "reason": reason})
	s.goRun(func(ctx context.Context) { s.replyControl(ctx, m, resp) })
}

type refusedControl struct {
	msg  *broker.Message
	resp *control.Response
}

func (s *agentState) refuseControl(m *broker.Message, resp *control.Response) {
	select {
	case s.refusedControls <- refusedControl{msg: m, resp: resp}:
	default:
	}
}

func (s *agentState) verifyControlStatement(ctx context.Context, statement, state string) (bool, string) {
	authn := s.a.core.Services().Authenticator()
	if statement == "" {
		_, allows := authn.(auth.EmptyTokenAuthenticator)
		return allows, statementMissing
	}
	_, err := authn.Authenticate(ctx, statement, controlState+s.cfg.Name+":"+state)
	switch {
	case err == nil:
		return true, ""
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return false, statementUnverifiable
	default:
		return false, statementRejected
	}
}

const (
	statementMissing      = "missing"
	statementUnverifiable = "verification_unavailable"
	statementRejected     = "rejected"
)

func (s *agentState) replyControl(ctx context.Context, m *broker.Message, resp *control.Response) {
	replyTo := m.ReplyTo
	if replyTo == "" {
		replyTo = m.UserProperties[a2a.PropReplyTo]
	}
	if replyTo == "" {
		return
	}
	body, err := control.MarshalResponse(resp)
	if err != nil {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, s.a.publishWait)
	defer cancel()
	if err := s.brk().Publish(pctx, replyTo, &broker.Message{Payload: body, CorrelationID: m.CorrelationID}); err != nil {
		s.a.logger.Warn("agent link control reply failed", "agentName", s.cfg.Name, "error", err)
	}
}

func (s *agentState) loadStopState(ctx context.Context) {
	defer close(s.stateReady)
	s.applyStopLease(ctx)
}

func (s *agentState) recheckStopLease(ctx context.Context) {
	if !s.leaseCheck.CompareAndSwap(false, true) {
		return
	}
	defer s.leaseCheck.Store(false)
	s.applyStopLease(ctx)
}

const defaultLeaseSettle = 5 * time.Second

const maxLeaseReads = 3

func (s *agentState) applyStopLease(ctx context.Context) {
	client := control.NewClient(s.brk(), s.ns(), control.WithTimeout(s.a.controlWait))
	for range maxLeaseReads {
		s.mu.Lock()
		readAt, readStart := s.controlGen, s.a.now()
		s.mu.Unlock()
		raw, err := client.Custom(ctx, "platform", "get", "agent-control/"+s.cfg.Name, nil)
		if err != nil {
			s.a.logger.Debug("agent link found no agent control state", "agentName", s.cfg.Name, "error", err)
			return
		}
		var lease struct {
			State     string `json:"state"`
			Statement string `json:"statement"`
		}
		if json.Unmarshal(raw, &lease) != nil || (lease.State != "stopped" && lease.State != "running") {
			s.a.logger.Warn(msgLeaseMalformed, "agentName", s.cfg.Name)
			return
		}
		verified, class := s.verifyControlStatement(ctx, lease.Statement, lease.State)
		if !verified {
			if lease.Statement == "" && lease.State == "running" {
				s.a.logger.Debug("agent link found an unsigned running lease, which changes nothing", "agentName", s.cfg.Name)
				return
			}
			s.a.logger.Warn(msgLeaseNotSigned, "agentName", s.cfg.Name, "state", lease.State, "reason", class)
			return
		}
		if lease.State == "running" && s.stopCommandedSince(readStart) {
			s.a.logger.Debug("agent link set aside a running lease read just after a stop; the next sweep reads it again", "agentName", s.cfg.Name)
			return
		}
		if s.applyStopState(lease.State == "stopped", &readAt) {
			return
		}
	}
}

func (s *agentState) stopCommandedSince(readStart time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.lastStopCommand.IsZero() && readStart.Sub(s.lastStopCommand) < s.a.leaseSettle
}

const (
	msgLeaseMalformed = "agent link ignored a malformed agent control lease"
	msgLeaseNotSigned = "agent link ignored an agent control lease the platform did not sign"
)

func sortStrings(s []string) { sort.Strings(s) }
