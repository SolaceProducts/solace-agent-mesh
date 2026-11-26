// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { MessageFE } from "@/types";

import { ChatMessage } from "./ChatMessage";

const TASK_ID = "22222222-2222-2222-2222-222222222222";

function completedAgentMessage(overrides: Partial<MessageFE> = {}): MessageFE {
    return {
        role: "agent",
        isUser: false,
        taskId: TASK_ID,
        isComplete: true,
        parts: [{ kind: "text", text: "All done." }],
        metadata: { messageId: "m1" },
        ...overrides,
    } as MessageFE;
}

describe("ChatMessage action row", () => {
    it("keeps copy and read-aloud when feedback collection is disabled", () => {
        renderWithProviders(<ChatMessage message={completedAgentMessage()} isLastWithTaskId />, {
            chatContextValues: { configCollectFeedback: false, submittedFeedback: {} },
        });

        expect(screen.getByRole("button", { name: "Copy to clipboard" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Like" })).not.toBeInTheDocument();
    });

    it("keeps copy and read-aloud when the message carries progress updates", () => {
        renderWithProviders(<ChatMessage message={completedAgentMessage({ progressUpdates: [{ type: "status", text: "Working", timestamp: 1 }] })} isLastWithTaskId />, {
            chatContextValues: { configCollectFeedback: false, submittedFeedback: {} },
        });

        expect(screen.getByRole("button", { name: "Copy to clipboard" })).toBeInTheDocument();
    });

    it("still offers the thumbs alongside copy when feedback collection is enabled", () => {
        renderWithProviders(<ChatMessage message={completedAgentMessage()} isLastWithTaskId />, {
            chatContextValues: { configCollectFeedback: true, submittedFeedback: {} },
        });

        expect(screen.getByRole("button", { name: "Copy to clipboard" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Like" })).toBeInTheDocument();
    });

    it("never renders the activity button in the action row", () => {
        renderWithProviders(<ChatMessage message={completedAgentMessage()} isLastWithTaskId />, {
            chatContextValues: { configCollectFeedback: true, submittedFeedback: {} },
        });

        expect(screen.queryByTestId("viewActivity")).not.toBeInTheDocument();
    });
});
