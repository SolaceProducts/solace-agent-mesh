// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (UsageRow) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageRow is one agent or workflow in a usage list, with its spend in the
window.`,
		Fields: map[string]apidoc.Field{
			"ID": {Doc: `ID is the agent's or workflow's UUID; null for one that Agent Mesh has no record of, such as one
defined in YAML, which bookedName then names.`},
			"Type": {Doc: "Type is agentUsage on an agents list and workflowUsage on a workflows list."},
			"Name": {Doc: "Name is the agent's or workflow's display name."},
			"BookedName": {Doc: `BookedName is the name the spend was recorded under when id is null, and null otherwise; the
kind's byName route reads it, as /usage/agents/byName?name={bookedName}.`},
			"Cost": {Doc: `Cost is the cost of the subject's priced LLM calls in the window, as a decimal string with ten
fractional digits.`},
			"Activities": {Doc: `Activities is the number of distinct user tasks the subject took part in, each counted once a
day. For an agent, these are the tasks with at least one call by it. For a workflow, they are the
tasks it ran in, whether started at it or delegated to it by an agent or another workflow. A call
that a node's agent passed on to a peer counts only for the task's root workflow.`},
			"CostPerActivity": {Doc: "CostPerActivity is cost divided by activities; null when activities is 0."},
			"ShareOfSpend": {Doc: `ShareOfSpend is cost as an unrounded percentage, 0–100, of the whole deployment's cost in the
window; 0 when the deployment had no spend.`},
			"CostByBucket":  {Doc: "CostByBucket splits cost by token use."},
			"Tokens":        {Doc: "Tokens counts the tokens of the subject's calls, priced or not."},
			"Calls":         {Doc: "Calls is the number of the subject's LLM calls."},
			"UnpricedCalls": {Doc: "UnpricedCalls is the number of those calls with no price configured."},
			"Daily": {Doc: `Daily is the subject's spend per UTC day of the window, every day present; null unless the
request sets expand=daily.`},
		},
	}
}

func (ModelUsageRow) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ModelUsageRow is one model in the models usage list: a model configuration,
or a model a call defined inline.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the model configuration's UUID; null for a model defined inline, which bookedName then names."},
			"Type": {Doc: "Type is always \"modelUsage\"."},
			"Name": {Doc: "Name is the model's display name."},
			"BookedName": {Doc: `BookedName is the name the spend was recorded under when id is null, and null otherwise;
/usage/models/byName?name={bookedName} reads it.`},
			"Cost": {Doc: `Cost is the cost of the model's priced calls in the window, as a decimal string with ten
fractional digits.`},
			"Activities": {Doc: `Activities is the number of distinct user tasks with at least one call on the model, counted once
a day.`},
			"CostPerActivity": {Doc: "CostPerActivity is cost divided by activities; null when activities is 0."},
			"ShareOfSpend": {Doc: `ShareOfSpend is cost as an unrounded percentage, 0–100, of the whole deployment's cost in the
window; 0 when the deployment had no spend.`},
			"CostByBucket":  {Doc: "CostByBucket splits cost by token use."},
			"Tokens":        {Doc: "Tokens counts the tokens of the model's calls, priced or not."},
			"Calls":         {Doc: "Calls is the number of the model's LLM calls."},
			"UnpricedCalls": {Doc: "UnpricedCalls is the number of those calls with no price configured."},
			"Daily": {Doc: `Daily is the model's spend per UTC day of the window, every day present; null unless the request
sets expand=daily.`},
			"Models": {Doc: `Models is every provider model that served the row's calls in the window, costliest first. Calls
whose details were not recorded add an entry with a null name.`},
		},
	}
}

func (UserUsageRow) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UserUsageRow is one user in the users usage list. A row with a null id is
spend by someone who signed in but has no user record; it has no report of
its own.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the user's UUID; null for spend by someone who signed in but has no user record."},
			"Type": {Doc: "Type is always \"userUsage\"."},
			"Name": {Doc: `Name is the user's display name. It is null when id is null, so the person's sign-in identity is
not disclosed, and when the user has neither a display name nor an email, or has been deleted.`},
			"Cost": {Doc: `Cost is the cost of the user's priced LLM calls in the window, as a decimal string with ten
fractional digits.`},
			"Activities":      {Doc: "Activities is the number of the user's tasks, counted once a day."},
			"CostPerActivity": {Doc: "CostPerActivity is cost divided by activities; null when activities is 0."},
			"ShareOfSpend": {Doc: `ShareOfSpend is cost as an unrounded percentage, 0–100, of the whole deployment's cost in the
window; 0 when the deployment had no spend.`},
			"CostByBucket":  {Doc: "CostByBucket splits cost by token use."},
			"Tokens":        {Doc: "Tokens counts the tokens of the user's calls, priced or not."},
			"Calls":         {Doc: "Calls is the number of the user's LLM calls."},
			"UnpricedCalls": {Doc: "UnpricedCalls is the number of those calls with no price configured."},
			"Daily": {Doc: `Daily is the user's spend per UTC day of the window, every day present; null unless the request
sets expand=daily.`},
			"User": {Doc: "User is the user record behind the spend; null when there is none: no id, or a user since deleted."},
			"Budget": {Doc: `Budget is the user's budget now and its standing this budget period, whatever the window; null
when no budget applies. Spend booked under a subject with no user record stands on the user
tier's default, as budget enforcement judges it.`},
			"BudgetUsage": {Doc: `BudgetUsage is the window's cost against the budget for the window; null when budget is null or
has no cap, or the window is empty.`},
		},
	}
}

