// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

var BudgetSchema = KindSchema{
	Kind:        "budget",
	PluralPath:  "budgets",
	Description: "An LLM spend budget: the Agent Mesh budget, which limits the total spend of the whole Agent Mesh, or a user budget, which limits the spend of each user assigned to it. The file states the whole budget, so an omitted warningPercentage clears the threshold and an unknown spec key fails the plan. The Agent Mesh budget is matched by budget scope, so a new name renames it; a user budget is matched by name, ignoring case, so any other new name plans as a delete plus a create.",
	Fields: []FieldSchema{
		{
			JSONName:    "subjectType",
			GoType:      "string",
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			OneOf:       []string{"user", "mesh"},
			Description: "The budget scope: user for a budget that limits each user assigned to it, mesh for the one budget that covers the whole Agent Mesh.",
		},
		{
			JSONName:            "amount",
			GoType:              "any",
			DisplayTypeOverride: "decimal or null",
			Required:            true,
			MinLen:              -1,
			MaxLen:              -1,
			Description:         "The budget's limit in US dollars for each budget period (a calendar month, UTC), with at most two decimal places, such as \"250\" or \"19.99\". null means unlimited. The key is required, so an unlimited budget writes amount: null. Quote an amount with many digits, since a YAML decimal number with more than 15 significant digits is refused.",
		},
		{
			JSONName:    "warningPercentage",
			GoType:      "*int",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "The share of the amount, from 1 to 100, at which the budget is reported as approaching its limit. Omit it or write null for no warning. Needs an amount: an unlimited budget cannot set one.",
		},
		{
			JSONName:    "isDefault",
			GoType:      "*bool",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "true makes this user budget the default, the one that applies to every user with no assignment. Omitting the key leaves the default unmanaged, because a default can only be moved to another budget, never cleared: false on the current default fails the plan unless another budget is marked true. The Agent Mesh budget refuses false.",
		},
		{
			JSONName:            "users",
			GoType:              goTypeStringSlice,
			DisplayTypeOverride: "list<string or object>",
			MinLen:              -1,
			MaxLen:              -1,
			Description:         "The user budget's assignees. Each entry is an email as a bare string, a service account as system:<name> (the built-in default, eval and evidence included; evidence is the one the evidence service books its model spend to), or a typed identity { type: email|sub|system, value, issuer? }, where issuer is required for a sub and allowed only for one. A file with the key manages the budget's whole assignee list: an entry moves its user here from any other budget, and removing an entry deletes that assignment, which needs --prune. A file without the key leaves assignments to the UI; users: null or [] removes every assignee and needs --prune. An entry for someone who has not signed in yet applies at their first sign-in. Not allowed on the Agent Mesh budget.",
		},
	},
}
