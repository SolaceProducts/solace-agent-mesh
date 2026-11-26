// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { LucideIcon } from "lucide-react";
import {
    Archive,
    BarChart3,
    Blocks,
    BookOpen,
    Bot,
    Brain,
    Cpu,
    CreditCard,
    Database,
    FileText,
    GitBranch,
    Globe,
    HardDrive,
    IdCard,
    LayoutList,
    Link,
    Lock,
    LogOut,
    Monitor,
    Plug,
    Radar,
    Radio,
    RotateCcw,
    ScrollText,
    Server,
    Settings,
    ShieldCheck,
    Signpost,
    Tag,
    Timer,
    Wrench,
    Zap,
} from "lucide-react";

import type { ConfigType } from "../builderUtils";

export interface SectionMapEntry {
    id: string;
    label: string;
    icon: LucideIcon;
    keys: string[];
}

export type SectionMap = SectionMapEntry[];

const AGENT_SECTION_MAP: SectionMap = [
    {
        id: "identity",
        label: "Agent Details",
        icon: IdCard,
        keys: ["agent_name", "display_name", "namespace", "description", "version", "agent_type"],
    },
    {
        id: "instruction",
        label: "Instructions",
        icon: FileText,
        keys: ["instruction", "global_instruction", "inject_current_time", "inject_system_purpose", "inject_response_format", "inject_user_profile"],
    },
    {
        id: "tools",
        label: "Tools",
        icon: Wrench,
        keys: ["tools"],
    },
    {
        id: "agent_card",
        label: "Agent Card",
        icon: Bot,
        keys: ["agent_card", "agent_card_publishing"],
    },
    {
        id: "model",
        label: "Model",
        icon: Cpu,
        keys: ["model", "supports_streaming", "max_tokens", "max_llm_calls_per_task", "enable_auto_continuation"],
    },
    {
        id: "discovery",
        label: "Discovery",
        icon: Radar,
        keys: ["agent_discovery", "inter_agent_communication", "max_call_depth"],
    },
    {
        id: "skills",
        label: "Skills",
        icon: BookOpen,
        keys: ["skills_base_path", "skills_cache_path", "skills"],
    },
    {
        id: "session",
        label: "Session",
        icon: Database,
        keys: ["session_service"],
    },
    {
        id: "artifacts",
        label: "Artifacts",
        icon: Archive,
        keys: ["artifact_service", "artifact_handling_mode", "text_artifact_content_max_length", "enable_embed_resolution", "enable_artifact_content_instruction", "enable_template_instructions", "enable_examples", "headless"],
    },
    {
        id: "tool_output",
        label: "Tool Output",
        icon: Settings,
        keys: ["tool_output_save_threshold_bytes", "tool_output_llm_return_max_bytes", "mcp_tool_response_save_threshold_bytes", "mcp_tool_llm_return_max_bytes", "mcp_intelligent_processing", "schema_max_keys", "schema_inference_depth"],
    },
    {
        id: "structured_io",
        label: "Structured I/O",
        icon: LayoutList,
        keys: ["input_schema", "output_schema", "validation_max_retries"],
    },
    {
        id: "hil",
        label: "Human-in-Loop",
        icon: ShieldCheck,
        keys: ["hil"],
    },
    {
        id: "streaming",
        label: "Streaming",
        icon: Radio,
        keys: ["stream_batching_threshold_bytes", "max_message_size_bytes"],
    },
    {
        id: "volumes",
        label: "Volumes",
        icon: HardDrive,
        keys: ["volumes"],
    },
];

const WORKFLOW_SECTION_MAP: SectionMap = [
    {
        id: "identity",
        label: "Identity",
        icon: GitBranch,
        keys: ["agent_name", "display_name", "namespace", "description", "version", "agent_type"],
    },
    {
        id: "io",
        label: "I/O & Mapping",
        icon: LayoutList,
        keys: ["input_schema", "output_schema", "output_mapping"],
    },
    {
        id: "timeouts",
        label: "Timeouts",
        icon: Timer,
        keys: ["workflow_timeout", "default_node_timeout"],
    },
    {
        id: "execution",
        label: "Execution",
        icon: Zap,
        keys: ["fail_fast", "max_call_depth"],
    },
    {
        id: "retry",
        label: "Retry",
        icon: RotateCcw,
        keys: ["retry_strategy"],
    },
    {
        id: "exit",
        label: "Exit Handlers",
        icon: LogOut,
        keys: ["on_exit"],
    },
    {
        id: "agent_card",
        label: "Agent Card",
        icon: CreditCard,
        keys: ["card_publish_interval_seconds", "agent_card"],
    },
    {
        id: "decision",
        label: "Decision Model",
        icon: Signpost,
        keys: ["decision"],
    },
    {
        id: "nodes",
        label: "Nodes",
        icon: LayoutList,
        keys: ["nodes"],
    },
];

