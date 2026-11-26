// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, onTestFinished, test, vi } from "vitest";

import { toolsetsApi } from "@/api/toolsets/versioned";

import { ConnectorType, type Toolset } from "@/types";

import {
    addressableComponentName,
    buildBlockedByCredsMessage,
    collectPendingHilInputMessages,
    componentTypeLabel,
    computeSuppressedActivityMessageId,
    detectComponentsWithMissingCredentials,
    detectConfigType,
    extractA2AAgentMetadata,
    extractAgentDiagramConfig,
    extractAgentMetadata,
    extractConfigName,
    extractConnectorMetadata,
    extractGatewayCreateRequest,
    extractGatewayMetadata,
    extractSkillBundleConfig,
    extractSkillConfig,
    extractWorkflowConfig,
    extractWorkflowMetadata,
    fetchBuiltinToolsetLookup,
    fetchPlatformDeploymentLookup,
    fetchPlatformSkillLookup,
    findLastAiMessageId,
    formatMissingCredentials,
    hasLiveBuildActivity,
    humanizeComponentName,
    isAgentFixableError,
    isBuildCompleteCtaShowing,
    isBuilderConfigMime,
    isBuildReadyToTest,
    isComponentLiveOnMesh,
    isConnectorOnlyBuildComplete,
    isEntrypointOnlyBuildComplete,
    isFirstNonUserMessageWithId,
    isInlineProgressActive,
    isLastNonUserMessage,
    isSaveOnlyBuild,
    isSecretKey,
    isSessionComponentLive,
    isSkillOnlyBuildComplete,
    legacyWorkflowName,
    operationNoun,
    resolveEffectiveManifest,
    resolvePlanAnchorId,
    resolveTestTargetName,
    resolveToolsetBindings,
    SAM_A2A_AGENT_CONFIG_MIME_TYPE,
    SAM_AGENT_CONFIG_MIME_TYPE,
    SAM_CONNECTOR_CONFIG_MIME_TYPE,
    SAM_REQUIRED_PLACEHOLDER,
    SAM_SKILL_CONFIG_MIME_TYPE,
    shouldAutoSaveOnly,
    shouldShowBuildCompleteCTA,
    shouldShowBuildCompleteCTAAddon,
    shouldShowCreationProgress,
    shouldShowCreationProgressCard,
    shouldShowEditCompleteCard,
    shouldShowEditingProgressCard,
    shouldShowPlanCard,
    skillToolNames,
    unrecognizedConfigMessage,
    validateAgentMetadata,
    validatedButLiveComponents,
} from "./builderUtils";

const AGENT_YAML = `
apps:
  - name: DocSummarizer
    app_config:
      agent_name: DocSummarizer
      description: Summarises documents into concise summaries.
      instruction: |
        You are a document summariser. Be concise. Be accurate.
      tools:
        - tool_type: builtin-group
          group_name: artifact_management
        - tool_type: builtin
          tool_name: web_request
      input_modes: [text, file]
      output_modes: [text]
      agent_card:
        description: Agent-card description for display purposes.
        defaultInputModes: [text]
        defaultOutputModes: [text, audio]
        skills:
          - id: summarise
            name: Summarise
            description: Produce a summary
          - name: Compare
            description: Compare multiple documents
`;

const AGENT_YAML_CARD_ONLY_DESC = `
apps:
  - name: MinimalAgent
    app_config:
      agent_name: MinimalAgent
      instruction: minimal
      agent_card:
        description: Only on agent_card
`;

const AGENT_YAML_NO_CARD = `
apps:
  - name: NoCardAgent
    app_config:
      agent_name: NoCardAgent
      description: Top-level description only.
      input_modes: [text]
      output_modes: [text]
`;

const WORKFLOW_YAML = `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      description: Pipeline that transforms documents.
      nodes:
        - id: start
          type: agent
          agent_name: DocSummarizer
      agent_card:
        display_name: My Workflow Display
        description: Workflow agent_card description.
`;

const WORKFLOW_YAML_NODES_ONLY = `
apps:
  - name: NodesOnly
    app_config:
      nodes:
        - id: a
`;

const WORKFLOW_YAML_NESTED = `
apps:
  - name: NestedWorkflow
    app_config:
      name: PlatformNestedWorkflow
      tools:
        - tool_type: builtin
          tool_name: web_request
      workflow:
        description: Nested workflow body description.
        nodes:
          - id: start
            type: agent
            agent_name: DocSummarizer
        agent_card:
          display_name: Nested Workflow Display
          description: Nested workflow agent_card description.
`;

const WORKFLOW_YAML_NESTED_NO_PLATFORM_NAME = `
apps:
  - name: NestedWorkflow
    app_config:
      workflow:
        description: Nested workflow body description.
        nodes:
          - id: start
            type: agent
            agent_name: DocSummarizer
`;

const WORKFLOW_YAML_FLAT_AGENT_NAME = `
apps:
  - name: mule_detection_workflow
    app_config:
      agent_name: MuleDetectionWorkflow
      agent_type: workflow
      nodes:
        - id: score_transaction
          type: agent
          agent_name: MuleRiskScorer
      output_mapping:
        risk: "{{score_transaction.output}}"
`;

const WORKFLOW_YAML_STREAMING_NULL_NODE = `
apps:
  - name: StreamingWorkflow
    app_config:
      tools: []
      workflow:
        nodes:
          -
`;

const GATEWAY_YAML = `
apps:
  - name: MyGateway
    app_config:
      event_handlers:
        - name: h1
          target_agent_name: DocSummarizer
          subscriptions:
            - topic: foo/bar
      output_handlers:
        - name: out1
          topic_expression: baz
      event_mesh_broker_config:
        url: tcp://broker
`;

const GATEWAY_YAML_MINIMAL = `
apps:
  - name: MinimalGateway
    app_config:
      event_handlers:
        - name: h1
`;

const SKILL_YAML = `
name: my-skill
instruction_content: |
  # Do stuff
  Follow these rules.
`;

const CONNECTOR_JSON = JSON.stringify({
    name: "JiraConnector",
    type: "api",
    subtype: "openapi",
    description: "Talks to Jira",
    icon: "jira",
    values: { base_url: "https://example.atlassian.net", auth_type: "none" },
});

const A2A_AGENT_JSON = JSON.stringify({
    name: "WeatherProxy",
    description: "Proxies an external weather agent.",
    agentUrl: "http://ec2-1-2-3-4.compute.amazonaws.com:9000",
    agentCardLocation: "well_known",
    agentAuthConfig: { type: "bearer", token: "<<__SAM_REQUIRED__>>" },
});

describe("isSecretKey", () => {
    test.each(["slack_bot_token", "password", "api_key", "apiKey", "client_secret", "PRIVATE_KEY", "auth_token", "aws_access_key_id", "external_id", "file_s3_key"])("masks secret-ish key %s", k => {
        expect(isSecretKey(k)).toBe(true);
    });
    test.each(["hostname", "port", "database", "base_url", "region", "name"])("does not mask non-secret key %s", k => {
        expect(isSecretKey(k)).toBe(false);
    });
});

describe("isAgentFixableError", () => {
    test("flat status: 4xx (not auth) is agent-fixable", () => {
        expect(isAgentFixableError({ status: 400, message: "bad request" })).toBe(true);
        expect(isAgentFixableError({ status: 422, message: "validation failed" })).toBe(true);
    });
    test("unfilled deploy credentials are for the user, not the builder agent", () => {
        const message = "entrypoint cannot be deployed until all required fields are provided: broker_password: broker_password is required";
        expect(isAgentFixableError({ status: 422, message })).toBe(false);
        const v2Message = "The entrypoint cannot be deployed until all required fields are provided: broker_password: broker_password is required";
        expect(isAgentFixableError({ status: 422, message: v2Message })).toBe(false);
    });
    test("an agent, remote agent or workflow deploy refused over an unfilled credential is for the user, not the builder agent", () => {
        for (const subject of ["agent", "remote agent", "workflow"]) {
            const message = `${subject} cannot be deployed until all required fields are provided: connectors.Pet Shop API.api_key: connector "Pet Shop API" has no value for api_key; set it on the Connectors page`;
            expect(isAgentFixableError({ status: 422, message })).toBe(false);
        }
    });
    test("flat status: 5xx / auth are NOT agent-fixable (manual retry, not a rebuild)", () => {
        expect(isAgentFixableError({ status: 500 })).toBe(false);
        expect(isAgentFixableError({ status: 503 })).toBe(false);
        expect(isAgentFixableError({ status: 401 })).toBe(false);
        expect(isAgentFixableError({ status: 403 })).toBe(false);
    });
    test("a missing route is infrastructure, not a config the agent can fix", () => {
        expect(isAgentFixableError({ status: 404, message: "404 page not found" })).toBe(false);
        expect(isAgentFixableError({ status: 405, message: "method not allowed" })).toBe(false);
        expect(isAgentFixableError({ response: { status: 404 } })).toBe(false);
    });

    test("axios-style response.status is still honored", () => {
        expect(isAgentFixableError({ response: { status: 422 } })).toBe(true);
        expect(isAgentFixableError({ response: { status: 500 } })).toBe(false);
    });
    test("network errors are NOT agent-fixable", () => {
        expect(isAgentFixableError({ code: "ERR_NETWORK" })).toBe(false);
        expect(isAgentFixableError({ code: "ETIMEDOUT" })).toBe(false);
    });
    test("a client-side throw (no status, no network code) is agent-fixable (bad YAML/validation)", () => {
        expect(isAgentFixableError(new Error("validateAgentMetadata: missing name"))).toBe(true);
    });
    test("non-objects are not agent-fixable", () => {
        expect(isAgentFixableError(null)).toBe(false);
        expect(isAgentFixableError("boom")).toBe(false);
    });
});

describe("isBuilderConfigMime", () => {
    test("recognises builder MIME types", () => {
        expect(isBuilderConfigMime(SAM_AGENT_CONFIG_MIME_TYPE)).toBe(true);
        expect(isBuilderConfigMime(SAM_CONNECTOR_CONFIG_MIME_TYPE)).toBe(true);
        expect(isBuilderConfigMime(SAM_SKILL_CONFIG_MIME_TYPE)).toBe(true);
        expect(isBuilderConfigMime(SAM_A2A_AGENT_CONFIG_MIME_TYPE)).toBe(true);
    });

    test("rejects unrelated MIME types", () => {
        expect(isBuilderConfigMime("text/plain")).toBe(false);
        expect(isBuilderConfigMime("application/json")).toBe(false);
        expect(isBuilderConfigMime("")).toBe(false);
    });
});

describe("componentTypeLabel", () => {
    test("shows the user-facing word for an entrypoint, never the internal 'gateway'", () => {
        expect(componentTypeLabel("gateway")).toBe("Entrypoint");
    });

    test("titles a remote agent and capitalizes every other type", () => {
        expect(componentTypeLabel("a2a_agent")).toBe("Remote Agent");
        expect(componentTypeLabel("workflow")).toBe("Workflow");
    });

    test.each(["constructor", "toString", "__proto__", "hasOwnProperty"])("does not label an inherited object key (%s) with a function or prototype", key => {
        expect(componentTypeLabel(key)).toBe(key.charAt(0).toUpperCase() + key.slice(1));
    });
});

describe("operationNoun", () => {
    test("names an entrypoint, a remote agent and the rest in lower case for an auto-fix chip", () => {
        expect(operationNoun("gateway")).toBe("entrypoint");
        expect(operationNoun("a2a_agent")).toBe("remote agent");
        expect(operationNoun("agent")).toBe("agent");
    });

    test("falls back to 'component' when the type is unknown", () => {
        expect(operationNoun(undefined)).toBe("component");
        expect(operationNoun("")).toBe("component");
    });
});

describe("unrecognizedConfigMessage", () => {
    test("ignores an inherited object key given as the manifest type", () => {
        expect(unrecognizedConfigMessage("x", "constructor")).toBe('The config for "x" is not a recognized component config, so it could not be saved.');
    });

    test("says 'entrypoint' for a gateway-typed component and names the expected shape", () => {
        const message = unrecognizedConfigMessage("order_events_entrypoint", "gateway");
        expect(message).toContain("The entrypoint config must be a YAML file");
        expect(message).not.toMatch(/gateway config/i);
    });
});

describe("humanizeComponentName", () => {
    test("title-cases a hyphenated slug", () => {
        expect(humanizeComponentName("cowboy-tone")).toBe("Cowboy Tone");
    });

    test("splits on underscores and whitespace", () => {
        expect(humanizeComponentName("data_processing_workflow")).toBe("Data Processing Workflow");
        expect(humanizeComponentName("daily report")).toBe("Daily Report");
        expect(humanizeComponentName("my-postgres_db")).toBe("My Postgres Db");
    });

    test("preserves casing a word already carries beyond its first letter", () => {
        expect(humanizeComponentName("MCP proxy")).toBe("MCP Proxy");
        expect(humanizeComponentName("SummarizesText")).toBe("SummarizesText");
    });

    test("collapses repeated, leading, and trailing separators", () => {
        expect(humanizeComponentName("cowboy--tone_")).toBe("Cowboy Tone");
        expect(humanizeComponentName("-cowboy-tone")).toBe("Cowboy Tone");
    });

    test("keeps digit-led words and does not case the digit", () => {
        expect(humanizeComponentName("s3-connector")).toBe("S3 Connector");
        expect(humanizeComponentName("agent-v2")).toBe("Agent V2");
    });

    test("does not split on non-separator punctuation", () => {
        expect(humanizeComponentName("my.agent")).toBe("My.agent");
    });

    test("title-cases a single-character name", () => {
        expect(humanizeComponentName("a")).toBe("A");
    });

    test("returns the original string when it has no word characters", () => {
        expect(humanizeComponentName("")).toBe("");
        expect(humanizeComponentName("---")).toBe("---");
    });
});

describe("detectConfigType", () => {
    test("detects agent configs", () => {
        expect(detectConfigType(AGENT_YAML)).toBe("agent");
    });

    test("detects workflow configs by agent_type", () => {
        expect(detectConfigType(WORKFLOW_YAML)).toBe("workflow");
    });

    test("detects workflow configs by nodes array", () => {
        expect(detectConfigType(WORKFLOW_YAML_NODES_ONLY)).toBe("workflow");
    });

    test("detects workflow configs nested under app_config.workflow (config-apply-authored shape)", () => {
        expect(detectConfigType(WORKFLOW_YAML_NESTED)).toBe("workflow");
    });

    test("detects gateway configs by event_handlers", () => {
        expect(detectConfigType(GATEWAY_YAML)).toBe("gateway");
    });

    test("detects connector JSON", () => {
        expect(detectConfigType(CONNECTOR_JSON)).toBe("connector");
    });

    test.each(Object.values(ConnectorType))("recognizes a '%s' connector config (manual-page ↔ builder parity)", type => {
        const json = JSON.stringify({ name: `${type}-conn`, type, subtype: "", description: "", values: { k: "v" } });
        expect(detectConfigType(json)).toBe("connector");
        expect(extractConnectorMetadata(json)?.type).toBe(type);
    });

    test("does NOT misclassify a JSON whose type is not a connector type (guards against loosening the membership check)", () => {
        const json = JSON.stringify({ name: "x", type: "definitely_not_a_connector", values: { k: "v" } });
        expect(detectConfigType(json)).toBe("unknown");
        expect(extractConnectorMetadata(json)).toBeNull();
    });

    test("detects skill YAML", () => {
        expect(detectConfigType(SKILL_YAML)).toBe("skill");
    });

    test("detects remote A2A agent JSON", () => {
        expect(detectConfigType(A2A_AGENT_JSON)).toBe("a2a_agent");
    });

    test("returns 'unknown' for an empty apps config", () => {
        expect(detectConfigType("apps: []")).toBe("unknown");
    });

    test("returns 'unknown' for garbage input", () => {
        expect(detectConfigType(":: not yaml ::")).toBe("unknown");
    });

    test("returns 'unknown' when app_config lacks identifying fields", () => {
        expect(detectConfigType("apps:\n  - name: X\n    app_config: {}\n")).toBe("unknown");
    });
});

