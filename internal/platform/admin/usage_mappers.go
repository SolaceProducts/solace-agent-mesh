// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const usageCurrency = "USD"

const msgUsageRead = "usage read"

var (
	usageBudgetSources = map[budget.Source]UsageBudgetSource{
		budget.SourceAssigned: UsageBudgetAssigned, budget.SourceDefault: UsageBudgetDefault,
	}
	usageBudgetStatuses = map[usage.Status]UsageBudgetStatus{
		usage.StatusOK: UsageBudgetOK, usage.StatusApproaching: UsageBudgetApproaching,
		usage.StatusExceeded: UsageBudgetExceeded, usage.StatusUnlimited: UsageBudgetUnlimited,
	}
	usageRootKinds = map[string]UsageRootKind{
		string(spend.RootAgent): UsageRootAgent, string(spend.RootWorkflow): UsageRootWorkflow,
	}
	usageNodeKinds = map[string]UsageNodeKind{
		"agent": UsageNodeAgent, "workflow": UsageNodeWorkflow, "tool": UsageNodeTool, "switch": UsageNodeSwitch,
		"loop": UsageNodeLoop, "map": UsageNodeMap, "decision": UsageNodeDecision,
	}
)

var spendReadRefusal = readRefusal{
	logMessage: "spend read refused",
	message:    "Reading every user's usage requires the " + rbac.ScopeReadUsage + " scope",
}

func toUsageSavingsResponse(s usage.Savings, scope UsageScope) UsageSavingsResponse {
	resp := UsageSavingsResponse{
		Scope: scope, DataAsOf: nullableRESTTimestamp(s.DataAsOf), HistoryComplete: s.HistoryComplete,
		Window: toUsageWindow(s.Window), Currency: usageCurrency,
		Calls: s.Calls, MeasuredCalls: s.MeasuredCalls, UnpricedCalls: s.UnpricedCalls,
	}
	if s.MeasuredCalls == 0 {
		return resp
	}
	resp.AvoidedInputTokens = new(s.AvoidedInputTokens)
	if s.PricedMeasuredCalls > 0 {
		resp.AvoidedCost = new(UsageMoney(s.AvoidedInputCost))
	}
	return resp
}

func toUsageSelfResponse(self usage.Self) UsageSelfResponse {
	return UsageSelfResponse{UsageUserResponse: toUsageUserResponse(self.User, UsageScopeSelf), Months: toUsageMonths(self.Months)}
}

func toUsageMonths(months []usage.Month) []UsageMonth {
	out := make([]UsageMonth, len(months))
	for i, m := range months {
		out[i] = UsageMonth{
			Month: time.UnixMilli(m.Start).UTC().Format("2006-01"), Cost: UsageMoney(m.Cost),
			BudgetAmount: moneyOrNil(m.BudgetAmount),
		}
		if m.PartialFrom != nil {
			out[i].PartialFromDate = new(time.UnixMilli(*m.PartialFrom).UTC().Format(time.DateOnly))
		}
	}
	return out
}

func toUsageModelResponse(s usage.Subject) UsageModelResponse {
	return UsageModelResponse{UsageSubjectResponse: toUsageSubjectResponse(s), Models: toUsageServedModels(s.Models)}
}

func parseUsageDate(text string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return t, nil
	}
	return time.Parse(time.DateOnly, text)
}

func moneyOrNil(v *int64) *UsageMoney {
	if v == nil {
		return nil
	}
	return new(UsageMoney(*v))
}

func toUsageWindow(w usage.Window) UsageWindow {
	return UsageWindow{StartTime: restTimestamp(w.Start), EndTime: restTimestamp(w.End)}
}

func toUsageCostByBucket(t store.UsageTotals) UsageCostByBucket {
	return UsageCostByBucket{
		UncachedInput: UsageMoney(t.UncachedInputCost), Output: UsageMoney(t.OutputCost),
		CacheRead: UsageMoney(t.CacheReadCost), CacheWrite: UsageMoney(t.CacheWriteCost),
	}
}

func toUsageTokens(t store.UsageTotals) UsageTokens {
	return UsageTokens{UncachedInput: t.UncachedInputTokens, Output: t.OutputTokens, CacheRead: t.CacheReadTokens, CacheWrite: t.CacheWriteTokens}
}

func toUsageDays(days []store.UsageDay) []UsageDay {
	out := make([]UsageDay, len(days))
	for i, d := range days {
		out[i] = UsageDay{
			Date: time.UnixMilli(d.Day).UTC().Format(time.DateOnly), Cost: UsageMoney(d.Cost), Activities: d.Activities,
			Tokens: UsageTokens{UncachedInput: d.UncachedInputTokens, Output: d.OutputTokens, CacheRead: d.CacheReadTokens, CacheWrite: d.CacheWriteTokens},
		}
	}
	return out
}

