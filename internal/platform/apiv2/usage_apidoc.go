// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (UsageSummary) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageSummary is the whole deployment's spend in a window, against the
comparison window before it and the mesh budget.`,
		Fields: map[string]apidoc.Field{
			"Scope":    {Doc: "Scope says whose spend the report covers: always tenant, every user's."},
			"DataAsOf": {Doc: "DataAsOf is when the usage figures were last brought up to date; null before the first update."},
			"HistoryComplete": {Doc: `HistoryComplete is false while the usage history is still being built, as it is when usage
reporting is first turned on. Every figure over days before the most recent two may then read
low, the mesh budget's period spend and status included, and so may the lists.`},
			"Window": {Doc: "Window is the window the report covers."},
			"ComparisonWindow": {Doc: `ComparisonWindow is the earlier window that comparison covers; null when it would start before
the retained history, or while historyComplete is false.`},
			"Currency": {Doc: "Currency is the ISO 4217 code of every amount: always USD."},
			"Totals":   {Doc: "Totals is the spend in the window."},
			"CostByKind": {Doc: `CostByKind is the cost behind each usage list in the window. For every list but workflows, it is
the sum of all the list's rows, so subtracting a page's rows gives the spend on the other pages.`},
			"Comparison": {Doc: "Comparison is the spend in comparisonWindow; null when that is null."},
			"MeshBudget": {Doc: `MeshBudget is the deployment's budget and its standing in the current budget period; null when
the mesh has no budget.`},
			"Daily": {Doc: "Daily is the spend per UTC day of the window, every day present."},
		},
	}
}

func (SelfUsage) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SelfUsage is the caller's own spend in a window, the budget it is judged
against, and their last three months.`,
		Fields: map[string]apidoc.Field{
			"UserID": {Doc: `UserID is the caller's user UUID. It is null when the caller's token names no user: their sign-in
identity has no user record, or has one or several that cannot be proven theirs.`},
			"Scope":    {Doc: "Scope says whose spend the report covers: always self, the caller's own."},
			"DataAsOf": {Doc: "DataAsOf is when the usage figures were last brought up to date; null before the first update."},
			"HistoryComplete": {Doc: `HistoryComplete is false while the usage history is still being built; figures over days
before the most recent two, the budget's included, may then read low.`},
			"Name": {Doc: `Name is the caller's display name; null whenever userId is, and when the user has neither a
display name nor an email.`},
			"User":   {Doc: "User is the caller's user record; null when userId is."},
			"Window": {Doc: "Window is the window the report covers."},
			"Totals": {Doc: `Totals is the caller's spend in the window. Its shareOfSpend is always null, since it would
disclose the deployment's spend.`},
			"Daily": {Doc: "Daily is the caller's spend per UTC day of the window, every day present."},
			"Budget": {Doc: `Budget is the caller's budget now and its standing this budget period, whatever the window;
null when no budget applies.`},
			"TopRoots": {Doc: "TopRoots is at most five agents and workflows the caller's tasks cost most at, costliest first."},
			"OtherCost": {Doc: `OtherCost is the cost of the caller's tasks started anywhere else, as a decimal string with ten
fractional digits.`},
			"OtherShareOfUser": {Doc: "OtherShareOfUser is otherCost as an unrounded percentage, 0–100, of the caller's cost."},
			"Months":           {Doc: "Months is the caller's spend in the current UTC month and the two before it, oldest first."},
		},
	}
}

func (UsageSavings) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageSavings is what Smart Context Management kept out of the LLM in a
window, over the same calls the usage summary's spend covers. It is an
estimate: the tokens are counted from the bytes withheld, about four per
token, and the cost prices each call's avoided tokens at the average rate
that call paid per input token.`,
		Fields: map[string]apidoc.Field{
			"Scope":    {Doc: "Scope says whose savings the report covers: tenant (every user's) or self (the caller's own)."},
			"DataAsOf": {Doc: "DataAsOf is when the usage figures were last brought up to date; null before the first update."},
			"HistoryComplete": {Doc: `HistoryComplete is false while the usage history is still being built; every figure over days
