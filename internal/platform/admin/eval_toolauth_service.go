// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "context"

type EvalToolAuthService struct {
	h *evalHandler
}

func (s *EvalToolAuthService) Checklist(ctx context.Context, experimentID string) (ToolAuthChecklist, error) {
	exp, err := s.h.experiments.GetBase(ctx, experimentID)
	if err != nil {
		return ToolAuthChecklist{}, err
	}
	return s.h.toolAuthChecklist(ctx, exp)
}

func (s *EvalToolAuthService) CandidateAgents(ctx context.Context, experimentID string) ([]string, error) {
	exp, err := s.h.experiments.GetBase(ctx, experimentID)
	if err != nil {
		return nil, err
	}
	return s.h.toolAuthCandidateAgents(ctx, exp)
}

func (s *EvalToolAuthService) Begin(ctx context.Context, experimentID, agentName, credentialKey, userID string) (ToolAuthBegun, error) {
	target, err := s.target(ctx, experimentID, agentName, credentialKey)
	if err != nil {
		return ToolAuthBegun{}, err
	}
	return s.h.beginToolAuth(ctx, target, userID)
}

func (s *EvalToolAuthService) Disconnect(ctx context.Context, experimentID, agentName, credentialKey, userID string) error {
	target, err := s.target(ctx, experimentID, agentName, credentialKey)
	if err != nil {
		return err
	}
	return s.h.disconnectToolAuth(ctx, target, userID)
}

func (s *EvalToolAuthService) PinAgent(ctx context.Context, experimentID, agentName, userID string) (ToolAuthPinnedAgent, error) {
	exp, err := s.h.experiments.GetBase(ctx, experimentID)
	if err != nil {
		return ToolAuthPinnedAgent{}, err
	}
	return s.h.pinToolAuthAgent(ctx, exp, agentName, userID)
}

func (s *EvalToolAuthService) UnpinAgent(ctx context.Context, experimentID, agentName, userID string) error {
	exp, err := s.h.experiments.GetBase(ctx, experimentID)
	if err != nil {
		return err
	}
	return s.h.unpinToolAuthAgent(ctx, exp, agentName, userID)
}

func (s *EvalToolAuthService) target(ctx context.Context, experimentID, agentName, credentialKey string) (toolAuthTarget, error) {
	exp, err := s.h.experiments.GetBase(ctx, experimentID)
	if err != nil {
		return toolAuthTarget{}, err
	}
	return s.h.resolveToolAuthTarget(ctx, exp, agentName, credentialKey)
}
