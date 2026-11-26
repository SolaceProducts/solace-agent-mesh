// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"fmt"
	"slices"
	"strings"
)

type BuiltinGroupInfo struct {
	Tools string
	Usage string
}

var ValidBuiltinGroups = map[string]BuiltinGroupInfo{
	"artifact_management": {
		Tools: "**list_artifacts** (list all artifacts), **load_artifact** (load artifact content with line ranges), **delete_artifact** (delete artifact with confirmation), **append_to_artifact** (append a chunk to an existing artifact as a new version), **artifact_search_and_replace_regex** (search-and-replace in artifacts), **artifact_grep** (regex search across artifacts)",
		Usage: "Agent needs to create, read, modify, or search files/artifacts. Most agents need this.",
	},
	"general_agent_tools": {
		Tools: "**get_current_time** (current time with timezone), **convert_file_to_markdown** (HTML/CSV/DOCX/XLSX/PPTX/EPUB/IPYNB → Markdown; STR-backed), **convert_pdf_to_markdown** (PDF → Markdown via markitdown; layout-aware, and bordered tables are read from the ruled-cell grid into a leading Markdown table section; STR-backed, Python), **pdf_extract_text** (per-page PDF text with page numbers for citation; STR-backed, Go, so it is present in embedded and desktop where the Python tools are not), **ask_user_question** (ask user questions with predefined options)",
		Usage: "Interactive agents. **WARNING: ask_user_question is only for interactive agents — never include for autonomous/workflow agents.**",
	},
	"image_tools": {
		Tools: "**create_image_from_description** (generate image from text via OpenAI-compatible API), **describe_image** (describe image using vision API), **describe_audio** (describe audio using multimodal API), **edit_image_with_gemini** (edit image with Gemini), **generate_image_with_gemini** (generate image from text with Gemini)",
		Usage: "Agent works with images or audio. Requires image-capable LLM model.",
	},
	"web_tools": {
		Tools: "**web_request** (HTTP GET/POST/PUT/DELETE, returns response as artifact), **web_search_google** (Google search; citations and sources are sent to the UI, not returned in the tool result)",
		Usage: "Agent needs to fetch web content, search the web, or call external APIs.",
	},
	"research": {
		Tools: "**web_search_google** (Google search; citations go to the UI, not the tool result), **deep_research** (iterative LLM-powered research with web search, source analysis, and report generation)",
		Usage: "Agent needs to search the web or conduct in-depth research on topics.",
	},
	"data_analysis": {
		Tools: "**query_data_with_sql** (SQL queries against CSV/JSON/YAML artifacts loaded as SQLite tables), **create_sqlite_db** (converts a CSV or JSON artifact into a persisted SQLite DB artifact for repeated querying), **transform_data_with_jmespath** (JMESPath expressions for filtering/reshaping JSON/YAML/CSV), **merge_structured_data** (RFC 7396 deep merge of JSON/YAML artifacts)",
		Usage: "Agent analyzes or transforms structured data.",
	},
	"builder_tools": {
		Tools: "validate_component_config, validate_build_manifest, yaml_merge_artifact, platform_list_components, platform_get_component_config, discover_mcp_tools, parse_openapi_spec, describe_tools, check_mcp_server_auth",
		Usage: "Internal — only for the builder agent.",
	},
	"observability_analytics": {
		Tools: "**query_task_health** (task health summary), **query_failed_tasks**, **query_slow_tasks**, **query_agent_performance**, **query_token_usage**, **query_scheduled_task_health**, **query_feedback_summary**, **query_tasks** (general ad-hoc analytics: filter by agent/user/status + group by agent/user/status/day/week/month) — read-only observability over the gateway operational store via the control protocol.",
		Usage: "Admin observability (Sentinel). The gateway enforces analytics:_:read server-side.",
	},
	"activity_reporting": {
		Tools: "**report_activity** (HTML report artifact — per-agent, per-workflow breakdowns for a window — plus the full underlying data as a queryable JSON artifact), **list_failed_tasks** (markdown report shown in the reply — paginated failed / error / timeout tasks for a window — plus the page as a queryable JSON artifact), **explain_task** (markdown report shown in the reply for one task ID — execution tree, timeline, root cause, related failures — plus the trace JSON the LLM uses for recommendations), **summarize_feedback** (markdown report shown in the reply — per-agent and per-workflow thumbs-up/down totals, plus recent negative comments — and the data as a queryable JSON artifact), **list_feedback** (markdown report shown in the reply — newest individual feedback entries for an agent, workflow or user — and the rows as a queryable JSON artifact), **explain_feedback** (markdown report shown in the reply for one feedback id — the chat session up to the rated turn, each turn's tree and timeline — plus the session JSON the LLM uses to explain the rating), **transform_data_with_jmespath** (queries the JSON artifact report_activity, list_failed_tasks, summarize_feedback or list_feedback saved, for anything beyond what's already shown). Handler-first attribution (topic-derived).",
		Usage: "The Activity Monitor's toolset. Invocation is gated by the standard agent-invoke machinery (same as Orchestrator — no custom scope required). Cross-user activity drill-down + content read requires the activity:*:read scope; feedback reads follow the caller's read_feedback reach, and a feedback session opens for its owner (unless another user owns one of its sub-tasks), an activity:*:read holder, or a caller whose read_trace covers every agent in it. All of it is enforced server-side. explain_task returns 404 rather than 403 when a non-admin queries a task they don't own — probing task IDs cannot leak existence.",
	},
	"observability_datadog": {
		Tools: "**datadog_query_logs** (arbitrary Datadog log query), **datadog_get_errors** (error logs by service), **correlate_task_with_logs** (logs for a task by traceID).",
		Usage: "Admin observability (Sentinel) — Datadog log analysis. Requires per-tool tool_config and the tool:datadog_logs:invoke scope.",
	},
	"observability_platform_read": {
		Tools: "**platform_list_components** (list agents/workflows/entrypoints/connectors/A2A agents/skills by id, name, description and status; tools, models and connector types by name), **platform_get_component_config** (full config of one component) — read-only inspection of platform component configs via the control protocol.",
		Usage: "Admin observability (Sentinel) — read-only config inspection. A read-only subset of builder_tools; the platform service enforces RBAC server-side.",
	},
	"builtin_artifact_tools": {
		Tools: "*(alias for `artifact_management`)*",
		Usage: "Legacy name — use `artifact_management` instead.",
	},
	"builtin_web_request_tools": {
		Tools: "*(alias for `web_tools`)*",
		Usage: "Legacy name — use `web_tools` instead.",
	},
	"builtin_research_tools": {
		Tools: "*(alias for `research`)*",
		Usage: "Legacy name — use `research` instead.",
	},
	"builtin_image_tools": {
		Tools: "*(alias for `image_tools`)*",
		Usage: "Legacy name — use `image_tools` instead.",
	},
	"builtin_file_tools": {
		Tools: "**convert_file_to_markdown** (HTML/CSV/Office formats; STR-backed), **convert_pdf_to_markdown** (PDF via markitdown, writes a Markdown artifact you can then search; STR-backed, Python), **pdf_extract_text** (per-page PDF text, in the result for a short PDF and spilled to a JSON artifact past the tool-output limit; STR-backed, Go, so it is present in embedded and desktop where the Python tools are not)",
		Usage: "File format conversion. For a PDF, prefer convert_pdf_to_markdown when you want a searchable Markdown artifact; pdf_extract_text gives per-page text with page numbers for citation, and is the route available when no Python tools are hosted.",
	},
	"builtin_time_tools": {
		Tools: "**get_current_time**",
		Usage: "Date/time only.",
	},
	"hil_tools": {
		Tools: "**ask_user_question** (human-in-the-loop question tool)",
		Usage: "When you need ONLY ask_user_question without datetime/markdown tools. Prefer `general_agent_tools` for interactive agents.",
	},
	"scheduling_tools": {
		Tools: "**schedule_task** (schedule a future or recurring invocation of a workflow or agent — one-shot at a timestamp, or repeating on cron/interval; the invocation runs as a fresh task under the caller's identity), **list_scheduled_tasks**, **update_scheduled_task**, **delete_scheduled_task**, **set_scheduled_task_enabled** (view and manage the caller's scheduled tasks in-chat on any gateway; ownership is enforced server-side)",
		Usage: "Agent or workflow needs to set a timer/reminder, run a follow-up after a delay, escalate if something hasn't happened, kick off recurring work, or let the user review/change/pause/delete their scheduled tasks conversationally.",
	},
	"code_execution": {
		Tools: "**execute_python_code** (run LLM-generated Python in a sandboxed runtime over artifacts — pandas, numpy, scipy, scikit-learn, matplotlib, Pillow, openpyxl, and more; reads input artifacts, writes output artifacts; no network access)",
		Usage: "Agent needs to run custom Python for computation, data wrangling, analysis, or chart/file generation over artifacts. Builder visibility is gated by the python_code_execution feature flag (off by default); the toolset is always valid at runtime.",
	},
	"builtin_code_execution": {
		Tools: "*(alias for `code_execution`)*",
		Usage: "Platform/builder name — resolves to `code_execution`.",
	},
	"builtin_document_tools": {
		Tools: "**pptx_create_presentation** (build PowerPoint decks; STR-backed), **pdf_ops** (merge/split/extract/rotate/watermark/encrypt PDFs; STR-backed), **render_document_to_images** (render a pptx/docx/xlsx/pdf to one PNG per page so the agent can visually inspect its output; STR-backed)",
		Usage: "Agent creates PowerPoint presentations, manipulates PDF files, or needs to visually check a rendered document/deck.",
	},
	"builtin_diagram_tools": {
		Tools: "**mermaid_diagram_generator** (render Mermaid syntax to PNG/SVG; STR-backed)",
		Usage: "Agent generates diagrams from Mermaid syntax.",
	},
	"builtin_media_tools": {
		Tools: "**ffmpeg** (encode/convert/process audio & video; STR-backed), **ffprobe** (inspect media; STR-backed), **image_magick** (resize/crop/convert/composite images; STR-backed)",
		Usage: "Agent processes audio, video, or images via FFmpeg/ImageMagick.",
	},
	"builtin_volume_tools": {
		Tools: "**volume_read**, **volume_write**, **volume_edit**, **volume_append**, **volume_list**, **volume_glob**, **volume_grep**, **volume_stat**, **volume_mkdir**, **volume_move**, **volume_delete** — filesystem operations over a mounted SAM volume (all STR-backed, via the volume_fs tool)",
		Usage: "Agent reads, writes, searches, and manages files in a persistent/ephemeral SAM volume mounted into the tool sandbox. Requires the `volumes` feature flag and a volume bound to the tool's workspace slot.",
	},
}

