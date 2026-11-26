// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func (g *GatewayCore) ReportSubmitRefusal(reason string) {
	if g == nil || reason == "" {
		return
	}
	g.ReportSubmitRejected(SubmitRejection{Reason: reason})
}

func SubmitFailureReason(err error) string {
	switch {
	case IsAuthorizationDenial(err):
		return taxonomy.RejectForbidden
	case IsBudgetRefusal(err):
		return taxonomy.RejectBudgetExhausted
	case IsBudgetUnconfirmed(err):
		return taxonomy.RejectBudgetUnconfirmed
	case IsAgentNotAvailable(err):
		return taxonomy.RejectAgentNotFound
	}
	return taxonomy.RejectInternal
}

var ErrAgentNotAvailable = errors.New("agent not available")

func IsAgentNotAvailable(err error) bool {
	return errors.Is(err, ErrAgentNotAvailable)
}

func IsBudgetRefusal(err error) bool {
	return errors.Is(err, budget.ErrExhausted)
}

func IsBudgetUnconfirmed(err error) bool {
	return errors.Is(err, budget.ErrUnconfirmed)
}

func BudgetRefusal(err error) (*budget.ExhaustedError, bool) {
	return errors.AsType[*budget.ExhaustedError](err)
}

const (
	BudgetExhaustedErrorCode   = -32029
	BudgetUnconfirmedErrorCode = -32030
)

func BudgetRefusalCode(err error) int {
	if _, spent := BudgetRefusal(err); spent {
		return BudgetExhaustedErrorCode
	}
	return BudgetUnconfirmedErrorCode
}

func BudgetRefusalTier(err error) string {
	refusal, ok := BudgetRefusal(err)
	if !ok {
		return ""
	}
	return string(refusal.Tier)
}

func LogSubmitFailure(logger *slog.Logger, faultLevel slog.Level, err error, args ...any) {
	if IsBudgetRefusal(err) {
		logger.Debug("task refused: a budget is spent", append(args, "budgetTier", BudgetRefusalTier(err))...)
		return
	}
	if IsBudgetUnconfirmed(err) {
		logger.Debug("task refused: the budget state is not confirmed yet", args...)
		return
	}
	if IsAgentNotAvailable(err) {
		logger.Debug("task refused: its agent is not deployed", append(args, "error", err)...)
		return
	}
	logger.Log(context.Background(), faultLevel, "failed to submit task", append(args, "error", err)...)
}

func IsBudgetStop(a2aErr *a2a.Error) bool {
	category, _ := llmFailureData(a2aErr)
	return category == taxonomy.ErrorCategoryBudgetExhausted
}

func IsBudgetStoppedTask(task *a2a.Task) bool {
	return taskFailureCategory(task) == taxonomy.ErrorCategoryBudgetExhausted
}

func taskFailureCategory(task *a2a.Task) string {
	if task == nil {
		return ""
	}
	category, _ := task.Metadata[a2a.ErrorDataLLMErrorCategory].(string)
	return category
}

func llmFailureData(a2aErr *a2a.Error) (category, provider string) {
	if a2aErr == nil {
		return "", ""
	}
	data, ok := a2aErr.Data.(map[string]any)
	if !ok {
		return "", ""
	}
	category, _ = data[a2a.ErrorDataLLMErrorCategory].(string)
	provider, _ = data[a2a.ErrorDataLLMProvider].(string)
	return category, provider
}

func IsAuthorizationDenial(err error) bool {
	return errors.Is(err, ErrAgentDelegationDenied) || errors.Is(err, ErrIdentityUnauthorized)
}
