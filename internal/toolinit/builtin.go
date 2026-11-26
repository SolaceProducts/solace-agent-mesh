// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

var builtinToolFactories = map[string]func(toolMap map[string]any) tool.BuiltinTool{
	"list_artifacts":                    func(map[string]any) tool.BuiltinTool { return &tools.ListArtifactsTool{} },
	"load_artifact":                     func(map[string]any) tool.BuiltinTool { return &tools.LoadArtifactTool{} },
	"delete_artifact":                   func(map[string]any) tool.BuiltinTool { return &tools.DeleteArtifactTool{} },
	"append_to_artifact":                func(map[string]any) tool.BuiltinTool { return &tools.AppendToArtifactTool{} },
	"artifact_search_and_replace_regex": func(map[string]any) tool.BuiltinTool { return &tools.SearchAndReplaceInArtifactTool{} },
	"get_current_time":                  func(map[string]any) tool.BuiltinTool { return &tools.GetCurrentDatetimeTool{} },
	"web_request":                       func(m map[string]any) tool.BuiltinTool { return tools.NewWebRequestTool(extractToolConfig(m)) },
	"create_image_from_description": func(m map[string]any) tool.BuiltinTool {
		return tools.NewCreateImageFromDescriptionTool(extractToolConfig(m))
	},
	"describe_image":         func(m map[string]any) tool.BuiltinTool { return tools.NewDescribeImageTool(extractToolConfig(m)) },
	"describe_audio":         func(m map[string]any) tool.BuiltinTool { return tools.NewDescribeAudioTool(extractToolConfig(m)) },
	"edit_image_with_gemini": func(m map[string]any) tool.BuiltinTool { return tools.NewEditImageWithGeminiTool(extractToolConfig(m)) },
	"generate_image_with_gemini": func(m map[string]any) tool.BuiltinTool {
		return tools.NewGenerateImageWithGeminiTool(extractToolConfig(m))
	},
	"select_voice":               func(m map[string]any) tool.BuiltinTool { return tools.NewSelectVoiceTool(extractToolConfig(m)) },
	"transcribe_audio":           func(m map[string]any) tool.BuiltinTool { return tools.NewTranscribeAudioTool(extractToolConfig(m)) },
	"ask_user_question":          func(map[string]any) tool.BuiltinTool { return &tools.AskUserQuestionTool{} },
	"notify_user":                func(map[string]any) tool.BuiltinTool { return &tools.NotifyUserTool{} },
	"schedule_task":              func(map[string]any) tool.BuiltinTool { return &tools.ScheduleTaskTool{} },
	"list_scheduled_tasks":       func(map[string]any) tool.BuiltinTool { return &tools.ListScheduledTasksTool{} },
	"update_scheduled_task":      func(map[string]any) tool.BuiltinTool { return &tools.UpdateScheduledTaskTool{} },
	"delete_scheduled_task":      func(map[string]any) tool.BuiltinTool { return &tools.DeleteScheduledTaskTool{} },
	"set_scheduled_task_enabled": func(map[string]any) tool.BuiltinTool { return &tools.SetScheduledTaskEnabledTool{} },
	"validate_component_config": func(map[string]any) tool.BuiltinTool {
		return &tools.ValidateComponentConfigTool{ToolHasToolset: BuiltinToolHasToolset}
	},
	"validate_build_manifest": func(map[string]any) tool.BuiltinTool { return &tools.ValidateBuildManifestTool{} },
	"yaml_merge_artifact":     func(map[string]any) tool.BuiltinTool { return &tools.YAMLMergeArtifactTool{} },
	"discover_mcp_tools":      func(map[string]any) tool.BuiltinTool { return &tools.DiscoverMCPToolsTool{} },
	"check_mcp_server_auth":   func(map[string]any) tool.BuiltinTool { return &tools.CheckMCPServerAuthTool{} },
	"parse_openapi_spec":      func(map[string]any) tool.BuiltinTool { return &tools.ParseOpenAPISpecTool{} },
	"describe_tools":          func(map[string]any) tool.BuiltinTool { return &tools.DescribeToolsTool{} },
	"query_data_with_sql":     func(m map[string]any) tool.BuiltinTool { return tools.NewQueryDataWithSQLTool(extractToolConfig(m)) },
	"transform_data_with_jmespath": func(m map[string]any) tool.BuiltinTool {
		return tools.NewTransformDataWithJMESPathTool(extractToolConfig(m))
	},
	"merge_structured_data":         func(map[string]any) tool.BuiltinTool { return &tools.MergeStructuredDataTool{} },
	"reinterpret_artifact":          func(map[string]any) tool.BuiltinTool { return &tools.ReinterpretArtifactTool{} },
	"artifact_grep":                 func(map[string]any) tool.BuiltinTool { return &tools.GrepTool{} },
	"index_search":                  func(map[string]any) tool.BuiltinTool { return &tools.IndexSearchTool{} },
	"platform_list_components":      func(map[string]any) tool.BuiltinTool { return &tools.ListPlatformComponentsTool{} },
	"platform_get_component_config": func(map[string]any) tool.BuiltinTool { return &tools.GetComponentConfigTool{} },
	"web_search_google":             func(m map[string]any) tool.BuiltinTool { return tools.NewGoogleWebSearchTool(extractToolConfig(m)) },
	"web_search":                    func(m map[string]any) tool.BuiltinTool { return tools.NewWebSearchTool(extractToolConfig(m)) },
	"deep_research":                 func(m map[string]any) tool.BuiltinTool { return tools.NewDeepResearchTool(extractToolConfig(m)) },
	"RedirectUser":                  func(m map[string]any) tool.BuiltinTool { return tools.NewRedirectUserTool(extractToolConfig(m)) },
	"redirect_user":                 func(m map[string]any) tool.BuiltinTool { return tools.NewRedirectUserTool(extractToolConfig(m)) },
	"request_build_activate":        func(map[string]any) tool.BuiltinTool { return &tools.RequestBuildActivateTool{} },
	"scheduled_task_redirect":       func(map[string]any) tool.BuiltinTool { return tools.NewScheduledTaskRedirectTool() },
	"create_sqlite_db":              func(m map[string]any) tool.BuiltinTool { return tools.NewCreateSQLiteDBTool(extractToolConfig(m)) },
	"query_task_health":             func(map[string]any) tool.BuiltinTool { return tools.NewQueryTaskHealthTool() },
	"query_failed_tasks":            func(map[string]any) tool.BuiltinTool { return tools.NewQueryFailedTasksTool() },
	"query_slow_tasks":              func(map[string]any) tool.BuiltinTool { return tools.NewQuerySlowTasksTool() },
	"query_agent_performance":       func(map[string]any) tool.BuiltinTool { return tools.NewQueryAgentPerformanceTool() },
	"query_token_usage":             func(map[string]any) tool.BuiltinTool { return tools.NewQueryTokenUsageTool() },
	"query_scheduled_task_health":   func(map[string]any) tool.BuiltinTool { return tools.NewQueryScheduledTaskHealthTool() },
	"query_feedback_summary":        func(map[string]any) tool.BuiltinTool { return tools.NewQueryFeedbackSummaryTool() },
	"query_tasks":                   func(map[string]any) tool.BuiltinTool { return tools.NewQueryTasksTool() },
	"report_activity":               func(map[string]any) tool.BuiltinTool { return tools.NewReportActivityTool() },
	"list_failed_tasks":             func(map[string]any) tool.BuiltinTool { return tools.NewListFailedTasksTool() },
	"summarize_feedback":            func(map[string]any) tool.BuiltinTool { return tools.NewSummarizeFeedbackTool() },
	"list_feedback":                 func(map[string]any) tool.BuiltinTool { return tools.NewListFeedbackTool() },
	"explain_task":                  func(map[string]any) tool.BuiltinTool { return tools.NewExplainTaskTool() },
	"explain_feedback":              func(map[string]any) tool.BuiltinTool { return tools.NewExplainFeedbackTool() },
	"datadog_query_logs":            func(m map[string]any) tool.BuiltinTool { return tools.NewDatadogQueryLogsTool(extractToolConfig(m)) },
	"datadog_get_errors":            func(m map[string]any) tool.BuiltinTool { return tools.NewDatadogGetErrorsTool(extractToolConfig(m)) },
	"correlate_task_with_logs": func(m map[string]any) tool.BuiltinTool {
		return tools.NewCorrelateTaskWithLogsTool(extractToolConfig(m))
	},
	subTaskToolName: func(map[string]any) tool.BuiltinTool { return nil },
}

