// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { consumePendingPrefill, stashPendingPrefill } from "@/utils/redirectPrefill";
import { BEFORE_RECOVERY_RELOAD_EVENT } from "@/utils/staleChunkRecovery";

import { ChatInputArea } from "@/features/chat/ChatInputArea";

import { StoryProvider } from "@/testing/providers/StoryProvider";

document.execCommand = vi.fn().mockReturnValue(true);

class NoopIntersectionObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords() {
        return [];
    }
    root = null;
    rootMargin = "";
    thresholds = [];
}
globalThis.IntersectionObserver = NoopIntersectionObserver as unknown as typeof IntersectionObserver;

function renderComponent(chatContextValues = {}) {
    return render(
        <MemoryRouter>
            <StoryProvider chatContextValues={chatContextValues}>
                <ChatInputArea agents={[]} />
            </StoryProvider>
        </MemoryRouter>
    );
}

const announceReload = () =>
    act(() => {
        globalThis.dispatchEvent(new Event(BEFORE_RECOVERY_RELOAD_EVENT));
    });

describe("ChatInputArea recovery stash", () => {
    beforeEach(() => {
        sessionStorage.clear();
    });

    afterEach(() => {
        sessionStorage.clear();
        vi.restoreAllMocks();
    });

    test("keeps a typed message readable by the next mount when the page reloads", async () => {
        renderComponent({ sessionId: "session-abc" });
        await userEvent.click(screen.getByTestId("chat-input"));
        await userEvent.keyboard("half a thought");

        announceReload();

        expect(consumePendingPrefill("")).toBe("half a thought");
    });

    test("leaves a prefill a redirect already staged alone", async () => {
        renderComponent({ sessionId: "session-abc" });
        await userEvent.click(screen.getByTestId("chat-input"));
        await userEvent.keyboard("salvage");
        stashPendingPrefill("what the redirect staged", null);

        announceReload();

        expect(consumePendingPrefill("")).toBe("what the redirect staged");
    });

    test("stashes nothing when there is nothing typed", async () => {
        renderComponent({ sessionId: "session-abc" });

        announceReload();

        expect(consumePendingPrefill("")).toBeNull();
    });
});