describe("extractWorkflowConfig", () => {
    test("returns the config when nodes are present", () => {
        const cfg = extractWorkflowConfig(WORKFLOW_YAML);
        expect(cfg).not.toBeNull();
        expect(Array.isArray(cfg?.nodes)).toBe(true);
    });

    test("returns null when there are no nodes", () => {
        expect(extractWorkflowConfig(AGENT_YAML)).toBeNull();
    });

    test("returns null for invalid YAML", () => {
        expect(extractWorkflowConfig("::bogus::")).toBeNull();
    });

    test("unwraps a nested app_config.workflow body (config-apply-authored shape)", () => {
        const cfg = extractWorkflowConfig(WORKFLOW_YAML_NESTED);
        expect(cfg).not.toBeNull();
        expect(Array.isArray(cfg?.nodes)).toBe(true);
        expect(cfg?.description).toBe("Nested workflow body description.");
    });

    test("returns null when a streaming node entry is still a null placeholder", () => {
        expect(extractWorkflowConfig(WORKFLOW_YAML_STREAMING_NULL_NODE)).toBeNull();
    });

    test("returns null when a node entry is an array rather than a mapping", () => {
        expect(
            extractWorkflowConfig(`
apps:
  - app_config:
      workflow:
        nodes:
          - [not, a, mapping]
`)
        ).toBeNull();
    });
});

describe("extractWorkflowMetadata", () => {
    test("prefers agent_card.display_name and agent_card.description", () => {
        const meta = extractWorkflowMetadata(WORKFLOW_YAML);
        expect(meta?.name).toBe("MyWorkflow");
        expect(meta?.displayName).toBe("My Workflow Display");
        expect(meta?.description).toBe("Workflow agent_card description.");
    });

    test("falls back to app_config.description when no agent_card", () => {
        const meta = extractWorkflowMetadata(WORKFLOW_YAML_NODES_ONLY);
        expect(meta?.description).toBe("");
        expect(meta?.displayName).toBe("NodesOnly");
    });

    test("returns null for non-apps YAML", () => {
        expect(extractWorkflowMetadata("not: apps")).toBeNull();
    });

    test("reads displayName/description from the nested app_config.workflow body, but name from the outer app_config (config-apply-authored shape)", () => {
        const meta = extractWorkflowMetadata(WORKFLOW_YAML_NESTED);
        expect(meta?.name).toBe("PlatformNestedWorkflow");
        expect(meta?.displayName).toBe("Nested Workflow Display");
        expect(meta?.description).toBe("Nested workflow agent_card description.");
    });

    test("falls back to the app-level YAML name when a nested body has no app_config.name", () => {
        expect(extractWorkflowMetadata(WORKFLOW_YAML_NESTED_NO_PLATFORM_NAME)?.name).toBe("NestedWorkflow");
    });

    test("names a flat Builder workflow by its agent_name, the spelling entrypoint rules target", () => {
        expect(extractWorkflowMetadata(WORKFLOW_YAML_FLAT_AGENT_NAME)?.name).toBe("MuleDetectionWorkflow");
        expect(addressableComponentName("workflow", WORKFLOW_YAML_FLAT_AGENT_NAME, "fallback")).toBe("MuleDetectionWorkflow");
    });

    test("an explicit app_config.name outranks agent_name, so the naming rule is unchanged for configs that state it", () => {
        const yamlText = WORKFLOW_YAML_FLAT_AGENT_NAME.replace("agent_name: MuleDetectionWorkflow", "name: StatedWorkflow\n      agent_name: MuleDetectionWorkflow");
        expect(extractWorkflowMetadata(yamlText)?.name).toBe("StatedWorkflow");
        expect(legacyWorkflowName(yamlText)).toBeUndefined();
    });

    test("a non-string agent_name is ignored rather than stored as the name", () => {
        const yamlText = WORKFLOW_YAML_FLAT_AGENT_NAME.replace("agent_name: MuleDetectionWorkflow", "agent_name: 123");
        expect(extractWorkflowMetadata(yamlText)?.name).toBe("mule_detection_workflow");
    });
});

describe("legacyWorkflowName", () => {
    test("returns the app-level name a pre-agent_name save stored the workflow under", () => {
        expect(legacyWorkflowName(WORKFLOW_YAML_FLAT_AGENT_NAME)).toBe("mule_detection_workflow");
    });

    test("is undefined when there is no config", () => {
        expect(legacyWorkflowName(null)).toBeUndefined();
        expect(legacyWorkflowName(undefined)).toBeUndefined();
    });
});

describe("resolveTestTargetName", () => {
    test("uses the agent_name when the platform stores the workflow under it", () => {
        expect(resolveTestTargetName("workflow", WORKFLOW_YAML_FLAT_AGENT_NAME, "Mule", new Set(["MuleDetectionWorkflow", "mule_detection_workflow"]))).toBe("MuleDetectionWorkflow");
    });

    test("falls back to the legacy stored name for a workflow not re-saved since agent_name joined the precedence", () => {
        expect(resolveTestTargetName("workflow", WORKFLOW_YAML_FLAT_AGENT_NAME, "Mule", new Set(["mule_detection_workflow"]))).toBe("mule_detection_workflow");
    });

    test("keeps the agent_name when neither name is stored or the platform list is unavailable", () => {
        expect(resolveTestTargetName("workflow", WORKFLOW_YAML_FLAT_AGENT_NAME, "Mule", new Set(["SomethingElse"]))).toBe("MuleDetectionWorkflow");
        expect(resolveTestTargetName("workflow", WORKFLOW_YAML_FLAT_AGENT_NAME, "Mule", undefined)).toBe("MuleDetectionWorkflow");
    });

    test("leaves non-workflow targets on their addressable name", () => {
        const agentYaml = WORKFLOW_YAML_FLAT_AGENT_NAME.replace("agent_type: workflow", `instruction: ${"x".repeat(120)}`);
        expect(resolveTestTargetName("agent", agentYaml, "Mule", new Set(["mule_detection_workflow"]))).toBe("MuleDetectionWorkflow");
    });
});

describe("extractAgentMetadata", () => {
    test("extracts full metadata from a complete agent YAML", () => {
        const meta = extractAgentMetadata(AGENT_YAML);
        expect(meta?.name).toBe("DocSummarizer");
        expect(meta?.description).toBe("Agent-card description for display purposes.");
        expect(meta?.systemPrompt).toContain("document summariser");
        expect(meta?.builtinGroupNames).toEqual(["artifact_management"]);
        expect(meta?.builtinToolNames).toEqual(["web_request"]);
        expect(meta?.inputModes).toEqual(["text"]);
        expect(meta?.outputModes).toEqual(["text", "audio"]);
        expect(meta?.skills).toHaveLength(2);
        expect(meta?.skills).toEqual([
            { id: "summarise", name: "Summarise", description: "Produce a summary" },
            { name: "Compare", description: "Compare multiple documents" },
        ]);
    });

    test("keeps every A2A field of an agent_card skill, as the server-apply save does", () => {
        const yamlText = `
apps:
  - name: HaikuBot
    app_config:
      agent_name: HaikuBot
      instruction: Write haiku.
      agent_card:
        description: Writes haiku.
        skills:
          - id: write-haiku
            name: Write Haiku
            description: Composes an original haiku
            tags: [poetry, haiku]
            examples: ["Write a haiku about autumn rain"]
            inputModes: [text]
            outputModes: [text, audio]
          - id: id-only-skill
          - description: no name or id
          - name: Loose Lists
            description: drops entries that are not strings
            tags: [ok, 3, null]
            examples: []
          - id: ""
            name: Empty Id
            input_modes: [text]
          - id: numeric-name
            name: 42
`;
        const meta = extractAgentMetadata(yamlText);
        expect(meta?.skills).toEqual([
            {
                id: "write-haiku",
                name: "Write Haiku",
                description: "Composes an original haiku",
                tags: ["poetry", "haiku"],
                examples: ["Write a haiku about autumn rain"],
                inputModes: ["text"],
                outputModes: ["text", "audio"],
            },
            { id: "id-only-skill", name: "id-only-skill", description: "" },
            { name: "Loose Lists", description: "drops entries that are not strings", tags: ["ok"] },
            { name: "Empty Id", description: "" },
            { id: "numeric-name", name: "numeric-name", description: "" },
        ]);
    });

    test("ignores an agent_card skills value that is not a list", () => {
        const yamlText = `
apps:
  - name: OddCard
    app_config:
      agent_name: OddCard
      instruction: odd
      agent_card:
        description: Card with a mapping for skills.
        skills:
          write-haiku: Composes haiku
`;
        expect(extractAgentMetadata(yamlText)?.skills).toBeUndefined();
    });

    test("agent_card.skills (A2A abilities) do NOT become loadable-skill bindings", () => {
        const meta = extractAgentMetadata(AGENT_YAML);
        expect(meta?.loadableSkillNames).toBeUndefined();
    });

    test("extracts the config-level skills block as loadableSkillNames, separate from card skills", () => {
        const yamlText = `
apps:
  - name: AdvisorAgent
    app_config:
      agent_name: AdvisorAgent
      description: Answers questions grounded in a loadable skill.
      instruction: Load the skill and answer from it.
      skills:
        - name: live-context-mesh-response
        - sam-templatize
        - path: /mnt/skills/local-only
      agent_card:
        description: Card description.
        skills:
          - id: advisor
            name: Context Mesh Advisor
            description: Card ability, display only
`;
        const meta = extractAgentMetadata(yamlText);
        expect(meta?.loadableSkillNames).toEqual(["live-context-mesh-response", "sam-templatize"]);
        expect(meta?.skills).toEqual([{ id: "advisor", name: "Context Mesh Advisor", description: "Card ability, display only" }]);
    });

    test("falls back to agent_card.description when no top-level description", () => {
        const meta = extractAgentMetadata(AGENT_YAML_CARD_ONLY_DESC);
        expect(meta?.description).toBe("Only on agent_card");
    });

    test("falls back to app_config.description when no agent_card", () => {
        const meta = extractAgentMetadata(AGENT_YAML_NO_CARD);
        expect(meta?.description).toBe("Top-level description only.");
        expect(meta?.inputModes).toEqual(["text"]);
        expect(meta?.outputModes).toEqual(["text"]);
        expect(meta?.skills).toBeUndefined();
    });

    test("returns null when there are no apps", () => {
        expect(extractAgentMetadata("apps: []")).toBeNull();
        expect(extractAgentMetadata("not yaml either: [")).toBeNull();
    });
});

describe("validateAgentMetadata", () => {
    const validAgent = {
        name: "MyAgent",
        description: "A reasonable description of what the agent does.",
        systemPrompt: "x".repeat(150),
        skills: [{ name: "s", description: "d" }],
    };

    test("accepts a valid agent", () => {
        expect(validateAgentMetadata(validAgent)).toEqual([]);
    });

    test("rejects names that are too short or too long", () => {
        expect(validateAgentMetadata({ ...validAgent, name: "ab" })).toHaveLength(1);
        expect(validateAgentMetadata({ ...validAgent, name: "a".repeat(41) })).toHaveLength(1);
    });

    test("rejects descriptions that are too short or too long", () => {
        expect(validateAgentMetadata({ ...validAgent, description: "short" })).toHaveLength(1);
        expect(validateAgentMetadata({ ...validAgent, description: "a".repeat(1001) })).toHaveLength(1);
    });

    test("accepts a terse system prompt", () => {
        expect(validateAgentMetadata({ ...validAgent, systemPrompt: "You are a helpful SQL assistant." })).toHaveLength(0);
    });

    test("rejects a system prompt over the maximum", () => {
        expect(validateAgentMetadata({ ...validAgent, systemPrompt: "a".repeat(32001) })).toHaveLength(1);
        expect(validateAgentMetadata({ ...validAgent, systemPrompt: "a".repeat(32000) })).toHaveLength(0);
    });

    test("counts emoji in a system prompt as one character each", () => {
        expect(validateAgentMetadata({ ...validAgent, systemPrompt: "😀".repeat(20000) })).toHaveLength(0);
    });

    test("rejects an empty system prompt (now required for standard agents)", () => {
        expect(validateAgentMetadata({ ...validAgent, systemPrompt: "" })).toHaveLength(1);
    });

    test("rejects more than 20 skills", () => {
        const tooMany = Array.from({ length: 21 }, (_, i) => ({ name: `s${i}`, description: "d" }));
        expect(validateAgentMetadata({ ...validAgent, skills: tooMany })).toHaveLength(1);
    });

    test("reports all violations together", () => {
        const errors = validateAgentMetadata({ name: "ab", description: "short" });
        expect(errors).toHaveLength(3);
    });
});

describe("extractAgentDiagramConfig", () => {
    test("extracts diagram fields from an agent YAML", () => {
        const cfg = extractAgentDiagramConfig(AGENT_YAML);
        expect(cfg?.name).toBe("DocSummarizer");
        expect(cfg?.instruction).toContain("document summariser");
        expect(cfg?.tools).toEqual([
            { tool_type: "builtin-group", group_name: "artifact_management", tool_name: undefined, tool_description: undefined },
            { tool_type: "builtin", tool_name: "web_request", group_name: undefined, tool_description: undefined },
        ]);
    });

    test.each([
        ["a number", "123"],
        ["a list", "[web_request]"],
        ["a map", "{ name: web_request }"],
    ])("treats a tool_name that is %s as absent", (_label, toolName) => {
        const yamlText = `
apps:
  - name: Malformed
    app_config:
      agent_name: Malformed
      tools:
        - tool_type: builtin
          tool_name: ${toolName}
`;
        expect(extractAgentDiagramConfig(yamlText)?.tools).toEqual([{ tool_type: "builtin", tool_name: undefined, group_name: undefined, tool_description: undefined }]);
    });

    test("skips a null or scalar entry in tools and skills", () => {
        const yamlText = `
apps:
  - name: Streaming
    app_config:
      tools:
        -
        - web_request
        - tool_type: builtin
          tool_name: web_request
      skills:
        -
        - name: Summarise
`;
        const cfg = extractAgentDiagramConfig(yamlText);
        expect(cfg?.tools).toEqual([{ tool_type: "builtin", tool_name: "web_request", group_name: undefined, tool_description: undefined }]);
        expect(cfg?.skills).toEqual([{ name: "Summarise", description: "" }]);
    });

    test.each([
        ["tools", "tools: web_request"],
        ["skills", "skills: 7"],
        ["toolsets", "toolsets: artifact_management"],
        ["inputModes", "input_modes: text"],
        ["outputModes", "output_modes: text"],
    ] as const)("treats a non-list %s as absent", (key, line) => {
        const yamlText = `
apps:
  - name: Malformed
    app_config:
      ${line}
`;
        const cfg = extractAgentDiagramConfig(yamlText);
        expect(cfg).not.toBeNull();
        expect(cfg?.[key]).toBeUndefined();
    });

    test("drops a non-string group_name, tool_type and toolsets entry instead of passing it to the preview", () => {
        const yamlText = `
apps:
  - name: Malformed
    app_config:
      toolsets: [artifact_management, 1, { a: 1 }]
      tools:
        - tool_type: builtin-group
          group_name: { a: 1 }
        - tool_type: [builtin]
          tool_name: web_request
`;
        const cfg = extractAgentDiagramConfig(yamlText);
        expect(cfg?.toolsets).toEqual(["artifact_management"]);
        expect(cfg?.tools).toEqual([
            { tool_type: "builtin-group", tool_name: undefined, group_name: undefined, tool_description: undefined },
            { tool_type: "", tool_name: "web_request", group_name: undefined, tool_description: undefined },
        ]);
    });

    test.each([
        ["a numeric agent_name", "agent_name: 123", { name: "Fallback", description: "Valid description.", instruction: "" }],
        ["a list agent_name", "agent_name: [x]", { name: "Fallback", description: "Valid description.", instruction: "" }],
        ["a numeric instruction", "instruction: 5", { name: "Fallback", description: "Valid description.", instruction: "" }],
        ["a map description", "description: { a: 1 }", { name: "Fallback", description: "", instruction: "" }],
        ["a map agent_card description", "agent_card: { description: { a: 1 } }", { name: "Fallback", description: "Valid description.", instruction: "" }],
        ["a scalar agent_card", "agent_card: text", { name: "Fallback", description: "Valid description.", instruction: "" }],
    ])("falls back instead of passing through %s", (_label, line, expected) => {
        const descriptionLine = line.startsWith("description") ? "" : "description: Valid description.";
        const yamlText = `
apps:
  - name: Fallback
    app_config:
      ${descriptionLine}
      ${line}
`;
        const cfg = extractAgentDiagramConfig(yamlText);
        expect(cfg?.name).toBe(expected.name);
        expect(cfg?.description).toBe(expected.description);
        expect(cfg?.instruction).toBe(expected.instruction);
    });

    test.each([
        ["a number", "1234"],
        ["a boolean", "true"],
        ["a list", "[a]"],
        ["a map", "{ a: 1 }"],
    ])("never returns a non-string name when the app name is %s", (_label, appName) => {
        const yamlText = `
apps:
  - name: ${appName}
    app_config:
      instruction: hi
`;
        expect(extractAgentDiagramConfig(yamlText)?.name).toBe("");
    });

    test("keeps only the string entries of the mode lists and ignores a non-list mode", () => {
        const yamlText = `
apps:
  - name: Modes
    app_config:
      input_modes: [text, 1, { a: 1 }, ""]
      output_modes: text
`;
        const cfg = extractAgentDiagramConfig(yamlText);
        expect(cfg?.inputModes).toEqual(["text"]);
        expect(cfg?.outputModes).toBeUndefined();
    });

    test("skips a nested list entry and non-string skill and tool descriptions", () => {
        const yamlText = `
apps:
  - name: Odd
    app_config:
      tools:
        - [web_request]
        - tool_type: builtin
          tool_name: web_request
          tool_description: { a: 1 }
          description: 5
      skills:
        - name: 7
          description: [x]
`;
        const cfg = extractAgentDiagramConfig(yamlText);
        expect(cfg?.tools).toEqual([{ tool_type: "builtin", tool_name: "web_request", group_name: undefined, tool_description: undefined }]);
        expect(cfg?.skills).toEqual([{ name: "", description: "" }]);
    });

    test("keeps every field of a valid agent YAML", () => {
        const yamlText = `
apps:
  - name: Full
    app_config:
      agent_name: FullAgent
      description: Top-level description.
      instruction: Be helpful.
      toolsets: [artifact_management]
      input_modes: [text, file]
      output_modes: [text]
      skills:
        - name: Summarise
          description: Produce a summary
      tools:
        - tool_type: python
          tool_name: lookup
          tool_description: Looks things up
        - tool_type: mcp
          description: Fallback description
`;
        expect(extractAgentDiagramConfig(yamlText)).toEqual({
            name: "FullAgent",
            description: "Top-level description.",
            instruction: "Be helpful.",
            skills: [{ name: "Summarise", description: "Produce a summary" }],
            tools: [
                { tool_type: "python", tool_name: "lookup", group_name: undefined, tool_description: "Looks things up" },
                { tool_type: "mcp", tool_name: undefined, group_name: undefined, tool_description: "Fallback description" },
            ],
            toolsets: ["artifact_management"],
            inputModes: ["text", "file"],
            outputModes: ["text"],
        });
    });

    test("returns null for non-app YAML", () => {
        expect(extractAgentDiagramConfig("apps: []")).toBeNull();
    });
});