before the most recent two may then read low.`},
			"Window":   {Doc: "Window is the window the report covers."},
			"Currency": {Doc: "Currency is the ISO 4217 code of every amount: always USD."},
			"AvoidedInputTokens": {Doc: `AvoidedInputTokens estimates the input tokens kept out of the model over the measured calls. It
is null when no call in the window was measured, such as in a window from before savings were
measured, so that window does not read as saving nothing.`},
			"AvoidedCost": {Doc: `AvoidedCost estimates the cost of those tokens over the priced measured calls, as a decimal
string with ten fractional digits; null when no measured call is priced.`},
			"Calls": {Doc: "Calls counts the LLM calls in the window."},
			"MeasuredCalls": {Doc: `MeasuredCalls counts the calls the agent counted savings on: those made within an agent's turn
while both the agent and the platform ran a release that measures savings. Compaction, session
titles and the other calls an agent makes outside its turns are never measured, so measuredCalls
stays below calls. An unmeasured call adds neither tokens nor cost.`},
			"UnpricedCalls": {Doc: "UnpricedCalls counts the calls with no price; a measured one among them adds tokens but no cost."},
		},
	}
}

func (UsageWindow) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageWindow is the resolved window a report covers: whole UTC days from
startTime to endTime. A request's endDate names a day it includes, while
endTime is the instant the window stops at, so to ask for the same window
again, send the original startDate and endDate.`,
		Fields: map[string]apidoc.Field{
			"StartTime": {Doc: "StartTime is the window's first instant, a UTC midnight."},
			"EndTime": {Doc: `EndTime is the instant the window ends, exclusive: the end of its last day, or now, whichever
is earlier.`},
		},
	}
}

func (UsageCostByBucket) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageCostByBucket splits a cost by what the tokens were used for. Each
amount is a decimal string with ten fractional digits.`,
		Fields: map[string]apidoc.Field{
			"UncachedInput": {Doc: "UncachedInput is the cost of input tokens not read from a cache."},
			"Output":        {Doc: "Output is the cost of output tokens."},
			"CacheRead":     {Doc: "CacheRead is the cost of input tokens read from a cache."},
			"CacheWrite":    {Doc: "CacheWrite is the cost of input tokens written to a cache."},
		},
	}
}

func (UsageTokens) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageTokens counts tokens by what they were used for.",
		Fields: map[string]apidoc.Field{
			"UncachedInput": {Doc: "UncachedInput counts the input tokens not read from a cache."},
			"Output":        {Doc: "Output counts the output tokens."},
			"CacheRead":     {Doc: "CacheRead counts the input tokens read from a cache."},
			"CacheWrite":    {Doc: "CacheWrite counts the input tokens written to a cache."},
		},
	}
}

func (UsageTotals) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageTotals is the spend over a whole report.",
		Fields: map[string]apidoc.Field{
			"Cost":         {Doc: "Cost is the cost of every priced LLM call, as a decimal string with ten fractional digits."},
			"CostByBucket": {Doc: "CostByBucket splits cost by token use."},
			"Tokens":       {Doc: "Tokens counts the tokens of every call, priced or not."},
			"Calls":        {Doc: "Calls is the number of LLM calls."},
			"UnpricedCalls": {Doc: `UnpricedCalls is the number of calls with no price configured for their model, which add
tokens but no cost.`},
			"Activities": {Doc: `Activities is the number of distinct user tasks, counted once a day however many agents and
models a task used that day.`},
		},
	}
}

func (UsageSubjectTotals) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageSubjectTotals is one subject's spend over a report's window.",
		Fields: map[string]apidoc.Field{
			"Cost": {Doc: `Cost is the cost of the subject's priced LLM calls, as a decimal string with ten fractional
digits.`},
			"Activities": {Doc: `Activities is the number of distinct user tasks the subject took part in, each counted once a
