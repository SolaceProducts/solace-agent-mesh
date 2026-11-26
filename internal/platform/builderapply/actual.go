// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"fmt"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const listPageSize = 100

const maxListPages = 100

func listAll[T any](
	ctx context.Context,
	fetch func(ctx context.Context, pageNumber, pageSize int) (*entplatform.PaginatedResponse[T], error),
) ([]T, error) {
	var out []T
	for page := 1; page <= maxListPages; page++ {
		resp, err := fetch(ctx, page, listPageSize)
		if err != nil {
			return nil, err
		}
		if resp == nil {
			return nil, fmt.Errorf("list returned a nil page at page %d; refusing to plan against a truncated list", page)
		}
		out = append(out, resp.Data...)
		if resp.Meta.Pagination.NextPage == nil {
			return out, nil
		}
	}
	return nil, fmt.Errorf("list exceeded %d pages of %d; refusing to plan against a truncated list", maxListPages, listPageSize)
}

type deps struct {
	Agents     *entplatform.AgentService
	Workflows  *entplatform.WorkflowService
	Connectors *entplatform.ConnectorService
	Gateways   *entplatform.GatewayService
	A2AAgents  *entplatform.A2AAgentService
	Skills     *entplatform.SkillService
	DB         *store.DB

	Ledger              *store.BuilderSessionResourceRepo
	AgentDeployments    *store.AgentDeploymentRepo
	WorkflowDeployments *store.WorkflowDeploymentRepo
	AgentDeploy         *entplatform.DeploymentService
	WorkflowDeploy      *entplatform.WorkflowDeploymentService
}

func (d deps) listAgents(ctx context.Context) ([]entplatform.AgentResponse, error) {
	return listAll(ctx, func(ctx context.Context, page, size int) (*entplatform.PaginatedResponse[entplatform.AgentResponse], error) {
		return d.Agents.ListPaginated(ctx, page, size, "")
	})
}

func (d deps) listConnectors(ctx context.Context) ([]entplatform.ConnectorResponse, error) {
	return listAll(ctx, func(ctx context.Context, page, size int) (*entplatform.PaginatedResponse[entplatform.ConnectorResponse], error) {
		return d.Connectors.ListPaginated(ctx, page, size, store.ConnectorFilter{}, store.ConnectorOrder{})
	})
}

func (d deps) listWorkflows(ctx context.Context) ([]entplatform.WorkflowResponse, error) {
	return listAll(ctx, func(ctx context.Context, page, size int) (*entplatform.PaginatedResponse[entplatform.WorkflowResponse], error) {
		return d.Workflows.ListPaginated(ctx, page, size)
	})
}

func (d deps) listGateways(ctx context.Context) ([]entplatform.GatewayResponse, error) {
	return listAll(ctx, func(ctx context.Context, page, size int) (*entplatform.PaginatedResponse[entplatform.GatewayResponse], error) {
		return d.Gateways.ListPaginated(ctx, page, size, "")
	})
}

func (d deps) listSkills(ctx context.Context) ([]entplatform.SkillDetailResponse, error) {
	return listAll(ctx, func(ctx context.Context, page, size int) (*entplatform.PaginatedResponse[entplatform.SkillDetailResponse], error) {
		return d.Skills.ListPaginated(ctx, page, size, "")
	})
}

func (d deps) listA2AAgents(ctx context.Context) ([]entplatform.RemoteAgentResponse, error) {
	return listAll(ctx, func(ctx context.Context, page, size int) (*entplatform.PaginatedResponse[entplatform.RemoteAgentResponse], error) {
		return d.A2AAgents.ListPaginated(ctx, page, size)
	})
}
