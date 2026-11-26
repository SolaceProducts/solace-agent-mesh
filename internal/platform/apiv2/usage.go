// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

const signedInReasonOwnUsage = "own usage: answers only the caller's own spend and savings, selected from the caller's identity, so it needs no scope"

const (
	usagePathSummary     = "/usage/summary"
	usagePathAgents      = "/usage/agents"
	usagePathWorkflows   = "/usage/workflows"
	usagePathUsers       = "/usage/users"
	usagePathModels      = "/usage/models"
	usagePathSavings     = "/usage/savings"
	usagePathSelf        = "/userUsage"
	usagePathSelfSavings = usagePathSelf + "/savings"

	usagePathByName          = "/byName"
	usagePathWorkflowsByName = usagePathWorkflows + usagePathByName
	usagePathAgentsByName    = usagePathAgents + usagePathByName
	usagePathModelsByName    = usagePathModels + usagePathByName

	usageTypeAgent    = "agentUsage"
	usageTypeWorkflow = "workflowUsage"
	usageTypeModel    = "modelUsage"
	usageTypeUser     = "userUsage"

	msgUsageBusy     = "Usage figures took too long to read; retry shortly, or choose a shorter date range"
	msgUsageNotFound = "No usage subject has that id or name"

	usageRetryAfter = 30 * time.Second
)

type UsageSummary struct {
	Scope            string           `json:"scope" example:"tenant"`
	DataAsOf         Instant          `json:"dataAsOf"`
	HistoryComplete  bool             `json:"historyComplete" example:"true"`
	Window           UsageWindow      `json:"window"`
	ComparisonWindow *UsageWindow     `json:"comparisonWindow"`
	Currency         string           `json:"currency" example:"USD"`
	Totals           UsageTotals      `json:"totals"`
	CostByKind       UsageCostByKind  `json:"costByKind"`
	Comparison       *UsageComparison `json:"comparison"`
	MeshBudget       *UsageBudget     `json:"meshBudget"`
	Daily            []UsageDay       `json:"daily"`
}

