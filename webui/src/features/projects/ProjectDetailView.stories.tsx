// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect } from "react";
import type { Meta, StoryContext, StoryFn, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { transformAgentCard } from "@/api/agent-cards";

import { useStartIndexing } from "@/features/projects/hooks";

import { createAgentCards, emptyProject, imageArtifact, jsonArtifact, markdownArtifact, mockAgentCards, mockSessions, pdfArtifact, populatedProject } from "@/testing/data";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";
import { ownerWithAuthorization } from "@/testing/helpers/parameters";

import { ProjectDetailView } from "./ProjectDetailView";

const mockArtifacts = [pdfArtifact, imageArtifact, jsonArtifact, markdownArtifact];
const transformedMockAgents = mockAgentCards.concat(createAgentCards(2)).map(transformAgentCard);
const agentNameDisplayNameMap = transformedMockAgents.reduce(
    (acc, agent) => {
        if (agent.name) acc[agent.name] = agent.displayName || agent.name;
        return acc;
    },
    {} as Record<string, string>
);

const MockIndexingTask = ({ projectId, children }: { projectId: string; children: ReactNode }) => {
    const startIndexing = useStartIndexing();

    useEffect(() => {
        startIndexing("/api/v1/sse/subscribe/mock-indexing-task", projectId, "upload");
    }, [startIndexing, projectId]);

    return <>{children}</>;
};

const MockFailedIndexingTask = ({ projectId, children }: { projectId: string; children: ReactNode }) => {
    const startIndexing = useStartIndexing();

    useEffect(() => {
        startIndexing("/api/v1/sse/subscribe/mock-failed-indexing-task", projectId, "upload");
    }, [startIndexing, projectId]);

    return <>{children}</>;
};

const handlers = [
    http.get("*/api/v1/sessions", ({ request }) => {
        const url = new URL(request.url);
        const projectId = url.searchParams.get("projectId");

        const data = projectId === populatedProject.id ? mockSessions : [];
        return HttpResponse.json({
            data,
            meta: getPaginatedMeta(data.length),
        });
    }),

    http.get("*/api/v1/projects/:projectId/artifacts", ({ params }) => {
        const { projectId } = params;

        if (projectId === populatedProject.id) {
            return HttpResponse.json(mockArtifacts);
        }
        return HttpResponse.json([]);
    }),

    http.get("*/api/v1/sse/subscribe/mock-indexing-task", () => {
        const stream = new ReadableStream({
            start(controller) {
                controller.enqueue(new TextEncoder().encode('event: index_message\ndata: {"type":"task_started"}\n\n'));
            },
        });

        return new HttpResponse(stream, {
            headers: {
                "Content-Type": "text/event-stream",
                "Cache-Control": "no-cache",
                Connection: "keep-alive",
            },
        });
    }),

    http.get("*/api/v1/sse/subscribe/mock-failed-indexing-task", () => {
        const stream = new ReadableStream({
            async start(controller) {
                await new Promise(resolve => setTimeout(resolve, 100));
                controller.enqueue(new TextEncoder().encode('event: index_message\ndata: {"type":"conversion_failed","file":"document.pdf","error":"Some reason."}\n\n'));
                await new Promise(resolve => setTimeout(resolve, 50));
                controller.enqueue(new TextEncoder().encode('event: index_message\ndata: {"type":"conversion_failed","file":"report.docx","error":"Another reason."}\n\n'));
                await new Promise(resolve => setTimeout(resolve, 50));
                controller.enqueue(new TextEncoder().encode('event: index_message\ndata: {"type":"indexing_failed","error":"Indexing failed for some reason."}\n\n'));
                await new Promise(resolve => setTimeout(resolve, 50));
                controller.enqueue(new TextEncoder().encode('event: index_message\ndata: {"type":"task_completed","files":[]}\n\n'));
                controller.close();
            },
        });

        return new HttpResponse(stream, {
            headers: {
                "Content-Type": "text/event-stream",
                "Cache-Control": "no-cache",
                Connection: "keep-alive",
            },
        });
    }),
];

const meta = {
    title: "Projects/ProjectDetailView",
    component: ProjectDetailView,
    parameters: {
        docs: {
            description: {
                component: "Detailed view of a single project showing chats, instructions, default agent, and knowledge sections.",
            },
        },
        msw: { handlers: withDefaults(...handlers) },
    },
    decorators: [
        (Story: StoryFn, context: StoryContext) => {
            sessionStorage.removeItem("sam_sse_tasks");
            return Story(context.args, context);
        },
    ],
} satisfies Meta<typeof ProjectDetailView>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        project: populatedProject,
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    parameters: ownerWithAuthorization("user-id"),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByTestId("editDetailsButton")).toBeVisible();
        expect(await canvas.findByTestId("startNewChatButton")).toBeVisible();
    },
};