var UngroupedBuiltinToolNames = map[string]bool{
	"index_search":     true,
	"select_voice":     true,
	"sub_task":         true,
	"transcribe_audio": true,
	"web_search":       true,
}

var AutoRegisteredBuiltinToolNames = map[string]bool{
	"index_search": true,
	"sub_task":     true,
}

var ValidBuiltinToolNames = map[string]bool{
	"list_artifacts":                    true,
	"load_artifact":                     true,
	"delete_artifact":                   true,
	"append_to_artifact":                true,
	"artifact_search_and_replace_regex": true,
	"artifact_grep":                     true,

	"get_current_time":  true,
	"ask_user_question": true,

	"create_image_from_description": true,
	"describe_image":                true,
	"describe_audio":                true,
	"edit_image_with_gemini":        true,
	"generate_image_with_gemini":    true,

	"select_voice":     true,
	"transcribe_audio": true,

	"web_search": true,

	"web_request":       true,
	"web_search_google": true,

	"deep_research": true,

	"query_data_with_sql":          true,
	"create_sqlite_db":             true,
	"transform_data_with_jmespath": true,
	"merge_structured_data":        true,

	"validate_component_config":     true,
	"validate_build_manifest":       true,
	"yaml_merge_artifact":           true,
	"platform_list_components":      true,
	"platform_get_component_config": true,
	"discover_mcp_tools":            true,
	"parse_openapi_spec":            true,
	"describe_tools":                true,
	"check_mcp_server_auth":         true,

	"schedule_task":              true,
	"list_scheduled_tasks":       true,
	"update_scheduled_task":      true,
	"delete_scheduled_task":      true,
	"set_scheduled_task_enabled": true,

	"query_task_health":           true,
	"query_failed_tasks":          true,
	"query_slow_tasks":            true,
	"query_agent_performance":     true,
	"query_token_usage":           true,
	"query_tasks":                 true,
	"query_scheduled_task_health": true,
	"query_feedback_summary":      true,

	"datadog_query_logs":       true,
	"datadog_get_errors":       true,
	"correlate_task_with_logs": true,

	"report_activity":    true,
	"list_failed_tasks":  true,
	"explain_task":       true,
	"summarize_feedback": true,
	"list_feedback":      true,
	"explain_feedback":   true,

	"index_search": true,

	"sub_task": true,
}

