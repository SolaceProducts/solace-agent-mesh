// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

//go:generate go run ./cmd -mode=schema-data -output=schema_data_gen.go -schema=kind=model:plural=models:request=CreateModelRequest:file=../wire/model.go:v2copy=true -schema=kind=connector:plural=connectors:request=CreateConnectorRequest:file=../wire/connector.go:v2copy=true -schema=kind=agent:plural=agents:request=CreateAgentRequest:file=../wire/agent.go:v2copy=true -schema=kind=entrypoint:plural=entrypoints:request=CreateEntrypointRequest:file=../wire/entrypoint.go:v2copy=true -schema=kind=workflow:plural=workflows:request=CreateWorkflowRequest:file=../wire/workflow.go:v2copy=true -schema=kind=toolset:plural=toolsets:request=CreateToolsetRequest:file=../wire/toolset.go:v2copy=true -schema=kind=dataset:plural=datasets:request=CreateDatasetRequest:file=../wire/dataset.go:v2copy=true -schema=kind=evaluator:plural=evaluators:request=CreateEvaluatorRequest:file=../wire/evaluator.go:v2copy=true -schema=kind=experiment:plural=experiments:request=CreateExperimentRequest:file=../wire/experiment.go:v2copy=true -schema=kind=linkAgent:plural=linkAgents:request=CreateLinkAgentRequest:file=../wire/link_agent.go:v2copy=true

//go:generate go run ./cmd -mode=workflow-schema -output=workflow_schema_gen.go -workflow-config=../../workflow/config.go
