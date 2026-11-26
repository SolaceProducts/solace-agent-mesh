// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { sessionKeys } from "@/api/sessions";

import { queryClient } from "@/providers/query/QueryClient";

import { ChatInputArea } from "@/features/chat/ChatInputArea";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { ContextUsage } from "@/types";

document.execCommand = vi.fn().mockReturnValue(true);

const MAX_INPUT_TOKENS = 200_000;

function usageFor(sessionId: string, percentage: number): ContextUsage {
    return {
        sessionId,
        currentContextTokens: (MAX_INPUT_TOKENS * percentage) / 100,
        promptTokens: 1,
        completionTokens: 1,
        cachedTokens: 0,
        maxInputTokens: MAX_INPUT_TOKENS,
        usagePercentage: percentage,
        model: "mock",
        totalEvents: 1,
        totalMessages: 6,
        totalTasks: 1,
        hasCompaction: false,
    };
}

function renderComposer(sessionId: string) {
    return (
        <MemoryRouter>
            <StoryProvider chatContextValues={{ sessionId, selectedAgentName: "", pendingPrompt: null }}>
                <ChatInputArea agents={[]} />
            </StoryProvider>
        </MemoryRouter>
    );
}

beforeEach(() => {
    queryClient.clear();
    queryClient.setQueryData(sessionKeys.contextUsage("session-a", undefined), usageFor("session-a", 70));
    queryClient.setQueryData(sessionKeys.contextUsage("session-b", undefined), usageFor("session-b", 80));
    queryClient.setQueryData(["platform", "models", "for-context-usage", "v1"], []);
});

afterEach(() => queryClient.clear());

test("switching sessions resets the context usage panel instead of carrying it open into the new session", async () => {
    const user = userEvent.setup();
    const { rerender } = render(renderComposer("session-a"));

    const ringA = await screen.findByRole("button", { name: "Context usage: 70%" });
    await user.click(ringA);
    await waitFor(() => expect(ringA).toHaveAttribute("aria-expanded", "true"));

    rerender(renderComposer("session-b"));

    const ringB = await screen.findByRole("button", { name: "Context usage: 80%" });
    expect(ringB).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText(/Session total/i)).not.toBeInTheDocument();
});
