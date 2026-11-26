// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import type { WorkflowConfig, WorkflowNodeConfig } from "@/utils/agentUtils";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";
import type { AgentCardInfo } from "@/types";

import WorkflowNodeDetailPanel from "./WorkflowNodeDetailPanel";

const meta: Meta<typeof WorkflowNodeDetailPanel> = {
    title: "Workflow/WorkflowVisualization/WorkflowNodeDetailPanel",
    component: WorkflowNodeDetailPanel,
    tags: ["autodocs"],
    decorators: [
        Story => (
            <div className="ml-auto h-screen w-2/5 border-l">
                <Story />
            </div>
        ),
    ],
};

export default meta;
type Story = StoryObj<typeof meta>;

const mockValidateOrderAgent: AgentCardInfo = {
    name: "validate_order",
    displayName: "OrderValidator",
    description: "Validates order data and ensures all required fields are present",
    version: "1.0.0",
    capabilities: {},
    defaultInputModes: [],
    defaultOutputModes: [],
    protocolVersion: "1.0",
    provider: { organization: "solace", url: "" },
    url: "",
    skills: [],
};

const loopNode = createWorkflowLayoutNode({
    id: "polling_loop",
    type: "loop",
    width: 200,
    height: 60,
    data: {
        label: "Polling Loop",
        condition: "{{check_status.output.ready}} == false",
        maxIterations: 10,
        delay: "5s",
    },
});

const agentNode = createWorkflowLayoutNode({
    id: "validate_order",
    type: "agent",
    data: {
        agentName: "validate_order",
        label: "OrderValidator",
        originalConfig: {
            id: "validate_order",
            type: "agent",
            agent_name: "validate_order",
            input: {
                order_data: "{{workflow.input.order}}",
                customer_id: "{{workflow.input.customer_id}}",
            },
            input_schema_override: {
                type: "object",
                properties: {
                    order_data: { type: "object", description: "Order details to validate" },
                    customer_id: { type: "string", description: "Customer identifier" },
                },
                required: ["order_data"],
            },
            output_schema_override: {
                type: "object",
                properties: {
                    is_valid: { type: "boolean", description: "Whether order is valid" },
                    validation_errors: { type: "array", items: { type: "string" }, description: "List of validation errors" },
                },
            },
        },
    },
});

const toolNode = createWorkflowLayoutNode({
    id: "build_deck",
    type: "tool",
    data: {
        toolName: "create_pptx",
        label: "build_deck",
        originalConfig: {
            id: "build_deck",
            type: "tool",
            tool_name: "create_pptx",
            input_schema: {
                type: "object",
                properties: {
                    title: { type: "string", description: "Deck title" },
                    slides: { type: "array", items: { type: "string" }, description: "Slide contents" },
                },
                required: ["title"],
            },
            output_schema: {
                type: "object",
                properties: {
                    artifact_uri: { type: "string", description: "URI of the generated deck" },
                },
            },
        },
    },
});

const connectorToolNode = createWorkflowLayoutNode({
    id: "fetch_pets",
    type: "tool",
    data: {
        toolName: "find_pets_by_status",
        connectorName: "PetStoreAPI",
        label: "fetch_pets",
        originalConfig: { id: "fetch_pets", type: "tool", connector: "PetStoreAPI", tool: "find_pets_by_status" },
    },
});

const connectorOnlyToolNode = createWorkflowLayoutNode({
    id: "send_alert",
    type: "tool",
    data: {
        connectorName: "SlackConn",
        label: "send_alert",
        originalConfig: { id: "send_alert", type: "tool", connector: "SlackConn" },
    },
});

const toolsetToolNode = createWorkflowLayoutNode({
    id: "get_weather_tool",
    type: "tool",
    data: {
        toolName: "WeatherToolset__get_current_weather",
        label: "get_weather_tool",
        originalConfig: {
            id: "get_weather_tool",
            type: "tool",
            tool_name: "WeatherToolset__get_current_weather",
            input_schema: {
                type: "object",
                properties: {
                    city: { type: "string", description: "City to look up weather for" },
                },
                required: ["city"],
            },
            output_schema: {
                type: "object",
                properties: {
                    temperature: { type: "number", description: "Current temperature" },
                },
            },
        },
    },
});

const mapNode = createWorkflowLayoutNode({
    id: "process_items",
    type: "map",
    width: 200,
    height: 60,
    data: { label: "Process Items", items: "{{workflow.input.order_items}}" },
});

const switchNode = createWorkflowLayoutNode({
    id: "check_priority",
    type: "switch",
    width: 200,
    height: 60,
    data: {
        label: "Check Priority",
        cases: [
            { condition: "{{priority}} == 'high'", node: "fast_path" },
            { condition: "{{priority}} == 'low'", node: "slow_path" },
        ],
    },
});

export const LoopNode: Story = {
    args: {
        node: loopNode,
        workflowConfig: null,
        agents: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
        expect(canvas.getAllByText("polling_loop").length).toBeGreaterThan(0);
        expect(canvas.getByText("Max Iterations")).toBeInTheDocument();
    },
};

export const AgentNode: Story = {
    args: {
        node: agentNode,
        workflowConfig: null,
        agents: [mockValidateOrderAgent],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
        expect(canvas.getAllByText("OrderValidator").length).toBeGreaterThan(0);
        expect(canvas.getByRole("tab", { name: "Input" })).toBeInTheDocument();
        expect(canvas.getByRole("tab", { name: "Output" })).toBeInTheDocument();
    },
};