describe("extractConfigName", () => {
    test("returns the app name for an agent YAML", () => {
        expect(extractConfigName(AGENT_YAML)).toBe("DocSummarizer");
    });

    test("returns the connector name for a connector JSON", () => {
        expect(extractConfigName(CONNECTOR_JSON)).toBe("JiraConnector");
    });

    test("falls back to 'Untitled' for unparseable input", () => {
        expect(extractConfigName("::bogus::")).toBe("Untitled");
    });
});

describe("extractConnectorMetadata", () => {
    test("extracts metadata from a valid connector JSON", () => {
        const meta = extractConnectorMetadata(CONNECTOR_JSON);
        expect(meta).toEqual({
            name: "JiraConnector",
            type: "api",
            subtype: "openapi",
            description: "Talks to Jira",
            icon: "jira",
            values: { base_url: "https://example.atlassian.net", auth_type: "none" },
        });
    });

    test("recognizes an event_mesh connector (Quick Build allowlist)", () => {
        const meta = extractConnectorMetadata(JSON.stringify({ name: "OrdersEventMesh", type: "event_mesh", subtype: "solace", values: { tool_name: "get_order" } }));
        expect(meta?.type).toBe("event_mesh");
        expect(meta?.subtype).toBe("solace");
    });

    test("returns null for a non-connector JSON type", () => {
        expect(extractConnectorMetadata(JSON.stringify({ type: "other", values: {} }))).toBeNull();
    });

    test("returns null for malformed JSON", () => {
        expect(extractConnectorMetadata("not json")).toBeNull();
    });

    test("handles missing optional fields gracefully", () => {
        const minimal = JSON.stringify({ type: "sql", values: {} });
        const meta = extractConnectorMetadata(minimal);
        expect(meta?.name).toBe("");
        expect(meta?.subtype).toBe("");
        expect(meta?.icon).toBeUndefined();
    });
});

describe("extractA2AAgentMetadata", () => {
    test("parses a valid remote A2A agent config", () => {
        const meta = extractA2AAgentMetadata(A2A_AGENT_JSON);
        expect(meta?.name).toBe("WeatherProxy");
        expect(meta?.agentUrl).toBe("http://ec2-1-2-3-4.compute.amazonaws.com:9000");
        expect(meta?.agentCardLocation).toBe("well_known");
        expect(meta?.agentAuthConfig).toEqual({ type: "bearer", token: "<<__SAM_REQUIRED__>>" });
    });

    test("defaults agentAuthConfig to none when omitted", () => {
        const meta = extractA2AAgentMetadata(JSON.stringify({ name: "NoAuthProxy", agentUrl: "https://x.example.com", agentCardLocation: "well_known" }));
        expect(meta?.agentAuthConfig).toEqual({ type: "none" });
    });

    test("returns null when required fields are missing", () => {
        expect(extractA2AAgentMetadata(JSON.stringify({ agentCardLocation: "well_known" }))).toBeNull();
    });

    test("returns null for malformed JSON", () => {
        expect(extractA2AAgentMetadata("not json")).toBeNull();
    });
});

describe("extractGatewayMetadata", () => {
    test("extracts event_handlers, output_handlers, and broker flag", () => {
        const meta = extractGatewayMetadata(GATEWAY_YAML);
        expect(meta?.name).toBe("MyGateway");
        expect(meta?.eventHandlers).toHaveLength(1);
        expect(meta?.outputHandlers).toHaveLength(1);
        expect(meta?.hasCustomBroker).toBe(true);
    });

    test("defaults output_handlers to empty and hasCustomBroker to false", () => {
        const meta = extractGatewayMetadata(GATEWAY_YAML_MINIMAL);
        expect(meta?.outputHandlers).toEqual([]);
        expect(meta?.hasCustomBroker).toBe(false);
    });

    test("returns null when event_handlers is missing", () => {
        expect(extractGatewayMetadata(AGENT_YAML)).toBeNull();
    });
});

const ORDER_VALIDATION_GATEWAY_YAML = `
apps:
  - name: OrderValidationGateway
    app_config:
      agent_card:
        description: Validates incoming order events and routes them to the OrderValidator agent.
      event_handlers:
        - name: order_events
          target_agent_name: OrderValidator
          payload_format: json
          subscriptions:
            - topic: Orders/incoming
              qos: 1
          on_success: validation_result
          on_error: validation_error
      output_handlers:
        - name: validation_result
          topic_expression: "static:Orders/validated"
        - name: validation_error
          topic_expression: "static:Orders/rejected"
`;

describe("extractGatewayCreateRequest", () => {
    test("forwards the OrderValidationGateway AWE shape under values for server-side migration", () => {
        const req = extractGatewayCreateRequest(ORDER_VALIDATION_GATEWAY_YAML);
        expect(req).not.toBeNull();
        expect(req?.name).toBe("OrderValidationGateway");
        expect(req?.type).toBe("event_mesh");
        expect(req?.deploy).toBe(false);
        expect(req?.description).toBe("Validates incoming order events and routes them to the OrderValidator agent.");

        expect(req?.values.event_rules).toBeUndefined();
        expect(req?.values.broker_connection_type).toBeUndefined();

        const handlers = req?.values.event_handlers as Array<Record<string, unknown>>;
        expect(handlers).toHaveLength(1);
        expect(handlers[0].name).toBe("order_events");
        expect(handlers[0].target_agent_name).toBe("OrderValidator");
        expect(handlers[0].subscriptions).toEqual([{ topic: "Orders/incoming", qos: 1 }]);

        const outputs = req?.values.output_handlers as Array<Record<string, unknown>>;
        expect(outputs).toHaveLength(2);
        expect(outputs[0]).toEqual({ name: "validation_result", topic_expression: "static:Orders/validated" });
    });

    test("forwards a custom broker config when present", () => {
        const yaml = `
apps:
  - name: CustomBrokerGateway
    app_config:
      event_mesh_broker_config:
        broker_url: tcps://broker.example.com:55443
        broker_vpn: prod
      event_handlers:
        - name: h1
          target_agent_name: A
          subscriptions:
            - topic: x/>
              qos: 1
`;
        const req = extractGatewayCreateRequest(yaml);
        expect(req?.values.event_mesh_broker_config).toEqual({
            broker_url: "tcps://broker.example.com:55443",
            broker_vpn: "prod",
        });
    });

    test("synthesizes a description for sparse configs (platform requires >= 10 chars)", () => {
        const req = extractGatewayCreateRequest(GATEWAY_YAML_MINIMAL);
        expect(req).not.toBeNull();
        expect((req?.description.length ?? 0) >= 10).toBe(true);
        const handlers = req?.values.event_handlers as Array<Record<string, unknown>>;
        expect(handlers).toHaveLength(1);
        expect(handlers[0].name).toBe("h1");
    });

    describe("entrypoint name", () => {
        const yamlWith = (appConfigExtra = "") => `
apps:
  - name: ent_orders_gateway
    app_config:
${appConfigExtra}      event_handlers:
        - name: h1
          target_agent_name: A
          subscriptions:
            - topic: ent/orders/>
`;

        test.each([
            ["the manifest component name, not the app-level YAML name", yamlWith(), "EntOrders", "EntOrders"],
            ["a snake_case manifest name, normalized", yamlWith(), "ent_orders", "EntOrders"],
            ["the app name when there is no manifest name", yamlWith(), undefined, "EntOrdersGateway"],
            ["the app name when the manifest name is blank", yamlWith(), "  ", "EntOrdersGateway"],
            ["an explicit gateway_id over the manifest name", yamlWith("      gateway_id: pet_orders\n"), "EntOrders", "PetOrders"],
            ["an explicit agent_name over the manifest name", yamlWith("      agent_name: pet_agent_gw\n"), "EntOrders", "PetAgentGw"],
            ["gateway_id over agent_name", yamlWith("      gateway_id: pet_orders\n      agent_name: other_gw\n"), "EntOrders", "PetOrders"],
            ["the manifest name when gateway_id is not text", yamlWith("      gateway_id: 42\n"), "EntOrders", "EntOrders"],
            ["the manifest name when gateway_id is blank", yamlWith('      gateway_id: "  "\n'), "EntOrders", "EntOrders"],
        ])("is %s", (_label, yaml, componentName, expected) => {
            expect(extractGatewayCreateRequest(yaml, componentName)?.name).toBe(expected);
        });
    });

    describe("entrypoint acknowledgment_policy", () => {
        const yamlWithPolicy = (policyLines: string, ownPolicy = "") => `
apps:
  - name: ent_ack
    app_config:
${policyLines}      event_handlers:
        - name: first
          target_agent_name: A
          subscriptions:
            - topic: ent/first/>
        - name: second
          target_agent_name: B
${ownPolicy}          subscriptions:
            - topic: ent/second/>
`;
        const policiesOf = (yaml: string) => (extractGatewayCreateRequest(yaml, "EntAck")?.values.event_handlers as Array<Record<string, unknown>>).map(h => h.acknowledgment_policy);
        const entrypoint = "      acknowledgment_policy:\n        mode: on_completion\n        timeout_seconds: 120\n        on_failure:\n          action: nack\n";
        const inherited = { mode: "on_completion", timeout_seconds: 120, on_failure: { action: "nack" } };

        test("is the default for every handler, with its timeout and failure handling", () => {
            expect(policiesOf(yamlWithPolicy(entrypoint))).toEqual([inherited, inherited]);
        });

        test("yields to a handler's own mapping, which replaces it outright", () => {
            expect(policiesOf(yamlWithPolicy(entrypoint, "          acknowledgment_policy:\n            timeout_seconds: 10\n"))).toEqual([inherited, { timeout_seconds: 10 }]);
        });

        test.each([
            ["null", "          acknowledgment_policy:\n"],
            ["a string", "          acknowledgment_policy: on_receive\n"],
            ["a list", "          acknowledgment_policy: [on_receive]\n"],
        ])("applies to a handler whose own policy is %s", (_label, own) => {
            expect(policiesOf(yamlWithPolicy(entrypoint, own))).toEqual([inherited, inherited]);
        });

        test("treats a handler's empty mapping as its own policy", () => {
            expect(policiesOf(yamlWithPolicy(entrypoint, "          acknowledgment_policy: {}\n"))).toEqual([inherited, {}]);
        });

        test.each([
            ["a mode only", "      acknowledgment_policy:\n        mode: on_completion\n", { mode: "on_completion" }],
            ["a timeout only", "      acknowledgment_policy:\n        timeout_seconds: 90\n", { timeout_seconds: 90 }],
            ["failure handling only", "      acknowledgment_policy:\n        on_failure:\n          action: ack\n", { on_failure: { action: "ack" } }],
        ])("forwards a partial mapping: %s", (_label, lines, expected) => {
            expect(policiesOf(yamlWithPolicy(lines))).toEqual([expected, expected]);
        });

        test("skips an event_handlers entry that is not a handler", () => {
            const yaml = `
apps:
  - name: ent_ack
    app_config:
${entrypoint}      event_handlers:
        - not a handler
        - name: only
          target_agent_name: A
          subscriptions:
            - topic: ent/only/>
`;
            const handlers = extractGatewayCreateRequest(yaml, "EntAck")?.values.event_handlers as unknown[];
            expect(handlers[0]).toBe("not a handler");
            expect((handlers[1] as Record<string, unknown>).acknowledgment_policy).toEqual(inherited);
        });

        test.each([
            ["absent", ""],
            ["a mode name rather than a mapping", "      acknowledgment_policy: on_completion\n"],
            ["a number", "      acknowledgment_policy: 42\n"],
            ["a mapping with no recognized key", "      acknowledgment_policy:\n        ack_mode: on_completion\n"],
        ])("leaves the handlers alone when it is %s", (_label, lines) => {
            expect(policiesOf(yamlWithPolicy(lines))).toEqual([undefined, undefined]);
        });
    });

    test("returns null when event_handlers is missing", () => {
        expect(extractGatewayCreateRequest(AGENT_YAML)).toBeNull();
    });

    test("forwards a string default_user_identity under values", () => {
        const yaml = `
apps:
  - name: IdentityGateway
    app_config:
      default_user_identity: gateway-bot@example.com
      event_handlers:
        - name: h1
          target_agent_name: A
`;
        const req = extractGatewayCreateRequest(yaml);
        expect(req?.values.default_user_identity).toBe("gateway-bot@example.com");
    });

    test("omits a non-string default_user_identity", () => {
        const yaml = `
apps:
  - name: BadIdentityGateway
    app_config:
      default_user_identity:
        user: someone
      event_handlers:
        - name: h1
          target_agent_name: A
`;
        const req = extractGatewayCreateRequest(yaml);
        expect(req?.values.default_user_identity).toBeUndefined();
    });

    test("drops a non-array output_handlers (e.g. an object)", () => {
        const yaml = `
apps:
  - name: BadOutputsGateway
    app_config:
      event_handlers:
        - name: h1
          target_agent_name: A
      output_handlers:
        name: not-an-array
`;
        const req = extractGatewayCreateRequest(yaml);
        expect(req).not.toBeNull();
        expect(req?.values.output_handlers).toBeUndefined();
    });
});

