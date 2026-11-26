// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	CategoryScheduledTask = a2a.CategoryScheduledTask
	CategoryAgentMessage  = a2a.CategoryAgentMessage
	CategoryHIL           = a2a.CategoryHIL
	CategoryChatShare     = a2a.CategoryChatShare

	CategoryBudgetSelfWarn        = a2a.CategoryBudgetSelfWarn
	CategoryBudgetSelfLimit       = a2a.CategoryBudgetSelfLimit
	CategoryBudgetMeshWarn        = a2a.CategoryBudgetMeshWarn
	CategoryBudgetMeshLimit       = a2a.CategoryBudgetMeshLimit
	CategoryBudgetSystemUserWarn  = a2a.CategoryBudgetSystemUserWarn
	CategoryBudgetSystemUserLimit = a2a.CategoryBudgetSystemUserLimit
)

type Category struct {
	Key              string
	Label            string
	Group            string
	Description      string
	GatesInbox       bool
	RequiresAnyScope []string
}

func (c Category) offeredTo(id *auth.Subject) bool {
	if len(c.RequiresAnyScope) == 0 {
		return true
	}
	return id != nil && slices.ContainsFunc(c.RequiresAnyScope, func(scope string) bool { return id.HasScope(scope) })
}

const (
	groupAgentsAndTasks     = "Agents and Tasks"
	groupRequestsAndSharing = "Requests and Sharing"
	groupBudget             = "Budget"
)

var categoryCatalog = []Category{
	{Key: CategoryScheduledTask, Label: "Scheduled task results", Group: groupAgentsAndTasks, GatesInbox: true},
	{
		Key: CategoryAgentMessage, Label: "Messages from agents and workflows", Group: groupAgentsAndTasks, GatesInbox: true,
		Description: "Sent when an agent or workflow finishes or finds something to report, without an open chat.",
	},
	{
		Key: CategoryHIL, Label: "Tool approvals and agent input requests", Group: groupRequestsAndSharing, GatesInbox: false,
		Description: "If a request expires, the agent continues without an answer, a tool call that waits for approval does not run, and a workflow step that waits for approval fails.",
	},
	{Key: CategoryChatShare, Label: "Chats shared with me", Group: groupRequestsAndSharing, GatesInbox: true},
	ownBudgetCategory(CategoryBudgetSelfWarn, "Approaching my budget threshold"),
	ownBudgetCategory(CategoryBudgetSelfLimit, "My budget reached"),
	adminBudgetCategory(CategoryBudgetMeshWarn, "Approaching Agent Mesh budget threshold"),
	adminBudgetCategory(CategoryBudgetMeshLimit, "Agent Mesh budget reached"),
	adminBudgetCategory(CategoryBudgetSystemUserWarn, "Approaching a service account budget threshold"),
	adminBudgetCategory(CategoryBudgetSystemUserLimit, "Service account budget reached"),
}

func ownBudgetCategory(key, label string) Category {
	return Category{Key: key, Label: label, Group: groupBudget, GatesInbox: true}
}

func adminBudgetCategory(key, label string) Category {
	c := ownBudgetCategory(key, label)
	c.RequiresAnyScope = rbac.BudgetAdminScopes()
	return c
}

func Categories() []Category {
	out := make([]Category, len(categoryCatalog))
	copy(out, categoryCatalog)
	return out
}

func (n *NotifyCore) offeredCategories(caller *auth.Subject) []Category {
	out := make([]Category, 0, len(categoryCatalog))
	for _, c := range categoryCatalog {
		if c.offeredTo(caller) {
			out = append(out, c)
		}
	}
	return out
}

func requestCaller(r *http.Request) *auth.Subject {
	id, _ := auth.SubjectFromContext(r.Context())
	return id
}

func LookupCategory(key string) (Category, error) {
	for _, c := range categoryCatalog {
		if c.Key == key {
			return c, nil
		}
	}
	return Category{}, unknownCategoryError(key)
}

func unknownCategoryError(key string) error {
	return fmt.Errorf("unknown notification category: %q", key)
}