const GATEWAY_SECTION_MAP: SectionMap = [
    {
        id: "identity",
        label: "Identity",
        icon: Globe,
        keys: ["namespace", "gateway_id"],
    },
    {
        id: "http",
        label: "HTTP Server",
        icon: Server,
        keys: ["fastapi_host", "fastapi_port", "cors_allowed_origins", "sse_max_queue_size"],
    },
    {
        id: "auth",
        label: "Auth",
        icon: Lock,
        keys: [
            "default_user_identity",
            "force_user_identity",
            "frontend_use_authorization",
            "external_auth_service_url",
            "external_auth_provider",
            "external_auth_callback_uri",
            "frontend_auth_login_url",
            "frontend_redirect_url",
            "frontend_server_url",
            "api_keys",
        ],
    },
    {
        id: "session",
        label: "Session",
        icon: Database,
        keys: ["session_service", "session_secret_key"],
    },
    {
        id: "artifacts",
        label: "Artifacts",
        icon: Archive,
        keys: ["artifact_service", "enable_embed_resolution", "gateway_artifact_content_limit_bytes"],
    },
    {
        id: "agent",
        label: "Agent Interaction",
        icon: Bot,
        keys: ["system_purpose", "response_format", "card_publish_interval_seconds"],
    },
    {
        id: "logging",
        label: "Task Logging",
        icon: BarChart3,
        keys: ["task_logging", "task_logger_queue_size"],
    },
    {
        id: "llm",
        label: "LLM",
        icon: Brain,
        keys: ["model"],
    },
    {
        id: "upload",
        label: "Limits",
        icon: Archive,
        keys: ["gateway_max_upload_size_bytes", "gateway_max_project_size_bytes", "gateway_max_batch_upload_size_bytes", "gateway_max_project_description_chars", "gateway_max_project_instructions_chars"],
    },
    {
        id: "frontend",
        label: "Frontend",
        icon: Monitor,
        keys: ["frontend_welcome_message", "frontend_app_name", "frontend_collect_feedback"],
    },
    {
        id: "platform",
        label: "Platform",
        icon: Blocks,
        keys: ["platform_service"],
    },
    {
        id: "event_mesh",
        label: "Event Mesh",
        icon: Radio,
        keys: ["event_mesh_broker_config", "acknowledgment_policy", "event_handlers", "output_handlers"],
    },
];

const SKILL_SECTION_MAP: SectionMap = [
    {
        id: "identity",
        label: "Identity",
        icon: BookOpen,
        keys: ["name", "description"],
    },
    {
        id: "discovery",
        label: "Discovery",
        icon: Tag,
        keys: ["tags", "examples"],
    },
    {
        id: "enterprise",
        label: "Enterprise",
        icon: ShieldCheck,
        keys: ["required_scopes", "required_builtins"],
    },
    {
        id: "content",
        label: "Content",
        icon: ScrollText,
        keys: ["instruction_content"],
    },
    {
        id: "references",
        label: "References",
        icon: Link,
        keys: ["references"],
    },
];

const CONNECTOR_SECTION_MAP: SectionMap = [
    {
        id: "identity",
        label: "Identity",
        icon: Plug,
        keys: ["name", "type", "subtype", "description"],
    },
    {
        id: "config",
        label: "Configuration",
        icon: Settings,
        keys: ["values"],
    },
];

const SECTION_MAPS: Record<string, SectionMap> = {
    agent: AGENT_SECTION_MAP,
    workflow: WORKFLOW_SECTION_MAP,
    gateway: GATEWAY_SECTION_MAP,
    skill: SKILL_SECTION_MAP,
    connector: CONNECTOR_SECTION_MAP,
};

export function getSectionMap(type: ConfigType): SectionMap {
    return SECTION_MAPS[type] ?? [];
}

export function claimedKeys(map: SectionMap): Set<string> {
    const set = new Set<string>();
    for (const entry of map) {
        for (const k of entry.keys) set.add(k);
    }
    return set;
}