func toUsageBudget(b *usage.BudgetStanding) *UsageBudget {
	if b == nil {
		return nil
	}
	out := &UsageBudget{
		ID: b.Budget.ID, Name: b.Budget.Name, Source: usageBudgetSources[b.Budget.Source],
		Amount: moneyOrNil(b.Budget.Amount), WarningPercentage: b.Budget.WarnPercentage,
		EffectiveAmount: moneyOrNil(b.Budget.EffectiveAmount),
		PeriodStart:     restTimestamp(b.Standing.PeriodStart), PeriodEnd: restTimestamp(b.Standing.PeriodEnd),
		PeriodSpend: UsageMoney(b.Standing.PeriodSpend), UsagePercent: b.Standing.UsagePercent,
		Status: usageBudgetStatuses[b.Standing.Status],
	}
	if t := b.Budget.TopUp; t != nil {
		out.TopUp = &UsageTopUp{ID: t.ID, Amount: UsageMoney(t.Amount), ExpiresTime: restTimestamp(t.ExpiresTime)}
	}
	return out
}

func toUsageSummaryResponse(s usage.Summary) UsageSummaryResponse {
	resp := UsageSummaryResponse{
		Scope: UsageScopeTenant, DataAsOf: nullableRESTTimestamp(s.DataAsOf), HistoryComplete: s.HistoryComplete,
		Window: toUsageWindow(s.Window), Currency: usageCurrency,
		Totals: UsageTotals{
			Cost: UsageMoney(s.Totals.Cost), CostByBucket: toUsageCostByBucket(s.Totals), Tokens: toUsageTokens(s.Totals),
			Calls: s.Totals.Calls, UnpricedCalls: s.Totals.UnpricedCalls, Activities: s.Totals.Activities,
		},
		CostByKind: UsageCostByKind{
			Users: UsageMoney(s.KindCosts.Users), Agents: UsageMoney(s.KindCosts.Agents),
			Workflows: UsageMoney(s.KindCosts.Workflows), Models: UsageMoney(s.KindCosts.Models),
		},
		MeshBudget: toUsageBudget(s.MeshBudget),
		Daily:      toUsageDays(s.Daily),
	}
	if c := s.Compared; c != nil {
		resp.ComparisonWindow = new(toUsageWindow(c.Window))
		resp.Comparison = &UsageComparison{Cost: UsageMoney(c.Cost), ChangePercent: c.ChangePercent}
	}
	return resp
}

func toUsageRow(r usage.Row) UsageRowResponse {
	row := UsageRowResponse{
		ID: r.SubjectID, Name: new(r.Name),
		Cost: UsageMoney(r.Totals.Cost), Activities: r.Totals.Activities, CostPerActivity: moneyOrNil(r.CostPerActivity),
		ShareOfSpend: r.ShareOfSpend, CostByBucket: toUsageCostByBucket(r.Totals), Tokens: toUsageTokens(r.Totals),
		Calls: r.Totals.Calls, UnpricedCalls: r.Totals.UnpricedCalls,
	}
	if r.SubjectID == nil {
		row.BookedName = r.SubjectName
	}
	if r.Daily != nil {
		row.Daily = toUsageDays(r.Daily)
	}
	return row
}

func toUsageRows(rows []usage.Row) []UsageRowResponse {
	out := make([]UsageRowResponse, len(rows))
	for i, r := range rows {
		out[i] = toUsageRow(r)
	}
	return out
}

func toUsageModelRows(rows []usage.Row) []UsageModelRowResponse {
	out := make([]UsageModelRowResponse, len(rows))
	for i, r := range rows {
		out[i] = UsageModelRowResponse{UsageRowResponse: toUsageRow(r), Models: toUsageServedModels(r.Models)}
	}
	return out
}

func toUsageServedModels(models []store.UsageServedModel) []UsageServedModel {
	out := make([]UsageServedModel, len(models))
	for i, m := range models {
		out[i] = UsageServedModel{
			Name: m.Name, Provider: m.Provider, Cost: UsageMoney(m.Totals.Cost), Activities: m.Totals.Activities,
			CostByBucket: toUsageCostByBucket(m.Totals), Tokens: toUsageTokens(m.Totals),
			Calls: m.Totals.Calls, UnpricedCalls: m.Totals.UnpricedCalls,
		}
	}
	return out
}