day. For an agent or a model, these are the tasks with at least one call by or on it. For a user,
they are the user's own tasks. For a workflow, they are the tasks it ran in, whether started at
it or delegated to it by an agent or another workflow. A call that a node's agent passed on to a
peer counts only for the task's root workflow.`},
			"CostPerActivity": {Doc: "CostPerActivity is cost divided by activities; null when activities is 0."},
			"ShareOfSpend": {Doc: `ShareOfSpend is Cost as an unrounded percentage, 0–100, of the whole deployment's cost in the
window; null in the caller's own report, where it would disclose the deployment's spend.`},
			"CostByBucket":  {Doc: "CostByBucket splits cost by token use."},
			"Tokens":        {Doc: "Tokens counts the tokens of the subject's calls, priced or not."},
			"Calls":         {Doc: "Calls is the number of the subject's LLM calls."},
			"UnpricedCalls": {Doc: "UnpricedCalls is the number of those calls with no price configured."},
		},
	}
}

func (UsageDay) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageDay is the spend on one UTC day.",
		Fields: map[string]apidoc.Field{
			"Date":       {Doc: "Date is the UTC day, as YYYY-MM-DD."},
			"Cost":       {Doc: "Cost is the cost of the day's priced calls, as a decimal string with ten fractional digits."},
			"Tokens":     {Doc: "Tokens is the day's tokens by use, from priced and unpriced calls alike."},
			"Activities": {Doc: "Activities is the number of distinct user tasks with a call that day."},
		},
	}
}

func (UsageComparison) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageComparison is the spend over the comparison window.",
		Fields: map[string]apidoc.Field{
			"Cost": {Doc: "Cost is the cost over the comparison window, as a decimal string with ten fractional digits."},
			"ChangePercent": {Doc: `ChangePercent is the change from cost to the window's cost, as an unrounded percentage that may
be negative or above 100; null when cost is 0.`},
		},
	}
}

func (UsageCostByKind) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageCostByKind is the cost each usage list covers over a window, each call counted once. For
every kind but workflows it is the sum of every row the list holds.`,
		Fields: map[string]apidoc.Field{
			"Users":  {Doc: "Users is the cost of every call, as every call is booked to a user; it equals totals.cost."},
			"Agents": {Doc: "Agents is the cost of calls run in an agent; less than totals.cost when some calls ran in none."},
			"Workflows": {Doc: `Workflows is the cost of calls run under a workflow; less than totals.cost when some calls ran
outside one. A call under nested workflows is in each one's row, so the workflows' rows can add
up to more. A call a node's agent passed on to a peer is under the task's root workflow only.`},
			"Models": {Doc: "Models is the cost of every call, as every call names a model; it equals totals.cost."},
		},
	}
}

func (UsageTopUp) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageTopUp is a budget's active top-up.",
		Fields: map[string]apidoc.Field{
			"Amount": {Doc: `Amount is what the top-up adds to the budget's amount, as a decimal string with ten fractional
digits.`},
			"ExpiresTime": {Doc: "ExpiresTime is when the top-up stops applying."},
		},
	}
}

func (UsageBudget) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageBudget is the budget that applies now, and the spend against it in the
current budget period, whatever the report's window.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the budget's UUID."},
			"Name": {Doc: "Name is the budget's name."},
			"Source": {Doc: `Source says how the budget applies: assigned (assigned to the subject) or default (the default
budget of the subject's tier).`},
			"Amount": {Doc: `Amount is the budget's monthly cap, as a decimal string with ten fractional digits; null when it
has none.`},
			"WarningPercentage": {Doc: `WarningPercentage is the percentage of effectiveAmount at which the status becomes approaching;
null when the budget sets none.`},
			"TopUp":           {Doc: "TopUp is the active top-up; null when there is none."},
			"EffectiveAmount": {Doc: "EffectiveAmount is amount plus the active top-up; null when amount is."},
			"PeriodStart":     {Doc: "PeriodStart is the start of the current budget period."},
			"PeriodEnd":       {Doc: "PeriodEnd is the end of the current budget period, exclusive."},
			"PeriodSpend": {Doc: `PeriodSpend is the spend in the current budget period, as a decimal string with ten fractional
digits.`},
			"UsagePercent": {Doc: `UsagePercent is periodSpend as an unrounded percentage of effectiveAmount, which may pass 100;
null when effectiveAmount is. A zero cap reads 100.`},
			"Status": {Doc: `Status is the standing against effectiveAmount: ok, approaching (at or above the warning
threshold), exceeded (the cap is reached) or unlimited (no cap).`},
		},
	}
}