type SelfUsage struct {
	UserID           *string            `json:"userId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Scope            string             `json:"scope" example:"self"`
	DataAsOf         Instant            `json:"dataAsOf"`
	HistoryComplete  bool               `json:"historyComplete" example:"true"`
	Name             *string            `json:"name" example:"Jane Doe"`
	User             *UsageUser         `json:"user"`
	Window           UsageWindow        `json:"window"`
	Totals           UsageSubjectTotals `json:"totals"`
	Daily            []UsageDay         `json:"daily"`
	Budget           *UsageBudget       `json:"budget"`
	TopRoots         []UsageRoot        `json:"topRoots"`
	OtherCost        string             `json:"otherCost" example:"0.0000000000"`
	OtherShareOfUser float64            `json:"otherShareOfUser" example:"0"`
	Months           []UsageMonth       `json:"months"`
}

type UsageSavings struct {
	Scope              string      `json:"scope" example:"tenant"`
	DataAsOf           Instant     `json:"dataAsOf"`
	HistoryComplete    bool        `json:"historyComplete" example:"true"`
	Window             UsageWindow `json:"window"`
	Currency           string      `json:"currency" example:"USD"`
	AvoidedInputTokens *int64      `json:"avoidedInputTokens" example:"52000"`
	AvoidedCost        *string     `json:"avoidedCost" example:"0.1560000000"`
	Calls              int64       `json:"calls" example:"240"`
	MeasuredCalls      int64       `json:"measuredCalls" example:"180"`
	UnpricedCalls      int64       `json:"unpricedCalls" example:"2"`
}

type UsageWindow struct {
	StartTime Instant `json:"startTime"`
	EndTime   Instant `json:"endTime"`
}

type UsageCostByBucket struct {
	UncachedInput string `json:"uncachedInput" example:"12.5000000000"`
	Output        string `json:"output" example:"30.2500000000"`
	CacheRead     string `json:"cacheRead" example:"1.1000000000"`
	CacheWrite    string `json:"cacheWrite" example:"0.4000000000"`
}

type UsageTokens struct {
	UncachedInput int64 `json:"uncachedInput" example:"120000"`
	Output        int64 `json:"output" example:"45000"`
	CacheRead     int64 `json:"cacheRead" example:"300000"`
	CacheWrite    int64 `json:"cacheWrite" example:"8000"`
}

type UsageTotals struct {
	Cost          string            `json:"cost" example:"44.2500000000"`
	CostByBucket  UsageCostByBucket `json:"costByBucket"`
	Tokens        UsageTokens       `json:"tokens"`
	Calls         int64             `json:"calls" example:"310"`
	UnpricedCalls int64             `json:"unpricedCalls" example:"2"`
	Activities    int64             `json:"activities" example:"42"`
}

type UsageSubjectTotals struct {
	Cost            string            `json:"cost" example:"44.2500000000"`
	Activities      int64             `json:"activities" example:"42"`
	CostPerActivity *string           `json:"costPerActivity" example:"1.0535714285"`
	ShareOfSpend    *float64          `json:"shareOfSpend" example:"12.5"`
	CostByBucket    UsageCostByBucket `json:"costByBucket"`
	Tokens          UsageTokens       `json:"tokens"`
	Calls           int64             `json:"calls" example:"310"`
	UnpricedCalls   int64             `json:"unpricedCalls" example:"2"`
}

type UsageDay struct {
	Date       string      `json:"date" example:"2026-10-03"`
	Cost       string      `json:"cost" example:"3.1000000000"`
	Tokens     UsageTokens `json:"tokens"`
	Activities int64       `json:"activities" example:"4"`
}

type UsageComparison struct {
	Cost          string   `json:"cost" example:"40.0000000000"`
	ChangePercent *float64 `json:"changePercent" example:"10.625"`
}

type UsageCostByKind struct {
	Users     string `json:"users" example:"44.2500000000"`
	Agents    string `json:"agents" example:"40.0000000000"`
	Workflows string `json:"workflows" example:"12.0000000000"`
	Models    string `json:"models" example:"44.2500000000"`
}

type UsageTopUp struct {
	Amount      string  `json:"amount" example:"50.0000000000"`
	ExpiresTime Instant `json:"expiresTime"`
}

type UsageBudget struct {
	ID                string      `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
	Name              string      `json:"name" example:"Engineering monthly"`
	Source            string      `json:"source" example:"assigned"`
	Amount            *string     `json:"amount" example:"250.0000000000"`
	WarningPercentage *int        `json:"warningPercentage" example:"80"`
	TopUp             *UsageTopUp `json:"topUp"`
	EffectiveAmount   *string     `json:"effectiveAmount" example:"300.0000000000"`
	PeriodStart       Instant     `json:"periodStart"`
	PeriodEnd         Instant     `json:"periodEnd"`
	PeriodSpend       string      `json:"periodSpend" example:"120.0000000000"`
	UsagePercent      *float64    `json:"usagePercent" example:"40"`
	Status            string      `json:"status" example:"ok"`
}

type UsageBudgetWindow struct {
	WindowBudget string  `json:"windowBudget" example:"250.0000000000"`
	UsagePercent float64 `json:"usagePercent" example:"17.7"`
}

type UsageUser struct {
	Email            *string `json:"email" example:"jane.doe@example.com"`
	IsSystemUser     bool    `json:"isSystemUser" example:"false"`
	IsServiceAccount bool    `json:"isServiceAccount" example:"false"`
}

