// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import WorkflowRenderer from "@/features/activities/flowChart/WorkflowRenderer";

import type { VisualizerStep } from "@/types";

const meta = {
    title: "Activities/ActivitiesVisualization/WorkflowRenderer",
    component: WorkflowRenderer,
    decorators: [
        Story => (
            <div style={{ width: 800, height: 600, background: "var(--background-w10)" }}>
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof WorkflowRenderer>;

export default meta;
type Story = StoryObj<typeof meta>;

const baseStep = { rawEventIds: [] as string[], nestingLevel: 0, owningTaskId: "task-1" };

const userRequestStep: VisualizerStep = {
    id: "s1",
    type: "USER_REQUEST",
    timestamp: "2024-01-01T00:00:00.000Z",
    title: "User Request",
    source: "User",
    target: "Orchestrator",
    data: { text: "do something" },
    ...baseStep,
};

const llmCallStep: VisualizerStep = {
    id: "s2",
    type: "AGENT_LLM_CALL",
    timestamp: "2024-01-01T00:00:00.100Z",
    title: "Orchestrator: LLM Call",
    source: "Orchestrator",
    target: "LLM",
    data: { llmCall: { modelName: "gpt-4", promptPreview: "p" } },
    ...baseStep,
};

const agentResponseStep: VisualizerStep = {
    id: "s3",
    type: "AGENT_RESPONSE_TEXT",
    timestamp: "2024-01-01T00:00:00.200Z",
    title: "Orchestrator: Response",
    source: "Orchestrator",
    data: { text: "done" },
    ...baseStep,
};

const simpleFlow = [userRequestStep, llmCallStep, agentResponseStep];

export const EmptyStepsShowsEmptyMessage: Story = {
    args: { processedSteps: [], agentNameMap: {} },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText(/no events to display/i)).toBeInTheDocument();
    },
};

export const ProcessingMessageWhenStepsButNoNodes: Story = {
    args: {
        processedSteps: [
            {
                id: "unknown",
                type: "TASK_COMPLETED",
                timestamp: "2024-01-01T00:00:00Z",
                title: "Done",
                data: {},
                ...baseStep,
            } as VisualizerStep,
        ],
        agentNameMap: {},
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.getByText(/processing flow data/i)).toBeInTheDocument();
    },
};

export const RendersUserAndAgentNodes: Story = {
    args: { processedSteps: simpleFlow, agentNameMap: { Orchestrator: "Orchestrator" } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const userNodes = await canvas.findAllByText("User");
        expect(userNodes.length).toBe(2);
        expect(canvas.getByText("Orchestrator")).toBeInTheDocument();
    },
};

export const ConnectorBetweenSiblingNodes: Story = {
    args: { processedSteps: simpleFlow, agentNameMap: { Orchestrator: "Orchestrator" } },
    play: async ({ canvasElement }) => {
        const connectors = canvasElement.querySelectorAll("[data-connector='sibling']");
        expect(connectors.length).toBe(2);
    },
};

export const NodeClickFiresHandler: Story = {
    args: {
        processedSteps: simpleFlow,
        agentNameMap: { Orchestrator: "Orchestrator" },
        onNodeClick: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        await userEvent.click(await canvas.findByText("Orchestrator"));
        expect(args.onNodeClick).toHaveBeenCalled();
        const [arg] = (args.onNodeClick as ReturnType<typeof fn>).mock.calls[0];
        expect(arg.type).toBe("agent");
    },
};

export const UnknownAgentCallbackInvoked: Story = {
    args: {
        processedSteps: simpleFlow,
        agentNameMap: {},
        onUnknownAgent: fn(),
    },
    play: async ({ args }) => {
        expect(args.onUnknownAgent).toHaveBeenCalledWith("Orchestrator");
    },
};