export const ToolNode: Story = {
    args: {
        node: toolNode,
        workflowConfig: null,
        agents: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
        expect(canvas.getByText("Tool", { selector: "span" })).toBeInTheDocument();
        expect(canvas.queryByText("Toolset", { selector: "span" })).not.toBeInTheDocument();
        expect(canvas.getAllByText("create_pptx").length).toBeGreaterThan(0);
        expect(canvas.getByRole("tab", { name: "Input" })).toBeInTheDocument();
        expect(canvas.getByRole("tab", { name: "Output" })).toBeInTheDocument();
    },
};

export const ToolNodeWithConnector: Story = {
    args: {
        node: connectorToolNode,
        workflowConfig: null,
        agents: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
        expect(canvas.getByText("Connector", { selector: "span" })).toBeInTheDocument();
        expect(canvas.getByText("PetStoreAPI", { selector: "code" })).toBeInTheDocument();
        expect(canvas.getByText("find_pets_by_status", { selector: "code" })).toBeInTheDocument();
    },
};

export const ToolNodeWithConnectorOnly: Story = {
    args: {
        node: connectorOnlyToolNode,
        workflowConfig: null,
        agents: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
        expect(canvas.getByText("Connector", { selector: "span" })).toBeInTheDocument();
        expect(canvas.getByText("SlackConn", { selector: "code" })).toBeInTheDocument();
        expect(canvas.queryByText("Tool", { selector: "span" })).not.toBeInTheDocument();
    },
};

export const ToolNodeWithToolset: Story = {
    args: {
        node: toolsetToolNode,
        workflowConfig: null,
        agents: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
        expect(canvas.getByText("Toolset", { selector: "span" })).toBeInTheDocument();
        expect(canvas.getByText("Tool", { selector: "span" })).toBeInTheDocument();
        expect(canvas.getByText("WeatherToolset", { selector: "code" })).toBeInTheDocument();
        expect(canvas.getByText("get_current_weather", { selector: "code" })).toBeInTheDocument();
    },
};

export const MapNode: Story = {
    args: {
        node: mapNode,
        workflowConfig: null,
        agents: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
        expect(canvas.getAllByText("process_items").length).toBeGreaterThan(0);
        expect(canvas.getByText("Items")).toBeInTheDocument();
    },
};

export const SwitchNode: Story = {
    args: {
        node: switchNode,
        workflowConfig: null,
        agents: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
        expect(canvas.getAllByText("check_priority").length).toBeGreaterThan(0);
        expect(canvas.getByText("Cases")).toBeInTheDocument();
    },
};

const decisionConfig: WorkflowNodeConfig = {
    id: "triage_ticket",
    type: "decision",
    depends_on: ["validate_order"],
    input: { ticket_text: "{{workflow.input.ticket_text}}" },
    questions: [
        { key: "category", type: "choice", instructions: "Which team owns this ticket?", criteria: ["billing", "technical"] },
        { key: "urgent", type: "boolean", instructions: "Does the customer need an answer today?" },
    ],
    route_on: "category",
    routes: { billing: "billing_desk", technical: "tech_desk" },
    default: "general_desk",
    escalate: "human_review",
    min_confidence: 0.8,
    on_provider_error: "escalate",
};

const decisionWorkflowConfig: WorkflowConfig = {
    nodes: [
        { id: "validate_order", type: "agent", agent_name: "validate_order" },
        decisionConfig,
        { id: "billing_desk", type: "agent", agent_name: "validate_order" },
        { id: "tech_desk", type: "agent", agent_name: "TechDeskAgent" },
        { id: "general_desk", type: "workflow", workflow_name: "GeneralDeskWorkflow" },
        { id: "human_review", type: "agent", agent_name: "HumanReviewAgent" },
    ],
};

const decisionNode = createWorkflowLayoutNode({
    id: "triage_ticket",
    type: "decision",
    data: { label: "triage_ticket", originalConfig: decisionConfig },
});

export const DecisionNode: Story = {
    args: {
        node: decisionNode,
        workflowConfig: decisionWorkflowConfig,
        agents: [mockValidateOrderAgent],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByRole("complementary", { name: "Node details panel" })).toBeInTheDocument();
        expect(canvas.getByTitle("Decision")).toBeInTheDocument();
        expect(canvas.getByText("Node ID")).toBeInTheDocument();
        const details = canvas.getByTestId("decisionNodeDetails");
        expect(details).toHaveTextContent("ticket_text: {{workflow.input.ticket_text}}");
        expect(within(details).getAllByText("ticket_text")).toHaveLength(1);
        expect(within(details).getByText("Which team owns this ticket?")).toBeInTheDocument();
        expect(within(details).getByLabelText("category options")).toHaveTextContent("billingtechnical");
        expect(within(details).getByText("Boolean")).toBeInTheDocument();
        expect(within(details).getAllByRole("button", { name: "Routes on this question" })).toHaveLength(1);
        expect(within(details).queryByText("Routes On")).not.toBeInTheDocument();

        expect(within(details).queryByRole("table", { name: "Decision routes" })).not.toBeInTheDocument();

        expect(within(details).queryByText("Default")).not.toBeInTheDocument();
        expect(within(details).getByText("Escalation Threshold")).toBeInTheDocument();
        expect(details).toHaveTextContent("< 80% confidence");
        expect(details).not.toHaveTextContent("HumanReviewAgent");
        expect(within(details).queryByText("Escalation Choice")).not.toBeInTheDocument();
        expect(within(details).getByText("On Provider Error")).toBeInTheDocument();
    },
};