type UsageServedModel struct {
	Name          *string           `json:"name" example:"claude-sonnet-4-6"`
	Provider      *string           `json:"provider" example:"anthropic"`
	Cost          string            `json:"cost" example:"20.0000000000"`
	Activities    int64             `json:"activities" example:"18"`
	CostByBucket  UsageCostByBucket `json:"costByBucket"`
	Tokens        UsageTokens       `json:"tokens"`
	Calls         int64             `json:"calls" example:"150"`
	UnpricedCalls int64             `json:"unpricedCalls" example:"0"`
}

type UsageRoot struct {
	Kind        string  `json:"kind" example:"agent"`
	ID          *string `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e60"`
	Name        string  `json:"name" example:"Support triage"`
	BookedName  *string `json:"bookedName" example:"support-triage"`
	Cost        string  `json:"cost" example:"8.0000000000"`
	ShareOfUser float64 `json:"shareOfUser" example:"64"`
}

type UsageMonth struct {
	Month           string  `json:"month" example:"2026-10"`
	Cost            string  `json:"cost" example:"12.5000000000"`
	BudgetAmount    *string `json:"budgetAmount" example:"250.0000000000"`
	PartialFromDate *string `json:"partialFromDate" example:"2026-08-18"`
}

type usageWindowRequest struct {
	StrictRequest
	Period    string `query:"period,nonempty" json:"-" example:"last_30_days"`
	StartDate string `query:"startDate,nonempty" json:"-" example:"2026-10-01"`
	EndDate   string `query:"endDate,nonempty" json:"-" example:"2026-10-15"`
}

type usageHandler struct {
	svc    *usage.Service
	logger *slog.Logger
}