describe("fetchPlatformDeploymentLookup", () => {
    const page = <T>(data: T[]) => ({ data, meta: { pagination: { pageNumber: 1, nextPage: null } } });
    const v2 = { runtimeStatus: { status: "running", problems: [] }, deploymentStatus: "deployed", createdTime: null, updatedTime: null };
    const connectorV2 = (id: string, name: string) => ({ id, type: "connector", connectorType: "api", connectorSubtype: "openapi", name, description: null, values: {}, agentCount: 0, createdTime: null, updatedTime: null });
    const workflowV2 = (id: string, name: string, appConfig: Record<string, unknown>) => ({ ...v2, id, type: "workflow", name, appConfig });

    const stubCollections = (byCollection: Record<string, unknown[] | number>) => {
        const fetchMock = vi.fn<typeof fetch>(async input => {
            const collection = /^\/api\/v2\/platform\/(\w+)\?/.exec(String(input))?.[1] ?? "";
            const rows = byCollection[collection] ?? [];
            return typeof rows === "number" ? new Response(JSON.stringify({ message: "boom", errorId: "e-1" }), { status: rows }) : new Response(JSON.stringify(page(rows)));
        });
        vi.stubGlobal("fetch", fetchMock);
        onTestFinished(() => {
            vi.unstubAllGlobals();
        });
        return fetchMock;
    };

    test("builds connectorsByName keyed by name and normalized name so pre-existing connectors resolve", async () => {
        stubCollections({ connectors: [connectorV2("conn-1", "pet diet")] });

        const lookup = await fetchPlatformDeploymentLookup();

        expect(lookup.connectorsByName.get("pet diet")?.id).toBe("conn-1");
        expect(lookup.connectorsByName.get("petdiet")?.id).toBe("conn-1");
        expect(lookup.fetchedKinds.connector).toBe(true);
    });

    test("reports connector fetch failure without throwing", async () => {
        stubCollections({ connectors: 500 });

        const lookup = await fetchPlatformDeploymentLookup();

        expect(lookup.connectorsByName.size).toBe(0);
        expect(lookup.fetchedKinds.connector).toBe(false);
    });

    test("builds remoteAgentsByName from /remoteAgents (excluded from /agents) so a2a proxies resolve", async () => {
        stubCollections({ remoteAgents: [{ ...v2, id: "ra-1", type: "remoteAgent", name: "WeatherProxy", deployed: null }] });

        const lookup = await fetchPlatformDeploymentLookup();

        expect(lookup.remoteAgentsByName.get("WeatherProxy")?.id).toBe("ra-1");
        expect(lookup.remoteAgentsByName.get("weatherproxy")?.deploymentStatus).toBe("deployed");
        expect(lookup.fetchedKinds.a2aAgent).toBe(true);
        expect(lookup.agentsByName.size).toBe(0);
    });

    test("carries each workflow's stored agent_name from a flat or nested appConfig", async () => {
        stubCollections({
            workflows: [
                workflowV2("wf-flat", "mule_detection_workflow", { agent_name: "MuleDetectionWorkflow", nodes: [] }),
                workflowV2("wf-nested", "nested_workflow", { workflow: { agent_name: "NestedWorkflow", nodes: [] } }),
                workflowV2("wf-none", "plain_workflow", { nodes: [] }),
                workflowV2("wf-non-string", "odd_workflow", { agent_name: 7 }),
            ],
        });

        const { workflowsByName } = await fetchPlatformDeploymentLookup();

        expect(workflowsByName.get("mule_detection_workflow")?.agentName).toBe("MuleDetectionWorkflow");
        expect(workflowsByName.get("nested_workflow")?.agentName).toBe("NestedWorkflow");
        expect(workflowsByName.get("plain_workflow")).not.toHaveProperty("agentName");
        expect(workflowsByName.get("odd_workflow")).not.toHaveProperty("agentName");
    });

    test("reads entrypoints from /api/v2", async () => {
        const entrypoint = { id: "ep-1", type: "entrypoint", name: "orders", deploymentStatus: "deployed", runtimeStatus: { status: "running", problems: [] }, createdTime: null, updatedTime: null };
        const fetchMock = vi.fn<typeof fetch>(async input => new Response(JSON.stringify(String(input).startsWith("/api/v2/platform/entrypoints?") ? page([entrypoint]) : page([]))));
        vi.stubGlobal("fetch", fetchMock);
        onTestFinished(() => {
            vi.unstubAllGlobals();
        });

        const { gatewaysByName, fetchedKinds } = await fetchPlatformDeploymentLookup();

        expect(gatewaysByName.get("orders")).toMatchObject({ id: "ep-1", deploymentStatus: "deployed" });
        expect(fetchedKinds.gateway).toBe(true);
    });

    test("reads agents, workflows and remote agents from /api/v2", async () => {
        const byCollection: Record<string, unknown[]> = {
            agents: [{ ...v2, id: "a-1", type: "agent", agentType: "standard", name: "Orders", connectorIds: [], skills: [], toolsetConfigs: [], skillConfigs: [], toolRegistrations: [], deployed: null, isEphemeral: false, expiresTime: null }],
            workflows: [{ ...v2, id: "wf-1", type: "workflow", name: "orders_flow", appConfig: { agent_name: "OrdersFlow" } }],
            remoteAgents: [{ ...v2, id: "ra-1", type: "remoteAgent", name: "WeatherProxy", deployed: null }],
        };
        const fetchMock = vi.fn<typeof fetch>(async input => {
            const collection = /^\/api\/v2\/platform\/(\w+)\?/.exec(String(input))?.[1] ?? "";
            return new Response(JSON.stringify(page(byCollection[collection] ?? [])));
        });
        vi.stubGlobal("fetch", fetchMock);
        onTestFinished(() => {
            vi.unstubAllGlobals();
        });

        const { agentsByName, workflowsByName, remoteAgentsByName, fetchedKinds } = await fetchPlatformDeploymentLookup();

        expect(agentsByName.get("Orders")).toMatchObject({ id: "a-1", deploymentStatus: "deployed" });
        expect(workflowsByName.get("orders_flow")).toMatchObject({ id: "wf-1", agentName: "OrdersFlow" });
        expect(remoteAgentsByName.get("WeatherProxy")?.id).toBe("ra-1");
        expect(fetchedKinds).toMatchObject({ agent: true, workflow: true, a2aAgent: true });
    });

    test("reads connectors from /api/v2", async () => {
        const connector = { id: "c-1", type: "connector", connectorType: "api", connectorSubtype: "openapi", name: "Orders API", description: null, values: {}, agentCount: 0, createdTime: null, updatedTime: null };
        const fetchMock = vi.fn<typeof fetch>(async input => new Response(JSON.stringify(page(String(input).startsWith("/api/v2/platform/connectors?") ? [connector] : []))));
        vi.stubGlobal("fetch", fetchMock);
        onTestFinished(() => {
            vi.unstubAllGlobals();
        });

        const { connectorsByName, fetchedKinds } = await fetchPlatformDeploymentLookup();

        expect(connectorsByName.get("Orders API")?.id).toBe("c-1");
        expect(fetchedKinds.connector).toBe(true);
    });
});

describe("findLastAiMessageId", () => {
    test("returns the last non-user message id", () => {
        const messages = [
            { isUser: true, metadata: { messageId: "u1" } },
            { isUser: false, metadata: { messageId: "a1" } },
            { isUser: true, metadata: { messageId: "u2" } },
            { isUser: false, metadata: { messageId: "a2" } },
        ];
        expect(findLastAiMessageId(messages)).toBe("a2");
    });

    test("skips a trailing user message", () => {
        const messages = [
            { isUser: false, metadata: { messageId: "a1" } },
            { isUser: true, metadata: { messageId: "u1" } },
        ];
        expect(findLastAiMessageId(messages)).toBe("a1");
    });

    test("returns undefined when there is no AI message or no id", () => {
        expect(findLastAiMessageId([])).toBeUndefined();
        expect(findLastAiMessageId([{ isUser: true, metadata: { messageId: "u1" } }])).toBeUndefined();
        expect(findLastAiMessageId([{ isUser: false, metadata: {} }])).toBeUndefined();
    });
});

describe("hasLiveBuildActivity", () => {
    const cd = (gen: boolean[]) => new Map<string, { isGenerating: boolean }>(gen.map((g, i) => [String(i), { isGenerating: g }]));
    const msg = (over: Record<string, unknown> = {}) => ({ isUser: false, isComplete: false, progressUpdates: [] as { type: string }[], ...over });

    test("true when any component is generating, regardless of message/flag", () => {
        expect(hasLiveBuildActivity(undefined, cd([false, true]), false)).toBe(true);
    });
    test("true when the current message has status updates (no generation)", () => {
        expect(hasLiveBuildActivity(msg({ progressUpdates: [{ type: "status" }] }), cd([false]), false)).toBe(true);
    });
    test("false on the initial gap: no generation and no updates (the pulse keeps showing)", () => {
        expect(hasLiveBuildActivity(msg({ progressUpdates: [] }), cd([false]), false)).toBe(false);
    });
    test("false when the last message is a user message", () => {
        expect(hasLiveBuildActivity(msg({ isUser: true, progressUpdates: [{ type: "status" }] }), cd([false]), false)).toBe(false);
    });
    test("false when the last message is already complete", () => {
        expect(hasLiveBuildActivity(msg({ isComplete: true, progressUpdates: [{ type: "status" }] }), cd([false]), false)).toBe(false);
    });
    test("thinking-only updates don't count when thinking is hidden", () => {
        expect(hasLiveBuildActivity(msg({ progressUpdates: [{ type: "thinking" }] }), cd([false]), false)).toBe(false);
        expect(hasLiveBuildActivity(msg({ progressUpdates: [{ type: "thinking" }] }), cd([false]), true)).toBe(true);
    });
});

describe("computeSuppressedActivityMessageId", () => {
    const base = { showCreationProgress: true, isApplyingChanges: false, isProgressCollapsed: false, progressAnchorMessageId: "anchor", lastAiMessageId: "last" };

    test("suppresses the live last message while building (not collapsed)", () => {
        expect(computeSuppressedActivityMessageId(base)).toBe("last");
    });

    test("suppresses the approval anchor once collapsed", () => {
        expect(computeSuppressedActivityMessageId({ ...base, isProgressCollapsed: true })).toBe("anchor");
    });

    test("falls back to the last message when collapsed but no anchor", () => {
        expect(computeSuppressedActivityMessageId({ ...base, isProgressCollapsed: true, progressAnchorMessageId: null })).toBe("last");
    });

    test("suppresses nothing when the creation timeline isn't shown or during an apply", () => {
        expect(computeSuppressedActivityMessageId({ ...base, showCreationProgress: false })).toBeUndefined();
        expect(computeSuppressedActivityMessageId({ ...base, isApplyingChanges: true })).toBeUndefined();
    });
});

describe("detectComponentsWithMissingCredentials", () => {
    const manifest = {
        components: [
            { name: "DbConn", type: "connector", origin: "session" },
            { name: "ApiConn", type: "connector", origin: "session" },
            { name: "PlatformConn", type: "connector", origin: "platform" },
            { name: "MyAgent", type: "agent", origin: "session" },
            { name: "RemotePeer", type: "a2a_agent", origin: "session" },
            { name: "PlatformPeer", type: "a2a_agent", origin: "platform" },
        ],
    };

    function dataWith(values: Record<string, Record<string, unknown>>) {
        return new Map(Object.entries(values).map(([name, v]) => [name, { connectorMetadata: { values: v } }]));
    }

    function a2aDataWith(metas: Record<string, Record<string, unknown>>) {
        return new Map(Object.entries(metas).map(([name, m]) => [name, { a2aAgentMetadata: m as unknown as import("@/api/remote-agents").CreateRemoteAgentRequest }]));
    }

    test("reports session connectors holding placeholder credentials with a count", () => {
        const data = dataWith({
            DbConn: { host: "db.example.com", password: SAM_REQUIRED_PLACEHOLDER },
            ApiConn: { apiKey: SAM_REQUIRED_PLACEHOLDER, token: SAM_REQUIRED_PLACEHOLDER },
        });
        const missing = detectComponentsWithMissingCredentials(manifest, data);
        expect(missing.get("DbConn")).toBe(1);
        expect(missing.get("ApiConn")).toBe(2);
        expect(missing.size).toBe(2);
    });

    test("ignores connectors with all credentials filled", () => {
        const data = dataWith({ DbConn: { host: "db", password: "secret" } });
        expect(detectComponentsWithMissingCredentials(manifest, data).size).toBe(0);
    });

    test("ignores platform-origin connectors, non-connectors, and connectors without parsed config", () => {
        const data = dataWith({
            PlatformConn: { token: SAM_REQUIRED_PLACEHOLDER },
            MyAgent: { token: SAM_REQUIRED_PLACEHOLDER },
        });
        expect(detectComponentsWithMissingCredentials(manifest, data).size).toBe(0);
    });

    test("handles null manifest / componentData", () => {
        expect(detectComponentsWithMissingCredentials(null, null).size).toBe(0);
        expect(detectComponentsWithMissingCredentials(manifest, null).size).toBe(0);
    });

    test("ignores connectors with null connectorMetadata or empty values", () => {
        const data = new Map<string, { connectorMetadata?: { values?: Record<string, unknown> | null } | null }>([
            ["DbConn", { connectorMetadata: null }],
            ["ApiConn", { connectorMetadata: { values: {} } }],
        ]);
        expect(detectComponentsWithMissingCredentials(manifest, data).size).toBe(0);
    });

    test("reports a2a agents whose auth secrets are still placeholders (agent + card auth)", () => {
        const data = a2aDataWith({
            RemotePeer: {
                agentAuthConfig: { type: "apikey", headerName: "X-Key", apiKey: SAM_REQUIRED_PLACEHOLDER },
                agentCardAuthConfig: { type: "bearer", token: SAM_REQUIRED_PLACEHOLDER },
            },
        });
        const missing = detectComponentsWithMissingCredentials(manifest, data);
        expect(missing.get("RemotePeer")).toBe(2);
        expect(missing.size).toBe(1);
    });

    test("ignores a2a agents whose auth is filled or needs none", () => {
        const data = a2aDataWith({
            RemotePeer: { agentAuthConfig: { type: "bearer", token: "real-token" } },
        });
        expect(detectComponentsWithMissingCredentials(manifest, data).size).toBe(0);

        const noAuth = a2aDataWith({ RemotePeer: { agentAuthConfig: { type: "none" } } });
        expect(detectComponentsWithMissingCredentials(manifest, noAuth).size).toBe(0);
    });

    test("ignores platform-origin a2a agents even with placeholder auth", () => {
        const data = a2aDataWith({
            PlatformPeer: { agentAuthConfig: { type: "bearer", token: SAM_REQUIRED_PLACEHOLDER } },
        });
        expect(detectComponentsWithMissingCredentials(manifest, data).size).toBe(0);
    });
});

describe("buildBlockedByCredsMessage", () => {
    test("includes the connector list and the credential warning", () => {
        const msg = buildBlockedByCredsMessage(new Map([["DbConn", 1]]));
        expect(msg).toContain("DbConn (1 credential)");
        expect(msg).toContain("Building is blocked");
        expect(msg).toContain("placeholder credentials");
    });

    test("names a route out, not just the problem", () => {
        expect(buildBlockedByCredsMessage(new Map([["DbConn", 1]]))).toMatch(/ask the builder for them in the chat/i);
    });

    test("qualifies the Connectors page rather than promising or denying it", () => {
        const msg = buildBlockedByCredsMessage(new Map([["DbConn", 1]]));
        expect(msg).toMatch(/Connectors page if the connector is already saved/i);
    });

    test("offers the Connectors page for secret values only, since other fields cannot be cleared that way", () => {
        const msg = buildBlockedByCredsMessage(new Map([["DbConn", 1]]));
        expect(msg).toMatch(/Secret values such as passwords and API keys can also be entered on the Connectors page/);
        expect(msg).not.toMatch(/or enter them on the Connectors page/);
    });

    test("carries the action it is refusing, so the Test path can share it", () => {
        expect(buildBlockedByCredsMessage(new Map([["DbConn", 1]]), "testing")).toContain("Testing is blocked");
        expect(buildBlockedByCredsMessage(new Map([["DbConn", 1]]))).toContain("Building is blocked");
    });
});

