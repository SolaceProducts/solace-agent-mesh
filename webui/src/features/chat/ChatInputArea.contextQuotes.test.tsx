// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { ChatInputArea } from "@/features/chat/ChatInputArea";

import { StoryProvider } from "@/testing/providers/StoryProvider";

document.execCommand = vi.fn().mockReturnValue(true);

afterEach(() => {
    vi.restoreAllMocks();
});

function renderComponent(chatContextValues = {}) {
    return render(
        <MemoryRouter>
            <StoryProvider chatContextValues={chatContextValues}>
                <ChatInputArea agents={[]} />
            </StoryProvider>
        </MemoryRouter>
    );
}

function dispatchFollowUpQuote(text: string, sourceMessageId: string) {
    act(() => {
        window.dispatchEvent(
            new CustomEvent("follow-up-question", {
                detail: { text, sourceMessageId },
            })
        );
    });
}

describe("ChatInputArea — context-quote chips clear on submit", () => {
    test("clears the context-quote chip after a successful submit", async () => {
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        renderComponent({ handleSubmit, isResponding: false });

        dispatchFollowUpQuote("selected snippet", "msg-1");

        await waitFor(() => {
            expect(screen.getByText(/selected snippet/)).toBeInTheDocument();
        });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("please explain this");

        await userEvent.click(screen.getByTestId("sendMessage"));

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });

        const message = handleSubmit.mock.calls[0][2] as string;
        expect(message).toContain("selected snippet");
        expect(message).toContain("please explain this");

        await waitFor(() => {
            expect(screen.queryByText(/selected snippet/)).not.toBeInTheDocument();
        });
    });

    test("clears multiple context-quote chips after submit", async () => {
        const handleSubmit = vi.fn().mockResolvedValue(undefined);
        renderComponent({ handleSubmit, isResponding: false });

        dispatchFollowUpQuote("first quote", "msg-1");
        dispatchFollowUpQuote("second quote", "msg-2");

        await waitFor(() => {
            expect(screen.getByText(/first quote/)).toBeInTheDocument();
            expect(screen.getByText(/second quote/)).toBeInTheDocument();
        });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("compare these");

        await userEvent.click(screen.getByTestId("sendMessage"));

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });

        await waitFor(() => {
            expect(screen.queryByText(/first quote/)).not.toBeInTheDocument();
            expect(screen.queryByText(/second quote/)).not.toBeInTheDocument();
        });
    });

    test("restores the context-quote chip when the submit fails", async () => {
        const handleSubmit = vi.fn().mockRejectedValue(new Error("Network error"));
        renderComponent({ handleSubmit, isResponding: false, displayError: vi.fn() });

        dispatchFollowUpQuote("selected snippet", "msg-1");

        await waitFor(() => {
            expect(screen.getByText(/selected snippet/)).toBeInTheDocument();
        });

        const input = screen.getByTestId("chat-input");
        await userEvent.click(input);
        await userEvent.keyboard("please explain this");

        await userEvent.click(screen.getByTestId("sendMessage"));

        await waitFor(() => {
            expect(handleSubmit).toHaveBeenCalled();
        });

        await waitFor(() => {
            expect(screen.getByText(/selected snippet/)).toBeInTheDocument();
        });
    });
});
