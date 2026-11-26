// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateExperimentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateExperimentRequest is config apply's copy of
apiv2.CreateExperimentRequest.`,
		Fields: map[string]apidoc.Field{
			"Name":        {Doc: "Name is the experiment's display name (unique per platform)."},
			"Description": {Doc: "Description summarises the experiment's purpose."},
			"DatasetID": {Doc: `DatasetID references the dataset to evaluate against. In config-apply
manifests this is the dataset's name, resolved to an ID at apply time.`},
			"TargetAgent": {Doc: `TargetAgent is the name of the agent under evaluation. Set it or
targetWorkflow, not both.`},
			"TargetWorkflow": {Doc: `TargetWorkflow is the name of the workflow under evaluation, in place of
targetAgent: set one or the other, not both. Its examples carry inputData
matching the workflow's input schema.`},
			"Models": {Doc: `Models optionally pins which model configs the agent runs under; each
entry is a map keyed by "modelConfigId" (the model-config to use). At
most 3 entries; omit to use the agent's configured model.`},
			"RunsPerExample": {Doc: "RunsPerExample repeats each example this many times (1–10; default 1)."},
			"MaxWorkers":     {Doc: "MaxWorkers caps concurrent example evaluations (1–20)."},
			"EvaluatorIDs": {Doc: `EvaluatorIDs lists the evaluators to score with (their names in
config-apply manifests). At least one is required; at most 5.`},
			"PrimaryEvaluatorID": {Doc: `PrimaryEvaluatorID names the evaluator whose score is the headline
metric; it must be one of evaluatorIds.`},
			"RunAs": {Doc: `RunAs names the Agent Mesh service account this experiment's runs execute as. A bare
name is namespaced to system:<name>; an already-prefixed value is kept
as-is. Omit (or send an empty string) to run as the built-in eval service
account. Runs carry exactly the scopes that principal holds, resolved from
the platform's merged RBAC state (baked-in system defaults, operator
authorization YAML, and DB-provisioned service accounts and role grants).
The named service account need not exist when the experiment is saved, but a
principal that holds no scopes when the run starts fails the run rather
than executing under a broader grant. Built-in service accounts other than
the eval one are rejected: tool credentials are keyed by principal, so an
experiment naming one would share a credential slot with the Agent Mesh
features that run as it, which Connect Tools then offers to disconnect.
Declare a service account of your own instead.`},
		},
	}
}

func (UpdateExperimentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateExperimentRequest is config apply's copy of
apiv2.UpdateExperimentRequest, a PATCH body. The experiment's ID goes in the
path, not the body.`,
	}
}

func (ExperimentResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ExperimentResponse is config apply's copy of apiv2.Experiment.",
	}
}
