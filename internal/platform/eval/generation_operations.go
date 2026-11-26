// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const generationGrace = 30 * time.Second

const (
	msgExamplesGenerationFailed = "The AI could not generate examples. Try again."
	msgDatasetGenerationFailed  = "The AI could not generate the dataset. Try again."
	msgGenerationError          = "Generation failed. Try again."
	msgGenerationExpired        = "Generation did not finish in time. Try again."
)

func (s *DatasetService) WithGenerationOperations(ops *store.EvalGenerationOperationRepo) *DatasetService {
	s.generations = ops
	return s
}

func (s *DatasetService) StartDatasetGeneration(
	ctx context.Context,
	req GenerateDatasetRequest,
	userID, requesterID, gatewayURL, authHeader string,
) (*store.EvalGenerationOperation, error) {
	if err := s.requireGeneration("AI dataset generation is not configured. Configure a broker to enable it."); err != nil {
		return nil, err
	}
	if err := validateGenerateDatasetRequest(req); err != nil {
		return nil, err
	}
	request := generationRequest{TargetAgent: req.TargetAgent, NumExamples: req.NumExamples, Focus: req.Focus, GatewayURL: gatewayURL, RequesterID: requesterID}
	return s.startGeneration(ctx, store.EvalGenerationDataset, nil, userID, request,
		func(ctx context.Context, operationID string) error {
			_, err := s.generateDataset(ctx, operationID, req, userID, gatewayURL, authHeader)
			return err
		})
}

func (s *DatasetService) StartExampleGeneration(
	ctx context.Context,
	datasetID string,
	req GenerateExamplesRequest,
	userID, requesterID, gatewayURL, authHeader string,
) (*store.EvalGenerationOperation, error) {
	if err := s.requireGeneration("AI example generation is not configured. Configure a broker to enable it."); err != nil {
		return nil, err
	}
	if err := validateGenerateExamplesRequest(req); err != nil {
		return nil, err
	}
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	count, err := s.examples.CountByDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("count examples: %w", err)
	}
	if err := exampleCapError(count, req.NumExamples, s.maxExamplesCap()); err != nil {
		return nil, err
	}
	request := generationRequest{TargetAgent: req.TargetAgent, NumExamples: req.NumExamples, Focus: req.Focus, GatewayURL: gatewayURL, RequesterID: requesterID}
	return s.startGeneration(ctx, store.EvalGenerationExamples, &datasetID, userID, request,
		func(ctx context.Context, operationID string) error {
			_, err := s.generateExamples(ctx, operationID, datasetID, req, userID, gatewayURL, authHeader)
			return err
		})
}

func (s *DatasetService) GetGeneration(ctx context.Context, id string) (*store.EvalGenerationOperation, error) {
	s.failExpiredGenerations(ctx)
	op, err := s.generations.Get(ctx, id)
	if errors.Is(err, store.ErrEvalNotFound) {
		return nil, &NotFoundError{Resource: "EvalGenerationOperation", ID: id}
	}
	return op, err
}

func (s *DatasetService) ListGenerations(ctx context.Context, datasetID string, pageNumber, pageSize int, oldestFirst bool) ([]*store.EvalGenerationOperation, int, error) {
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return nil, 0, mapDatasetNotFound(err, datasetID)
	}
	s.failExpiredGenerations(ctx)
	return s.generations.ListByDataset(ctx, datasetID, pageNumber, pageSize, oldestFirst)
}

func (s *DatasetService) requireGeneration(disabled string) error {
	if s.aiGen == nil || s.generations == nil {
		return &FeatureDisabledError{Message: disabled}
	}
	return nil
}

type generateFunc func(ctx context.Context, operationID string) error

