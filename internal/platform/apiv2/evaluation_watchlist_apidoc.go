// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (WatchlistEntry) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `WatchlistEntry is one agent or workflow on the caller's watchlist. It has no
id: the name identifies it, and each caller has a watchlist of their own.`,
		Fields: map[string]apidoc.Field{
			"AgentName":             {Doc: "AgentName is the name of the watched agent, or of the workflow when targetType is workflow."},
			"TargetType":            {Doc: "TargetType is what the watched name is: agent or workflow. A name is a workflow when workflow evaluations are turned on and a workflow, or the target of a workflow experiment, has exactly that name. A name that is both an agent and a workflow is the workflow."},
			"ResolvedModelProvider": {Doc: "ResolvedModelProvider is the model the agent runs under, as the alias of its model configuration. Set to \"general\" when the agent has none of its own. Null for a workflow, whose nodes each run under their own model."},
			"IsBuiltIn":             {Doc: "IsBuiltIn is true for a built-in mesh agent, which has no agent record on the platform."},
			"Position":              {Doc: "Position is the entry's place in the watchlist, counting from 0."},
		},
	}
}

func (ReplaceWatchlistRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ReplaceWatchlistRequest replaces the caller's whole watchlist.",
		Fields: map[string]apidoc.Field{
			"Page":       {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters of the page of the new watchlist the response returns."},
			"AgentNames": {Doc: "AgentNames are the agents to watch, or the workflows while workflow evaluations are turned on, in order. Up to 5 after duplicates are dropped. Each name is trimmed, must not be blank, and is up to 255 characters. A name sent twice counts once, at its first place. An empty list or null clears the watchlist."},
		},
	}
}

func (WatchlistChartSeries) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `WatchlistChartSeries is the chart of one watched agent or workflow: how the
experiments that evaluate it have scored over their most recent runs. It has
no id: the name identifies it.`,
		Fields: map[string]apidoc.Field{
			"AgentName":             {Doc: "AgentName is the name of the watched agent, or of the workflow when targetType is workflow."},
			"TargetType":            {Doc: "TargetType is what the watched name is: agent or workflow."},
			"ResolvedModelProvider": {Doc: "ResolvedModelProvider is the model the agent runs under, as the alias of its model configuration. Set to \"general\" when the agent has none of its own. Null for a workflow."},
			"IsBuiltIn":             {Doc: "IsBuiltIn is true for a built-in mesh agent, which has no agent record on the platform."},
			"Position":              {Doc: "Position is the entry's place in the watchlist, counting from 0."},
			"Experiments":           {Doc: "Experiments are the experiments with the most completed run groups against the agent under its model, or against the workflow under any model. Up to 3. Each has the primary evaluator's scores from its last 10 completed run groups. Empty when none has completed."},
		},
	}
}

func (WatchlistExperimentSeries) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "WatchlistExperimentSeries is one experiment's line on an agent's or a workflow's chart.",
		Fields: map[string]apidoc.Field{
			"ExperimentID":         {Doc: "ExperimentID is the UUID of the experiment."},
			"ExperimentName":       {Doc: "ExperimentName is the experiment's name."},
			"PrimaryEvaluatorID":   {Doc: "PrimaryEvaluatorID is the UUID of the experiment's headline evaluator, whose score the points show."},
			"PrimaryEvaluatorName": {Doc: "PrimaryEvaluatorName is the headline evaluator's name, or null when the evaluator was deleted."},
			"Points":               {Doc: "Points are the experiment's most recent runs, oldest first, one per run group."},
		},
	}
}

func (WatchlistChartPoint) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "WatchlistChartPoint is one run's score on a chart line.",
		Fields: map[string]apidoc.Field{
			"RunGroupID":  {Doc: "RunGroupID is the UUID of the run group the point stands for."},
			"StartedTime": {Doc: "StartedTime is when the run group started."},
			"Score":       {Doc: "Score is the run's score from the headline evaluator, or null when the run has none."},
		},
	}
}
