// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";
import { sessionKeys } from "@/api/sessions";

import { queryClient } from "@/providers/query/QueryClient";

import { ContextUsageIndicator } from "@/features/chat/ContextUsageIndicator";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { ContextUsage } from "@/types";

const { getSessionContextUsageMock, fetchModelConfigsMock, isMobileMock } = vi.hoisted(() => ({
    getSessionContextUsageMock: vi.fn(),
    fetchModelConfigsMock: vi.fn(),
    isMobileMock: vi.fn(() => false),
}));

vi.mock("@/hooks", async importOriginal => {
    const actual = await importOriginal<typeof import("@/hooks")>();
    return {
        ...actual,
        useIsMobile: () => isMobileMock(),
    };
});

vi.mock("@/api/sessions/service", async importOriginal => {
    const actual = await importOriginal<typeof import("@/api/sessions/service")>();
    return {
        ...actual,
        getSessionContextUsage: getSessionContextUsageMock,
    };
});

vi.mock("@/api/models/service", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/models/service")>()),
    fetchModelConfigs: fetchModelConfigsMock,
}));

const SESSION_ID = "sess_abc";
const MAX_INPUT_TOKENS = 200_000;

function tokensAt(percentage: number): number {
    return (MAX_INPUT_TOKENS * percentage) / 100;
}

function makeUsage(overrides: Partial<ContextUsage> = {}): ContextUsage {
    return {
        sessionId: SESSION_ID,
        currentContextTokens: tokensAt(60),
        promptTokens: 800,
        completionTokens: 200,
        cachedTokens: 0,
        maxInputTokens: MAX_INPUT_TOKENS,
        usagePercentage: 60,
        model: "claude-opus-4-7",
        totalEvents: 1,
        totalMessages: 2,
        totalTasks: 1,
        hasCompaction: false,
        ...overrides,
    };
}

function seedUsage(usage: ContextUsage) {
    queryClient.setQueryData(sessionKeys.contextUsage(SESSION_ID, undefined), usage);
    queryClient.setQueryData(["platform", "models", "for-context-usage", "v1"], []);
}

function renderIndicator(agentName?: string) {
    return render(
        <StoryProvider chatContextValues={{ selectedAgentName: agentName ?? "" }}>
            <ContextUsageIndicator sessionId={SESSION_ID} />
        </StoryProvider>
    );
}

function renderInToolbar() {
    return render(
        <StoryProvider chatContextValues={{ selectedAgentName: "" }}>
            <input aria-label="Message" />
            <ContextUsageIndicator sessionId={SESSION_ID} />
            <button type="button">Next toolbar item</button>
        </StoryProvider>
    );
}

function wait(ms: number) {
    return new Promise(resolve => setTimeout(resolve, ms));
}

function findRing(percentage?: number) {
    return screen.findByRole("button", { name: percentage === undefined ? /^Context usage:/ : `Context usage: ${percentage}%` });
}

beforeEach(() => {
    queryClient.clear();
    getSessionContextUsageMock.mockReset();
    fetchModelConfigsMock.mockReset();
    fetchModelConfigsMock.mockResolvedValue([]);
    isMobileMock.mockReturnValue(false);
});

afterEach(() => {
    queryClient.clear();
    vi.restoreAllMocks();
});

describe("ContextUsageIndicator — divergent SSE-driven update path", () => {
    test("re-renders from React Query cache when SSE handler writes a new context_usage block — no extra HTTP", async () => {
        seedUsage(makeUsage({ currentContextTokens: tokensAt(60), totalMessages: 2 }));

        renderIndicator();

        expect(await findRing(60)).toBeInTheDocument();

        act(() => {
            queryClient.setQueryData(sessionKeys.contextUsage(SESSION_ID, undefined), makeUsage({ currentContextTokens: tokensAt(85), totalMessages: 6 }));
        });

        expect(await findRing(85)).toBeInTheDocument();

        expect(getSessionContextUsageMock).not.toHaveBeenCalled();
    });

    test("falls back silently to cached value when an event omits context_usage", async () => {
        seedUsage(makeUsage({ currentContextTokens: tokensAt(60), totalMessages: 4 }));

        renderIndicator();

        expect(await findRing(60)).toBeInTheDocument();

        await new Promise(r => setTimeout(r, 0));
        expect(await findRing(60)).toBeInTheDocument();
        expect(getSessionContextUsageMock).not.toHaveBeenCalled();
    });
});

