// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (UsageReport) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageReport is one agent's spend in a window.",
		Fields: map[string]apidoc.Field{
			"ID":       {Doc: "ID is the agent's UUID; null for an agent read by the name its spend was booked under."},
			"Type":     {Doc: "Type is always \"agentUsage\"."},
			"Scope":    {Doc: "Scope says whose spend the report covers: always tenant, every user's."},
			"DataAsOf": {Doc: "DataAsOf is when the usage figures were last brought up to date; null before the first update."},
			"HistoryComplete": {Doc: `HistoryComplete is false while the usage history is still being built; figures over days before
the most recent two may then read low.`},
			"Name":       {Doc: "Name is the agent's display name."},
			"BookedName": {Doc: "BookedName is the name the spend was recorded under when id is null, and null otherwise."},
			"Window":     {Doc: "Window is the window the report covers."},
			"Totals":     {Doc: "Totals is the agent's spend in the window."},
			"Daily":      {Doc: "Daily is the agent's spend per UTC day of the window, every day present."},
		},
	}
}

func (ModelUsageReport) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ModelUsageReport is one model's spend in a window.",
		Fields: map[string]apidoc.Field{
			"ID":       {Doc: "ID is the model configuration's UUID; null for a model read by the name its spend was booked under."},
			"Type":     {Doc: "Type is always \"modelUsage\"."},
			"Scope":    {Doc: "Scope says whose spend the report covers: always tenant, every user's."},
			"DataAsOf": {Doc: "DataAsOf is when the usage figures were last brought up to date; null before the first update."},
			"HistoryComplete": {Doc: `HistoryComplete is false while the usage history is still being built; figures over days before
the most recent two may then read low.`},
			"Name":       {Doc: "Name is the model's display name."},
			"BookedName": {Doc: "BookedName is the name the spend was recorded under when id is null, and null otherwise."},
			"Window":     {Doc: "Window is the window the report covers."},
			"Totals":     {Doc: "Totals is the model's spend in the window."},
			"Daily":      {Doc: "Daily is the model's spend per UTC day of the window, every day present."},
			"Models":     {Doc: "Models is every provider model that served the model's calls in the window, costliest first."},
		},
	}
}

func (WorkflowUsageReport) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "WorkflowUsageReport is one workflow's spend in a window, by top-level node.",
		Fields: map[string]apidoc.Field{
			"ID":       {Doc: "ID is the workflow's UUID; null for a workflow read by the name its spend was booked under."},
			"Type":     {Doc: "Type is always \"workflowUsage\"."},
			"Scope":    {Doc: "Scope says whose spend the report covers: always tenant, every user's."},
			"DataAsOf": {Doc: "DataAsOf is when the usage figures were last brought up to date; null before the first update."},
			"HistoryComplete": {Doc: `HistoryComplete is false while the usage history is still being built; figures over days before
the most recent two may then read low.`},
			"Name":       {Doc: "Name is the workflow's display name."},
			"BookedName": {Doc: "BookedName is the name the spend was recorded under when id is null, and null otherwise."},
			"Window":     {Doc: "Window is the window the report covers."},
			"ComparisonWindow": {Doc: `ComparisonWindow is the window each node's changePercent compares against; null when it would
start before the retained history, or while historyComplete is false.`},
			"Totals": {Doc: "Totals is the workflow's spend in the window."},
			"Daily":  {Doc: "Daily is the workflow's spend per UTC day of the window, every day present."},
			"Nodes": {Doc: `Nodes is every top-level node of the workflow's definition, with spend or not, then any node with
spend that the definition no longer has. Only the tasks started at the workflow are split by
node, so when an agent or another workflow delegated to it the nodes' costs add up to less than
totals.cost.`},
		},
	}
}

func (UsageWorkflowNode) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageWorkflowNode is one top-level node of a workflow and the spend under it.",
		Fields: map[string]apidoc.Field{
			"Key": {Doc: "Key is the node's id in the workflow definition."},
			"Name": {Doc: `Name is the node's target: the agent, workflow, connector or tool it runs, or its key when it has
none.`},
			"Kind": {Doc: `Kind is the node's type in the workflow definition: agent, workflow, tool, switch, loop, map or
decision; null for spend under a node the definition no longer has, or for another type.`},
			"Cost": {Doc: `Cost is the cost of the calls made under the node in the window, as a decimal string with ten
fractional digits.`},
			"ShareOfWorkflow": {Doc: "ShareOfWorkflow is cost as an unrounded percentage, 0–100, of the workflow's cost."},
			"ChangePercent": {Doc: `ChangePercent is the unrounded percentage change from the node's cost in the comparison window;
null when it had none there or the report has no comparison window.`},
		},
	}
}

func (UserUsageReport) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UserUsageReport is one user's spend in a window, the budget that applies to
them and where their tasks started.`,
		Fields: map[string]apidoc.Field{
			"ID":       {Doc: "ID is the user's UUID."},
			"Type":     {Doc: "Type is always \"userUsage\"."},
			"Scope":    {Doc: "Scope says whose spend the report covers: always tenant, every user's."},
			"DataAsOf": {Doc: "DataAsOf is when the usage figures were last brought up to date; null before the first update."},
			"HistoryComplete": {Doc: `HistoryComplete is false while the usage history is still being built; figures over days before
the most recent two, the budget's included, may then read low.`},
			"Name": {Doc: `Name is the user's display name; null when the user has neither a display name nor an email, or
has been deleted.`},
			"User":   {Doc: "User is the user record behind the spend; null for a user since deleted."},
			"Window": {Doc: "Window is the window the report covers."},
			"Totals": {Doc: "Totals is the user's spend in the window."},
			"Daily":  {Doc: "Daily is the user's spend per UTC day of the window, every day present."},
			"Budget": {Doc: `Budget is the user's budget now and its standing this budget period, whatever the window; null
when no budget applies.`},
			"TopRoots": {Doc: "TopRoots is at most five agents and workflows the user's tasks cost most at, costliest first."},
			"OtherCost": {Doc: `OtherCost is the cost of the user's tasks started anywhere else, as a decimal string with ten
fractional digits.`},
			"OtherShareOfUser": {Doc: "OtherShareOfUser is otherCost as an unrounded percentage, 0–100, of the user's cost."},
		},
	}
}

func (usageByIDRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "usageByIDRequest addresses one subject's report by its UUID.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the subject's UUID."},
			"Period": {Doc: `Period is a named window: this_month (the default), last_month, last_7_days, last_30_days or
last_90_days. Leave it out to send startDate and endDate instead.`},
			"StartDate": {Doc: `StartDate is the window's first day, as YYYY-MM-DD or an ISO 8601 date-time standing for the UTC
day containing it; sent together with endDate.`},
			"EndDate": {Doc: "EndDate is the window's last day, included, in the same form as startDate."},
		},
	}
}

func (usageByNameRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `usageByNameRequest addresses one subject's report by the name its spend was
booked under.`,
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the name the spend was booked under, as a usage row's bookedName gives it; at most 256
bytes.`},
			"Period": {Doc: `Period is a named window: this_month (the default), last_month, last_7_days, last_30_days or
last_90_days. Leave it out to send startDate and endDate instead.`},
			"StartDate": {Doc: `StartDate is the window's first day, as YYYY-MM-DD or an ISO 8601 date-time standing for the UTC
day containing it; sent together with endDate.`},
			"EndDate": {Doc: "EndDate is the window's last day, included, in the same form as startDate."},
		},
	}
}