func toUsageUserRows(rows []usage.Row) []UsageUserRowResponse {
	out := make([]UsageUserRowResponse, len(rows))
	for i, r := range rows {
		out[i] = UsageUserRowResponse{UsageRowResponse: toUsageRow(r), Budget: toUsageBudget(r.Budget)}
		out[i].BookedName = nil
		if r.SubjectID == nil || r.Name == "" {
			out[i].Name = nil
		}
		if r.Live {
			out[i].User = &UsageUser{Email: r.Email, IsSystemUser: r.SystemUser}
		}
		if u := r.BudgetUsage; u != nil && u.WindowBudget != nil && u.UsagePercent != nil {
			out[i].BudgetUsage = &UsageBudgetWindow{WindowBudget: UsageMoney(*u.WindowBudget), UsagePercent: *u.UsagePercent}
		}
	}
	return out
}

func toUsageSubjectTotals(t usage.SubjectTotals) UsageSubjectTotals {
	return UsageSubjectTotals{
		Cost: UsageMoney(t.Cost), Activities: t.Activities, CostPerActivity: moneyOrNil(t.CostPerActivity),
		ShareOfSpend: t.ShareOfSpend, CostByBucket: toUsageCostByBucket(t.UsageTotals), Tokens: toUsageTokens(t.UsageTotals),
		Calls: t.Calls, UnpricedCalls: t.UnpricedCalls,
	}
}

func subjectRef(s store.UsageSubject) (id, bookedName *string) {
	if s.ID != "" {
		return new(s.ID), nil
	}
	return nil, new(s.Name)
}

func toUsageSubjectResponse(s usage.Subject) UsageSubjectResponse {
	id, booked := subjectRef(s.Subject)
	return UsageSubjectResponse{
		Scope: UsageScopeTenant, DataAsOf: nullableRESTTimestamp(s.DataAsOf), HistoryComplete: s.HistoryComplete,
		ID: id, Name: s.Info.Name, BookedName: booked,
		Window: toUsageWindow(s.Window), Totals: toUsageSubjectTotals(s.Totals), Daily: toUsageDays(s.Daily),
	}
}

func toUsageWorkflowResponse(wf usage.Workflow) UsageWorkflowResponse {
	id, booked := subjectRef(wf.Subject.Subject)
	resp := UsageWorkflowResponse{
		Scope: UsageScopeTenant, DataAsOf: nullableRESTTimestamp(wf.DataAsOf), HistoryComplete: wf.HistoryComplete,
		ID: id, Name: wf.Info.Name, BookedName: booked,
		Window: toUsageWindow(wf.Window), Totals: toUsageSubjectTotals(wf.Totals), Daily: toUsageDays(wf.Daily),
		Nodes: make([]UsageWorkflowNode, len(wf.Nodes)),
	}
	if wf.Comparison != nil {
		resp.ComparisonWindow = new(toUsageWindow(*wf.Comparison))
	}
	for i, n := range wf.Nodes {
		resp.Nodes[i] = UsageWorkflowNode{NodeID: n.NodeID, Name: n.Name, Cost: UsageMoney(n.Cost), ShareOfWorkflow: n.ShareOfWorkflow, ChangePercent: n.ChangePercent}
		if n.Kind != nil {
			if kind, ok := usageNodeKinds[*n.Kind]; ok {
				resp.Nodes[i].Kind = &kind
			}
		}
	}
	return resp
}

func toUsageUserResponse(u usage.User, scope UsageScope) UsageUserResponse {
	resp := UsageUserResponse{
		Scope: scope, DataAsOf: nullableRESTTimestamp(u.DataAsOf), HistoryComplete: u.HistoryComplete,
		Window: toUsageWindow(u.Window), Totals: toUsageSubjectTotals(u.Totals),
		Daily: toUsageDays(u.Daily), Budget: toUsageBudget(u.Budget),
		TopRoots: make([]UsageRoot, len(u.TopRoots)), OtherCost: UsageMoney(u.OtherCost), OtherShareOfUser: u.OtherShareOfUser,
	}
	if u.Subject.Subject.ID != "" {
		resp.ID = new(u.Subject.Subject.ID)
		if u.Info.Name != "" {
			resp.Name = new(u.Info.Name)
		}
	}
	if u.Info.Live {
		resp.User = &UsageUser{Email: u.Info.Email, IsSystemUser: u.Info.SystemUser}
	}
	for i, r := range u.TopRoots {
		resp.TopRoots[i] = UsageRoot{Kind: usageRootKinds[r.Kind], ID: r.ID, BookedName: r.BookedName, Name: r.Name, Cost: UsageMoney(r.Cost), ShareOfUser: r.ShareOfUser}
	}
	return resp
}