describe("formatMissingCredentials", () => {
    test("pluralizes and joins entries", () => {
        const m = new Map([
            ["DbConn", 1],
            ["ApiConn", 2],
        ]);
        expect(formatMissingCredentials(m)).toBe("DbConn (1 credential), ApiConn (2 credentials)");
    });
});

describe("isComponentLiveOnMesh", () => {
    test("matches a live card by exact name", () => {
        expect(isComponentLiveOnMesh("DocSummarizer", [{ name: "DocSummarizer" }])).toBe(true);
    });

    test("matches by displayName when the card name differs (broker instance name)", () => {
        expect(isComponentLiveOnMesh("Doc Summarizer", [{ name: "doc-summarizer-7f3a", displayName: "Doc Summarizer" }])).toBe(true);
    });

    test("matches across casing / punctuation variants via normalizeComponentName", () => {
        expect(isComponentLiveOnMesh("DataProcessingWorkflow", [{ name: "data_processing_workflow" }])).toBe(true);
        expect(isComponentLiveOnMesh("Data Processing Workflow", [{ name: "DataProcessingWorkflow" }])).toBe(true);
    });

    test("false when no card matches", () => {
        expect(isComponentLiveOnMesh("DocSummarizer", [{ name: "OtherAgent" }])).toBe(false);
    });

    test("false for an empty / undefined card list (skills, gateways — never published)", () => {
        expect(isComponentLiveOnMesh("MySkill", [])).toBe(false);
        expect(isComponentLiveOnMesh("MySkill", undefined)).toBe(false);
    });

    test("ignores cards missing both name and displayName", () => {
        expect(isComponentLiveOnMesh("DocSummarizer", [{}])).toBe(false);
    });
});