func usageRoutes(deps *entplatform.RouteDeps, logger *slog.Logger) []Route {
	if deps.UsageSvc == nil {
		return nil
	}
	h := &usageHandler{svc: deps.UsageSvc, logger: logger}
	read := func(path, id, summary, text string, handler Handler) Route {
		return Route{Method: http.MethodGet, Path: path, Gate: GateUsageRead, Stability: StabilityDraft,
			Doc:     Doc{ID: id, Summary: summary, Text: text, Tag: usageTag, Errors: []int{http.StatusServiceUnavailable}},
			Handler: handler}
	}
	return []Route{
		read(usagePathSummary, "getUsageSummary", "Get the usage summary",
			"Returns the whole deployment's spend in the window, with its comparison window, its daily spend and the mesh budget's standing.",
			Handle(h.summary, "Unable to get the usage summary")),
		read(usagePathWorkflows, "listWorkflowUsage", "Get a list of workflow usage",
			"Returns one page of workflows with their spend in the window, costliest first unless a sort is given.",
			Handle(h.listWorkflows, "Unable to list workflow usage")),
		read(usagePathAgents, "listAgentUsage", "Get a list of agent usage",
			"Returns one page of agents with their spend in the window, costliest first unless a sort is given.",
			Handle(h.listAgents, "Unable to list agent usage")),
		returnsIsSystemUser(read(usagePathUsers, "listUserUsage", "Get a list of user usage",
			"Returns one page of users with their spend in the window and the budget that applies to each, costliest first unless a sort is given.",
			Handle(h.listUsers, "Unable to list user usage"))),
		read(usagePathModels, "listModelUsage", "Get a list of model usage",
			"Returns one page of models with their spend in the window and the provider models that served them, costliest first unless a sort is given.",
			Handle(h.listModels, "Unable to list model usage")),
		read(usagePathWorkflows+"/{id}", "getWorkflowUsage", "Get the usage of a workflow",
			"Returns one workflow's spend in the window, by top-level node.",
			Handle(h.workflowByID, "Unable to get workflow usage")),
		read(usagePathWorkflowsByName, "getWorkflowUsageByName", "Get the usage of a workflow by the name it was booked under",
			"Returns the spend booked under a workflow name, for a workflow that Agent Mesh has no record of, such as one defined in YAML. Its usage row has a null id and gives the name in bookedName.",
			Handle(h.workflowByName, "Unable to get workflow usage")),
		read(usagePathAgents+"/{id}", "getAgentUsage", "Get the usage of an agent",
			"Returns one agent's spend in the window.",
			Handle(h.agentByID, "Unable to get agent usage")),
		read(usagePathAgentsByName, "getAgentUsageByName", "Get the usage of an agent by the name it was booked under",
			"Returns the spend booked under an agent name, for an agent that Agent Mesh has no record of, such as one defined in YAML. Its usage row has a null id and gives the name in bookedName.",
			Handle(h.agentByName, "Unable to get agent usage")),
		read(usagePathModels+"/{id}", "getModelUsage", "Get the usage of a model",
			"Returns one model's spend in the window and the provider models that served it.",
			Handle(h.modelByID, "Unable to get model usage")),
		read(usagePathModelsByName, "getModelUsageByName", "Get the usage of a model by the name it was booked under",
			"Returns the spend booked under a model name, for a model that Agent Mesh has no configuration for, such as one a call defined inline. Its usage row has a null id and gives the name in bookedName.",
			Handle(h.modelByName, "Unable to get model usage")),
		returnsIsSystemUser(read(usagePathUsers+"/{id}", "getUserUsage", "Get the usage of a user",
			"Returns one user's spend in the window, the budget that applies to them and the agents and workflows their tasks cost most at.",
			Handle(h.userByID, "Unable to get user usage"))),
		{Method: http.MethodGet, Path: usagePathSelf, Gate: GateSignedIn, Stability: StabilityDraft,
			Deprecation: isSystemUserFieldDeprecation,
			Doc: Doc{ID: "getOwnUsage", Summary: "Get the caller's own usage",
				Text: "Returns the caller's own spend in the window, the budget it is judged against and their last three months. A caller who has spent nothing gets a report of zeros.",
				Tag:  userUsageTag, Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.self, "Unable to get your usage")},
		read(usagePathSavings, "getUsageSavings", "Get the Smart Context Management savings",
			"Returns an estimate of the input tokens Smart Context Management kept out of the model across the whole deployment in the window, and what they would have cost.",
			Handle(h.savings, "Unable to get the usage savings")),
		{Method: http.MethodGet, Path: usagePathSelfSavings, Gate: GateSignedIn, Stability: StabilityDraft,
			Doc: Doc{ID: "getOwnUsageSavings", Summary: "Get the caller's own Smart Context Management savings",
				Text: "Returns an estimate of the input tokens Smart Context Management kept out of the model on the caller's own calls in the window, and what they would have cost.",
				Tag:  userUsageTag, Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.selfSavings, "Unable to get your usage savings")},
	}
}

func (h *usageHandler) summary(ctx context.Context, req usageWindowRequest) (UsageSummary, error) {
	problems := newQueryProblems()
	window := problems.window(req, h.svc.Now())
	if err := problems.err(); err != nil {
		return UsageSummary{}, err
	}
	sum, err := h.svc.Summary(ctx, window)
	if err != nil {
		return UsageSummary{}, usageError(err)
	}
	h.audit(ctx, usagePathSummary, "")
	return toUsageSummary(entplatform.UsageSummaryDTO(sum)), nil
}

func (h *usageHandler) self(ctx context.Context, req usageWindowRequest) (SelfUsage, error) {
	problems := newQueryProblems()
	window := problems.window(req, h.svc.Now())
	if err := problems.err(); err != nil {
		return SelfUsage{}, err
	}
	self, err := h.svc.Self(ctx, callerOf(ctx), window)
	if err != nil {
		return SelfUsage{}, usageError(err)
	}
	return toSelfUsage(entplatform.UsageSelfDTO(self)), nil
}

func (h *usageHandler) savings(ctx context.Context, req usageWindowRequest) (UsageSavings, error) {
	problems := newQueryProblems()
	window := problems.window(req, h.svc.Now())
	if err := problems.err(); err != nil {
		return UsageSavings{}, err
	}
	savings, err := h.svc.Savings(ctx, window)
	if err != nil {
		return UsageSavings{}, usageError(err)
	}
	h.audit(ctx, usagePathSavings, "")
	return toUsageSavings(entplatform.UsageSavingsDTO(savings)), nil
}

