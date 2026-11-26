// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
}

const mockUseChatContext = vi.fn();
const mockUseAudioSettings = vi.fn();
const mockUseConfigContext = vi.fn();
const mockApiGet = vi.fn();
const mockApiPost = vi.fn();

const GREETING = "Hi! Describe the task you'd like to automate.";
const TASK_UPDATE_RESPONSE = {
    message: "Great! I've configured a daily report task.",
    taskUpdates: {
        name: "Daily Report",
        description: "Generate daily summary",
        scheduleType: "cron",
        scheduleExpression: "0 9 * * *",
        targetAgentName: "Orchestrator",
        taskMessage: "Generate yesterday's report",
        timezone: "UTC",
    },
    confidence: 0.95,
    readyToSave: true,
};

describe("TaskBuilderChat", () => {
    let TaskBuilderChat: React.ComponentType<{
        onConfigUpdate: (config: Record<string, unknown>) => void;
        currentConfig: Record<string, unknown>;
        initialMessage?: string | null;
        availableAgents?: Array<{ name: string }>;
    }>;

    beforeEach(async () => {
        vi.resetModules();
        mockUseChatContext.mockReset();
        mockUseAudioSettings.mockReset();
        mockUseConfigContext.mockReset();
        mockApiGet.mockReset();
        mockApiPost.mockReset();

        mockUseChatContext.mockReturnValue({ addNotification: vi.fn() });
        mockUseAudioSettings.mockReturnValue({ settings: { speechToText: false } });
        mockUseConfigContext.mockReturnValue({ speechToTextEnabled: false });

        mockApiGet.mockResolvedValue({ message: GREETING });
        mockApiPost.mockResolvedValue(TASK_UPDATE_RESPONSE);

        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: mockUseChatContext,
        }));

        vi.doMock("@/providers/contexts/AudioSettingsContext", () => ({
            useAudioSettings: mockUseAudioSettings,
        }));

        vi.doMock("@/providers/contexts/ConfigContext", () => ({
            useConfigContext: mockUseConfigContext,
        }));

        vi.doMock("@/api/client", () => ({
            api: {
                webui: { get: mockApiGet, post: mockApiPost },
            },
        }));

        const mod = await import("@/features/scheduledTasks/TaskBuilderChat");
        TaskBuilderChat = mod.TaskBuilderChat as typeof TaskBuilderChat;
    });

    function renderChat(overrides: Partial<React.ComponentProps<typeof TaskBuilderChat>> = {}) {
        const onConfigUpdate = overrides.onConfigUpdate ?? vi.fn();
        const queryClient = new QueryClient({
            defaultOptions: {
                queries: { retry: false },
                mutations: { retry: false },
            },
        });
        const result = render(
            <QueryClientProvider client={queryClient}>
                <TaskBuilderChat onConfigUpdate={onConfigUpdate} currentConfig={{}} initialMessage={overrides.initialMessage ?? null} availableAgents={overrides.availableAgents ?? [{ name: "Orchestrator" }]} />
            </QueryClientProvider>
        );
        return { ...result, onConfigUpdate };
    }

    test("renders greeting after it loads", async () => {
        renderChat();
        expect(await screen.findByText(GREETING)).toBeInTheDocument();
        expect(screen.queryByLabelText("Waiting for response")).not.toBeInTheDocument();
    });

    test("auto-sends initialMessage, clears the responding indicator once the POST settles, and applies task updates", async () => {
        let resolvePost: (value: typeof TASK_UPDATE_RESPONSE) => void;
        mockApiPost.mockReturnValueOnce(
            new Promise<typeof TASK_UPDATE_RESPONSE>(resolve => {
                resolvePost = resolve;
            })
        );

        const { onConfigUpdate } = renderChat({
            initialMessage: "Generate a daily report at 9am",
        });

        expect(await screen.findByText("Generate a daily report at 9am")).toBeInTheDocument();

        await waitFor(() => {
            expect(screen.getByLabelText("Waiting for response")).toBeInTheDocument();
        });

        resolvePost!(TASK_UPDATE_RESPONSE);
        expect(await screen.findByText(TASK_UPDATE_RESPONSE.message)).toBeInTheDocument();

        await waitFor(() => {
            expect(screen.queryByLabelText("Waiting for response")).not.toBeInTheDocument();
        });

        expect(onConfigUpdate).toHaveBeenCalledWith(TASK_UPDATE_RESPONSE.taskUpdates);

        expect(mockApiPost).toHaveBeenCalledTimes(1);
        expect(mockApiPost.mock.calls[0][0]).toBe("/api/v1/scheduledTasks/builder/chat");
        expect(mockApiPost.mock.calls[0][1]).toMatchObject({
            message: "Generate a daily report at 9am",
            availableAgents: [{ name: "Orchestrator" }],
        });
    });

    test("typed user message sends and updates preview on success", async () => {
        const { onConfigUpdate } = renderChat();

        await screen.findByText(GREETING);

        const textarea = screen.getByRole("textbox");
        fireEvent.change(textarea, { target: { value: "Run cleanup every Sunday at 2am" } });

        const form = textarea.closest("form");
        if (!form) throw new Error("form element not found");
        fireEvent.submit(form);

        expect(await screen.findByText(TASK_UPDATE_RESPONSE.message)).toBeInTheDocument();
        await waitFor(() => {
            expect(screen.queryByLabelText("Waiting for response")).not.toBeInTheDocument();
        });
        expect(onConfigUpdate).toHaveBeenCalledWith(TASK_UPDATE_RESPONSE.taskUpdates);
    });

    test("clears spinner and shows notification when the mutation fails", async () => {
        const addNotification = vi.fn();
        mockUseChatContext.mockReturnValue({ addNotification });
        mockApiPost.mockRejectedValueOnce(new Error("boom"));

        renderChat({ initialMessage: "Something" });

        expect(await screen.findByTestId("inlineNotice-error")).toHaveTextContent(/I encountered an error/i);
        await waitFor(() => {
            expect(screen.queryByLabelText("Waiting for response")).not.toBeInTheDocument();
        });
        expect(addNotification).toHaveBeenCalled();
    });

    test.each([
        { isWarning: true, notice: "inlineNotice-warning" },
        { isWarning: false, notice: "inlineNotice-error" },
    ])("shows an error reply as $notice and applies no task updates", async ({ isWarning, notice }) => {
        mockApiPost.mockResolvedValueOnce({ message: "Nothing could be done.", taskUpdates: TASK_UPDATE_RESPONSE.taskUpdates, confidence: 0, readyToSave: false, isError: true, isWarning });

        const { onConfigUpdate } = renderChat({ initialMessage: "Something" });

        expect(await screen.findByTestId(notice)).toHaveTextContent("Nothing could be done.");
        expect(onConfigUpdate).not.toHaveBeenCalled();
    });

    test("leaves an error reply out of the history sent with the next message", async () => {
        mockApiPost.mockResolvedValueOnce({ message: "Nothing could be done.", taskUpdates: {}, confidence: 0, readyToSave: false, isError: true, isWarning: true });
        renderChat({ initialMessage: "Something" });
        await screen.findByTestId("inlineNotice-warning");

        const textarea = screen.getByRole("textbox");
        fireEvent.change(textarea, { target: { value: "Try again" } });
        fireEvent.submit(textarea.closest("form")!);

        await waitFor(() => expect(mockApiPost).toHaveBeenCalledTimes(2));
        const history = mockApiPost.mock.calls[1][1].conversationHistory as Array<{ content: string }>;
        expect(history.map(m => m.content)).toContain("Something");
        expect(history.map(m => m.content)).not.toContain("Nothing could be done.");
    });
});