func (s *DatasetService) startGeneration(
	ctx context.Context,
	operationType string,
	datasetID *string,
	userID string,
	request generationRequest,
	generate generateFunc,
) (*store.EvalGenerationOperation, error) {
	s.failExpiredGenerations(ctx)
	encoded, err := encodeGenerationRequest(request)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	op := &store.EvalGenerationOperation{
		ID:            idgen.New(),
		OperationType: operationType,
		DatasetID:     datasetID,
		Status:        store.EvalGenerationInProgress,
		CreatedBy:     userID,
		CreatedTime:   now.UnixMilli(),
		Deadline:      now.Add(s.aiGen.Timeout + 2*generationGrace).UnixMilli(),
		Request:       encoded,
	}
	if err := s.generations.Create(ctx, op); err != nil {
		if errors.Is(err, store.ErrEvalGenerationActive) {
			return nil, &ConflictError{Message: "This dataset is already generating examples. Wait for it to finish."}
		}
		return nil, fmt.Errorf("record generation: %w", err)
	}

	if s.dispatch != nil {
		if err := s.dispatch.trigger.PublishGenerationTrigger(ctx, op.ID); err != nil {
			s.logger.Warn("eval: publishing the generation trigger failed", "operationID", op.ID, "error", err)
		}
		return op, nil
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.aiGen.Timeout+generationGrace)
	go func() {
		defer cancel()
		s.finishGeneration(runCtx, op, recovering(generate))
	}()
	return op, nil
}

func (s *DatasetService) finishGeneration(ctx context.Context, op *store.EvalGenerationOperation, generate generateFunc) {
	err := generate(ctx, op.ID)
	if err == nil {
		return
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		s.logger.Info("eval: generation left for a takeover", "operationID", op.ID, "operationType", op.OperationType)
		return
	}
	if errors.Is(err, store.ErrEvalGenerationNotInProgress) {
		s.logger.Info("eval: generation finished after its operation ended; nothing saved", "operationID", op.ID, "operationType", op.OperationType)
		return
	}
	errorID := idgen.New()
	s.logGenerationFailure(op, errorID, err)
	s.failGeneration(context.WithoutCancel(ctx), op, generationFailureMessage(err, op.OperationType), errorID)
}

func (s *DatasetService) failGeneration(ctx context.Context, op *store.EvalGenerationOperation, message, errorID string) {
	if err := s.generations.Fail(ctx, op.ID, message, errorID, time.Now().UnixMilli()); err != nil {
		s.logger.Error("eval: recording generation result failed", "operationID", op.ID, "operationType", op.OperationType, "error", err)
	}
}

func (s *DatasetService) logGenerationFailure(op *store.EvalGenerationOperation, errorID string, err error) {
	var panicked *generationPanic
	if errors.As(err, &panicked) {
		s.logger.Error("eval: generation panicked", "operationID", op.ID, "operationType", op.OperationType, "errorId", errorID, "panic", panicked.value, "stack", panicked.stack)
		return
	}
	s.logger.Warn("eval: generation failed", "operationID", op.ID, "operationType", op.OperationType, "errorId", errorID, "error", err)
}

type generationPanic struct {
	value string
	stack string
}

func (p *generationPanic) Error() string { return "generation panicked: " + p.value }

func recovering(generate generateFunc) generateFunc {
	return func(ctx context.Context, operationID string) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = &generationPanic{value: fmt.Sprint(r), stack: string(debug.Stack())}
			}
		}()
		return generate(ctx, operationID)
	}
}

func generationFailureMessage(err error, operationType string) string {
	br, ok := errors.AsType[*BusinessRuleViolationError](err)
	switch {
	case !ok:
		return msgGenerationError
	case br.Code == CodeExampleCapReached, br.Code == CodeDatasetGenInvalidResponse, br.Code == CodeExampleGenInvalidResponse:
		return br.Message
	case operationType == store.EvalGenerationDataset:
		return msgDatasetGenerationFailed
	default:
		return msgExamplesGenerationFailed
	}
}

func (s *DatasetService) failExpiredGenerations(ctx context.Context) {
	ids, err := s.generations.FailExpired(ctx, time.Now().UnixMilli(), msgGenerationExpired)
	if err != nil {
		s.logger.Warn("eval: expiring generations failed", "error", err)
		return
	}
	for _, id := range ids {
		s.logger.Warn("eval: generation expired", "operationID", id, "errorId", id)
	}
}

func exampleCapError(existing, requested, maxCap int) error {
	if existing+requested <= maxCap {
		return nil
	}
	return &BusinessRuleViolationError{
		Code: CodeExampleCapReached,
		Message: fmt.Sprintf(
			"Dataset already has %d examples; generating %d more would exceed the %d-example cap.",
			existing, requested, maxCap,
		),
	}
}
