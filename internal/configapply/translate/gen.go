// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

//go:generate go run ../codegen/cmd -package=translate -output=agent_translator_gen.go -spec=Agent:CreateAgentRequest:AgentResponse:../../platform/admin/agent_dto.go:github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin:entplatform
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=agent_v2_translator_gen.go -spec=AgentV2:CreateAgentRequest:AgentResponse:../wire/agent.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=workflow_v2_translator_gen.go -spec=WorkflowV2:CreateWorkflowRequest:WorkflowResponse:../wire/workflow.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=entrypoint_v2_translator_gen.go -spec=EntrypointV2:CreateEntrypointRequest:EntrypointResponse:../wire/entrypoint.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=model_v2_translator_gen.go -spec=ModelV2:CreateModelRequest:ModelResponse:../wire/model.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=connector_v2_translator_gen.go -spec=ConnectorV2:CreateConnectorRequest:ConnectorResponse:../wire/connector.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=toolset_v2_translator_gen.go -spec=ToolsetV2:CreateToolsetRequest:ToolsetResponse:../wire/toolset.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=dataset_v2_translator_gen.go -spec=DatasetV2:CreateDatasetRequest:DatasetResponse:../wire/dataset.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=evaluator_v2_translator_gen.go -spec=EvaluatorV2:CreateEvaluatorRequest:EvaluatorResponse:../wire/evaluator.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=experiment_v2_translator_gen.go -spec=ExperimentV2:CreateExperimentRequest:ExperimentResponse:../wire/experiment.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=rbac_role_v2_translator_gen.go -spec=RBACRoleV2:CreateRoleRequest:RoleResponse:../wire/rbac.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
//go:generate go run ../codegen/cmd -package=translate -null-is-absent -output=link_agent_v2_translator_gen.go -spec=LinkAgentV2:CreateLinkAgentRequest:LinkAgentResponse:../wire/link_agent.go:github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire:wire