func (listAgentUsageRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listAgentUsageRequest selects a page of agents and their spend.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: `Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20; at most 25 with
expand=daily) query parameters.`},
			"Sort": {Doc: `Sort orders the list by cost, shareOfSpend, activities, costPerActivity, name, uncachedInputCost,
outputCost, cacheReadCost or cacheWriteCost, as field:asc or field:desc; leave it out for the
costliest first.`},
			"Period": {Doc: `Period is a named window: this_month (the default), last_month, last_7_days, last_30_days or
last_90_days. Leave it out to send startDate and endDate instead.`},
			"StartDate": {Doc: `StartDate is the window's first day, as YYYY-MM-DD or an ISO 8601 date-time standing for the UTC
day containing it; sent together with endDate.`},
			"EndDate": {Doc: "EndDate is the window's last day, included, in the same form as startDate."},
			"Search":  {Doc: "Search limits the list to agents whose name contains it, ignoring case; at most 256 bytes."},
			"Population": {Doc: `Population is all (the default), which lists agents with no spend in the window too, or
spenders, which lists only those with spend.`},
			"Expand": {Doc: "Expand adds each row's daily spend when it is daily."},
		},
	}
}

func (listWorkflowUsageRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listWorkflowUsageRequest selects a page of workflows and their spend.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: `Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20; at most 25 with
expand=daily) query parameters.`},
			"Sort": {Doc: `Sort orders the list by cost, shareOfSpend, activities, costPerActivity, name, uncachedInputCost,
outputCost, cacheReadCost or cacheWriteCost, as field:asc or field:desc; leave it out for the
costliest first.`},
			"Period": {Doc: `Period is a named window: this_month (the default), last_month, last_7_days, last_30_days or
last_90_days. Leave it out to send startDate and endDate instead.`},
			"StartDate": {Doc: `StartDate is the window's first day, as YYYY-MM-DD or an ISO 8601 date-time standing for the UTC
day containing it; sent together with endDate.`},
			"EndDate": {Doc: "EndDate is the window's last day, included, in the same form as startDate."},
			"Search":  {Doc: "Search limits the list to workflows whose name contains it, ignoring case; at most 256 bytes."},
			"Population": {Doc: `Population is all (the default), which lists workflows with no spend in the window too, or
spenders, which lists only those with spend.`},
			"Expand": {Doc: "Expand adds each row's daily spend when it is daily."},
		},
	}
}

func (listModelUsageRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listModelUsageRequest selects a page of models and their spend.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: `Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20; at most 25 with
expand=daily) query parameters.`},
			"Sort": {Doc: `Sort orders the list by cost, shareOfSpend, activities, costPerActivity, name, uncachedInputCost,
outputCost, cacheReadCost, cacheWriteCost or model (the provider model that served most of each
row's cost), as field:asc or field:desc; leave it out for the costliest first.`},
			"Period": {Doc: `Period is a named window: this_month (the default), last_month, last_7_days, last_30_days or
last_90_days. Leave it out to send startDate and endDate instead.`},
			"StartDate": {Doc: `StartDate is the window's first day, as YYYY-MM-DD or an ISO 8601 date-time standing for the UTC
day containing it; sent together with endDate.`},
			"EndDate": {Doc: "EndDate is the window's last day, included, in the same form as startDate."},
			"Search":  {Doc: "Search limits the list to models whose name contains it, ignoring case; at most 256 bytes."},
			"Population": {Doc: `Population is all (the default), which lists models with no spend in the window too, or
spenders, which lists only those with spend.`},
			"Expand": {Doc: "Expand adds each row's daily spend when it is daily."},
		},
	}
}

func (listUserUsageRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listUserUsageRequest selects a page of users and their spend.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: `Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20; at most 25 with
expand=daily) query parameters.`},
			"Sort": {Doc: `Sort orders the list by cost, shareOfSpend, activities, costPerActivity, name, uncachedInputCost,
outputCost, cacheReadCost, cacheWriteCost, budget (the budget's effective amount) or budgetUsage
(the window's cost against its budget), as field:asc or field:desc; leave it out for the
costliest first.`},
			"Period": {Doc: `Period is a named window: this_month (the default), last_month, last_7_days, last_30_days or
last_90_days. Leave it out to send startDate and endDate instead.`},
			"StartDate": {Doc: `StartDate is the window's first day, as YYYY-MM-DD or an ISO 8601 date-time standing for the UTC
day containing it; sent together with endDate.`},
			"EndDate": {Doc: "EndDate is the window's last day, included, in the same form as startDate."},
			"Search": {Doc: `Search limits the list to users whose display name or email contains it, ignoring case; at most
256 bytes.`},
			"Population": {Doc: `Population is all (the default), which lists users with no spend in the window too, or spenders,
which lists only those with spend.`},
			"Expand": {Doc: "Expand adds each row's daily spend when it is daily."},
			"BudgetID": {Doc: `BudgetID limits the list to the users a user budget applies to now, by the budget's UUID; an id
that names no user budget lists no one.`},
		},
	}
}
