// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { MessageFE } from "@/types";

import { ChatMessage } from "./ChatMessage";

const TASK_ID = "11111111-1111-1111-1111-111111111111";

function failedAgentMessage(overrides: Partial<MessageFE> = {}): MessageFE {
    return {
        role: "agent",
        isUser: false,
        taskId: TASK_ID,
        isError: true,
        isComplete: true,
        parts: [{ kind: "text", text: "Error: Something broke" }],
        metadata: { messageId: "m1" },
        ...overrides,
    } as MessageFE;
}

describe("ChatMessage feedback on failed tasks", () => {
    it("offers the thumbs feedback buttons on a failed task message", () => {
        renderWithProviders(<ChatMessage message={failedAgentMessage()} isLastWithTaskId />, {
            chatContextValues: { configCollectFeedback: true, submittedFeedback: {} },
        });

        expect(screen.getByRole("button", { name: "Like" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Dislike" })).toBeInTheDocument();
    });

    it("withholds the thumbs when the failed message carries no task id (the pre-fix regression)", () => {
        renderWithProviders(<ChatMessage message={failedAgentMessage({ taskId: undefined })} isLastWithTaskId />, {
            chatContextValues: { configCollectFeedback: true, submittedFeedback: {} },
        });

        expect(screen.queryByRole("button", { name: "Like" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Dislike" })).not.toBeInTheDocument();
    });
});

describe("ChatMessage feedback already submitted elsewhere", () => {
    it("reports a 409 as already recorded instead of failing silently", async () => {
        const addNotification = vi.fn();
        const handleFeedbackSubmit = vi.fn().mockRejectedValue(Object.assign(new Error("conflict"), { status: 409 }));
        renderWithProviders(<ChatMessage message={failedAgentMessage()} isLastWithTaskId />, {
            chatContextValues: { configCollectFeedback: true, submittedFeedback: {}, handleFeedbackSubmit, addNotification },
        });

        fireEvent.click(screen.getByRole("button", { name: "Like" }));
        fireEvent.click(await screen.findByRole("button", { name: "Submit Feedback" }));

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Feedback for this response was already recorded.", "info"));
        expect(addNotification).not.toHaveBeenCalledWith("Feedback submitted successfully", "success");
        await waitFor(() => expect(screen.queryByRole("button", { name: "Submit Feedback" })).not.toBeInTheDocument());
    });

    it("locks both thumbs after a 409 so a retry cannot hit the conflict again", async () => {
        const handleFeedbackSubmit = vi.fn().mockRejectedValue(Object.assign(new Error("conflict"), { status: 409 }));
        renderWithProviders(<ChatMessage message={failedAgentMessage()} isLastWithTaskId />, {
            chatContextValues: { configCollectFeedback: true, submittedFeedback: {}, handleFeedbackSubmit, addNotification: vi.fn() },
        });

        fireEvent.click(screen.getByRole("button", { name: "Dislike" }));
        fireEvent.click(await screen.findByRole("button", { name: "Submit Feedback" }));

        await waitFor(() => expect(screen.getByRole("button", { name: "Like" })).toBeDisabled());
        expect(screen.getByRole("button", { name: "Dislike" })).toBeDisabled();

        fireEvent.click(screen.getByRole("button", { name: "Like" }));
        expect(screen.queryByRole("button", { name: "Submit Feedback" })).not.toBeInTheDocument();
        expect(handleFeedbackSubmit).toHaveBeenCalledTimes(1);
        expect(screen.getByRole("status")).toHaveTextContent("Feedback already recorded");
        for (const name of ["Like", "Dislike"]) {
            expect(screen.getByRole("button", { name })).not.toHaveClass("!opacity-100");
        }
    });

    it("shows the recorded rating instead of the label once it has loaded", async () => {
        const handleFeedbackSubmit = vi.fn().mockRejectedValue(Object.assign(new Error("conflict"), { status: 409 }));
        const tree = (submittedFeedback: Record<string, { type: "up" | "down"; text: string }>) => (
            <MemoryRouter>
                <StoryProvider chatContextValues={{ configCollectFeedback: true, submittedFeedback, handleFeedbackSubmit, addNotification: vi.fn() }}>
                    <ChatMessage message={failedAgentMessage()} isLastWithTaskId />
                </StoryProvider>
            </MemoryRouter>
        );
        const { rerender } = render(tree({}));

        fireEvent.click(screen.getByRole("button", { name: "Like" }));
        fireEvent.click(await screen.findByRole("button", { name: "Submit Feedback" }));
        await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Feedback already recorded"));

        rerender(tree({ [TASK_ID]: { type: "down", text: "" } }));

        expect(screen.getByRole("status")).toBeEmptyDOMElement();
        const dislike = screen.getByRole("button", { name: "Dislike" });
        expect(dislike).toBeDisabled();
        expect(dislike).toHaveClass("!opacity-100");
        expect(dislike.querySelector("svg")).toHaveClass("fill-[var(--brand-wMain)]");
    });

    it("keeps the thumbs enabled after a failure that is not a conflict", async () => {
        const handleFeedbackSubmit = vi.fn().mockRejectedValue(Object.assign(new Error("server error"), { status: 500 }));
        renderWithProviders(<ChatMessage message={failedAgentMessage()} isLastWithTaskId />, {
            chatContextValues: { configCollectFeedback: true, submittedFeedback: {}, handleFeedbackSubmit, addNotification: vi.fn() },
        });

        fireEvent.click(screen.getByRole("button", { name: "Like" }));
        fireEvent.click(await screen.findByRole("button", { name: "Submit Feedback" }));

        await waitFor(() => expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled());
        fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

        await waitFor(() => expect(screen.queryByRole("button", { name: "Submit Feedback" })).not.toBeInTheDocument());
        expect(handleFeedbackSubmit).toHaveBeenCalledTimes(1);
        expect(screen.getByRole("button", { name: "Like" })).toBeEnabled();
        expect(screen.getByRole("button", { name: "Dislike" })).toBeEnabled();
    });
});
