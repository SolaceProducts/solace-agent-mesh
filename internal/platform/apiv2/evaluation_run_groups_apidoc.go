// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (RunGroup) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunGroup is the runs one trigger started, one per model, with the status the
group reaches as a whole. Its id is the operation's id.`,
		Fields: map[string]apidoc.Field{
			"ID":              {Doc: "ID is the run group's UUID."},
			"Type":            {Doc: "Type is always \"runGroup\"."},
			"ExperimentID":    {Doc: "ExperimentID is the UUID of the experiment the runs belong to."},
			"RunName":         {Doc: "RunName names the runs in reports, or is null when none was recorded."},
			"Status":          {Doc: "Status is the group's combined state: pending if any run is pending, else running if any run is running, else the first of failed, cancelled, completed_with_warnings, and completed that any run reached."},
			"ProgressCurrent": {Doc: "ProgressCurrent is the trials finished across the group's runs."},
			"ProgressTotal":   {Doc: "ProgressTotal is the trials the group's runs will finish."},
			"CreatedTime":     {Doc: "CreatedTime is when the first of the group's runs was triggered."},
			"CompletedTime":   {Doc: "CompletedTime is when the last of the group's runs finished, or null while any run has not."},
			"Runs":            {Doc: "Runs are the group's runs, ordered by model."},
			"ConfigOutdated":  {Doc: "ConfigOutdated is true when the experiment was changed after the group's runs started."},
		},
	}
}

func (RunGroupSummary) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunGroupSummary is one row of the reports list: a run group collapsed to its
status and timing, with the experiment it ran.`,
		Fields: map[string]apidoc.Field{
			"ID":              {Doc: "ID is the run group's UUID."},
			"Type":            {Doc: "Type is always \"runGroup\"."},
			"ExperimentID":    {Doc: "ExperimentID is the UUID of the experiment the runs belong to."},
			"ExperimentName":  {Doc: "ExperimentName is the experiment's name."},
			"TargetType":      {Doc: "TargetType is what the experiment evaluates: agent or workflow."},
			"TargetAgent":     {Doc: "TargetAgent is the name of the agent the experiment evaluates, or of the workflow when targetType is workflow."},
			"RunName":         {Doc: "RunName names the runs in reports, or is null when none was recorded."},
			"Status":          {Doc: "Status is the group's combined state, as on a run group."},
			"CreatedTime":     {Doc: "CreatedTime is when the first of the group's runs was triggered."},
			"CompletedTime":   {Doc: "CompletedTime is when the last of the group's finished runs finished, or null when none has."},
			"DurationSeconds": {Doc: "DurationSeconds is the seconds from createdTime to completedTime, or null while no run has finished."},
			"RunCount":        {Doc: "RunCount is how many runs the group holds, one per model."},
		},
	}
}

func (RunGroupAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunGroupAgent names an agent or workflow some run group evaluated. A name
evaluated both as an agent and as a workflow is listed once for each.`,
		Fields: map[string]apidoc.Field{
			"TargetAgent": {Doc: "TargetAgent is the agent's or workflow's name, usable as the targetAgent filter of the run group list."},
			"TargetType":  {Doc: "TargetType is what the name is: agent or workflow, usable as the targetType filter of the run group list."},
		},
	}
}

func (RunGroupModelResult) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunGroupModelResult is one run of a run group, the model it ran under and
what its trials cost in tokens.`,
		Fields: map[string]apidoc.Field{
			"ModelConfigID": {Doc: "ModelConfigID is the UUID of the model configuration the run used, or null for a run on the agent's own model or on a model named without a configuration."},
			"RunID":         {Doc: "RunID is the run's UUID, which GET /evaluations/runs/{runId}/results takes to list its results."},
			"TokenSummary":  {Doc: "TokenSummary is the mean token usage across the run's trials."},
		},
	}
}

func (RunTokenSummary) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `RunTokenSummary is the mean token usage over a run's trials that reported
usage. Each mean covers only the trials that reported that count, and those
counts are not exposed. Treat a mean as an indication, not a weight. A
workflow trial with an incomplete tokenBreakdown counts as given, as a lower
bound, so a mean over workflow trials can understate.`,
		Fields: map[string]apidoc.Field{
			"UsageResultCount":      {Doc: "UsageResultCount is how many trials reported any usage. Errored trials and trials without usage are left out."},
			"MeanInputTokens":       {Doc: "MeanInputTokens is the mean input tokens, or null when no trial reported it."},
			"MeanCachedInputTokens": {Doc: "MeanCachedInputTokens is the mean input tokens served from cache, or null when no trial reported it."},
			"MeanOutputTokens":      {Doc: "MeanOutputTokens is the mean output tokens, or null when no trial reported it."},
			"MeanReasoningTokens":   {Doc: "MeanReasoningTokens is the mean reasoning tokens, or null when no trial reported it."},
			"MeanTotalTokens":       {Doc: "MeanTotalTokens is the mean total tokens, or null when no trial reported it."},
		},
	}
}

func (listRunGroupsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listRunGroupsRequest selects a page of run groups.",
		Fields: map[string]apidoc.Field{
			"Page":        {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"Name":        {Doc: "Name limits the list to run groups whose run name contains this text, ignoring case."},
			"TargetAgent": {Doc: "TargetAgent limits the list to run groups of experiments that evaluate this agent or workflow name."},
			"TargetType":  {Doc: "TargetType limits the list to run groups of experiments that evaluate this kind of target: agent or workflow."},
		},
	}
}

func (listRunGroupAgentsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listRunGroupAgentsRequest selects a page of agents and workflows.",
		Fields: map[string]apidoc.Field{
			"Page":       {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
			"TargetType": {Doc: "TargetType limits the list to one kind of target: agent or workflow. Leave it out for both."},
		},
	}
}

func (listRunGroupResultsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listRunGroupResultsRequest selects a page of a run group's per-model results.",
		Fields: map[string]apidoc.Field{
			"RunGroupID": {Doc: "RunGroupID is the UUID of the run group."},
			"Page":       {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
		},
	}
}