func IsValidBuiltinGroup(name string) bool {
	_, ok := ValidBuiltinGroups[name]
	return ok
}

var builtinGroupAliases = map[string]string{
	"artifact_management": "builtin_artifact_tools",
	"web_tools":           "builtin_web_request_tools",
	"research":            "builtin_research_tools",
	"image_tools":         "builtin_image_tools",
	"code_execution":      "builtin_code_execution",
}

func CanonicalBuiltinGroup(name string) string {
	if id, ok := builtinGroupAliases[name]; ok {
		return id
	}
	return name
}

func BuiltinGroupAliasesFor(toolsetID string) []string {
	var out []string
	for alias, target := range builtinGroupAliases {
		if target == toolsetID {
			out = append(out, alias)
		}
	}
	slices.Sort(out)
	return out
}

func CanonicalBuiltinGroups(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		id := CanonicalBuiltinGroup(name)
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func IsValidBuiltinToolName(name string) bool {
	return ValidBuiltinToolNames[name]
}

var primaryGroups = []string{
	"artifact_management",
	"general_agent_tools",
	"image_tools",
	"web_tools",
	"research",
	"data_analysis",
	"code_execution",
	"builder_tools",
	"hil_tools",
}

func GenerateToolGroupsTable() string {
	var b strings.Builder
	b.WriteString("| Group | Tools Included | Use When |\n")
	b.WriteString("|-------|---------------|----------|\n")

	written := make(map[string]bool, len(primaryGroups))

	for _, name := range primaryGroups {
		info, ok := ValidBuiltinGroups[name]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, info.Tools, info.Usage)
		written[name] = true
	}

	remaining := make([]string, 0)
	for name := range ValidBuiltinGroups {
		if !written[name] {
			remaining = append(remaining, name)
		}
	}
	slices.Sort(remaining)
	for _, name := range remaining {
		info := ValidBuiltinGroups[name]
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, info.Tools, info.Usage)
	}

	return b.String()
}
