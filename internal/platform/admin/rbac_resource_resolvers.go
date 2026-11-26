// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const resourceResolverPageSize = 100

type paginatedLister[T any] func(ctx context.Context, pageNumber, pageSize int) (*PaginatedResponse[T], error)

func collectResourceInstances[T any](ctx context.Context, list paginatedLister[T], toInstance func(T) ResourceInstance) ([]ResourceInstance, error) {
	var out []ResourceInstance
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := list(ctx, page, resourceResolverPageSize)
		if err != nil {
			return nil, err
		}
		for _, item := range resp.Data {
			out = append(out, toInstance(item))
		}
		if resp.Meta.Pagination.NextPage == nil {
			break
		}
	}
	return out, nil
}

func agentToInstance(a AgentResponse) ResourceInstance {
	return ResourceInstance{CanonicalName: cmp.Or(a.InstanceName, a.Name), DisplayName: a.Name}
}

func workflowToInstance(wf WorkflowResponse) ResourceInstance {
	return ResourceInstance{CanonicalName: a2a.InstanceName(a2a.KindWorkflow, wf.ID), DisplayName: wf.Name}
}

func agentResourceResolver(agentSvc *AgentService) ResourceResolver {
	return func(ctx context.Context) ([]ResourceInstance, error) {
		return collectResourceInstances(ctx,
			func(ctx context.Context, page, pageSize int) (*PaginatedResponse[AgentResponse], error) {
				return agentSvc.ListPaginated(ctx, page, pageSize, "")
			},
			agentToInstance)
	}
}

func workflowResourceResolver(workflowSvc *WorkflowService) ResourceResolver {
	return func(ctx context.Context) ([]ResourceInstance, error) {
		return collectResourceInstances(ctx, workflowSvc.ListPaginated, workflowToInstance)
	}
}

func platformAgentToInstance(a AgentResponse) ResourceInstance {
	return ResourceInstance{CanonicalName: a.ID, DisplayName: a.Name}
}

func platformWorkflowToInstance(wf WorkflowResponse) ResourceInstance {
	return ResourceInstance{CanonicalName: wf.ID, DisplayName: wf.Name}
}

func agentSubjectResourceResolver(agentSvc *AgentService) ResourceResolver {
	return func(ctx context.Context) ([]ResourceInstance, error) {
		return collectResourceInstances(ctx,
			func(ctx context.Context, page, pageSize int) (*PaginatedResponse[AgentResponse], error) {
				return agentSvc.ListPaginated(ctx, page, pageSize, "")
			},
			platformAgentToInstance)
	}
}

func workflowSubjectResourceResolver(workflowSvc *WorkflowService) ResourceResolver {
	return func(ctx context.Context) ([]ResourceInstance, error) {
		return collectResourceInstances(ctx, workflowSvc.ListPaginated, platformWorkflowToInstance)
	}
}

func rbacResourceResolvers(agentSvc *AgentService, workflowSvc *WorkflowService) map[string]ResourceResolver {
	return map[string]ResourceResolver{
		rbac.ScopeAgentInvokeAny:          agentResourceResolver(agentSvc),
		rbac.ScopeWorkflowInvokeAny:       workflowResourceResolver(workflowSvc),
		rbac.ScopeAgentReadFeedbackAny:    agentSubjectResourceResolver(agentSvc),
		rbac.ScopeAgentReadTraceAny:       agentSubjectResourceResolver(agentSvc),
		rbac.ScopeWorkflowReadFeedbackAny: workflowSubjectResourceResolver(workflowSvc),
	}
}

func agentInvokeScopeResolver(agentSvc *AgentService, a2aSvc *A2AAgentService) InvokeScopeResolver {
	return func(ctx context.Context, id string) (string, error) {
		resp, err := agentSvc.GetByID(ctx, id)
		if err == nil {
			return rbac.InvokeScopeForInstance(cmp.Or(resp.InstanceName, resp.Name)), nil
		}
		if !errors.As(err, new(*NotFoundError)) {
			return "", err
		}
		remote, err := a2aSvc.GetByID(ctx, id)
		if err != nil {
			return "", err
		}
		return rbac.InvokeScopeForInstance(cmp.Or(remote.InstanceName, remote.Name)), nil
	}
}

func workflowInvokeScopeResolver(workflowSvc *WorkflowService) InvokeScopeResolver {
	return func(ctx context.Context, id string) (string, error) {
		wf, err := workflowSvc.GetByID(ctx, id)
		if err != nil {
			return "", err
		}
		return rbac.WorkflowInvokeScopeForInstance(a2a.InstanceName(a2a.KindWorkflow, wf.ID)), nil
	}
}