func (h *usageHandler) selfSavings(ctx context.Context, req usageWindowRequest) (UsageSavings, error) {
	problems := newQueryProblems()
	window := problems.window(req, h.svc.Now())
	if err := problems.err(); err != nil {
		return UsageSavings{}, err
	}
	savings, err := h.svc.SelfSavings(ctx, callerOf(ctx), window)
	if err != nil {
		return UsageSavings{}, usageError(err)
	}
	return toUsageSavings(entplatform.UsageSelfSavingsDTO(savings)), nil
}

func callerOf(ctx context.Context) usage.Caller {
	if identity, ok := auth.SubjectFromContext(ctx); ok && identity != nil {
		return usage.Caller{PlatformUserID: identity.PlatformUserID(), Subject: identity.UserID()}
	}
	return usage.Caller{}
}

func (h *usageHandler) audit(ctx context.Context, path, subjectID string, attrs ...any) {
	entplatform.AuditUsageRead(ctx, h.logger, http.MethodGet+" "+platpkg.APIV2Prefix+path, subjectID, attrs...)
}

type queryProblems struct {
	syntax, semantic map[string][]string
}

func newQueryProblems() queryProblems {
	return queryProblems{syntax: map[string][]string{}, semantic: map[string][]string{}}
}

func (p queryProblems) named(field string) bool {
	return len(p.syntax[field]) > 0 || len(p.semantic[field]) > 0
}

func (p queryProblems) addSyntax(field, problem string) {
	p.syntax[field] = append(p.syntax[field], problem)
}

func (p queryProblems) addSemantic(field, problem string) {
	p.semantic[field] = append(p.semantic[field], problem)
}

func (p queryProblems) addService(problems usage.ValidationErrors) {
	named := map[string]bool{}
	for _, v := range problems {
		named[v.Field] = named[v.Field] || p.named(v.Field)
	}
	for _, v := range problems {
		if !named[v.Field] {
			p.addSemantic(v.Field, v.Message)
		}
	}
}

func (p queryProblems) window(req usageWindowRequest, now time.Time) usage.WindowRequest {
	window := p.windowSyntax(req)
	if _, err := usage.ResolveWindow(window, now); err != nil {
		p.addService(usage.Problems(err))
	}
	return window
}

func (p queryProblems) windowSyntax(req usageWindowRequest) usage.WindowRequest {
	window := usage.WindowRequest{Period: usage.Period(req.Period)}
	for _, d := range []struct {
		field, value string
		into         **time.Time
	}{{"startDate", req.StartDate, &window.StartDate}, {"endDate", req.EndDate, &window.EndDate}} {
		if d.value == "" {
			continue
		}
		t, err := entplatform.ParseUsageDate(d.value)
		if err != nil {
			p.addSyntax(d.field, "must be an ISO 8601 date or date-time")
			continue
		}
		*d.into = &t
	}
	return window
}

func (p queryProblems) err() error {
	switch {
	case len(p.syntax) > 0:
		details := p.syntax
		for field, problems := range p.semantic {
			details[field] = append(details[field], problems...)
		}
		return &CallerError{Status: http.StatusBadRequest, Message: msgInvalidQueryParam, Details: details}
	case len(p.semantic) > 0:
		return &CallerError{Status: http.StatusUnprocessableEntity, Message: msgInvalidQueryParam, Details: p.semantic}
	}
	return nil
}

