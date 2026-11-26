// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { ChatPage } from "@/features/chat/ChatPage";

import { mockLoadingMessage, mockMessages } from "@/testing/data";
import { defaultPromptGroups } from "@/testing/data/prompts";
import { withDefaults, withSessionArtifacts } from "@/testing/helpers";
import type { ArtifactInfo, MessageFE } from "@/types";

const SESSION_ID = "mock-session-id";
const mockArtifacts: ArtifactInfo[] = [{ filename: "test.md", size: 1024, mimeType: "text/markdown", lastModified: new Date().toISOString() }];

const handlers = withDefaults(
    http.get("*/api/v1/prompts/groups/all", () => {
        return HttpResponse.json(defaultPromptGroups);
    }),
    http.get("*/api/v1/sessions/:sessionId/contextUsage", () =>
        HttpResponse.json({
            sessionId: "mock-session-id",
            currentContextTokens: 0,
            promptTokens: 0,
            completionTokens: 0,
            cachedTokens: 0,
            maxInputTokens: null,
            usagePercentage: 0,
            model: "mock",
            totalEvents: 0,
            totalMessages: 0,
            totalTasks: 0,
            hasCompaction: false,
        })
    ),
    http.get("*/api/v1/share/link/:sessionId", () => new HttpResponse(null, { status: 404 }))
);

const defaultChatContext = {
    sessionId: "mock-session-id",
    messages: mockMessages,
    isResponding: false,
    isCancelling: false,
    selectedAgentName: "Orchestrator",
    isSidePanelCollapsed: true,
    activeSidePanelTab: "files",
};

const defaultConfigContext = {
    persistenceEnabled: false,
};

const meta = {
    title: "Chat/ChatPage",
    component: ChatPage,
    parameters: {
        docs: {
            description: {
                component: "The main chat page component that displays the chat interface, side panels, and handles user interactions.",
            },
        },
        msw: { handlers },
        featureContext: {
            show_thinking_content: false,
        },
    },
} satisfies Meta<typeof ChatPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: defaultChatContext,
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("expandPanel");
        await canvas.findByTestId("sendMessage");

        await canvas.findByTestId("Files");
        await canvas.findByTestId("Activity");
    },
};

export const LoadingSession: Story = {
    parameters: {
        chatContext: {
            ...defaultChatContext,
            isLoadingSession: true,
            messages: [],
        },
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Loading Session");
    },
};

export const WithLoadingMessage: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            currentTaskId: "mock-task-id",
            messages: [...mockMessages, mockLoadingMessage],
            isResponding: true,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            isSidePanelCollapsed: true,
            activeSidePanelTab: "files",
        },
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("expandPanel");
        await canvas.findByTestId("cancel");
    },
};

export const WithLongInput: Story = {
    parameters: {
        chatContext: defaultChatContext,
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        const chatInput = await canvas.findByTestId("chat-input");

        const longContent = Array(25).fill("This is a line of text to test the input area scrolling behavior.").join("\n");

        chatInput.textContent = longContent;
        chatInput.dispatchEvent(new InputEvent("input", { bubbles: true }));

        expect(chatInput.scrollHeight).toBeGreaterThan(chatInput.clientHeight);
    },
};

export const WithSidePanelOpen: Story = {
    parameters: {
        chatContext: {
            ...defaultChatContext,
            isSidePanelTransitioning: false,
            artifacts: [],
            artifactsLoading: false,
        },
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        const openRightSidePanel = await canvas.findByTestId("expandPanel");
        openRightSidePanel.click();

        await canvas.findByTestId("collapsePanel");
        await canvas.findByText("No files available");

        const collapseButton = await canvas.findByTestId("collapsePanel");
        collapseButton.click();

        await canvas.findByTestId("expandPanel");
    },
};

export const WithPromptDialogOpen: Story = {
    parameters: {
        chatContext: {
            ...defaultChatContext,
            isSidePanelTransitioning: false,
            artifacts: [],
            artifactsLoading: false,
        },
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        const chatInput = await canvas.findByTestId("chat-input");
        await userEvent.type(chatInput, "/");
        const promptCommand = await canvas.findByTestId("promptCommand");
        expect(promptCommand).toBeVisible();
    },
};

const mockResearchProgressMessage = {
    isUser: false,
    isComplete: false,
    taskId: "mock-research-task-id",
    parts: [
        {
            kind: "data",
            data: {
                type: "deep_research_progress",
                phase: "searching",
                status_text: "Searching for relevant sources...",
                progress_percentage: 40,
                current_iteration: 2,
                total_iterations: 5,
                sources_found: 8,
                current_query: "latest advances in large language models 2024",
                fetching_urls: [
                    { url: "https://arxiv.org/abs/2401.00001", title: "Advances in LLMs", favicon: "" },
                    { url: "https://openai.com/research", title: "OpenAI Research Blog", favicon: "" },
                ],
                elapsed_seconds: 45,
                max_runtime_seconds: 300,
                query_history: [
                    {
                        query: "large language model benchmarks 2024",
                        timestamp: "2024-01-01T00:00:00Z",
                        urls: [{ url: "https://arxiv.org/abs/2401.00001", title: "LLM Benchmarks Survey", favicon: "" }],
                    },
                ],
            },
        },
    ],
    metadata: { sessionId: "mock-session-id", lastProcessedEventSequence: 10 },
};

export const WithResearchInProgress: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            currentTaskId: "mock-research-task-id",
            messages: [...mockMessages, mockResearchProgressMessage],
            isResponding: true,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            isSidePanelCollapsed: true,
            activeSidePanelTab: "files",
        },
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText(/Searching for relevant sources/i);
    },
};