describe("isSessionComponentLive", () => {
    const ID = "01a160d0-2474-7426-830a-ae0397fcdc9a";

    test("true only for the card named for the recorded id of the component's kind", () => {
        expect(isSessionComponentLive({ id: ID, type: "agent" }, [{ name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a" }])).toBe(true);
        expect(isSessionComponentLive({ id: ID, type: "workflow" }, [{ name: "workflow_01a160d0_2474_7426_830a_ae0397fcdc9a" }])).toBe(true);
        expect(isSessionComponentLive({ id: ID, type: "workflow" }, [{ name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a" }])).toBe(false);
    });

    test("never by name: a same-named card another session deployed does not count", () => {
        expect(isSessionComponentLive({ id: ID, type: "agent" }, [{ name: "agent_other", displayName: "DocSummarizer" } as { name: string }])).toBe(false);
    });

    test("never by name: a plain-named card (a YAML or built-in agent) does not make a same-named component live", () => {
        const component = { id: ID, type: "agent", name: "DocSummarizer" };
        expect(isSessionComponentLive(component, [{ name: "DocSummarizer" }])).toBe(false);
        expect(isSessionComponentLive(component, [{ name: "DocSummarizer" }, { name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a" }])).toBe(true);
    });

    test("one card named for the recorded id is enough among unrelated ones, wherever it sits", () => {
        const own = { name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a" };
        const others = [{ name: "OrchestratorAgent" }, { name: "agent_01a160ce_7f96_7e82_80dd_7e47b3032891" }];
        expect(isSessionComponentLive({ id: ID, type: "agent" }, [...others, own])).toBe(true);
        expect(isSessionComponentLive({ id: ID, type: "agent" }, [own, ...others])).toBe(true);
        expect(isSessionComponentLive({ id: ID, type: "agent" }, others)).toBe(false);
    });

    test("false without a recorded id, for a kind that publishes no card, or with no cards", () => {
        const cards = [{ name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a" }];
        expect(isSessionComponentLive({ type: "agent" }, cards)).toBe(false);
        expect(isSessionComponentLive({ id: ID, type: "skill" }, cards)).toBe(false);
        expect(isSessionComponentLive({ id: ID, type: "agent" }, [])).toBe(false);
        expect(isSessionComponentLive({ id: ID, type: "agent" }, undefined)).toBe(false);
    });
});

describe("validatedButLiveComponents", () => {
    const ID = "01a160d0-2474-7426-830a-ae0397fcdc9a";
    const cards = [{ name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a" }];

    test("selects a validated session component whose own instance is live", () => {
        const own = { id: ID, type: "agent", origin: "session", status: "validated" };
        expect(validatedButLiveComponents([own], cards)).toEqual([own]);
    });

    test("leaves out a same-named validated component with no recorded id", () => {
        expect(validatedButLiveComponents([{ type: "agent", origin: "session", status: "validated", name: "Poet" }], [{ name: "agent_other", displayName: "Poet" } as { name: string }])).toEqual([]);
    });

    test("never by name: a plain-named card does not select a same-named validated component, with or without an id", () => {
        const withId = { id: ID, name: "Poet", type: "agent", origin: "session", status: "validated" };
        const withoutId = { name: "Poet", type: "agent", origin: "session", status: "validated" };
        expect(validatedButLiveComponents([withId, withoutId], [{ name: "Poet" }])).toEqual([]);
        expect(validatedButLiveComponents([withId, withoutId], [{ name: "Poet" }, ...cards])).toEqual([withId]);
    });

    test("leaves out components that are not validated, are platform-origin, or publish no card", () => {
        expect(validatedButLiveComponents([{ id: ID, type: "agent", origin: "session", status: "draft" }], cards)).toEqual([]);
        expect(validatedButLiveComponents([{ id: ID, type: "agent", origin: "session", status: "deployed" }], cards)).toEqual([]);
        expect(validatedButLiveComponents([{ id: ID, type: "agent", origin: "platform", status: "validated" }], cards)).toEqual([]);
        expect(validatedButLiveComponents([{ id: ID, type: "gateway", origin: "session", status: "validated" }], cards)).toEqual([]);
    });
});

describe("extractSkillConfig", () => {
    test("pulls reference (asset) and tool names from a skill config", () => {
        const yamlText = `
name: weather-kb
description: Weather knowledge.
instruction_content: Use the references.
references:
  - name: endpoints.md
    content: "# Endpoints"
  - name: glossary.md
    artifact: uploaded_glossary
tools:
  - tool_name: web_request
  - search_artifact
`;
        expect(extractSkillConfig(yamlText)).toEqual({
            references: ["endpoints.md", "glossary.md"],
            tools: ["web_request", "search_artifact"],
        });
    });

    test("returns empty arrays when the skill has no references or tools", () => {
        expect(extractSkillConfig("name: bare\ndescription: nothing else")).toEqual({ references: [], tools: [] });
    });

    test("returns null on unparseable YAML", () => {
        expect(extractSkillConfig(":\n  - [unbalanced")).toBeNull();
    });
});

describe("extractSkillConfig — edge cases", () => {
    test("prefers tool_name over name and ignores a non-list tools value", () => {
        expect(extractSkillConfig("tools:\n  - tool_name: web_request\n    name: ignored\n")).toEqual({ references: [], tools: ["web_request"] });
        expect(extractSkillConfig("name: s\ndescription: d\ntools: web_request")).toEqual({ references: [], tools: [] });
    });
});

describe("extractSkillBundleConfig", () => {
    test("extracts the full bundle config from a builder-generated skill YAML", () => {
        const yamlText = `
name: report-skill
description: Renders the quarterly report.
tags: [reporting]
examples:
  - Render the Q3 report
instruction_content: |
  # Report Skill
  Use instantiate_template with the bundled asset.
references:
  - name: guide.md
    content: "# Guide"
  - name: spec.md
    artifact: uploaded_spec
assets:
  - name: quarterly_report.samt
    artifact: packaged_report
`;
        expect(extractSkillBundleConfig(yamlText)).toEqual({
            name: "report-skill",
            description: "Renders the quarterly report.",
            tags: ["reporting"],
            examples: ["Render the Q3 report"],
            instruction_content: "# Report Skill\nUse instantiate_template with the bundled asset.\n",
            references: [
                { name: "guide.md", content: "# Guide" },
                { name: "spec.md", artifact: "uploaded_spec" },
            ],
            assets: [{ name: "quarterly_report.samt", artifact: "packaged_report" }],
        });
    });

    test("keeps optional sections undefined when absent (identity-only config still saves)", () => {
        const config = extractSkillBundleConfig("name: bare\ndescription: A minimal identity-only config.");
        expect(config).toMatchObject({ name: "bare", description: "A minimal identity-only config." });
        expect(config?.instruction_content).toBeUndefined();
        expect(config?.references).toBeUndefined();
        expect(config?.assets).toBeUndefined();
    });

    test("returns null when identity fields are missing or YAML is unparseable", () => {
        expect(extractSkillBundleConfig("name: only-name")).toBeNull();
        expect(extractSkillBundleConfig(":\n  - [unbalanced")).toBeNull();
    });

    test("drops malformed reference/asset entries but keeps well-formed ones", () => {
        const yamlText = `
name: mixed
description: Skill with some malformed entries.
references:
  - just-a-string
  - name: good.md
    content: ok
assets:
  - 42
  - name: good.samt
    artifact: packaged
`;
        expect(extractSkillBundleConfig(yamlText)).toMatchObject({
            references: [{ name: "good.md", content: "ok" }],
            assets: [{ name: "good.samt", artifact: "packaged" }],
        });
    });
});

describe("skillToolNames", () => {
    test("unions component + config tools and de-duplicates", () => {
        expect(skillToolNames(["a", "b"], ["b", "c"])).toEqual(["a", "b", "c"]);
    });
    test("handles undefined inputs", () => {
        expect(skillToolNames(undefined, undefined)).toEqual([]);
        expect(skillToolNames(["x"], undefined)).toEqual(["x"]);
    });
});

describe("build-complete CTA gating helpers", () => {
    const noEdits = {
        versions: new Map([
            ["workflow.yaml", 0],
            ["agent.yaml", 0],
            ["gateway.yaml", 0],
            ["a2a_agent.yaml", 0],
            ["skill.yaml", 0],
            ["connector.yaml", 0],
        ]),
        serverApplyEnabled: false,
    };

    const ai = (id: string, extra: Record<string, unknown> = {}) => ({ isUser: false, metadata: { messageId: id }, ...extra });
    const user = (id: string) => ({ isUser: true, metadata: { messageId: id } });
    const hil = (id: string, extra: { responded?: boolean; timedOut?: boolean; cancelled?: boolean } = {}) => ai(id, { userInputRequest: { responded: false, timedOut: false, ...extra } });

    describe("shouldShowBuildCompleteCTA", () => {
        const base = { hasApplyComplete: false, anchored: true, inBuildTab: true, isApplyingChanges: false, isResponding: false };
        test("shows when anchored, in build tab, and no suppressor", () => {
            expect(shouldShowBuildCompleteCTA(base)).toBe(true);
        });
        test("suppressed by applyComplete / applying / responding", () => {
            expect(shouldShowBuildCompleteCTA({ ...base, hasApplyComplete: true })).toBe(false);
            expect(shouldShowBuildCompleteCTA({ ...base, isApplyingChanges: true })).toBe(false);
            expect(shouldShowBuildCompleteCTA({ ...base, isResponding: true })).toBe(false);
        });
        test("hidden when not anchored or not in build tab", () => {
            expect(shouldShowBuildCompleteCTA({ ...base, anchored: false })).toBe(false);
            expect(shouldShowBuildCompleteCTA({ ...base, inBuildTab: false })).toBe(false);
        });
    });

    describe("shouldShowPlanCard", () => {
        const base = { loadedForEdit: false, hasManifest: true, planApproved: false, hasPlanAnchor: false, isAnchored: () => true, isLastMessage: true };
        test("hidden when loaded for edit or no manifest", () => {
            expect(shouldShowPlanCard({ ...base, loadedForEdit: true })).toBe(false);
            expect(shouldShowPlanCard({ ...base, hasManifest: false })).toBe(false);
        });
        test("before approval, tracks the last message", () => {
            expect(shouldShowPlanCard({ ...base, isLastMessage: true })).toBe(true);
            expect(shouldShowPlanCard({ ...base, isLastMessage: false })).toBe(false);
        });
        test("once approved with an anchor, defers to isAnchored instead of isLastMessage", () => {
            expect(shouldShowPlanCard({ ...base, planApproved: true, hasPlanAnchor: true, isAnchored: () => true, isLastMessage: false })).toBe(true);
            expect(shouldShowPlanCard({ ...base, planApproved: true, hasPlanAnchor: true, isAnchored: () => false, isLastMessage: true })).toBe(false);
        });
        test("approved without an anchor still falls back to isLastMessage", () => {
            expect(shouldShowPlanCard({ ...base, planApproved: true, hasPlanAnchor: false, isLastMessage: true })).toBe(true);
        });
    });

    describe("shouldShowCreationProgressCard", () => {
        const base = { showCreationProgress: true, hasManifest: true, isApplyingChanges: false, hasProgressAnchor: false, isProgressCollapsed: false, isAnchored: () => true, isLastMessage: true };
        test("hidden when creation progress isn't showing, no manifest, or applying changes", () => {
            expect(shouldShowCreationProgressCard({ ...base, showCreationProgress: false })).toBe(false);
            expect(shouldShowCreationProgressCard({ ...base, hasManifest: false })).toBe(false);
            expect(shouldShowCreationProgressCard({ ...base, isApplyingChanges: true })).toBe(false);
        });
        test("uncollapsed, tracks the last message", () => {
            expect(shouldShowCreationProgressCard({ ...base, isLastMessage: true })).toBe(true);
            expect(shouldShowCreationProgressCard({ ...base, isLastMessage: false })).toBe(false);
        });
        test("once collapsed with an anchor, defers to isAnchored instead of isLastMessage", () => {
            expect(shouldShowCreationProgressCard({ ...base, isProgressCollapsed: true, hasProgressAnchor: true, isAnchored: () => true, isLastMessage: false })).toBe(true);
            expect(shouldShowCreationProgressCard({ ...base, isProgressCollapsed: true, hasProgressAnchor: true, isAnchored: () => false, isLastMessage: true })).toBe(false);
        });
        test("collapsed without an anchor still falls back to isLastMessage", () => {
            expect(shouldShowCreationProgressCard({ ...base, isProgressCollapsed: true, hasProgressAnchor: false, isLastMessage: true })).toBe(true);
        });
    });

    describe("shouldShowEditingProgressCard", () => {
        const base = { isLastMessage: true, isApplyingChanges: true, hasManifest: true, inBuildTab: true };
        test("shows only when every gate holds", () => {
            expect(shouldShowEditingProgressCard(base)).toBe(true);
        });
        test("hidden if not last message, not applying, no manifest, or outside build tab", () => {
            expect(shouldShowEditingProgressCard({ ...base, isLastMessage: false })).toBe(false);
            expect(shouldShowEditingProgressCard({ ...base, isApplyingChanges: false })).toBe(false);
            expect(shouldShowEditingProgressCard({ ...base, hasManifest: false })).toBe(false);
            expect(shouldShowEditingProgressCard({ ...base, inBuildTab: false })).toBe(false);
        });
    });

    describe("shouldShowBuildCompleteCTAAddon", () => {
        const base = { hasManifest: true, hasDeployFailure: false, loadedForEdit: false, hasApplyComplete: false, anchored: true, inBuildTab: true, isApplyingChanges: false, isResponding: false };
        test("shows when the outer gates and shouldShowBuildCompleteCTA both hold", () => {
            expect(shouldShowBuildCompleteCTAAddon(base)).toBe(true);
        });
        test("hidden without a manifest, with a deploy failure, or loaded for edit", () => {
            expect(shouldShowBuildCompleteCTAAddon({ ...base, hasManifest: false })).toBe(false);
            expect(shouldShowBuildCompleteCTAAddon({ ...base, hasDeployFailure: true })).toBe(false);
            expect(shouldShowBuildCompleteCTAAddon({ ...base, loadedForEdit: true })).toBe(false);
        });
        test("still defers to shouldShowBuildCompleteCTA's own suppressors", () => {
            expect(shouldShowBuildCompleteCTAAddon({ ...base, hasApplyComplete: true })).toBe(false);
            expect(shouldShowBuildCompleteCTAAddon({ ...base, anchored: false })).toBe(false);
        });
    });

    describe("shouldShowEditCompleteCard", () => {
        const base = { hasApplyComplete: true, anchored: true, inBuildTab: true };
        test("shows when apply is complete, anchored, and in the build tab", () => {
            expect(shouldShowEditCompleteCard(base)).toBe(true);
        });
        test("hidden without an apply-complete artifact", () => {
            expect(shouldShowEditCompleteCard({ ...base, hasApplyComplete: false })).toBe(false);
        });
        test("hidden when not anchored — the card must ride a fixed anchor, not the last message", () => {
            expect(shouldShowEditCompleteCard({ ...base, anchored: false })).toBe(false);
        });
        test("hidden outside the build tab", () => {
            expect(shouldShowEditCompleteCard({ ...base, inBuildTab: false })).toBe(false);
        });
    });

    describe("resolvePlanAnchorId", () => {
        const cases: Array<{ name: string; msgs: Array<ReturnType<typeof ai> | ReturnType<typeof user>>; expected: string | null }> = [
            { name: "a lone assistant message", msgs: [user("u1"), ai("a1")], expected: "a1" },
            { name: "the latest assistant message after a follow-up turn, not the first plan message", msgs: [user("u1"), ai("plan1"), user("u2"), ai("build"), user("u3"), ai("followup")], expected: "followup" },
            { name: "the assistant message ahead of the user request still waiting for its reply", msgs: [user("u1"), ai("plan1"), user("u2")], expected: "plan1" },
            { name: "above a trailing expired question", msgs: [user("u1"), ai("build1"), hil("q1", { timedOut: true })], expected: "build1" },
            { name: "above a trailing open question", msgs: [user("u1"), ai("build1"), hil("q1")], expected: "build1" },
            { name: "above every answered or cancelled question in a row", msgs: [ai("build1"), ai("reply1"), hil("q1", { responded: true }), hil("q2", { cancelled: true })], expected: "reply1" },
            { name: "a message without an id is never the anchor", msgs: [ai("a1"), { isUser: false, metadata: {} } as ReturnType<typeof ai>], expected: "a1" },
            { name: "nothing when every assistant message is a question", msgs: [user("u1"), hil("q1")], expected: null },
            { name: "nothing for an empty chat", msgs: [], expected: null },
        ];
        test.each(cases)("anchors on $name", ({ msgs, expected }) => {
            expect(resolvePlanAnchorId(msgs)).toBe(expected);
        });
    });

    describe("isLastNonUserMessage / isFirstNonUserMessageWithId", () => {
        test("identifies the last non-user message by identity", () => {
            const a1 = ai("a1");
            const a2 = ai("a2");
            const msgs = [a1, user("u1"), a2];
            expect(isLastNonUserMessage(msgs, a2)).toBe(true);
            expect(isLastNonUserMessage(msgs, a1)).toBe(false);
        });
        test("matches the first non-user message carrying the target id", () => {
            const a1 = ai("a1");
            const a2 = ai("a2");
            expect(isFirstNonUserMessageWithId([a1, a2], a1, "a1")).toBe(true);
            expect(isFirstNonUserMessageWithId([a1, a2], a2, "a1")).toBe(false);
        });
    });

    describe("collectPendingHilInputMessages", () => {
        test("returns only still-open prompts (not answered or timed out)", () => {
            const open = hil("q1");
            const msgs = [ai("a1"), open, hil("q2", { responded: true }), hil("q3", { timedOut: true }), hil("q4", { cancelled: true })];
            expect(collectPendingHilInputMessages(msgs)).toEqual([open]);
        });
    });

    describe("isBuildReadyToTest", () => {
        const comp = (type: string, status: string, extra: Record<string, unknown> = {}) => ({ origin: "session", type, status, filename: `${type}.yaml`, deployed_version: 0, ...extra });

        describe("with undeployed edits", () => {
            const wf = (extra: Record<string, unknown>) => ({ origin: "session", type: "workflow", status: "deployed", filename: "wf.yaml", ...extra });

            test("not complete while a deployed component's config has moved on", () => {
                const versions = new Map([["wf.yaml", 2]]);
                expect(isBuildReadyToTest([wf({ deployed_version: 0 })], { versions, serverApplyEnabled: false })).toBe(false);
            });

            test("not complete when the deploy cannot be proven — the observed dead end", () => {
                expect(isBuildReadyToTest([wf({})], { versions: new Map(), serverApplyEnabled: false })).toBe(false);
            });

            test("complete when the deployed version matches the current artifact", () => {
                const versions = new Map([["wf.yaml", 2]]);
                expect(isBuildReadyToTest([wf({ deployed_version: 2 })], { versions, serverApplyEnabled: false })).toBe(true);
            });

            test("isBuildCompleteCtaShowing follows it, so the two cannot drift", () => {
                const versions = new Map([["wf.yaml", 2]]);
                expect(isBuildCompleteCtaShowing([wf({ deployed_version: 0 })], { versions, serverApplyEnabled: false })).toBe(false);
                expect(isBuildCompleteCtaShowing([wf({ deployed_version: 2 })], { versions, serverApplyEnabled: false })).toBe(true);
            });
        });

        describe("with an unsaved connector edit", () => {
            const agent = { origin: "session", type: "agent", status: "deployed", filename: "agent.yaml", deployed_version: 1 };
            const connector = (extra: Record<string, unknown> = {}) => ({ origin: "session", type: "connector", status: "validated", filename: "db.yaml", deployed_version: 1, ...extra });
            const at = (connectorVersion: number) =>
                new Map([
                    ["agent.yaml", 1],
                    ["db.yaml", connectorVersion],
                ]);

            test.each([
                ["connector edited past its saved version", [agent, connector()], at(2), true, false],
                ["connector at its saved version", [agent, connector()], at(1), true, true],
                ["connector edited, server-side apply off", [agent, connector()], at(2), false, true],
                ["connector with no saved version recorded", [agent, connector({ deployed_version: undefined })], at(2), true, true],
                ["platform-origin connector edited", [agent, connector({ origin: "platform" })], at(2), true, true],
                ["no connector at all", [agent], at(2), true, true],
            ])("%s", (_label, components, versions, serverApplyEnabled, expected) => {
                expect(isBuildReadyToTest(components, { versions, serverApplyEnabled })).toBe(expected);
            });

            test("isBuildCompleteCtaShowing follows it, so the plan card offers Build & Activate", () => {
                expect(isBuildCompleteCtaShowing([agent, connector()], { versions: at(2), serverApplyEnabled: true })).toBe(false);
                expect(isBuildCompleteCtaShowing([agent, connector()], { versions: at(1), serverApplyEnabled: true })).toBe(true);
            });
        });

        describe("with an unsaved skill edit", () => {
            const agent = { origin: "session", type: "agent", status: "deployed", filename: "agent.yaml", deployed_version: 1 };
            const skill = (extra: Record<string, unknown> = {}) => ({ origin: "session", type: "skill", status: "validated", filename: "notes.yaml", deployed_version: 1, ...extra });
            const at = (skillVersion: number) =>
                new Map([
                    ["agent.yaml", 1],
                    ["notes.yaml", skillVersion],
                ]);

            test.each([
                ["skill edited past its saved version", [agent, skill()], at(2), true, false],
                ["skill edited, server-side apply off: a skill is saved either way", [agent, skill()], at(2), false, false],
                ["skill at its saved version", [agent, skill()], at(1), true, true],
                ["skill with no saved version recorded", [agent, skill({ deployed_version: undefined })], at(2), true, true],
                ["platform-origin skill edited", [agent, skill({ origin: "platform" })], at(2), true, true],
            ])("%s", (_label, components, versions, serverApplyEnabled, expected) => {
                expect(isBuildReadyToTest(components, { versions, serverApplyEnabled })).toBe(expected);
            });

            test("isBuildCompleteCtaShowing follows it", () => {
                expect(isBuildCompleteCtaShowing([agent, skill()], { versions: at(2), serverApplyEnabled: true })).toBe(false);
                expect(isBuildCompleteCtaShowing([agent, skill()], { versions: at(1), serverApplyEnabled: true })).toBe(true);
            });
        });

        test("true when every testable component is deployed and there's no gateway", () => {
            expect(isBuildReadyToTest([comp("workflow", "deployed"), comp("agent", "deployed")], noEdits)).toBe(true);
        });
        test("true only when the gateway is ALSO deployed", () => {
            expect(isBuildReadyToTest([comp("workflow", "deployed"), comp("agent", "deployed"), comp("gateway", "deployed")], noEdits)).toBe(true);
        });
        test("false when a gateway failed/hasn't deployed — the build isn't fully activated, so no Test CTA", () => {
            expect(isBuildReadyToTest([comp("workflow", "deployed"), comp("agent", "deployed"), comp("gateway", "validated")], noEdits)).toBe(false);
        });
        test("false when a testable component is not yet deployed", () => {
            expect(isBuildReadyToTest([comp("workflow", "deployed"), comp("agent", "validated")], noEdits)).toBe(false);
        });
        test("false when there are no testable components (skill-only / gateway-only build)", () => {
            expect(isBuildReadyToTest([comp("skill", "validated"), comp("gateway", "deployed")], noEdits)).toBe(false);
            expect(isBuildReadyToTest([], noEdits)).toBe(false);
            expect(isBuildReadyToTest(undefined, noEdits)).toBe(false);
        });
        test("ignores platform-origin components (pre-existing, not part of this build)", () => {
            expect(isBuildReadyToTest([comp("agent", "deployed"), { origin: "platform", type: "gateway", status: "validated" }], noEdits)).toBe(true);
        });
    });

    describe("isSkillOnlyBuildComplete", () => {
        const comp = (type: string, status: string, extra: Record<string, unknown> = {}) => ({ origin: "session", type, status, filename: `${type}.yaml`, deployed_version: 0, ...extra });

        test("true when there are no testable components and every skill is built (validated)", () => {
            expect(isSkillOnlyBuildComplete([comp("skill", "validated")])).toBe(true);
            expect(isSkillOnlyBuildComplete([comp("skill", "validated"), comp("skill", "deployed")])).toBe(true);
        });
        test("false for a built skill alongside an UNDEPLOYED gateway — the entrypoint must deploy first", () => {
            expect(isSkillOnlyBuildComplete([comp("skill", "validated"), comp("gateway", "validated")])).toBe(false);
        });
        test("true for a built skill alongside a DEPLOYED gateway", () => {
            expect(isSkillOnlyBuildComplete([comp("skill", "validated"), comp("gateway", "deployed")])).toBe(true);
        });
        test("false when any testable component exists (that path is governed by isBuildReadyToTest)", () => {
            expect(isSkillOnlyBuildComplete([comp("skill", "validated"), comp("agent", "deployed")])).toBe(false);
        });
        test("false when a skill is not yet built, or there are no skills", () => {
            expect(isSkillOnlyBuildComplete([comp("skill", "draft")])).toBe(false);
            expect(isSkillOnlyBuildComplete([comp("gateway", "deployed")])).toBe(false);
            expect(isSkillOnlyBuildComplete([])).toBe(false);
        });
    });

    describe("isEntrypointOnlyBuildComplete", () => {
        const comp = (type: string, status: string, extra: Record<string, unknown> = {}) => ({ origin: "session", type, status, filename: `${type}.yaml`, deployed_version: 0, ...extra });

        test.each([
            ["a deployed entrypoint alone", [comp("gateway", "deployed")], true],
            ["two deployed entrypoints", [comp("gateway", "deployed"), comp("gateway", "deployed")], true],
            ["a deployed entrypoint beside a platform agent it targets", [comp("gateway", "deployed"), comp("agent", "deployed", { origin: "platform" })], true],
            ["an entrypoint that is only validated, as when the deploy was refused", [comp("gateway", "validated")], false],
            ["one entrypoint deployed and another not", [comp("gateway", "deployed"), comp("gateway", "validated")], false],
            ["an entrypoint with a session agent, which the test card owns", [comp("gateway", "deployed"), comp("agent", "deployed")], false],
            ["an entrypoint with a skill, which the skill card owns", [comp("gateway", "deployed"), comp("skill", "validated")], false],
            ["an entrypoint with a connector, which the connector card owns", [comp("gateway", "deployed"), comp("connector", "validated")], false],
            ["a platform entrypoint only, which is not part of this build", [comp("gateway", "deployed", { origin: "platform" })], false],
            ["no components", [], false],
        ])("%s", (_label, components, expected) => {
            expect(isEntrypointOnlyBuildComplete(components, noEdits)).toBe(expected);
        });

        test.each([
            ["a deployed version the saved artifact equals", { versions: new Map([["gateway.yaml", 1]]), serverApplyEnabled: true }, 1, true],
            ["an artifact saved past the deployed version", { versions: new Map([["gateway.yaml", 2]]), serverApplyEnabled: true }, 1, false],
            ["no deployed version recorded under server-side apply", { versions: new Map([["gateway.yaml", 2]]), serverApplyEnabled: true }, undefined, false],
            ["an edit with server-side apply off", { versions: new Map([["gateway.yaml", 2]]), serverApplyEnabled: false }, 1, true],
        ])("pending edits: %s", (_label, edits, deployedVersion, expected) => {
            expect(isEntrypointOnlyBuildComplete([comp("gateway", "deployed", { deployed_version: deployedVersion })], edits)).toBe(expected);
        });

        test("isBuildCompleteCtaShowing follows it, so the plan card does not offer Build & Activate beside the card", () => {
            expect(isBuildCompleteCtaShowing([comp("gateway", "deployed")], noEdits)).toBe(true);
            expect(isBuildCompleteCtaShowing([comp("gateway", "validated")], noEdits)).toBe(false);
            const edited = { versions: new Map([["gateway.yaml", 2]]), serverApplyEnabled: true };
            expect(isBuildCompleteCtaShowing([comp("gateway", "deployed", { deployed_version: 1 })], edited)).toBe(false);
            expect(isBuildCompleteCtaShowing([comp("gateway", "deployed", { deployed_version: 2 })], edited)).toBe(true);
        });

        test("isBuildCompleteCtaShowing is true for any blocked session component and false for no components", () => {
            expect(isBuildCompleteCtaShowing([comp("gateway", "deployed"), comp("agent", "blocked")], noEdits)).toBe(true);
            expect(isBuildCompleteCtaShowing([comp("agent", "blocked", { origin: "platform" })], noEdits)).toBe(false);
            expect(isBuildCompleteCtaShowing(undefined, noEdits)).toBe(false);
        });
    });

    describe("isConnectorOnlyBuildComplete", () => {
        const comp = (type: string, status: string, extra: Record<string, unknown> = {}) => ({ origin: "session", type, status, filename: `${type}.yaml`, deployed_version: 0, ...extra });

        test("true when there are no testable components and every connector is built (validated) — the fix", () => {
            expect(isConnectorOnlyBuildComplete([comp("connector", "validated")])).toBe(true);
            expect(isConnectorOnlyBuildComplete([comp("connector", "validated"), comp("connector", "validated")])).toBe(true);
        });
        test("false when a connector is not yet built, or there are no connectors", () => {
            expect(isConnectorOnlyBuildComplete([comp("connector", "draft")])).toBe(false);
            expect(isConnectorOnlyBuildComplete([comp("gateway", "deployed")])).toBe(false);
            expect(isConnectorOnlyBuildComplete([])).toBe(false);
        });
        test("false when any testable component exists (that path is governed by isBuildReadyToTest)", () => {
            expect(isConnectorOnlyBuildComplete([comp("connector", "validated"), comp("agent", "deployed")])).toBe(false);
        });
        test("false for a built connector alongside an UNDEPLOYED gateway — the entrypoint must deploy first", () => {
            expect(isConnectorOnlyBuildComplete([comp("connector", "validated"), comp("gateway", "validated")])).toBe(false);
        });
        test("true for a built connector alongside a DEPLOYED gateway — entrypoint up (positive half of the gateway rule)", () => {
            expect(isConnectorOnlyBuildComplete([comp("connector", "validated"), comp("gateway", "deployed")])).toBe(true);
        });
        test("false when one connector in a multi-connector build is still a draft (every, not some)", () => {
            expect(isConnectorOnlyBuildComplete([comp("connector", "validated"), comp("connector", "draft")])).toBe(false);
        });
        test("false for a connector in error status", () => {
            expect(isConnectorOnlyBuildComplete([comp("connector", "error")])).toBe(false);
        });
        test("defers to the skill CTA for a mixed skill+connector build (no overlap)", () => {
            expect(isConnectorOnlyBuildComplete([comp("skill", "validated"), comp("connector", "validated")])).toBe(false);
        });
        test("ignores platform-origin connectors (pre-existing, not part of this build)", () => {
            expect(isConnectorOnlyBuildComplete([comp("connector", "validated"), { origin: "platform", type: "connector", status: "draft" }])).toBe(true);
        });
    });

    describe("isSaveOnlyBuild", () => {
        const comp = (type: string, extra: Record<string, unknown> = {}) => ({ origin: "session", type, ...extra });

        test("true for a connector-only build (no deployable component) — drives the save-only auto-save", () => {
            expect(isSaveOnlyBuild([comp("connector")])).toBe(true);
        });
        test("true for a skill-only build, and for connectors+skills together", () => {
            expect(isSaveOnlyBuild([comp("skill")])).toBe(true);
            expect(isSaveOnlyBuild([comp("connector"), comp("skill")])).toBe(true);
        });
        test("false when any deployable component (agent/workflow/gateway) is present", () => {
            expect(isSaveOnlyBuild([comp("connector"), comp("agent")])).toBe(false);
            expect(isSaveOnlyBuild([comp("connector"), comp("gateway")])).toBe(false);
            expect(isSaveOnlyBuild([comp("workflow")])).toBe(false);
        });
        test("false for an a2a_agent-only build — remote agents deploy, not save-only", () => {
            expect(isSaveOnlyBuild([comp("a2a_agent")])).toBe(false);
            expect(isSaveOnlyBuild([comp("connector"), comp("a2a_agent")])).toBe(false);
        });
        test("false when there are no session components (empty / platform-only)", () => {
            expect(isSaveOnlyBuild([])).toBe(false);
            expect(isSaveOnlyBuild(undefined)).toBe(false);
            expect(isSaveOnlyBuild([{ origin: "platform", type: "connector" }])).toBe(false);
        });
    });

    describe("shouldAutoSaveOnly", () => {
        const base = {
            saveOnlyBuild: true,
            readyToSave: true,
            agentResponded: true,
            allSaved: false,
            isSaving: false,
            isResponding: false,
            hasUnsavedEdits: false,
            allMatchLastSave: false,
            hasSkill: false,
            declined: false,
            saveOnlyTriggered: false,
            autoSaveTriggered: false,
        };
        test("fires when a save-only build is ready and the agent worked this mount", () => {
            expect(shouldAutoSaveOnly(base)).toBe(true);
        });
        test("does NOT fire on a cold revisit — agent didn't respond this mount (reload re-POST guard)", () => {
            expect(shouldAutoSaveOnly({ ...base, agentResponded: false })).toBe(false);
            expect(shouldAutoSaveOnly({ ...base, agentResponded: false, hasUnsavedEdits: true })).toBe(false);
        });
        test("does NOT fire for a non-save-only build, or before readyToSave", () => {
            expect(shouldAutoSaveOnly({ ...base, saveOnlyBuild: false })).toBe(false);
            expect(shouldAutoSaveOnly({ ...base, readyToSave: false })).toBe(false);
        });
        test("suppressed while saving / already triggered / manual autoSave running, with edits to save or not", () => {
            for (const hasUnsavedEdits of [false, true]) {
                expect(shouldAutoSaveOnly({ ...base, hasUnsavedEdits, isSaving: true })).toBe(false);
                expect(shouldAutoSaveOnly({ ...base, hasUnsavedEdits, saveOnlyTriggered: true })).toBe(false);
                expect(shouldAutoSaveOnly({ ...base, hasUnsavedEdits, autoSaveTriggered: true })).toBe(false);
            }
        });

        test.each([
            ["never saved this mount, nothing known of the platform copy", false, false, false, false, true],
            ["never saved this mount, mid-turn (the first save of a build lands at the turn's end)", false, false, false, true, true],
            ["never saved this mount, but every component is proven to be what the platform holds: an unrelated message writes nothing", false, false, true, true, false],
            ["never saved this mount, proven held, turn over", false, false, true, false, false],
            ["saved this mount, no edit since", true, false, true, false, false],
            ["saved this mount, no record of which version, no edit proven", true, false, false, false, false],
            ["saved this mount, edited since, turn over", true, true, false, false, true],
            ["saved this mount, edited since, mid-turn: the turn that made the edit is not over", true, true, false, true, false],
            ["not saved this mount, edited since a save an earlier mount recorded, turn over", false, true, false, false, true],
            ["not saved this mount, edited since, mid-turn", false, true, false, true, false],
        ])("%s", (_label, allSaved, hasUnsavedEdits, allMatchLastSave, isResponding, fires) => {
            expect(shouldAutoSaveOnly({ ...base, allSaved, hasUnsavedEdits, allMatchLastSave, isResponding })).toBe(fires);
        });

        test.each([
            ["an edit to save", { hasUnsavedEdits: true }],
            ["a skill in the build", { hasSkill: true, allSaved: true }],
            ["a build not yet saved this mount", { allSaved: false }],
        ])("a declined question is not asked again for %s", (_label, shape) => {
            expect(shouldAutoSaveOnly({ ...base, ...shape })).toBe(true);
            expect(shouldAutoSaveOnly({ ...base, ...shape, declined: true })).toBe(false);
        });

        test.each([
            ["saved this mount, nothing edited, turn over", true, false, false, true],
            ["saved this mount, proven unchanged, turn over", true, true, false, true],
            ["never saved this mount, turn over", false, false, false, true],
            ["never saved this mount, proven unchanged", false, true, false, true],
            ["saved this mount, mid-turn: wait for the turn to end", true, false, true, false],
            ["never saved this mount, mid-turn: the first save waits for the turn too", false, false, true, false],
        ])("a skill build, %s", (_label, allSaved, allMatchLastSave, isResponding, fires) => {
            expect(shouldAutoSaveOnly({ ...base, hasSkill: true, allSaved, allMatchLastSave, isResponding })).toBe(fires);
        });
    });

    describe("isBuildCompleteCtaShowing", () => {
        const comp = (type: string, status: string) => ({ origin: "session", type, status, filename: `${type}.yaml`, deployed_version: 0 });

        test("true when the whole build (incl. gateway) is deployed", () => {
            expect(isBuildCompleteCtaShowing([comp("workflow", "deployed"), comp("gateway", "deployed")], noEdits)).toBe(true);
        });
        test("true when a skill-only build is complete (skill alone)", () => {
            expect(isBuildCompleteCtaShowing([comp("skill", "validated")], noEdits)).toBe(true);
        });
        test("true when a connector-only build is complete (connector alone) — the fix", () => {
            expect(isBuildCompleteCtaShowing([comp("connector", "validated")], noEdits)).toBe(true);
        });
        test("false for a built skill + undeployed gateway (retry lives in the plan)", () => {
            expect(isBuildCompleteCtaShowing([comp("skill", "validated"), comp("gateway", "validated")], noEdits)).toBe(false);
        });
        test("false when a testable build has an undeployed/failed gateway (retry lives in the plan, not a Test CTA)", () => {
            expect(isBuildCompleteCtaShowing([comp("workflow", "deployed"), comp("gateway", "validated")], noEdits)).toBe(false);
        });
        test("false when neither a testable build is deployed nor a skill build is complete", () => {
            expect(isBuildCompleteCtaShowing([comp("agent", "validated"), comp("gateway", "validated")], noEdits)).toBe(false);
            expect(isBuildCompleteCtaShowing([comp("skill", "draft")], noEdits)).toBe(false);
        });
    });
});

describe("addressableComponentName", () => {
    const WF_YAML = `
apps:
  - name: MoodToPlaylistWorkflow
    app_config:
      name: mood_to_playlist_workflow
      agent_type: workflow
      nodes:
        - id: start
          type: agent
          agent_name: MoodClassifier
`;
    const AGENT_YAML = `
apps:
  - name: MoodClassifierApp
    app_config:
      agent_name: mood_classifier
      instruction: ${"x".repeat(120)}
      description: Classifies the mood of a message for downstream routing.
`;

    test("resolves a workflow target to its app_config.name (not the manifest name)", () => {
        expect(addressableComponentName("workflow", WF_YAML, "MoodToPlaylistWorkflow")).toBe("mood_to_playlist_workflow");
    });
    test("resolves an agent target to its app_config.agent_name", () => {
        expect(addressableComponentName("agent", AGENT_YAML, "MoodClassifierApp")).toBe("mood_classifier");
    });
    test("falls back to the manifest name when no config yaml is available", () => {
        expect(addressableComponentName("workflow", null, "MoodToPlaylistWorkflow")).toBe("MoodToPlaylistWorkflow");
        expect(addressableComponentName("workflow", undefined, "Fallback")).toBe("Fallback");
    });
    test("falls back to the manifest name for types without name divergence (skill/connector)", () => {
        expect(addressableComponentName("skill", WF_YAML, "SomeSkill")).toBe("SomeSkill");
        expect(addressableComponentName("connector", WF_YAML, "SomeConnector")).toBe("SomeConnector");
    });
    test("falls back to the manifest name when the yaml is unparseable", () => {
        expect(addressableComponentName("workflow", ":::not yaml:::", "MoodToPlaylistWorkflow")).toBe("MoodToPlaylistWorkflow");
    });
});

describe("extractAgentMetadata — built-in tool references", () => {
    const agentYaml = (toolsBlock: string) => `
apps:
  - name: T
    app_config:
      agent_name: T
      instruction: |
        ${"x".repeat(120)}
      agent_card:
        description: A description long enough to pass validation.
${toolsBlock}
`;

    test("collects builtin-group names and individual builtin tool names", () => {
        const meta = extractAgentMetadata(
            agentYaml(`      tools:
        - tool_type: builtin-group
          group_name: web_tools
        - tool_type: builtin
          tool_name: web_request`)
        );
        expect(meta?.builtinGroupNames).toEqual(["web_tools"]);
        expect(meta?.builtinToolNames).toEqual(["web_request"]);
    });

    test("ignores mcp and python tools, which are modelled as connectors", () => {
        const meta = extractAgentMetadata(
            agentYaml(`      tools:
        - tool_type: mcp
          connection_params: {url: "http://x"}
        - tool_type: python
          tool_name: my_fn
          component_module: mod`)
        );
        expect(meta?.builtinGroupNames).toBeUndefined();
        expect(meta?.builtinToolNames).toBeUndefined();
    });

    test("omits both fields when there is no tools block", () => {
        const meta = extractAgentMetadata(agentYaml("      input_modes: [text]"));
        expect(meta?.builtinGroupNames).toBeUndefined();
        expect(meta?.builtinToolNames).toBeUndefined();
    });

    test("deduplicates repeated references", () => {
        const meta = extractAgentMetadata(
            agentYaml(`      tools:
        - tool_type: builtin
          tool_name: web_request
        - tool_type: builtin
          tool_name: web_request
        - tool_type: builtin-group
          group_name: web_tools
        - tool_type: builtin-group
          group_name: web_tools`)
        );
        expect(meta?.builtinToolNames).toEqual(["web_request"]);
        expect(meta?.builtinGroupNames).toEqual(["web_tools"]);
    });

    test("still reads a hand-authored toolsets key", () => {
        const meta = extractAgentMetadata(
            agentYaml(`      toolsets:
        - builtin_web_request_tools`)
        );
        expect(meta?.toolsets).toEqual(["builtin_web_request_tools"]);
    });
});

describe("resolveToolsetBindings", () => {
    const lookup = {
        toolsetIdsByToolName: new Map([
            ["web_request", ["builtin_web_request_tools"]],
            ["web_search_google", ["builtin_research_tools", "builtin_web_request_tools"]],
            ["deep_research", ["builtin_research_tools"]],
            ["get_current_time", ["builtin_time_tools"]],
        ]),
        toolNamesByToolsetId: new Map([
            ["builtin_web_request_tools", ["web_request", "web_search_google"]],
            ["builtin_research_tools", ["web_search_google", "deep_research"]],
            ["builtin_time_tools", ["get_current_time"]],
        ]),
        toolsetIdByGroupName: new Map([
            ["builtin_web_request_tools", "builtin_web_request_tools"],
            ["web_tools", "builtin_web_request_tools"],
            ["builtin_research_tools", "builtin_research_tools"],
            ["research", "builtin_research_tools"],
            ["builtin_time_tools", "builtin_time_tools"],
        ]),
    };

    test("binds an individual tool's toolset and excludes its siblings", () => {
        expect(resolveToolsetBindings([], ["web_request"], lookup)).toEqual({
            toolsets: ["builtin_web_request_tools"],
            toolsetExcludedTools: { builtin_web_request_tools: ["web_search_google"] },
        });
    });

    test("emits no exclusions when every tool in the group is requested", () => {
        expect(resolveToolsetBindings([], ["web_request", "web_search_google"], lookup)).toEqual({
            toolsets: ["builtin_web_request_tools"],
        });
    });

    test("resolves an aliased group name to its platform id", () => {
        expect(resolveToolsetBindings(["web_tools"], [], lookup)).toEqual({ toolsets: ["builtin_web_request_tools"] });
    });

    test("passes an unknown group name through as authored", () => {
        expect(resolveToolsetBindings(["general_agent_tools"], [], lookup)).toEqual({ toolsets: ["general_agent_tools"] });
    });

    test("an ALIASED group reference still beats a narrowing exclusion", () => {
        expect(resolveToolsetBindings(["web_tools"], ["web_request"], lookup)).toEqual({
            toolsets: ["builtin_web_request_tools"],
        });
    });

    test("a group reference wins over a narrowing exclusion for the same toolset", () => {
        expect(resolveToolsetBindings(["builtin_web_request_tools"], ["web_request"], lookup)).toEqual({
            toolsets: ["builtin_web_request_tools"],
        });
    });

    test("names every unresolved tool in the thrown error", () => {
        expect(() => resolveToolsetBindings([], ["web_request", "not_a_real_tool"], lookup)).toThrow(/not_a_real_tool/);
    });

    test("an ALIASED group grant covers a tool owned by several toolsets", () => {
        expect(resolveToolsetBindings(["web_tools"], ["web_search_google"], lookup)).toEqual({
            toolsets: ["builtin_web_request_tools"],
        });
    });

    test("binds the highest-ranked owner when no group grant covers the tool", () => {
        expect(resolveToolsetBindings([], ["web_search_google"], lookup)).toEqual({
            toolsets: ["builtin_research_tools"],
            toolsetExcludedTools: { builtin_research_tools: ["deep_research"] },
        });
    });

    test("handles tools spanning several toolsets", () => {
        const result = resolveToolsetBindings([], ["web_request", "get_current_time"], lookup);
        expect(result.toolsets).toEqual(["builtin_time_tools", "builtin_web_request_tools"]);
        expect(result.toolsetExcludedTools).toEqual({ builtin_web_request_tools: ["web_search_google"] });
    });

    test("an exclusion never names a tool outside its own toolset", () => {
        const result = resolveToolsetBindings([], ["web_request"], lookup);
        const members = lookup.toolNamesByToolsetId.get("builtin_web_request_tools") ?? [];
        for (const name of result.toolsetExcludedTools?.builtin_web_request_tools ?? []) {
            expect(members).toContain(name);
        }
    });

    test("returns no toolsets for empty input", () => {
        expect(resolveToolsetBindings([], [], lookup)).toEqual({ toolsets: [] });
    });

    test("throws when the catalog is unavailable rather than under-granting", () => {
        const empty = { toolsetIdsByToolName: new Map(), toolNamesByToolsetId: new Map(), toolsetIdByGroupName: new Map() };
        expect(() => resolveToolsetBindings([], ["web_request"], empty)).toThrow(/catalog could not be loaded/);
    });

    test("throws for a tool no available toolset provides", () => {
        expect(() => resolveToolsetBindings([], ["schedule_task"], lookup)).toThrow(/schedule_task/);
    });

    test.each([
        ["a skill's tool", ["xlsx_read"]],
        ["a connector's runtime", ["execute_sql_query"]],
        ["a name nothing provides", ["no_such_tool"]],
        ["several at once", ["xlsx_read", "execute_sql_query", "docx_create_document"]],
    ])("the refusal for %s names the tools and the routes that work", (_label, tools) => {
        let message = "";
        try {
            resolveToolsetBindings([], tools, lookup);
        } catch (err) {
            message = (err as Error).message;
        }

        for (const name of tools) expect(message).toContain(`"${name}"`);
        expect(message).toContain("skills:");
        expect(message).toContain("connector");
        expect(message).toContain("tool_type: builtin-group");
    });

    test("an unavailable catalog is a 503 the auto-fix loop does not treat as a config error", () => {
        const empty = { toolsetIdsByToolName: new Map(), toolNamesByToolsetId: new Map(), toolsetIdByGroupName: new Map() };
        let thrown: unknown;
        try {
            resolveToolsetBindings([], ["web_request"], empty);
        } catch (err) {
            thrown = err;
        }

        expect((thrown as { status?: number }).status).toBe(503);
        expect(isAgentFixableError(thrown)).toBe(false);
    });

    test.each([
        ["a 403 from the toolsets endpoint", { status: 403, message: "Forbidden" }, 403, /Forbidden.*access to toolsets/],
        ["a 401 as an axios-style response", { response: { status: 401 }, message: "Unauthorized" }, 401, /Unauthorized.*access to toolsets/],
        ["a 500", { status: 500, message: "upstream broke" }, 503, /upstream broke.*Try again in a moment/],
        ["a 400 from a proxy", { status: 400, message: "bad request" }, 503, /bad request.*Try again in a moment/],
        ["a 429 from a proxy", { status: 429, message: "slow down" }, 503, /slow down.*Try again in a moment/],
        ["a missing route", { status: 404, message: "not found" }, 503, /not found.*Try again in a moment/],
        ["a network failure with no status", { message: "Network Error" }, 503, /Network Error.*Try again in a moment/],
    ])("the outage error for %s keeps its cause and is not sent to the builder", (_label, cause, status, message) => {
        const empty = { toolsetIdsByToolName: new Map(), toolNamesByToolsetId: new Map(), toolsetIdByGroupName: new Map(), loadError: Object.assign(new Error(cause.message), cause) };
        let thrown: unknown;
        try {
            resolveToolsetBindings([], ["web_request"], empty);
        } catch (err) {
            thrown = err;
        }

        expect((thrown as { status?: number }).status).toBe(status);
        expect((thrown as Error).message).toMatch(message);
        expect((thrown as Error).cause).toBe(empty.loadError);
        expect(isAgentFixableError(thrown)).toBe(false);
    });

    test("a catalog that failed to load part-way is an outage, not a missing tool", () => {
        const partial = {
            toolsetIdsByToolName: new Map([["web_request", ["web_tools"]]]),
            toolNamesByToolsetId: new Map([["web_tools", ["web_request"]]]),
            toolsetIdByGroupName: new Map(),
            loadError: new Error("page 2 failed"),
        };

        let thrown: unknown;
        try {
            resolveToolsetBindings([], ["web_request"], partial);
        } catch (err) {
            thrown = err;
        }

        expect((thrown as Error).message).toMatch(/page 2 failed/);
        expect((thrown as { status?: number }).status).toBe(503);
        expect(isAgentFixableError(thrown)).toBe(false);
    });

    test("a group-only config still resolves when the catalog is unavailable", () => {
        const empty = { toolsetIdsByToolName: new Map(), toolNamesByToolsetId: new Map(), toolsetIdByGroupName: new Map() };
        expect(resolveToolsetBindings(["web_tools"], [], empty)).toEqual({ toolsets: ["web_tools"] });
    });
});

describe("the builder's catalog lookups on /api/v2", () => {
    const v2Page = (data: unknown[]) =>
        new Response(JSON.stringify({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage: null, totalPages: 1 } } }), { status: 200, headers: { "content-type": "application/json" } });
    const fetchMock = vi.fn<typeof fetch>();
    const urls = () => fetchMock.mock.calls.map(([input]) => String(input));

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });
    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("the built-in toolset lookup reads ?origin=builtin and indexes it", async () => {
        fetchMock.mockResolvedValueOnce(
            v2Page([
                {
                    id: "builtin_time_tools",
                    name: "Time Tools",
                    origin: "builtin",
                    aliases: ["time"],
                    tools: [{ toolName: "get_current_time", description: null, timeoutSeconds: null, sandboxProfile: null, parameters: null, configSchema: null }],
                    agents: [],
                    bundle: null,
                    discoveryErrors: [],
                    createdTime: null,
                    updatedTime: null,
                },
            ])
        );

        const lookup = await fetchBuiltinToolsetLookup();

        expect(urls()).toEqual([expect.stringContaining("/api/v2/platform/toolsets?origin=builtin&")]);
        expect(lookup.toolsetIdsByToolName.get("get_current_time")).toEqual(["builtin_time_tools"]);
        expect(lookup.toolsetIdByGroupName.get("time")).toBe("builtin_time_tools");
    });

    test("the skill lookup reads custom and built-in skills by origin", async () => {
        const skillV2 = (id: string, name: string, origin: "custom" | "builtin") => ({
            id,
            type: "skill",
            origin,
            name,
            label: null,
            description: null,
            tags: [],
            discoveryStatus: "ready",
            hasReferences: false,
            hasAssets: false,
            hasTools: false,
            resourceCount: 0,
            referenceCount: 0,
            assetCount: 0,
            bundledTools: [],
            toolsets: [],
            bundle: null,
            agents: [],
            configSummary: null,
            createdBy: null,
            updatedBy: null,
            createdTime: null,
            updatedTime: null,
        });
        fetchMock.mockImplementation(async input => (String(input).includes("origin=custom") ? v2Page([skillV2("sk-1", "order-lookup", "custom")]) : v2Page([skillV2("sam-docs", "sam-docs", "builtin")])));

        const lookup = await fetchPlatformSkillLookup();

        expect(urls()).toHaveLength(2);
        expect(urls().some(u => u.includes("/api/v2/platform/skills?origin=custom&"))).toBe(true);
        expect(urls().some(u => u.includes("/api/v2/platform/skills?origin=builtin&"))).toBe(true);
        expect(lookup.customIdsByName.get("order-lookup")).toBe("sk-1");
        expect(lookup.builtinNames).toEqual(new Set(["sam-docs"]));
    });
});

describe("fetchBuiltinToolsetLookup", () => {
    afterEach(() => vi.restoreAllMocks());

    test("indexes the catalog in both directions", async () => {
        vi.spyOn(toolsetsApi, "getBuiltin").mockResolvedValue([
            { id: "builtin_web_request_tools", name: "Web Request Tools", tools: [{ toolName: "web_request" }, { toolName: "web_search_google" }] },
            { id: "builtin_time_tools", name: "Time Tools", tools: [{ toolName: "get_current_time" }] },
        ] as Toolset[]);

        const lookup = await fetchBuiltinToolsetLookup();

        expect(lookup.toolsetIdsByToolName.get("web_request")).toEqual(["builtin_web_request_tools"]);
        expect(lookup.toolNamesByToolsetId.get("builtin_web_request_tools")).toEqual(["web_request", "web_search_google"]);
        expect(lookup.toolNamesByToolsetId.get("builtin_time_tools")).toEqual(["get_current_time"]);
    });

    test("records every owner of a shared tool, smaller group ranked first", async () => {
        vi.spyOn(toolsetsApi, "getBuiltin").mockResolvedValue([
            { id: "builtin_web_request_tools", name: "Web Request Tools", tools: [{ toolName: "web_request" }, { toolName: "web_search_google" }] },
            { id: "builtin_research_tools", name: "Research Tools", tools: [{ toolName: "web_search_google" }] },
        ] as Toolset[]);

        const lookup = await fetchBuiltinToolsetLookup();

        expect(lookup.toolsetIdsByToolName.get("web_search_google")).toEqual(["builtin_research_tools", "builtin_web_request_tools"]);
        expect(lookup.toolsetIdsByToolName.get("web_request")).toEqual(["builtin_web_request_tools"]);
    });

    test("an equal-sized tie resolves by id, independent of catalog order", async () => {
        const catalog = [
            { id: "builtin_web_request_tools", name: "Web Request Tools", tools: [{ toolName: "shared" }] },
            { id: "builtin_research_tools", name: "Research Tools", tools: [{ toolName: "shared" }] },
        ];
        vi.spyOn(toolsetsApi, "getBuiltin").mockResolvedValue(catalog as Toolset[]);
        const forward = await fetchBuiltinToolsetLookup();

        vi.restoreAllMocks();
        vi.spyOn(toolsetsApi, "getBuiltin").mockResolvedValue([...catalog].reverse() as Toolset[]);
        const reversed = await fetchBuiltinToolsetLookup();

        expect(forward.toolsetIdsByToolName.get("shared")).toEqual(["builtin_research_tools", "builtin_web_request_tools"]);
        expect(reversed.toolsetIdsByToolName.get("shared")).toEqual(forward.toolsetIdsByToolName.get("shared"));
    });

    test("returns empty tables without throwing when the catalog fetch fails", async () => {
        vi.spyOn(toolsetsApi, "getBuiltin").mockRejectedValue(new Error("boom"));

        const lookup = await fetchBuiltinToolsetLookup();

        expect(lookup.toolsetIdsByToolName.size).toBe(0);
        expect(lookup.toolNamesByToolsetId.size).toBe(0);
        expect((lookup.loadError as Error).message).toBe("boom");
    });

    test("skips toolsets that advertise no tools", async () => {
        vi.spyOn(toolsetsApi, "getBuiltin").mockResolvedValue([{ id: "builtin_empty", name: "Empty", tools: [] }] as unknown as Toolset[]);

        const lookup = await fetchBuiltinToolsetLookup();

        expect(lookup.toolNamesByToolsetId.has("builtin_empty")).toBe(false);
    });
});

describe("Quick Build toolset regression", () => {
    test("a tools:-only agent config resolves to a non-empty toolsets list", () => {
        const meta = extractAgentMetadata(`
apps:
  - name: DocFetcher
    app_config:
      agent_name: DocFetcher
      instruction: |
        ${"x".repeat(120)}
      agent_card:
        description: Fetches and summarizes pages from docs.solace.com.
      tools:
        - tool_type: builtin
          tool_name: web_request
`);
        expect(meta?.toolsets).toBeUndefined();

        const bindings = resolveToolsetBindings(meta?.builtinGroupNames ?? [], meta?.builtinToolNames ?? [], {
            toolsetIdsByToolName: new Map([["web_request", ["builtin_web_request_tools"]]]),
            toolNamesByToolsetId: new Map([["builtin_web_request_tools", ["web_request", "web_search_google"]]]),
            toolsetIdByGroupName: new Map([["builtin_web_request_tools", "builtin_web_request_tools"]]),
        });

        expect(bindings.toolsets).toContain("builtin_web_request_tools");
    });
});

describe("fetchBuiltinToolsetLookup — group aliases", () => {
    afterEach(() => vi.restoreAllMocks());

    test("indexes each toolset by its id and every alias it advertises", async () => {
        vi.spyOn(toolsetsApi, "getBuiltin").mockResolvedValue([
            {
                id: "builtin_web_request_tools",
                name: "Web Request Tools",
                aliases: ["web_tools"],
                tools: [{ toolName: "web_request" }, { toolName: "web_search_google" }],
            },
        ] as Toolset[]);

        const lookup = await fetchBuiltinToolsetLookup();

        expect(lookup.toolsetIdByGroupName.get("web_tools")).toBe("builtin_web_request_tools");
        expect(lookup.toolsetIdByGroupName.get("builtin_web_request_tools")).toBe("builtin_web_request_tools");
    });

    test("a toolset with no tools is still indexed by name so its group grant resolves", async () => {
        vi.spyOn(toolsetsApi, "getBuiltin").mockResolvedValue([{ id: "builtin_empty", name: "Empty", aliases: ["empty_alias"], tools: [] }] as unknown as Toolset[]);

        const lookup = await fetchBuiltinToolsetLookup();

        expect(lookup.toolsetIdByGroupName.get("empty_alias")).toBe("builtin_empty");
        expect(lookup.toolNamesByToolsetId.has("builtin_empty")).toBe(false);
    });
});

describe("shouldShowCreationProgress", () => {
    const manifest = (...origins: string[]) => ({ name: "build", components: origins.map((origin, i) => ({ name: `c${i}`, filename: `c${i}.yaml`, type: "agent", origin })) });

    test("narrates a build that creates something here", () => {
        expect(shouldShowCreationProgress(true, manifest("session"))).toBe(true);
        expect(shouldShowCreationProgress(true, manifest("platform", "session"))).toBe(true);
    });

    test("stays silent for a build that only reuses platform components", () => {
        expect(shouldShowCreationProgress(true, manifest("platform"))).toBe(false);
    });

    test("stays silent before the plan is approved, and with nothing to show", () => {
        expect(shouldShowCreationProgress(false, manifest("session"))).toBe(false);
        expect(shouldShowCreationProgress(true, null)).toBe(false);
        expect(shouldShowCreationProgress(true, { name: "build", components: [] })).toBe(false);
        expect(shouldShowCreationProgress(true, { name: "build" })).toBe(false);
    });
});

describe("resolveEffectiveManifest", () => {
    const live = { name: "build", components: [{ name: "live", filename: "live.yaml", type: "agent" }] };
    const snapshot = { name: "build", components: [{ name: "snapshot", filename: "snapshot.yaml", type: "agent" }] };

    test("prefers the live manifest whenever there is one", () => {
        expect(resolveEffectiveManifest(live, true, snapshot)).toBe(live);
        expect(resolveEffectiveManifest(live, false, snapshot)).toBe(live);
    });

    test("falls back to the approved snapshot through a re-fetch", () => {
        expect(resolveEffectiveManifest(null, true, snapshot)).toBe(snapshot);
    });

    test("has nothing to fall back to before approval", () => {
        expect(resolveEffectiveManifest(null, false, snapshot)).toBeNull();
        expect(resolveEffectiveManifest(null, true, null)).toBeNull();
    });
});

describe("isInlineProgressActive", () => {
    const base = { isResponding: true, showCreationProgress: true, isApplyingChanges: false, currentTurnHasActivity: true, inBuildTab: true };

    test("never pulses outside a turn", () => {
        expect(isInlineProgressActive({ ...base, isResponding: false })).toBe(false);
        expect(isInlineProgressActive({ ...base, isResponding: false, isApplyingChanges: true })).toBe(false);
    });

    test("an apply turn tracks the build tab, and nothing else", () => {
        expect(isInlineProgressActive({ ...base, isApplyingChanges: true, showCreationProgress: false, currentTurnHasActivity: false })).toBe(true);
        expect(isInlineProgressActive({ ...base, isApplyingChanges: true, inBuildTab: false })).toBe(false);
    });

    test("a creation turn needs the timeline and activity together", () => {
        expect(isInlineProgressActive(base)).toBe(true);
        expect(isInlineProgressActive({ ...base, currentTurnHasActivity: false })).toBe(false);
        expect(isInlineProgressActive({ ...base, showCreationProgress: false })).toBe(false);
    });

    test("a creation turn does not consult the build tab", () => {
        expect(isInlineProgressActive({ ...base, inBuildTab: false })).toBe(true);
    });
});