describe("ContextUsageIndicator — visibility threshold", () => {
    test("stays hidden below 50% usage", async () => {
        seedUsage(makeUsage({ currentContextTokens: tokensAt(49) }));

        renderIndicator();

        await new Promise(r => setTimeout(r, 0));
        expect(screen.queryByRole("button", { name: /^Context usage:/ })).not.toBeInTheDocument();
    });

    test("appears once usage reaches 50%", async () => {
        seedUsage(makeUsage({ currentContextTokens: tokensAt(50) }));

        renderIndicator();

        expect(await findRing(50)).toBeInTheDocument();
    });

    test("hides when maxInputTokens is null (model context limit unknown)", async () => {
        seedUsage(makeUsage({ maxInputTokens: null }));

        renderIndicator();

        await new Promise(r => setTimeout(r, 0));
        expect(screen.queryByRole("button", { name: /^Context usage:/ })).not.toBeInTheDocument();
    });
});

describe("ContextUsageIndicator — ring trigger and hover panel", () => {
    test("ring trigger is a real button and clicking opens the expanded panel", async () => {
        seedUsage(makeUsage());

        renderIndicator();

        const trigger = await findRing(60);
        expect(trigger.tagName).toBe("BUTTON");
        expect(trigger).toHaveAttribute("aria-expanded", "false");

        fireEvent.click(trigger);
        await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "true"));
    });

    test("hovering the ring opens the panel and leaving closes it after the grace period", async () => {
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderIndicator();

        const trigger = await findRing(60);
        await user.hover(trigger);
        expect(await screen.findByText(/Session total/i)).toBeInTheDocument();

        await user.unhover(trigger);
        await waitFor(() => expect(screen.queryByText(/Session total/i)).not.toBeInTheDocument());
        expect(trigger).toHaveAttribute("aria-expanded", "false");
    });

    test("moving from the ring into the panel keeps it open", async () => {
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderIndicator();

        const trigger = await findRing(60);
        await user.hover(trigger);
        const panelText = await screen.findByText(/Session total/i);

        await user.unhover(trigger);
        await user.hover(panelText);
        await new Promise(r => setTimeout(r, 300));
        expect(screen.getByText(/Session total/i)).toBeInTheDocument();
    });

    test("leaving the panel itself closes it after the grace period", async () => {
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderIndicator();

        const trigger = await findRing(60);
        await user.hover(trigger);
        const panelText = await screen.findByText(/Session total/i);
        await user.hover(panelText);
        await user.unhover(panelText);

        await waitFor(() => expect(screen.queryByText(/Session total/i)).not.toBeInTheDocument());
        expect(trigger).toHaveAttribute("aria-expanded", "false");
    });

    test("hovering the ring leaves focus in the chat input", async () => {
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderInToolbar();

        const input = screen.getByRole("textbox", { name: "Message" });
        input.focus();
        const ring = await findRing(60);
        await user.hover(ring);
        await screen.findByText(/Session total/i);
        expect(input).toHaveFocus();

        await user.unhover(ring);
        await waitFor(() => expect(ring).toHaveAttribute("aria-expanded", "false"));
        await act(() => wait(0));
        expect(input).toHaveFocus();
    });

    test("a click that doesn't move focus (Safari) leaves focus in the chat input when the panel closes", async () => {
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderInToolbar();

        const input = screen.getByRole("textbox", { name: "Message" });
        input.focus();
        const ring = await findRing(60);
        await user.hover(ring);
        await screen.findByText(/Session total/i);
        fireEvent.click(ring);
        expect(input).toHaveFocus();

        await user.unhover(ring);
        await waitFor(() => expect(ring).toHaveAttribute("aria-expanded", "false"));
        await act(() => wait(0));
        expect(input).toHaveFocus();
    });

    test("a keyboard open after an earlier hover still moves focus into the panel", async () => {
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderInToolbar();

        const ring = await findRing(60);
        await user.hover(ring);
        await screen.findByText(/Session total/i);
        await user.unhover(ring);
        await waitFor(() => expect(ring).toHaveAttribute("aria-expanded", "false"));
        await act(() => wait(0));

        ring.focus();
        await user.keyboard("{Enter}");

        await waitFor(() => expect(screen.getByRole("button", { name: "Compress Conversation" })).toHaveFocus());
    });

    test("Escape on a keyboard-opened panel returns focus to the ring", async () => {
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderInToolbar();

        const ring = await findRing(60);
        ring.focus();
        await user.keyboard("{Enter}");
        await waitFor(() => expect(screen.getByRole("button", { name: "Compress Conversation" })).toHaveFocus());

        await user.keyboard("{Escape}");
        await user.keyboard("{Escape}");
        await waitFor(() => expect(ring).toHaveAttribute("aria-expanded", "false"));
        await waitFor(() => expect(ring).toHaveFocus());
    });

    test("keyboard users can open the panel with Enter and land on Compress Conversation", async () => {
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderInToolbar();

        const ring = await findRing(60);
        screen.getByRole("textbox", { name: "Message" }).focus();
        await user.tab();
        expect(ring).toHaveFocus();

        await user.keyboard("{Enter}");

        await waitFor(() => expect(screen.getByRole("button", { name: "Compress Conversation" })).toHaveFocus());
        expect(ring).toHaveAttribute("aria-expanded", "true");
    });

    test("never opens the panel on mobile, by hover or by click", async () => {
        isMobileMock.mockReturnValue(true);
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderIndicator();

        const ring = await findRing(60);
        await user.hover(ring);
        await user.click(ring);
        await act(() => wait(300));

        expect(ring).toHaveAttribute("aria-expanded", "false");
        expect(screen.queryByText(/Session total/i)).not.toBeInTheDocument();
    });

    test("a tap on mobile doesn't leave the panel queued to open on a wider viewport", async () => {
        isMobileMock.mockReturnValue(true);
        seedUsage(makeUsage());
        const user = userEvent.setup();

        const view = renderIndicator();

        await user.click(await findRing(60));
        isMobileMock.mockReturnValue(false);
        view.rerender(
            <StoryProvider chatContextValues={{ selectedAgentName: "" }}>
                <ContextUsageIndicator sessionId={SESSION_ID} />
            </StoryProvider>
        );

        await act(() => wait(0));
        expect(await findRing(60)).toHaveAttribute("aria-expanded", "false");
    });

    test("clicking the ring after hovering keeps the panel open instead of toggling it closed", async () => {
        seedUsage(makeUsage());
        const user = userEvent.setup();

        renderIndicator();

        const trigger = await findRing(60);
        await user.hover(trigger);
        await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "true"));

        await user.click(trigger);
        expect(trigger).toHaveAttribute("aria-expanded", "true");
    });

    test("expanded panel is portaled outside the trigger container (so an overflow-hidden ancestor can't clip it)", async () => {
        seedUsage(makeUsage({ totalMessages: 6 }));

        const { container } = renderIndicator();

        fireEvent.click(await findRing(60));

        const panelText = await screen.findByText(/Session total/i);
        expect(document.body).toContainElement(panelText);
        expect(container).not.toContainElement(panelText);
    });

    test("a pointer-down outside the panel dismisses it (Radix interact-outside)", async () => {
        seedUsage(makeUsage({ currentContextTokens: tokensAt(85), totalMessages: 6 }));

        renderIndicator();

        const toggle = await findRing(85);
        fireEvent.click(toggle);
        await waitFor(() => expect(toggle).toHaveAttribute("aria-expanded", "true"));

        const outside = document.createElement("button");
        document.body.appendChild(outside);
        fireEvent.pointerDown(outside);
        fireEvent.click(outside);

        await waitFor(() => expect(toggle).toHaveAttribute("aria-expanded", "false"));
        outside.remove();
    });
});