export const WithResearchComplete: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            messages: [
                ...mockMessages,
                {
                    ...mockResearchProgressMessage,
                    isComplete: true,
                    parts: [
                        {
                            kind: "data",
                            data: {
                                ...mockResearchProgressMessage.parts[0].data,
                                phase: "writing",
                                status_text: "Research complete",
                                progress_percentage: 100,
                                current_iteration: 5,
                                sources_found: 8,
                            },
                        },
                        {
                            kind: "text",
                            text: "Based on my research, here is a summary of the latest advances in large language models...",
                        },
                    ],
                },
            ],
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            isSidePanelCollapsed: true,
            activeSidePanelTab: "files",
            ragData: [
                {
                    taskId: "mock-research-task-id",
                    searchType: "deep_research",
                    query: "latest advances in large language models 2024",
                    timestamp: "2024-01-01T00:00:00Z",
                    sources: [
                        {
                            citationId: "1",
                            title: "Advances in LLMs",
                            url: "https://arxiv.org/abs/2401.00001",
                            contentPreview: "[Full Content Fetched] LLM research...",
                            metadata: { fetched: true, favicon: "" },
                        },
                        {
                            citationId: "2",
                            title: "OpenAI Research Blog",
                            url: "https://openai.com/research",
                            contentPreview: "[Full Content Fetched] OpenAI research...",
                            metadata: { fetched: true, favicon: "" },
                        },
                    ],
                },
            ],
        },
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Research complete");
    },
};

export const AgentDropdownFiltersWorkflows: Story = {
    parameters: {
        chatContext: defaultChatContext,
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Orchestrator");

        const mockWorkflowElements = canvasElement.innerHTML.includes("MockWorkflow");
        expect(mockWorkflowElements).toBe(false);
    },
};

const collaborativeMessages: MessageFE[] = [
    {
        isUser: true,
        parts: [{ kind: "text", text: "Hi! Can you help me create a Python script to process CSV files?" }],
    },
    {
        isUser: false,
        parts: [{ kind: "text", text: "I'd be happy to help! I'll create a script that reads CSV files, processes the data, and generates reports." }],
    },
    {
        isUser: true,
        parts: [{ kind: "text", text: "Great! Can you also add error handling for missing columns?" }],
    },
    {
        isUser: false,
        parts: [
            { kind: "text", text: "I've created a Python script with comprehensive error handling for missing columns." },
            {
                kind: "artifact",
                status: "completed",
                name: "csv_processor.py",
                description: "Python script for CSV processing",
                file: {
                    name: "csv_processor.py",
                    mimeType: "text/x-python",
                    size: 2400,
                    lastModified: new Date().toISOString(),
                },
            },
        ],
    },
    {
        isUser: true,
        parts: [{ kind: "text", text: "Thanks for adding me! Does this handle different CSV delimiters like semicolons?" }],
    },
    {
        isUser: false,
        parts: [{ kind: "text", text: "Yes! The script uses Python's csv.Sniffer to automatically detect delimiters including semicolons, tabs, and pipes." }],
    },
    {
        isUser: true,
        parts: [{ kind: "text", text: "I just tested it with a semicolon-delimited file and it works perfectly!" }],
    },
    {
        isUser: false,
        parts: [{ kind: "text", text: "Great to hear! The auto-detection is working as expected." }],
    },
    {
        isUser: true,
        parts: [{ kind: "text", text: "Excellent! Can we also add summary statistics?" }],
    },
    {
        isUser: false,
        parts: [{ kind: "text", text: "Absolutely! I'll add a summary module that calculates count, mean, median, and standard deviation for numeric columns." }],
    },
];

export const CollaborativeChat: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-collaborative-session",
            sessionName: "Python Script Development",
            messages: collaborativeMessages,
            isResponding: false,
            isCancelling: false,
            selectedAgentName: "Orchestrator",
            isSidePanelCollapsed: true,
            activeSidePanelTab: "files",
        },
        configContext: defaultConfigContext,
    },
};

export const EmptyInputState: Story = {
    parameters: {
        chatContext: defaultChatContext,
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        const sendButton = await canvas.findByTestId("sendMessage");
        const chatInput = await canvas.findByTestId("chat-input");

        expect(sendButton).toBeDisabled();

        await userEvent.type(chatInput, "Hello");
        expect(sendButton).not.toBeDisabled();

        await userEvent.clear(chatInput);
        expect(sendButton).toBeDisabled();
    },
};

export const ActivityPanelEmpty: Story = {
    parameters: {
        chatContext: { ...defaultChatContext, activeSidePanelTab: "activity" },
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        const activityButton = await canvas.findByTestId("Activity");
        activityButton.click();

        await canvas.findByText("No task selected to display");
    },
};

export const ArtifactPanelEmpty: Story = {
    decorators: [withSessionArtifacts(SESSION_ID, [])],
    parameters: {
        chatContext: defaultChatContext,
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        const openRightSidePanel = await canvas.findByTestId("expandPanel");
        openRightSidePanel.click();
        await canvas.findByText("No files available");
    },
};

export const ArtifactPanel: Story = {
    decorators: [withSessionArtifacts(SESSION_ID, mockArtifacts)],
    parameters: {
        chatContext: defaultChatContext,
        configContext: defaultConfigContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);

        const openRightSidePanel = await canvas.findByTestId("expandPanel");
        openRightSidePanel.click();
        await canvas.findByText("test.md");
    },
};
