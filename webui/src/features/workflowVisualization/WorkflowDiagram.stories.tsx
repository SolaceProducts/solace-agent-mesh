// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";

import type { WorkflowConfig } from "@/utils/agentUtils";

import WorkflowDiagram from "./WorkflowDiagram";

const nodeTypeShowcaseConfig: WorkflowConfig = {
    description: "Demonstrates the agent, switch, map, loop, and nested workflow node types.",
    nodes: [
        {
            id: "classify",
            type: "agent",
            agent_name: "TextClassifierAgent",
            input: { text: "{{workflow.input.text}}" },
        },
        {
            id: "route",
            type: "switch",
            depends_on: ["classify"],
            cases: [
                { condition: "'{{classify.output.classification}}' == 'short'", node: "format_items" },
                { condition: "'{{classify.output.classification}}' == 'long'", node: "validate_loop" },
            ],
            default: "format_items",
        },
        {
            id: "format_items",
            type: "map",
            depends_on: ["route"],
            items: "{{workflow.input.items}}",
            node: "format_one",
        },
        {
            id: "format_one",
            type: "agent",
            agent_name: "ItemFormatterAgent",
            input: { item: "{{_map_item}}", index: "{{_map_index}}" },
        },
        {
            id: "validate_loop",
            type: "loop",
            depends_on: ["route"],
            condition: "{{validate_step.output.valid}} == true",
            max_iterations: 5,
            delay: "1s",
            node: "validate_step",
        },
        {
            id: "validate_step",
            type: "agent",
            agent_name: "ValidatorAgent",
            input: { value: "{{workflow.input.value}}", attempts: "{{validate_step.output.attempts}}" },
        },
        {
            id: "transform",
            type: "workflow",
            workflow_name: "StringTransformWorkflow",
            depends_on: ["format_items", "validate_loop"],
            input: { value: { coalesce: ["{{validate_step.output.value}}", "{{workflow.input.value}}"] } },
        },
    ],
};

const meta = {
    title: "Workflow/WorkflowDiagram",
    component: WorkflowDiagram,
    decorators: [
        Story => (
            <div style={{ width: 720, height: 960, background: "var(--background-w10)" }}>
                <Story />
            </div>
        ),
    ],
    parameters: {
        docs: {
            description: {
                component: "The full ReactFlow workflow diagram. This story feeds it a config that touches five of the six node types — agent, switch, map, loop, and nested workflow (not tool) — plus the auto-generated Start/End nodes.",
            },
        },
    },
} satisfies Meta<typeof WorkflowDiagram>;

export default meta;
type Story = StoryObj<typeof meta>;

export const AllNodeTypes: Story = {
    args: {
        config: nodeTypeShowcaseConfig,
        knownWorkflows: new Set(["StringTransformWorkflow"]),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Start")).toBeInTheDocument();
        expect(await canvas.findByText("Switch")).toBeInTheDocument();
        expect(await canvas.findByText("Map")).toBeInTheDocument();
        expect(await canvas.findByText("Loop")).toBeInTheDocument();
        expect(await canvas.findByText("End")).toBeInTheDocument();
    },
};

const decisionWorkflowConfig: WorkflowConfig = {
    description: "Routes a support ticket on a decision model's answer, escalating low-confidence calls to a human.",
    nodes: [
        {
            id: "triage_ticket",
            type: "decision",
            questions: [
                { key: "category", type: "choice", instructions: "Which team owns this ticket?", criteria: ["billing", "technical"] },
                { key: "urgent", type: "boolean", instructions: "Does the customer need an answer today?" },
            ],
            route_on: "category",
            routes: { billing: "billing_desk", technical: "tech_desk" },
            default: "general_desk",
            escalate: "human_review",
            escalation_mode: "handoff",
            min_confidence: 0.8,
        },
        { id: "billing_desk", type: "agent", agent_name: "BillingAgent" },
        { id: "tech_desk", type: "agent", agent_name: "TechSupportAgent" },
        { id: "general_desk", type: "agent", agent_name: "GeneralSupportAgent" },
        { id: "human_review", type: "agent", agent_name: "HumanReviewAgent" },
    ],
};

export const DecisionWorkflow: Story = {
    args: {
        config: decisionWorkflowConfig,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const card = await canvas.findByTestId("decision-node");
        expect(within(card).getByText("Decision")).toBeInTheDocument();
        expect(within(card).getByRole("img", { name: "Routes on this question" })).toBeInTheDocument();
        expect(card).not.toHaveTextContent("escalates below");
        expect(await canvas.findByTitle("billing")).toBeInTheDocument();
        expect(canvas.getByTitle("technical")).toBeInTheDocument();
        expect(canvas.getByTitle("default")).toBeInTheDocument();
        expect(within(canvas.getByTitle("escalate < 80% confidence")).getByText("escalate < 80% confidence")).toHaveClass("text-(--warning-wMain)");
        const dashed = [...canvasElement.querySelectorAll<SVGPathElement>(".react-flow__edge path")].filter(p => getComputedStyle(p).strokeDasharray !== "none");
        expect(dashed).toHaveLength(0);
        expect(canvas.queryByText("answer")).toBeNull();
    },
};

export const DecisionWorkflowReroutingEscalation: Story = {
    args: {
        config: {
            description: "Routes a support ticket; a low-confidence answer goes to a classifier whose answer still picks the desk.",
            nodes: [
                {
                    id: "triage",
                    type: "decision",
                    questions: [
                        { key: "department", type: "choice", instructions: "Which team should own `ticket_text`?", criteria: ["billing", "technical", "general"] },
                        { key: "urgent", type: "boolean", instructions: "`ticket_text` needs immediate attention" },
                    ],
                    route_on: "department",
                    routes: { billing: "billing", technical: "technical", general: "general" },
                    escalate: "classify",
                    min_confidence: 0.8,
                },
                { id: "classify", type: "agent", agent_name: "ClassifierAgent", depends_on: ["triage"] },
                { id: "billing", type: "agent", agent_name: "BillingAgent", depends_on: ["triage"] },
                { id: "technical", type: "agent", agent_name: "TechnicalAgent", depends_on: ["triage"] },
                { id: "general", type: "agent", agent_name: "GeneralAgent", depends_on: ["triage"] },
            ],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByTitle("escalate < 80% confidence")).toBeInTheDocument();
        await waitFor(() => {
            const dashed = [...canvasElement.querySelectorAll<SVGPathElement>(".react-flow__edge path")].filter(p => getComputedStyle(p).strokeDasharray !== "none");
            expect(dashed).toHaveLength(1);
        });
        expect(canvas.getByText("answer")).toBeInTheDocument();
    },
};
