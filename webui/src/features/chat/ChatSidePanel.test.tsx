// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import { ChatSidePanel } from "@/features/chat/ChatSidePanel";

import { documentSearchRagData } from "@/testing/data/ragSearchResults";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { TaskFE } from "@/types";

describe("document-search sources rendering", () => {
    const chatContextWithRagData = {
        activeSidePanelTab: "rag" as const,
        ragData: documentSearchRagData,
        ragEnabled: true,
    };

    const taskContext = {
        monitoredTasks: {},
        loadTaskFromBackend: vi.fn().mockResolvedValue(null),
    };

    test("shows DocumentSourcesPanel for document-search results", async () => {
        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={chatContextWithRagData} taskContextValues={taskContext}>
                    <ChatSidePanel onCollapsedToggle={vi.fn()} isSidePanelCollapsed={false} setIsSidePanelCollapsed={vi.fn()} />
                </StoryProvider>
            </MemoryRouter>
        );

        expect(await screen.findByText(/3 Documents/)).toBeInTheDocument();
        expect(await screen.findByText(/6 Citations/)).toBeInTheDocument();

        expect(screen.queryByText(/6 Sources/)).not.toBeInTheDocument();
    });
});

describe("background-task-completed reload (OAuth pause/resume visualization)", () => {
    const staleTaskId = "019e8e57-c851-73ad-af71-cf26e05597dd";
    const staleTask: TaskFE = {
        taskId: staleTaskId,
        initialRequestText: "Call probe_alpha, probe_beta and probe_gamma in parallel",
        events: [
            {
                event_type: "a2a_message",
                timestamp: "2026-06-03T16:36:18.170Z",
                solace_topic: "greg_go_demo/a2a/v1/gateway/status/go-webui-gw/" + staleTaskId,
                direction: "status_update",
                source_entity: "OAuthProbe",
                target_entity: "unknown",
                task_id: staleTaskId,
                payload_summary: {},
                full_payload: { result: { status: { state: "working" } } },
            },
        ],
        firstSeen: new Date("2026-06-03T16:36:18.155Z"),
        lastUpdated: new Date("2026-06-03T16:36:18.170Z"),
    };

    const renderPanel = (loadTaskFromBackend: (taskId: string) => Promise<TaskFE | null>) =>
        render(
            <MemoryRouter>
                <StoryProvider
                    chatContextValues={{ activeSidePanelTab: "activity" as const, taskIdInSidePanel: staleTaskId }}
                    taskContextValues={{
                        monitoredTasks: { [staleTaskId]: staleTask },
                        loadTaskFromBackend,
                    }}
                >
                    <ChatSidePanel onCollapsedToggle={vi.fn()} isSidePanelCollapsed={false} setIsSidePanelCollapsed={vi.fn()} />
                </StoryProvider>
            </MemoryRouter>
        );

    test("reloads the visible task from the backend when it completes in the background", async () => {
        const loadTaskFromBackend = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        renderPanel(loadTaskFromBackend);

        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(staleTaskId));
        loadTaskFromBackend.mockClear();

        window.dispatchEvent(new CustomEvent("background-task-completed", { detail: { taskId: staleTaskId } }));

        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(staleTaskId));
    });

    test("ignores completion of a task that is not the one on screen", async () => {
        const loadTaskFromBackend = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        renderPanel(loadTaskFromBackend);

        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(staleTaskId));
        loadTaskFromBackend.mockClear();

        window.dispatchEvent(new CustomEvent("background-task-completed", { detail: { taskId: "some-other-task" } }));

        await new Promise(resolve => setTimeout(resolve, 0));
        expect(loadTaskFromBackend).not.toHaveBeenCalled();
    });
});

describe("activity_event source selection", () => {
    const liveTaskId = "019e8e57-c851-73ad-af71-cf26e05597dd";
    const liveTask: TaskFE = {
        taskId: liveTaskId,
        initialRequestText: "live activity request",
        events: [
            {
                event_type: "a2a_message",
                timestamp: "2026-06-03T16:36:18.170Z",
                solace_topic: "greg_go_demo/a2a/v1/gateway/status/go-webui-gw/" + liveTaskId,
                direction: "status_update",
                source_entity: "LiveAgent",
                target_entity: "unknown",
                task_id: liveTaskId,
                payload_summary: {},
                full_payload: { result: { status: { state: "working" } } },
            },
        ],
        firstSeen: new Date("2026-06-03T16:36:18.155Z"),
        lastUpdated: new Date("2026-06-03T16:36:18.170Z"),
    };

    test("renders the live activity store immediately and reconciles against the backfill", async () => {
        const loadTaskFromBackend = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ activeSidePanelTab: "activity" as const, taskIdInSidePanel: liveTaskId, activityEventTasks: { [liveTaskId]: liveTask } }} taskContextValues={{ monitoredTasks: {}, loadTaskFromBackend }}>
                    <ChatSidePanel onCollapsedToggle={vi.fn()} isSidePanelCollapsed={false} setIsSidePanelCollapsed={vi.fn()} />
                </StoryProvider>
            </MemoryRouter>
        );

        expect(await screen.findByText(/live activity request/)).toBeInTheDocument();
        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(liveTaskId));
    });
});

describe("activity backfill is lazy (fetch only when the Activity tab is visible)", () => {
    const taskId = "019e8e57-c851-73ad-af71-cf26e05597dd";

    const renderPanel = (activeTab: "files" | "activity", loadTaskFromBackend: (taskId: string) => Promise<TaskFE | null>) =>
        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ activeSidePanelTab: activeTab, taskIdInSidePanel: taskId }} taskContextValues={{ monitoredTasks: {}, loadTaskFromBackend }}>
                    <ChatSidePanel onCollapsedToggle={vi.fn()} isSidePanelCollapsed={false} setIsSidePanelCollapsed={vi.fn()} />
                </StoryProvider>
            </MemoryRouter>
        );

    test("does not fetch task events until the Activity tab is shown, since the backfill walks the whole task tree", async () => {
        const loadTaskFromBackend = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        renderPanel("files", loadTaskFromBackend);

        await new Promise(resolve => setTimeout(resolve, 1500));
        expect(loadTaskFromBackend).not.toHaveBeenCalled();
    });

    test("does not fetch task events while the panel is collapsed", async () => {
        const loadTaskFromBackend = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ activeSidePanelTab: "activity" as const, taskIdInSidePanel: taskId }} taskContextValues={{ monitoredTasks: {}, loadTaskFromBackend }}>
                    <ChatSidePanel onCollapsedToggle={vi.fn()} isSidePanelCollapsed={true} setIsSidePanelCollapsed={vi.fn()} />
                </StoryProvider>
            </MemoryRouter>
        );

        await new Promise(resolve => setTimeout(resolve, 1500));
        expect(loadTaskFromBackend).not.toHaveBeenCalled();
    });

    test("fetches task events once the Activity tab is shown", async () => {
        const loadTaskFromBackend = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        renderPanel("activity", loadTaskFromBackend);

        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(taskId));
    });
});
