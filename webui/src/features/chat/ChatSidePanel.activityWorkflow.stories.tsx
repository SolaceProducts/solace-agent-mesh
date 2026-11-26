// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import type { MessageFE } from "@/types/chat";

import { ChatSidePanel } from "@/features/chat/ChatSidePanel";

import { mockMessages } from "@/testing/data";
import {
    artifactCreationMonitoredTasks,
    inProgressMonitoredTasks,
    mixedParallelMonitoredTasks,
    parallelPeerMonitoredTasks,
    parallelToolCallMonitoredTasks,
    peerDelegationMonitoredTasks,
    simpleToolCallMonitoredTasks,
    taskFailedMonitoredTasks,
    workflowMapMonitoredTasks,
    workflowMonitoredTasks,
} from "@/testing/data/a2aEventSSEPayloads";

const findCommonAncestor = (a: Element, b: Element, boundary: Element): Element | null => {
    let current: Element | null = a;
    while (current && current !== boundary) {
        if (current.contains(b)) return current;
        current = current.parentElement;
    }
    return null;
};

const meta = {
    title: "Chat/ChatSidePanel",
    component: ChatSidePanel,
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        configContext: {
            persistenceEnabled: false,
        },
    },
    args: {
        isSidePanelCollapsed: false,
        onCollapsedToggle: () => {},
        setIsSidePanelCollapsed: () => {},
    },
    decorators: [
        Story => (
            <div className="ml-auto h-screen w-2/5 border-l">
                <Story />
            </div>
        ),
    ],
    tags: ["autodocs"],
} satisfies Meta<typeof ChatSidePanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SimpleToolCall: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: simpleToolCallMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("What is the weather in San Francisco?")).toBeInTheDocument();
        expect(await canvas.findByText("Completed")).toBeInTheDocument();

        expect(canvas.getByRole("tab", { name: /Activity/i, selected: true })).toBeInTheDocument();
        expect(await canvas.findByTestId("collapsePanel")).toBeInTheDocument();
    },
};

export const PeerDelegation: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: peerDelegationMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Validate order #5678 and check inventory")).toBeInTheDocument();
        expect(await canvas.findByText("Completed")).toBeInTheDocument();

        expect(await canvas.findByText("ValidatorAgent")).toBeInTheDocument();
    },
};

export const TaskFailed: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: taskFailedMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Connect to the production database and fetch all user records")).toBeInTheDocument();
        expect(await canvas.findByText("Failed")).toBeInTheDocument();

        expect(await canvas.findByTestId("tool-node-query_database")).toBeInTheDocument();
    },
};

export const WorkflowExecution: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: workflowMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Run the data processing pipeline on Q4 sales data")).toBeInTheDocument();
        expect(await canvas.findByText("Completed")).toBeInTheDocument();

        expect(await canvas.findByText("Data Processing Pipeline")).toBeInTheDocument();
    },
};

const inProgressLoadingMessage: MessageFE = {
    isUser: false,
    parts: [{ kind: "text", text: "Analyzing sentiment data..." }],
    isStatusBubble: true,
    isComplete: false,
    taskId: "task-1",
    metadata: { sessionId: "mock-session-id", lastProcessedEventSequence: 5 },
};

export const InProgress: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            currentTaskId: "task-1",
            messages: [...mockMessages, inProgressLoadingMessage],
            isResponding: true,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: inProgressMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Analyze the customer feedback data and generate a sentiment report")).toBeInTheDocument();

        expect(await canvas.findByText("Analyzing sentiment data...")).toBeInTheDocument();

        expect(canvas.queryByText("Completed")).not.toBeInTheDocument();
        expect(canvas.queryByText("Failed")).not.toBeInTheDocument();

        expect(await canvas.findByTestId("tool-node-analyze_sentiment")).toBeInTheDocument();
    },
};

export const ArtifactCreation: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: artifactCreationMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Generate a sales report PDF for Q4")).toBeInTheDocument();
        expect(await canvas.findByText("Completed")).toBeInTheDocument();

        expect(await canvas.findByTestId("tool-node-generate_report")).toBeInTheDocument();

        expect(await canvas.findByText("1 artifact")).toBeInTheDocument();
    },
};

export const ParallelToolCalls: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: parallelToolCallMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Get the weather in New York and London simultaneously")).toBeInTheDocument();
        expect(await canvas.findByText("Completed")).toBeInTheDocument();

        const toolNodes = canvasElement.querySelectorAll('[data-testid="tool-node-get_weather"]');
        expect(toolNodes.length).toBe(2);

        const commonAncestor = findCommonAncestor(toolNodes[0], toolNodes[1], canvasElement);
        expect(commonAncestor).not.toBeNull();
        expect(window.getComputedStyle(commonAncestor!).flexDirection).toBe("row");
    },
};

export const ParallelPeerDelegation: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: parallelPeerMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Check order #9999 validity and fetch shipping estimate")).toBeInTheDocument();
        expect(await canvas.findByText("Completed")).toBeInTheDocument();

        const validatorNode = await canvas.findByText("ValidatorAgent");
        expect(validatorNode).toBeInTheDocument();
        const shippingNode = await canvas.findByText("ShippingAgent");
        expect(shippingNode).toBeInTheDocument();

        const commonAncestor = findCommonAncestor(validatorNode, shippingNode, canvasElement);
        expect(commonAncestor).not.toBeNull();
        expect(window.getComputedStyle(commonAncestor!).flexDirection).toBe("row");
    },
};

export const MixedParallelPeerDelegation: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: mixedParallelMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Analyze Q1 sales data and produce a report with charts")).toBeInTheDocument();
        expect(await canvas.findByText("Completed")).toBeInTheDocument();

        expect(await canvas.findByText("AnalysisAgent")).toBeInTheDocument();
        expect(await canvas.findByText("ReportAgent")).toBeInTheDocument();

        const fetchDataNode = await canvas.findByTestId("tool-node-fetch_data");
        expect(fetchDataNode).toBeInTheDocument();
        const analyzeDataNode = await canvas.findByTestId("tool-node-analyze_data");
        expect(analyzeDataNode).toBeInTheDocument();

        const seqAncestor = findCommonAncestor(fetchDataNode, analyzeDataNode, canvasElement);
        expect(seqAncestor).not.toBeNull();
        expect(window.getComputedStyle(seqAncestor!).flexDirection).toBe("column");

        const chartNode = await canvas.findByTestId("tool-node-generate_chart");
        expect(chartNode).toBeInTheDocument();
        const summaryNode = await canvas.findByTestId("tool-node-generate_summary");
        expect(summaryNode).toBeInTheDocument();

        const parAncestor = findCommonAncestor(chartNode, summaryNode, canvasElement);
        expect(parAncestor).not.toBeNull();
        expect(window.getComputedStyle(parAncestor!).flexDirection).toBe("row");
    },
};

export const WorkflowMapProgress: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: "task-1",
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: workflowMapMonitoredTasks,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Process all customer files through the review pipeline")).toBeInTheDocument();
        expect(await canvas.findByText("Completed")).toBeInTheDocument();

        expect(await canvas.findByText("Customer Review Pipeline")).toBeInTheDocument();
    },
};

export const NoTaskSelected: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: mockMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            activeSidePanelTab: "activity",
            isSidePanelCollapsed: false,
            taskIdInSidePanel: null,
            artifacts: [],
            artifactsLoading: false,
        },
        taskContext: {
            monitoredTasks: {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("No task selected to display")).toBeInTheDocument();
    },
};
