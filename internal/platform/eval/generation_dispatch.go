// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type generationRequest struct {
	TargetAgent string  `json:"targetAgent"`
	NumExamples int     `json:"numExamples"`
	Focus       *string `json:"focus,omitempty"`
	GatewayURL  string  `json:"gatewayUrl"`
	RequesterID string  `json:"requesterId,omitempty"`
}

type GenerationAuth interface {
	AuthHeader(ctx context.Context, userID, platformUserID string) (string, error)
}

type generationTrigger interface {
	PublishGenerationTrigger(ctx context.Context, operationID string) error
}

type generationDispatch struct {
	trigger generationTrigger
	auth    GenerationAuth

	mu      sync.Mutex
	running map[string]struct{}
}

const maxGenerationTakeovers = 3

const generationTriggerGrace = 30 * time.Second

const msgGenerationTakeoverCap = "The generation stopped making progress after being restarted several times. Try again."

func (s *DatasetService) WithGenerationDispatch(trigger generationTrigger, auth GenerationAuth) *DatasetService {
	s.dispatch = &generationDispatch{trigger: trigger, auth: auth, running: map[string]struct{}{}}
	return s
}

func (d *generationDispatch) claim(operationID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.running[operationID]; ok {
		return false
	}
	d.running[operationID] = struct{}{}
	return true
}

func (d *generationDispatch) release(operationID string) {
	d.mu.Lock()
	delete(d.running, operationID)
	d.mu.Unlock()
}

func (d *generationDispatch) isRunning(operationID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.running[operationID]
	return ok
}

func (s *DatasetService) GenerationsToTakeOver(ctx context.Context) []string {
	if s.dispatch == nil || s.generations == nil {
		return nil
	}
	s.failExpiredGenerations(ctx)
	ops, err := s.generations.ListInProgressBefore(ctx, time.Now().Add(-generationTriggerGrace).UnixMilli())
	if err != nil {
		s.logger.Warn("eval: list generations to take over failed", "error", err)
		return nil
	}
	ids := make([]string, 0, len(ops))
	for _, op := range ops {
		if op.Request != nil && !s.dispatch.isRunning(op.ID) {
			ids = append(ids, op.ID)
		}
	}
	return ids
}

func (s *DatasetService) RunGeneration(ctx context.Context, operationID string, takeover bool) {
	if s.dispatch == nil || s.generations == nil || s.aiGen == nil || !s.dispatch.claim(operationID) {
		return
	}
	defer s.dispatch.release(operationID)
	op, err := s.generations.Get(ctx, operationID)
	if err != nil || op.Status != store.EvalGenerationInProgress || op.Request == nil {
		return
	}
	if takeover && !s.takeOverGeneration(ctx, op) {
		return
	}
	var req generationRequest
	if err = json.Unmarshal([]byte(*op.Request), &req); err != nil {
		s.failGeneration(ctx, op, msgGenerationError, op.ID)
		return
	}
	header := ""
	if s.dispatch.auth != nil {
		if header, err = s.dispatch.auth.AuthHeader(ctx, op.CreatedBy, req.RequesterID); err != nil {
			errorID := op.ID
			s.logger.Warn("eval: signing the generation's token failed", "operationID", op.ID, "errorId", errorID, "error", err)
			s.failGeneration(ctx, op, msgGenerationError, errorID)
			return
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, s.aiGen.Timeout+generationGrace)
	defer cancel()
	s.finishGeneration(runCtx, op, recovering(s.generationFor(op, req, header)))
}

func (s *DatasetService) takeOverGeneration(ctx context.Context, op *store.EvalGenerationOperation) bool {
	deadline := time.Now().Add(s.aiGen.Timeout + 2*generationGrace).UnixMilli()
	takeovers, ok, err := s.generations.TakeOver(ctx, op.ID, deadline)
	if err != nil {
		s.logger.Warn("eval: take over generation failed", "operationID", op.ID, "error", err)
		return false
	}
	if !ok {
		return false
	}
	if takeovers > maxGenerationTakeovers {
		s.logger.Warn("eval: generation failed after repeated takeovers", "operationID", op.ID, "errorId", op.ID, "takeovers", takeovers)
		s.failGeneration(ctx, op, msgGenerationTakeoverCap, op.ID)
		return false
	}
	s.logger.Info("eval: taking over generation", "operationID", op.ID, "takeovers", takeovers)
	return true
}

func (s *DatasetService) generationFor(op *store.EvalGenerationOperation, req generationRequest, header string) generateFunc {
	return func(ctx context.Context, operationID string) error {
		switch op.OperationType {
		case store.EvalGenerationDataset:
			_, err := s.generateDataset(ctx, operationID, GenerateDatasetRequest{
				TargetAgent: req.TargetAgent, NumExamples: req.NumExamples, Focus: req.Focus,
			}, op.CreatedBy, req.GatewayURL, header)
			return err
		case store.EvalGenerationExamples:
			if op.DatasetID == nil {
				return fmt.Errorf("example generation %s has no dataset", op.ID)
			}
			_, err := s.generateExamples(ctx, operationID, *op.DatasetID, GenerateExamplesRequest{
				TargetAgent: req.TargetAgent, NumExamples: req.NumExamples, Focus: req.Focus,
			}, op.CreatedBy, req.GatewayURL, header)
			return err
		default:
			return fmt.Errorf("unknown generation type %q", op.OperationType)
		}
	}
}

func encodeGenerationRequest(req generationRequest) (*string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode generation request: %w", err)
	}
	encoded := string(body)
	return &encoded, nil
}