func usageError(err error) error {
	if problems := usage.Problems(err); len(problems) > 0 {
		p := newQueryProblems()
		p.addService(problems)
		return p.err()
	}
	cause := entplatform.UsageReadCause(err)
	switch {
	case errors.Is(err, usage.ErrNotFound):
		return &CallerError{Status: http.StatusNotFound, Message: msgUsageNotFound, Cause: err}
	case dbutil.IsBusy(err):
		return RetryAfter(&CallerError{Status: http.StatusServiceUnavailable, Message: msgUsageBusy, Cause: cause}, usageRetryAfter)
	}
	return cause
}

func (p queryProblems) checkName(field, value string) {
	if len(value) > usageMaxNameBytes {
		p.addSemantic(field, fmt.Sprintf("must be at most %d bytes", usageMaxNameBytes))
	}
}

func usageMoney(m entplatform.UsageMoney) string {
	return pricing.FormatCost(int64(m))
}

func usageMoneyOrNil(m *entplatform.UsageMoney) *string {
	if m == nil {
		return nil
	}
	return new(usageMoney(*m))
}

func toUsageWindow(w entplatform.UsageWindow) UsageWindow {
	return UsageWindow{StartTime: Instant(w.StartTime), EndTime: Instant(w.EndTime)}
}

func toUsageWindowOrNil(w *entplatform.UsageWindow) *UsageWindow {
	if w == nil {
		return nil
	}
	return new(toUsageWindow(*w))
}

func toUsageCostByBucket(c entplatform.UsageCostByBucket) UsageCostByBucket {
	return UsageCostByBucket{UncachedInput: usageMoney(c.UncachedInput), Output: usageMoney(c.Output),
		CacheRead: usageMoney(c.CacheRead), CacheWrite: usageMoney(c.CacheWrite)}
}

func toUsageTokens(t entplatform.UsageTokens) UsageTokens {
	return UsageTokens{UncachedInput: t.UncachedInput, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite}
}

func toUsageSubjectTotals(t entplatform.UsageSubjectTotals) UsageSubjectTotals {
	return UsageSubjectTotals{
		Cost: usageMoney(t.Cost), Activities: t.Activities, CostPerActivity: usageMoneyOrNil(t.CostPerActivity),
		ShareOfSpend: t.ShareOfSpend, CostByBucket: toUsageCostByBucket(t.CostByBucket), Tokens: toUsageTokens(t.Tokens),
		Calls: t.Calls, UnpricedCalls: t.UnpricedCalls,
	}
}

func toUsageDays(days []entplatform.UsageDay) []UsageDay {
	if days == nil {
		return nil
	}
	out := make([]UsageDay, len(days))
	for i, d := range days {
		out[i] = UsageDay{Date: d.Date, Cost: usageMoney(d.Cost), Tokens: toUsageTokens(d.Tokens), Activities: d.Activities}
	}
	return out
}

func toUsageBudget(b *entplatform.UsageBudget) *UsageBudget {
	if b == nil {
		return nil
	}
	out := &UsageBudget{
		ID: b.ID, Name: b.Name, Source: string(b.Source), Amount: usageMoneyOrNil(b.Amount),
		WarningPercentage: b.WarningPercentage, EffectiveAmount: usageMoneyOrNil(b.EffectiveAmount),
		PeriodStart: Instant(b.PeriodStart), PeriodEnd: Instant(b.PeriodEnd), PeriodSpend: usageMoney(b.PeriodSpend),
		UsagePercent: b.UsagePercent, Status: string(b.Status),
	}
	if t := b.TopUp; t != nil {
		out.TopUp = &UsageTopUp{Amount: usageMoney(t.Amount), ExpiresTime: Instant(t.ExpiresTime)}
	}
	return out
}

func toUsageUser(u *entplatform.UsageUser) *UsageUser {
	if u == nil {
		return nil
	}
	return &UsageUser{Email: u.Email, IsSystemUser: u.IsSystemUser, IsServiceAccount: u.IsSystemUser}
}