func (UsageBudgetWindow) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageBudgetWindow is a user's spend in the report's window against the
budget for that window.`,
		Fields: map[string]apidoc.Field{
			"WindowBudget": {Doc: `WindowBudget is the budget for the window, as a decimal string with ten fractional digits. A
whole calendar month counts its full amount and a partial month its amount prorated by day; the
current month uses the effective amount, earlier months the base amount.`},
			"UsagePercent": {Doc: `UsagePercent is the window's cost as an unrounded percentage of windowBudget; a zero
windowBudget reads 100.`},
		},
	}
}

func (UsageUser) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageUser is the user record behind a user's spend.",
		Fields: map[string]apidoc.Field{
			"Email": {Doc: "Email is the user's email address; null when the identity provider gave none."},
			"IsSystemUser": {Doc: `IsSystemUser is true for a service account, such as one an API key acts as.

Deprecated: Reason: system users were renamed to service accounts; read isServiceAccount instead. Removal Date: 2027-12-01.`},
			"IsServiceAccount": {Doc: "IsServiceAccount is true for a service account, such as one an API key acts as."},
		},
	}
}

func (UsageServedModel) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageServedModel is the spend on one provider model that served a model's
calls in the window.`,
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the provider's name for the model, or the name an inline model's call gave; null for
calls whose details were not recorded, which happens under load.`},
			"Provider": {Doc: "Provider is the model's provider; null when name is, and for a model defined inline."},
			"Cost": {Doc: `Cost is the cost of the calls this provider model served, as a decimal string with ten
fractional digits.`},
			"Activities":    {Doc: "Activities is the number of distinct user tasks with a call it served, counted once a day."},
			"CostByBucket":  {Doc: "CostByBucket splits cost by token use."},
			"Tokens":        {Doc: "Tokens counts the tokens of the calls it served, priced or not."},
			"Calls":         {Doc: "Calls is the number of calls it served."},
			"UnpricedCalls": {Doc: "UnpricedCalls is the number of those calls with no price configured."},
		},
	}
}

func (UsageRoot) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UsageRoot is an agent or workflow a user's tasks started at, and what those
tasks cost.`,
		Fields: map[string]apidoc.Field{
			"Kind": {Doc: "Kind is what the tasks started at: agent or workflow."},
			"ID": {Doc: `ID is the agent's or workflow's UUID; null for one that Agent Mesh has no record of, such as one
defined in YAML.`},
			"Name": {Doc: "Name is the agent's or workflow's display name."},
			"BookedName": {Doc: `BookedName is the name the spend was recorded under when id is null, and null otherwise;
GET /usage/agents/byName or /usage/workflows/byName reads it.`},
			"Cost": {Doc: `Cost is the cost of the user's tasks started there in the window, as a decimal string with ten
fractional digits.`},
			"ShareOfUser": {Doc: "ShareOfUser is Cost as an unrounded percentage, 0–100, of the user's cost."},
		},
	}
}

func (UsageMonth) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UsageMonth is the caller's spend in one UTC calendar month.",
		Fields: map[string]apidoc.Field{
			"Month": {Doc: "Month is the month, as YYYY-MM."},
			"Cost":  {Doc: "Cost is the caller's spend in the month, as a decimal string with ten fractional digits."},
			"BudgetAmount": {Doc: `BudgetAmount is the caller's budget for the month; null when no budget applies or it has no cap.
Budgets keep no history, so every month carries the current budget: the effective amount for
this month and the base amount for earlier ones.`},
			"PartialFromDate": {Doc: `PartialFromDate is the first day of the month still in the retained history, as YYYY-MM-DD;
null when the whole month is.`},
		},
	}
}

func (usageWindowRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "usageWindowRequest selects the window of a report.",
		Fields: map[string]apidoc.Field{
			"Period": {Doc: `Period is a named window: this_month (the default), last_month, last_7_days, last_30_days or
last_90_days. Leave it out to send startDate and endDate instead.`},
			"StartDate": {Doc: `StartDate is the window's first day, as YYYY-MM-DD or an ISO 8601 date-time standing for the UTC
day containing it; sent together with endDate.`},
			"EndDate": {Doc: "EndDate is the window's last day, included, in the same form as startDate."},
		},
	}
}
