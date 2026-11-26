// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const builderToolsGroup = "builder_tools"

const (
	reasonSentinelAnalytics = "Sentinel analytics, admin only, outside the builder toolset catalog"
	reasonSentinelDatadog   = "Sentinel Datadog analysis, admin only, outside the builder toolset catalog"
	reasonActivityMonitor   = "Activity Monitor tool, outside the builder toolset catalog"
)

var nonBindableGroups = []string{
	builderToolsGroup,
	"observability_analytics",
	"observability_datadog",
	"observability_platform_read",
	"activity_reporting",
}

var nonBindableBuiltins = map[string]string{
	"platform_list_components":      "platform control tool",
	"platform_get_component_config": "platform control tool",
	"sub_task":                      "registered by the agent itself; its factory returns nil",
	"index_search":                  "registered by the agent itself in an indexed project chat; nothing to bind",
	"select_voice":                  "in no toolset; not available to a built-in agent",
	"transcribe_audio":              "in no toolset; not available to a built-in agent",
	"web_search":                    "in no toolset; a Web Search connector serves it under the connector tool's name",
	"redirect_user":                 "chat UI redirect, in no toolset",
	"RedirectUser":                  "PascalCase spelling of redirect_user, in no toolset",
	"scheduled_task_redirect":       "chat UI redirect, in no toolset",
	"notify_user":                   "in no catalogued toolset; gated by notify:_:send",
	"query_task_health":             reasonSentinelAnalytics,
	"query_failed_tasks":            reasonSentinelAnalytics,
	"query_slow_tasks":              reasonSentinelAnalytics,
	"query_agent_performance":       reasonSentinelAnalytics,
	"query_token_usage":             reasonSentinelAnalytics,
	"query_scheduled_task_health":   reasonSentinelAnalytics,
	"query_feedback_summary":        reasonSentinelAnalytics,
	"query_tasks":                   reasonSentinelAnalytics,
	"datadog_query_logs":            reasonSentinelDatadog,
	"datadog_get_errors":            reasonSentinelDatadog,
	"correlate_task_with_logs":      reasonSentinelDatadog,
	"report_activity":               reasonActivityMonitor,
	"list_failed_tasks":             reasonActivityMonitor,
	"explain_task":                  reasonActivityMonitor,
	"summarize_feedback":            reasonActivityMonitor,
	"list_feedback":                 reasonActivityMonitor,
	"explain_feedback":              reasonActivityMonitor,
}

func AgentBindableBuiltin(name string) bool {
	if _, ok := builtinToolFactories[name]; !ok {
		return false
	}
	if _, excluded := nonBindableBuiltins[name]; excluded {
		return false
	}
	return !slices.Contains(LookupBuiltinGroup(builderToolsGroup, Options{}), name)
}

func STRBuiltinNames() []string {
	names := map[string]struct{}{}
	for group := range schema.ValidBuiltinGroups {
		if slices.Contains(nonBindableGroups, group) {
			continue
		}
		for _, name := range LookupBuiltinGroup(group, Options{}) {
			if _, inProcess := builtinToolFactories[name]; !inProcess {
				names[name] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(names))
}