func toUsageServedModels(models []entplatform.UsageServedModel) []UsageServedModel {
	out := make([]UsageServedModel, len(models))
	for i, m := range models {
		out[i] = UsageServedModel{
			Name: m.Name, Provider: m.Provider, Cost: usageMoney(m.Cost), Activities: m.Activities,
			CostByBucket: toUsageCostByBucket(m.CostByBucket), Tokens: toUsageTokens(m.Tokens),
			Calls: m.Calls, UnpricedCalls: m.UnpricedCalls,
		}
	}
	return out
}

func toUsageRoots(roots []entplatform.UsageRoot) []UsageRoot {
	out := make([]UsageRoot, len(roots))
	for i, r := range roots {
		out[i] = UsageRoot{Kind: string(r.Kind), ID: r.ID, Name: r.Name, BookedName: r.BookedName,
			Cost: usageMoney(r.Cost), ShareOfUser: r.ShareOfUser}
	}
	return out
}

func toUsageSummary(s entplatform.UsageSummaryResponse) UsageSummary {
	out := UsageSummary{
		Scope: string(s.Scope), DataAsOf: InstantOf(s.DataAsOf), HistoryComplete: s.HistoryComplete,
		Window: toUsageWindow(s.Window), ComparisonWindow: toUsageWindowOrNil(s.ComparisonWindow), Currency: s.Currency,
		Totals: UsageTotals{
			Cost: usageMoney(s.Totals.Cost), CostByBucket: toUsageCostByBucket(s.Totals.CostByBucket),
			Tokens: toUsageTokens(s.Totals.Tokens), Calls: s.Totals.Calls, UnpricedCalls: s.Totals.UnpricedCalls,
			Activities: s.Totals.Activities,
		},
		CostByKind: UsageCostByKind{
			Users: usageMoney(s.CostByKind.Users), Agents: usageMoney(s.CostByKind.Agents),
			Workflows: usageMoney(s.CostByKind.Workflows), Models: usageMoney(s.CostByKind.Models),
		},
		MeshBudget: toUsageBudget(s.MeshBudget),
		Daily:      nonNilList(toUsageDays(s.Daily)),
	}
	if c := s.Comparison; c != nil {
		out.Comparison = &UsageComparison{Cost: usageMoney(c.Cost), ChangePercent: c.ChangePercent}
	}
	return out
}

func toUsageSavings(s entplatform.UsageSavingsResponse) UsageSavings {
	out := UsageSavings{
		Scope: string(s.Scope), DataAsOf: InstantOf(s.DataAsOf), HistoryComplete: s.HistoryComplete,
		Window: toUsageWindow(s.Window), Currency: s.Currency, AvoidedInputTokens: s.AvoidedInputTokens,
		Calls: s.Calls, MeasuredCalls: s.MeasuredCalls, UnpricedCalls: s.UnpricedCalls,
	}
	if s.AvoidedCost != nil {
		out.AvoidedCost = new(usageMoney(*s.AvoidedCost))
	}
	return out
}

func toSelfUsage(s entplatform.UsageSelfResponse) SelfUsage {
	out := SelfUsage{
		UserID: s.ID, Scope: string(s.Scope), DataAsOf: InstantOf(s.DataAsOf), HistoryComplete: s.HistoryComplete,
		Name: s.Name, User: toUsageUser(s.User), Window: toUsageWindow(s.Window), Totals: toUsageSubjectTotals(s.Totals),
		Daily: nonNilList(toUsageDays(s.Daily)), Budget: toUsageBudget(s.Budget), TopRoots: toUsageRoots(s.TopRoots),
		OtherCost: usageMoney(s.OtherCost), OtherShareOfUser: s.OtherShareOfUser,
		Months: make([]UsageMonth, len(s.Months)),
	}
	for i, m := range s.Months {
		out.Months[i] = UsageMonth{Month: m.Month, Cost: usageMoney(m.Cost), BudgetAmount: usageMoneyOrNil(m.BudgetAmount), PartialFromDate: m.PartialFromDate}
	}
	return out
}
