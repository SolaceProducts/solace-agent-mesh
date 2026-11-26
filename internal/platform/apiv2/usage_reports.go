// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
)

type UsageReport struct {
	ID              *string            `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e60"`
	Type            string             `json:"type" example:"agentUsage"`
	Scope           string             `json:"scope" example:"tenant"`
	DataAsOf        Instant            `json:"dataAsOf"`
	HistoryComplete bool               `json:"historyComplete" example:"true"`
	Name            string             `json:"name" example:"Support triage"`
	BookedName      *string            `json:"bookedName" example:"support-triage"`
	Window          UsageWindow        `json:"window"`
	Totals          UsageSubjectTotals `json:"totals"`
	Daily           []UsageDay         `json:"daily"`
}

type ModelUsageReport struct {
	ID              *string            `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e70"`
	Type            string             `json:"type" example:"modelUsage"`
	Scope           string             `json:"scope" example:"tenant"`
	DataAsOf        Instant            `json:"dataAsOf"`
	HistoryComplete bool               `json:"historyComplete" example:"true"`
	Name            string             `json:"name" example:"General purpose"`
	BookedName      *string            `json:"bookedName" example:"gpt-4o"`
	Window          UsageWindow        `json:"window"`
	Totals          UsageSubjectTotals `json:"totals"`
	Daily           []UsageDay         `json:"daily"`
	Models          []UsageServedModel `json:"models"`
}

type WorkflowUsageReport struct {
	ID               *string             `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e80"`
	Type             string              `json:"type" example:"workflowUsage"`
	Scope            string              `json:"scope" example:"tenant"`
	DataAsOf         Instant             `json:"dataAsOf"`
	HistoryComplete  bool                `json:"historyComplete" example:"true"`
	Name             string              `json:"name" example:"Nightly report"`
	BookedName       *string             `json:"bookedName" example:"nightly-report"`
	Window           UsageWindow         `json:"window"`
	ComparisonWindow *UsageWindow        `json:"comparisonWindow"`
	Totals           UsageSubjectTotals  `json:"totals"`
	Daily            []UsageDay          `json:"daily"`
	Nodes            []UsageWorkflowNode `json:"nodes"`
}

type UsageWorkflowNode struct {
	Key             string   `json:"key" example:"summarize"`
	Name            string   `json:"name" example:"Summarizer"`
	Kind            *string  `json:"kind" example:"agent"`
	Cost            string   `json:"cost" example:"6.0000000000"`
	ShareOfWorkflow float64  `json:"shareOfWorkflow" example:"50"`
	ChangePercent   *float64 `json:"changePercent" example:"-12.5"`
}