describe("ContextUsageIndicator — compaction", () => {
    test("toolbar compress icon → confirm → success banner is shown and dismissable", async () => {
        const initial = makeUsage({ currentContextTokens: tokensAt(85), totalMessages: 6 });
        vi.spyOn(api.webui, "get").mockResolvedValue(initial as unknown as Response);
        vi.spyOn(api.webui, "post").mockResolvedValue({ eventsCompacted: 3, remainingTokens: 1000 } as unknown as Response);
        seedUsage(initial);

        const user = userEvent.setup();
        renderIndicator();

        const icon = await screen.findByRole("button", { name: "Compress conversation" });
        await user.click(icon);

        await user.click(await screen.findByTestId("dialogConfirmButton"));

        const banner = await screen.findByText(/Compacted 3 events/i);
        expect(banner).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: /Dismiss/i }));
        await waitFor(() => expect(screen.queryByText(/Compacted 3 events/i)).not.toBeInTheDocument());
    });

    test("stays mounted to show the success banner when compaction drops usage below 50%", async () => {
        const before = makeUsage({ currentContextTokens: tokensAt(85), totalMessages: 6 });
        const after = makeUsage({ currentContextTokens: tokensAt(10), totalMessages: 6 });
        vi.spyOn(api.webui, "get").mockResolvedValue(after as unknown as Response);
        vi.spyOn(api.webui, "post").mockResolvedValue({ eventsCompacted: 5, remainingTokens: tokensAt(10) } as unknown as Response);
        seedUsage(before);

        const user = userEvent.setup();
        renderIndicator();

        const trigger = await findRing(85);
        await user.hover(trigger);
        await user.click(await screen.findByRole("button", { name: "Compress Conversation" }));
        await user.click(await screen.findByTestId("dialogConfirmButton"));

        expect(await screen.findByText(/Compacted 5 events/i)).toBeInTheDocument();
        expect(await findRing(10)).toBeInTheDocument();

        await user.click(screen.getByRole("button", { name: /Dismiss/i }));
        await waitFor(() => expect(screen.queryByRole("button", { name: /^Context usage:/ })).not.toBeInTheDocument());

        act(() => {
            queryClient.setQueryData(sessionKeys.contextUsage(SESSION_ID, undefined), makeUsage({ currentContextTokens: tokensAt(70), totalMessages: 6 }));
        });
        const ring = await findRing(70);
        expect(ring).toHaveAttribute("aria-expanded", "false");
        expect(screen.queryByText(/Session total/i)).not.toBeInTheDocument();
    });

    test("keeps the panel open while the confirmation dialog is up, even after the pointer leaves", async () => {
        vi.spyOn(api.webui, "post").mockReturnValue(new Promise(() => {}) as never);
        seedUsage(makeUsage({ currentContextTokens: tokensAt(60), totalMessages: 6 }));
        const user = userEvent.setup({ pointerEventsCheck: 0 });

        renderIndicator();

        await user.hover(await findRing(60));
        const panelText = await screen.findByText(/Session total/i);
        await user.click(screen.getByRole("button", { name: "Compress Conversation" }));
        await screen.findByTestId("dialogConfirmButton");

        await user.unhover(panelText);
        await act(() => wait(300));
        expect(screen.getByText(/Session total/i)).toBeInTheDocument();
    });

    test("keeps the panel open while compressing after the dialog is escaped and the pointer leaves", async () => {
        vi.spyOn(api.webui, "post").mockReturnValue(new Promise(() => {}) as never);
        seedUsage(makeUsage({ currentContextTokens: tokensAt(60), totalMessages: 6 }));
        const user = userEvent.setup({ pointerEventsCheck: 0 });

        renderIndicator();

        await user.hover(await findRing(60));
        const panelText = await screen.findByText(/Session total/i);
        await user.click(screen.getByRole("button", { name: "Compress Conversation" }));
        await user.click(await screen.findByTestId("dialogConfirmButton"));
        await screen.findAllByText(/Compressing/i);
        await user.keyboard("{Escape}");
        await waitFor(() => expect(screen.queryByTestId("dialogConfirmButton")).not.toBeInTheDocument());

        await user.hover(panelText);
        await user.unhover(panelText);
        await act(() => wait(300));
        expect(screen.getByText(/Session total/i)).toBeInTheDocument();
    });

    test("a failed compaction started from the toolbar icon opens the panel to show the error", async () => {
        vi.spyOn(api.webui, "post").mockRejectedValue(new Error("compaction blew up"));
        seedUsage(makeUsage({ currentContextTokens: tokensAt(85), totalMessages: 6 }));
        const user = userEvent.setup();

        renderIndicator();

        const ring = await findRing(85);
        expect(ring).toHaveAttribute("aria-expanded", "false");
        await user.click(screen.getByRole("button", { name: "Compress conversation" }));
        await user.click(await screen.findByTestId("dialogConfirmButton"));

        expect(await screen.findByRole("alert")).toHaveTextContent(/compaction blew up/i);
        expect(ring).toHaveAttribute("aria-expanded", "true");
    });

    test("failed compaction shows a dismissable error banner", async () => {
        vi.spyOn(api.webui, "post").mockRejectedValue(new Error("compaction blew up"));
        seedUsage(makeUsage({ currentContextTokens: tokensAt(85), totalMessages: 6 }));

        const user = userEvent.setup();
        renderIndicator();

        await user.click(await findRing(85));

        await user.click(await screen.findByRole("button", { name: "Compress Conversation" }));

        await user.click(await screen.findByTestId("dialogConfirmButton"));

        const banner = await screen.findByText(/compaction blew up/i);
        expect(banner).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: /Dismiss/i }));
        await waitFor(() => expect(screen.queryByText(/compaction blew up/i)).not.toBeInTheDocument());
    });
});
