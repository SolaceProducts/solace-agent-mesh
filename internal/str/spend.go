// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const (
	missingSpendMessage       = "tool LLM call carries no spend attribution; its spend is not recorded"
	missingSpendWarnInterval  = 10 * time.Minute
	configLookupFailedMessage = "tool model configuration lookup failed; spend booked without it"
)

func (w *Worker) spendPublisher() *spend.Publisher {
	return w.spend.Get()
}

func (w *Worker) closeSpendPublisher(ctx context.Context) {
	if err := w.spend.Close(ctx); err != nil {
		w.logger.Warn("str spend publisher close failed", "error", err)
	}
}

type rawBindingResolver interface {
	ResolveWithRawOrFetch(ctx context.Context, alias string) (llm.Client, map[string]any, error)
}

func (w *Worker) resolveBinding(ctx context.Context, b runtime.ModelBindings, alias string, withConfigID bool) (llm.Client, string, error) {
	if !withConfigID {
		client, err := b.ResolveOrFetch(ctx, alias)
		return client, "", err
	}
	if rb, ok := b.(rawBindingResolver); ok {
		client, raw, err := rb.ResolveWithRawOrFetch(ctx, alias)
		return client, samapp.ModelConfigID(raw), err
	}
	client, err := b.ResolveOrFetch(ctx, alias)
	if err != nil {
		return nil, "", err
	}
	return client, w.boundConfigID(ctx, b, alias), nil
}

func (w *Worker) boundConfigID(ctx context.Context, b runtime.ModelBindings, alias string) string {
	raw, err := b.ResolveRawOrFetch(ctx, alias)
	if err != nil {
		if !errors.Is(err, bindings.ErrAliasUnbound) && ctx.Err() == nil {
			w.logger.Warn(configLookupFailedMessage, "alias", alias, "error", err)
		}
		return ""
	}
	return samapp.ModelConfigID(raw)
}

func invocationSpend(inv *remotetool.InvokeParams) *remotetool.SpendAttribution {
	if inv == nil {
		return nil
	}
	return inv.Spend
}

func (w *Worker) noteMissingSpend(toolName string, inv *remotetool.InvokeParams) {
	var agentName, taskID, traceID string
	if inv != nil {
		agentName, taskID, traceID = inv.AppName, inv.TaskID, inv.TraceID
	}
	if suppressed, warn := w.missingSpend.Admit(agentName + "/" + toolName); warn {
		w.logger.With("traceID", traceID, "tool", toolName, "taskID", taskID, "agentName", agentName).
			Warn(missingSpendMessage, "suppressed", suppressed)
	}
}

func pinSpendUser(a *remotetool.SpendAttribution, verified *auth.Identity, claimedUserID string) {
	if a == nil || verified == nil {
		return
	}
	a.UserID, a.UserSub = spend.VerifiedSpendUser(verified, claimedUserID)
}

func toolCallSpend(a *remotetool.SpendAttribution, toolName, modelAlias, modelConfigID string) *llm.SpendAttribution {
	if a == nil {
		return nil
	}
	return &llm.SpendAttribution{
		UserID:         a.UserID,
		UserSub:        a.UserSub,
		AgentID:        a.AgentID,
		AgentName:      a.AgentName,
		WorkflowID:     a.WorkflowID,
		WorkflowName:   a.WorkflowName,
		WorkflowNodeID: a.WorkflowNodeID,
		RootKind:       a.RootKind,
		RootID:         a.RootID,
		RootName:       a.RootName,
		RootNodeID:     a.RootNodeID,
		EntrypointID:   a.EntrypointID,
		EntrypointName: a.EntrypointName,
		Channel:        a.Channel,
		TriggerKind:    a.TriggerKind,
		TriggerID:      a.TriggerID,
		Surface:        string(spend.SurfaceTool),
		ToolName:       toolName,
		ModelKey:       modelAlias,
		ModelConfigID:  modelConfigID,
		TaskID:         a.TaskID,
		ParentTaskID:   a.ParentTaskID,
		TraceID:        a.TraceID,
		SessionID:      a.SessionID,
	}
}