type UserUsageReport struct {
	ID               string             `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type             string             `json:"type" example:"userUsage"`
	Scope            string             `json:"scope" example:"tenant"`
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
}

type usageByIDRequest struct {
	StrictRequest
	ID        string `path:"id" json:"-"`
	Period    string `query:"period,nonempty" json:"-" example:"last_30_days"`
	StartDate string `query:"startDate,nonempty" json:"-" example:"2026-10-01"`
	EndDate   string `query:"endDate,nonempty" json:"-" example:"2026-10-15"`
}

type usageByNameRequest struct {
	StrictRequest
	Name      string `query:"name,nonempty" required:"true" json:"-" example:"support-triage"`
	Period    string `query:"period,nonempty" json:"-" example:"last_30_days"`
	StartDate string `query:"startDate,nonempty" json:"-" example:"2026-10-01"`
	EndDate   string `query:"endDate,nonempty" json:"-" example:"2026-10-15"`
}

func (r usageByIDRequest) window() usageWindowRequest {
	return usageWindowRequest{Period: r.Period, StartDate: r.StartDate, EndDate: r.EndDate}
}

func (r usageByNameRequest) window() usageWindowRequest {
	return usageWindowRequest{Period: r.Period, StartDate: r.StartDate, EndDate: r.EndDate}
}

func (h *usageHandler) byID(kind store.UsageKind, req usageByIDRequest) (store.UsageSubject, usage.WindowRequest, error) {
	problems := newQueryProblems()
	window := problems.window(req.window(), h.svc.Now())
	return store.UsageSubject{Kind: kind, ID: req.ID}, window, problems.err()
}

func (h *usageHandler) byName(kind store.UsageKind, req usageByNameRequest) (store.UsageSubject, usage.WindowRequest, error) {
	problems := newQueryProblems()
	if req.Name == "" {
		problems.addSemantic("name", "is required")
	}
	problems.checkName("name", req.Name)
	window := problems.window(req.window(), h.svc.Now())
	return store.UsageSubject{Kind: kind, Name: req.Name}, window, problems.err()
}

func (h *usageHandler) agentByID(ctx context.Context, req usageByIDRequest) (UsageReport, error) {
	subject, window, err := h.byID(store.UsageAgents, req)
	if err != nil {
		return UsageReport{}, err
	}
	return h.agentReport(ctx, subject, window, usagePathAgents+"/{id}")
}

func (h *usageHandler) agentByName(ctx context.Context, req usageByNameRequest) (UsageReport, error) {
	subject, window, err := h.byName(store.UsageAgents, req)
	if err != nil {
		return UsageReport{}, err
	}
	return h.agentReport(ctx, subject, window, usagePathAgentsByName)
}

func (h *usageHandler) agentReport(ctx context.Context, subject store.UsageSubject, window usage.WindowRequest, path string) (UsageReport, error) {
	got, err := h.svc.Subject(ctx, subject, window)
	if err != nil {
		return UsageReport{}, usageError(err)
	}
	h.audit(ctx, path, subject.ID)
	d := entplatform.UsageSubjectDTO(got)
	return UsageReport{
		ID: d.ID, Type: usageTypeAgent, Scope: string(d.Scope), DataAsOf: InstantOf(d.DataAsOf),
		HistoryComplete: d.HistoryComplete, Name: d.Name, BookedName: d.BookedName, Window: toUsageWindow(d.Window),
		Totals: toUsageSubjectTotals(d.Totals), Daily: nonNilList(toUsageDays(d.Daily)),
	}, nil
}

func (h *usageHandler) modelByID(ctx context.Context, req usageByIDRequest) (ModelUsageReport, error) {
	subject, window, err := h.byID(store.UsageModels, req)
	if err != nil {
		return ModelUsageReport{}, err
	}
	return h.modelReport(ctx, subject, window, usagePathModels+"/{id}")
}

func (h *usageHandler) modelByName(ctx context.Context, req usageByNameRequest) (ModelUsageReport, error) {
	subject, window, err := h.byName(store.UsageModels, req)
	if err != nil {
		return ModelUsageReport{}, err
	}
	return h.modelReport(ctx, subject, window, usagePathModelsByName)
}

func (h *usageHandler) modelReport(ctx context.Context, subject store.UsageSubject, window usage.WindowRequest, path string) (ModelUsageReport, error) {
	got, err := h.svc.Subject(ctx, subject, window)
	if err != nil {
		return ModelUsageReport{}, usageError(err)
	}
	h.audit(ctx, path, subject.ID)
	d := entplatform.UsageModelDTO(got)
	return ModelUsageReport{
		ID: d.ID, Type: usageTypeModel, Scope: string(d.Scope), DataAsOf: InstantOf(d.DataAsOf),
		HistoryComplete: d.HistoryComplete, Name: d.Name, BookedName: d.BookedName, Window: toUsageWindow(d.Window),
		Totals: toUsageSubjectTotals(d.Totals), Daily: nonNilList(toUsageDays(d.Daily)), Models: toUsageServedModels(d.Models),
	}, nil
}

func (h *usageHandler) workflowByID(ctx context.Context, req usageByIDRequest) (WorkflowUsageReport, error) {
	subject, window, err := h.byID(store.UsageWorkflows, req)
	if err != nil {
		return WorkflowUsageReport{}, err
	}
	return h.workflowReport(ctx, subject, window, usagePathWorkflows+"/{id}")
}

func (h *usageHandler) workflowByName(ctx context.Context, req usageByNameRequest) (WorkflowUsageReport, error) {
	subject, window, err := h.byName(store.UsageWorkflows, req)
	if err != nil {
		return WorkflowUsageReport{}, err
	}
	return h.workflowReport(ctx, subject, window, usagePathWorkflowsByName)
}

func (h *usageHandler) workflowReport(ctx context.Context, subject store.UsageSubject, window usage.WindowRequest, path string) (WorkflowUsageReport, error) {
	got, err := h.svc.Workflow(ctx, subject, window)
	if err != nil {
		return WorkflowUsageReport{}, usageError(err)
	}
	h.audit(ctx, path, subject.ID)
	d := entplatform.UsageWorkflowDTO(got)
	nodes := make([]UsageWorkflowNode, len(d.Nodes))
	for i, n := range d.Nodes {
		nodes[i] = UsageWorkflowNode{Key: n.NodeID, Name: n.Name, Cost: usageMoney(n.Cost), ShareOfWorkflow: n.ShareOfWorkflow, ChangePercent: n.ChangePercent}
		if n.Kind != nil {
			nodes[i].Kind = new(string(*n.Kind))
		}
	}
	return WorkflowUsageReport{
		ID: d.ID, Type: usageTypeWorkflow, Scope: string(d.Scope), DataAsOf: InstantOf(d.DataAsOf),
		HistoryComplete: d.HistoryComplete, Name: d.Name, BookedName: d.BookedName, Window: toUsageWindow(d.Window),
		ComparisonWindow: toUsageWindowOrNil(d.ComparisonWindow), Totals: toUsageSubjectTotals(d.Totals),
		Daily: nonNilList(toUsageDays(d.Daily)), Nodes: nodes,
	}, nil
}

func (h *usageHandler) userByID(ctx context.Context, req usageByIDRequest) (UserUsageReport, error) {
	subject, window, err := h.byID(store.UsageUsers, req)
	if err != nil {
		return UserUsageReport{}, err
	}
	got, err := h.svc.User(ctx, subject, window, usage.ScopeTenant)
	if err != nil {
		return UserUsageReport{}, usageError(err)
	}
	h.audit(ctx, usagePathUsers+"/{id}", subject.ID)
	d := entplatform.UsageUserDTO(got)
	return UserUsageReport{
		ID: subject.ID, Type: usageTypeUser, Scope: string(d.Scope), DataAsOf: InstantOf(d.DataAsOf),
		HistoryComplete: d.HistoryComplete, Name: d.Name, User: toUsageUser(d.User), Window: toUsageWindow(d.Window),
		Totals: toUsageSubjectTotals(d.Totals), Daily: nonNilList(toUsageDays(d.Daily)), Budget: toUsageBudget(d.Budget),
		TopRoots: toUsageRoots(d.TopRoots), OtherCost: usageMoney(d.OtherCost), OtherShareOfUser: d.OtherShareOfUser,
	}, nil
}
