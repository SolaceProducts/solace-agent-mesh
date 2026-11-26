// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import { CollaborativeUserMessage } from "@/features/chat/CollaborativeUserMessage";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { MessageFE } from "@/types";

vi.mock("@/features/chat/MessageHoverButtons", () => ({
    MessageHoverButtons: () => <div data-testid="hover-buttons" />,
}));

function makeMessage(text: string): MessageFE {
    return {
        isUser: true,
        role: "user",
        parts: [{ kind: "text" as const, text }],
    } as MessageFE;
}

function renderMessage(text: string, userName: string) {
    return render(
        <MemoryRouter>
            <StoryProvider>
                <CollaborativeUserMessage message={makeMessage(text)} userName={userName} timestamp={1700000000000} />
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("CollaborativeUserMessage", () => {
    test("renders user name attribution", () => {
        renderMessage("Hello", "Alice");
        expect(screen.getByText("Alice")).toBeInTheDocument();
    });

    test("renders message text content", () => {
        renderMessage("Can you help me with this?", "Bob");
        expect(screen.getByText("Can you help me with this?")).toBeInTheDocument();
    });

    test("still renders user name when message has no text part", () => {
        const message = {
            isUser: true,
            role: "user",
            parts: [],
        } as unknown as MessageFE;

        render(
            <MemoryRouter>
                <StoryProvider>
                    <CollaborativeUserMessage message={message} userName="Charlie" timestamp={1700000000000} />
                </StoryProvider>
            </MemoryRouter>
        );

        expect(screen.getByText("Charlie")).toBeInTheDocument();
    });
});