const subTaskToolName = "sub_task"

func LookupBuiltinTool(name string, toolMap map[string]any, opts Options) tool.BuiltinTool {
	if factory, ok := builtinToolFactories[name]; ok {
		return factory(toolMap)
	}
	if opts.AdditionalBuiltin != nil {
		return opts.AdditionalBuiltin(name, toolMap)
	}
	return nil
}

func SkillResourceToolNames() []string {
	return []string{skillruntime.ListResourcesToolName, skillruntime.ReadResourceToolName, skillruntime.GrepResourcesToolName}
}

func TemplateToolNames() []string {
	return []string{"instantiate_template", "read_template"}
}

func TemplateAuthoringToolNames() []string {
	return []string{"verify_template_candidate", "package_template"}
}

func LookupBuiltinGroup(group string, opts Options) []string {
	switch group {
	case "artifact_management":
		return []string{"list_artifacts", "load_artifact", "delete_artifact", "append_to_artifact", "artifact_search_and_replace_regex", "artifact_grep"}
	case "general_agent_tools":
		return []string{"get_current_time", "convert_file_to_markdown", "convert_pdf_to_markdown", "pdf_extract_text", "ask_user_question"}
	case "image_tools":
		return []string{"create_image_from_description", "describe_image", "describe_audio", "edit_image_with_gemini", "generate_image_with_gemini"}
	case "web_tools":
		return []string{"web_request", "web_search_google"}
	case "research":
		return []string{"web_search_google", "deep_research"}
	case "builtin_artifact_tools":
		return LookupBuiltinGroup("artifact_management", opts)
	case "builtin_web_request_tools":
		return LookupBuiltinGroup("web_tools", opts)
	case "builtin_research_tools":
		return LookupBuiltinGroup("research", opts)
	case "builtin_image_tools":
		return LookupBuiltinGroup("image_tools", opts)
	case "builtin_file_tools":
		return []string{"convert_file_to_markdown", "convert_pdf_to_markdown", "pdf_extract_text"}
	case "builtin_time_tools":
		return []string{"get_current_time"}
	case "builtin_document_tools":
		return []string{"pptx_create_presentation", "pdf_ops", "render_document_to_images", "html_to_pdf"}
	case "builtin_diagram_tools":
		return []string{"mermaid_diagram_generator"}
	case "builtin_media_tools":
		return []string{"ffmpeg", "ffprobe", "image_magick"}
	case "builtin_volume_tools":
		return []string{
			"volume_read", "volume_write", "volume_edit", "volume_append",
			"volume_list", "volume_glob", "volume_grep", "volume_stat",
			"volume_mkdir", "volume_move", "volume_delete",
		}
	case "hil_tools":
		return []string{"ask_user_question"}
	case "notification_tools":
		return []string{"notify_user"}
	case "scheduling_tools":
		return []string{
			"schedule_task",
			"list_scheduled_tasks",
			"update_scheduled_task",
			"delete_scheduled_task",
			"set_scheduled_task_enabled",
		}
	case "code_execution", "builtin_code_execution":
		return []string{"execute_python_code"}
	case "builder_tools":
		return []string{
			"validate_component_config",
			"validate_build_manifest",
			"yaml_merge_artifact",
			"platform_list_components",
			"platform_get_component_config",
			"discover_mcp_tools",
			"parse_openapi_spec",
			"check_mcp_server_auth",
			"describe_tools",
			"request_build_activate",
		}
	case "observability_analytics":
		return []string{
			"query_task_health",
			"query_failed_tasks",
			"query_slow_tasks",
			"query_agent_performance",
			"query_token_usage",
			"query_scheduled_task_health",
			"query_feedback_summary",
			"query_tasks",
		}
	case "activity_reporting":
		return []string{
			"report_activity",
			"list_failed_tasks",
			"explain_task",
			"summarize_feedback",
			"list_feedback",
			"explain_feedback",
			"transform_data_with_jmespath",
		}
	case "observability_datadog":
		return []string{
			"datadog_query_logs",
			"datadog_get_errors",
			"correlate_task_with_logs",
		}
	case "observability_platform_read":
		return []string{
			"platform_list_components",
			"platform_get_component_config",
		}
	case "data_analysis":
		return []string{
			"query_data_with_sql",
			"create_sqlite_db",
			"transform_data_with_jmespath",
			"merge_structured_data",
			"reinterpret_artifact",
			"create_chart_from_plotly_config",
		}
	}
	if opts.AdditionalBuiltinGroup != nil {
		return opts.AdditionalBuiltinGroup(group)
	}
	return nil
}

func BuiltinToolNames() []string {
	return slices.Sorted(maps.Keys(builtinToolFactories))
}
