// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { agentUIMode } from "@/providers/contexts/UIModeContext";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { MessageFE, ProgressUpdate } from "@/types";

import { ChatArea } from "./ChatArea";

const update = (text: string, type: ProgressUpdate["type"] = "status"): ProgressUpdate => ({ type, text, timestamp: 1 });

const welcomeBubble: MessageFE = {
    isUser: false,
    parts: [{ kind: "text", text: "Hi! I'm the Orchestrator Agent." }],
    isComplete: true,
    metadata: { sessionId: "s", lastProcessedEventSequence: 0 },
};

const userTurn: MessageFE = {
    isUser: true,
    parts: [{ kind: "text", text: "Build me an agent." }],
    isComplete: true,
    metadata: { sessionId: "s", lastProcessedEventSequence: 1 },
};

const streamingTurn = (progressUpdates: ProgressUpdate[]): MessageFE => ({
    isUser: false,
    parts: [{ kind: "text", text: "" }],
    isComplete: false,
    taskId: "task-1",
    progressUpdates,
    metadata: { sessionId: "s", messageId: "m-stream", lastProcessedEventSequence: 2 },
});

function renderChat(messages: MessageFE[], props: Partial<React.ComponentProps<typeof ChatArea>> = {}, options: Partial<Parameters<typeof renderWithProviders>[1]> = {}) {
    return renderWithProviders(<ChatArea {...props} />, {
        chatContextValues: { messages, isResponding: true, isLoadingSession: false, agents: [] },
        featureFlags: { show_thinking_content: false },
        ...options,
    });
}

describe("ChatArea working-indicator arbitration", () => {
    it("shows one indicator once a step is streaming", () => {
        renderChat([userTurn, streamingTurn([update("Reading config")])]);
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });

    it("shows one indicator during the gap before the first step arrives", () => {
        renderChat([userTurn, streamingTurn([])]);
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });

    it("never renders the activity button on the trailing loading row", () => {
        renderChat([userTurn, streamingTurn([])]);
        expect(screen.queryByTestId("viewActivity")).not.toBeInTheDocument();
    });

    it("shows one indicator in agent mode once a step is streaming", () => {
        renderChat([userTurn, streamingTurn([update("Reading config")])], {}, { uiModeContextValues: agentUIMode("my-agent") });
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
        expect(screen.getByText("Reading config")).toBeInTheDocument();
    });

    it("shows one indicator in agent mode during the gap before the first step arrives", () => {
        renderChat([userTurn, streamingTurn([])], {}, { uiModeContextValues: agentUIMode("my-agent") });
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });

    it("never renders the activity button in agent mode — there is no panel to open", () => {
        renderChat([userTurn, streamingTurn([update("Reading config")])], {}, { uiModeContextValues: agentUIMode("my-agent") });
        expect(screen.queryByTestId("viewActivity")).not.toBeInTheDocument();
    });

    it("shows one indicator when the streaming message carries no messageId", () => {
        const noId = { ...streamingTurn([update("Reading config")]), metadata: { sessionId: "s", lastProcessedEventSequence: 2 } };
        renderChat([userTurn, noId]);
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });

    it("keeps a cue when the streaming message has no taskId", () => {
        const noTask = { ...streamingTurn([update("Reading config")]), taskId: undefined };
        renderChat([userTurn, noTask]);
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });

    it("keeps a cue when the streaming message is a status bubble", () => {
        const statusBubble = { ...streamingTurn([update("Reading config")]), isStatusBubble: true };
        renderChat([userTurn, statusBubble]);
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });

    it("keeps a cue when a compaction notification renders instead of the timeline", () => {
        const compaction = {
            ...streamingTurn([update("Reading config")]),
            parts: [{ kind: "data", data: { type: "compaction_notification", summary: "trimmed" } }],
        } as unknown as MessageFE;
        renderChat([userTurn, compaction]);
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });

    it("shows one indicator when thinking-only updates are hidden by the flag", () => {
        renderChat([userTurn, streamingTurn([update("Thinking", "thinking")])]);
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });

    it("shows one indicator when the host filters out an earlier message", () => {
        const messageFilter = (message: MessageFE, index: number) => index !== 0 || message.isUser || !!message.taskId;
        renderChat([welcomeBubble, userTurn, streamingTurn([update("Reading config")])], { messageFilter });
        expect(screen.getAllByLabelText("Working")).toHaveLength(1);
    });
});