export const Empty: Story = {
    args: {
        project: emptyProject,
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    parameters: {
        chatContext: {
            agents: transformedMockAgents,
            agentNameDisplayNameMap,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const startNewChatNoChatsButton = await canvas.findByTestId("startNewChatButtonNoChats");
        expect(startNewChatNoChatsButton).toBeVisible();
    },
};

export const EditDetailsDialog: Story = {
    args: {
        project: populatedProject,
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("editDetailsButton");
        expect(editButton).toBeVisible();
        await userEvent.click(editButton);

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        const dialogContent = within(dialog);

        expect(await dialogContent.findByText("Edit Project Details")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();
        expect(await dialogContent.findByRole("button", { name: "Discard Changes" })).toBeEnabled();
    },
};

export const EditDetailsDescriptionLimit: Story = {
    args: {
        project: populatedProject,
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const editButton = await canvas.findByTestId("editDetailsButton");
        await userEvent.click(editButton);

        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);
        const descriptionInput = await dialogContent.findByPlaceholderText("Project description");

        await userEvent.clear(descriptionInput);
        const atLimitText = "a".repeat(1000);
        await userEvent.click(descriptionInput);
        await userEvent.paste(atLimitText);
        expect(await dialogContent.findByText("1000 / 1000")).toBeInTheDocument();

        await userEvent.type(descriptionInput, "b");
        expect(await dialogContent.findByText("Description must be 1000 characters or fewer (currently 1001)")).toBeInTheDocument();
        expect(descriptionInput).toHaveAttribute("aria-invalid", "true");
        expect(descriptionInput).toHaveAccessibleDescription(/1000/);

        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeDisabled();
    },
};

export const EditDetailsCountsStoredCrlfAsOneNewline: Story = {
    args: {
        project: { ...populatedProject, description: "a\r\nb\r\nc\r\nd\r\ne" },
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    parameters: {
        configContext: { validationLimits: { projectDescriptionMax: 10 } },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(await canvas.findByTestId("editDetailsButton"));

        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);
        const descriptionInput = await dialogContent.findByPlaceholderText("Project description");

        expect(await dialogContent.findByText("9 / 10")).toBeInTheDocument();
        expect(descriptionInput).toHaveAttribute("aria-invalid", "false");
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();
    },
};

export const EditDetailsCountsEmojiAndTrimsWhitespace: Story = {
    args: {
        project: populatedProject,
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    parameters: {
        configContext: { validationLimits: { projectDescriptionMax: 10 } },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(await canvas.findByTestId("editDetailsButton"));

        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);
        const descriptionInput = await dialogContent.findByPlaceholderText("Project description");

        await userEvent.clear(descriptionInput);
        await userEvent.click(descriptionInput);
        await userEvent.paste("😀".repeat(8));
        expect(await dialogContent.findByText("8 / 10")).toBeInTheDocument();
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();

        await userEvent.clear(descriptionInput);
        await userEvent.click(descriptionInput);
        await userEvent.paste(`${"a".repeat(10)}\n\n`);
        expect(await dialogContent.findByText("10 / 10")).toBeInTheDocument();
        expect(descriptionInput).toHaveAttribute("aria-invalid", "false");
        expect(await dialogContent.findByRole("button", { name: "Save" })).toBeEnabled();
    },
};

export const EditDetailsRequiredFields: Story = {
    args: {
        project: populatedProject,
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(await canvas.findByTestId("editDetailsButton"));

        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        const nameLabel = await dialogContent.findByText("Project Name", { exact: false });
        expect(within(nameLabel).getByText("*")).toHaveClass("text-(--brand-wMain)");

        expect(await dialogContent.findByText("Description", { exact: true })).toBeInTheDocument();
    },
};

export const EditDetailsEmptyNameRejected: Story = {
    args: {
        project: populatedProject,
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(await canvas.findByTestId("editDetailsButton"));

        const dialog = await screen.findByRole("dialog");
        const dialogContent = within(dialog);

        await userEvent.clear(await dialogContent.findByPlaceholderText("Project name"));
        await userEvent.click(await dialogContent.findByRole("button", { name: "Save" }));

        expect(await dialogContent.findByText("Project name is required")).toBeInTheDocument();
        expect(screen.getByRole("dialog")).toBeInTheDocument();
    },
};

export const Indexing: Story = {
    args: {
        project: populatedProject,
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    parameters: ownerWithAuthorization("user-id"),
    decorators: [
        (Story: StoryFn, context: StoryContext) => {
            const storyResult = Story(context.args, context);
            return <MockIndexingTask projectId={populatedProject.id}>{storyResult}</MockIndexingTask>;
        },
    ],
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByTestId("messageBanner")).toBeVisible();

        expect(await canvas.findByTestId("editDetailsButton")).toBeDisabled();
        expect(await canvas.findByTestId("editInstructions")).toBeDisabled();
        expect(await canvas.findByTestId("editDefaultAgent")).toBeDisabled();
        expect(await canvas.findByTestId("startNewChatButton")).toBeDisabled();
    },
};

export const IndexingError: Story = {
    args: {
        project: populatedProject,
        onBack: () => alert("Will navigate back to project list"),
        onStartNewChat: () => alert("Will start a new chat"),
        onChatClick: (sessionId: string) => alert("Will open chat " + sessionId),
    },
    parameters: ownerWithAuthorization("user-id"),
    decorators: [
        (Story: StoryFn, context: StoryContext) => {
            const storyResult = Story(context.args, context);
            return <MockFailedIndexingTask projectId={populatedProject.id}>{storyResult}</MockFailedIndexingTask>;
        },
    ],
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const banner = await canvas.findByTestId("messageBanner");
        expect(banner).toBeVisible();
        await waitFor(() => expect(banner).toHaveTextContent("Indexing failed for some reason. Unable to process: document.pdf, report.docx. Please ensure all files are valid and try again."));

        await waitFor(async () => expect(await canvas.findByTestId("editDetailsButton", {}, { timeout: 2000 })).toBeEnabled());
        expect(await canvas.findByTestId("editInstructions")).toBeEnabled();
        expect(await canvas.findByTestId("editDefaultAgent")).toBeEnabled();
        expect(await canvas.findByTestId("startNewChatButton")).toBeEnabled();
    },
};
