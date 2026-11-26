// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { CrossUserFeedback } from "@/api/feedback";
import * as service from "@/api/feedback/service";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { FeedbackDetailPanel } from "./FeedbackDetailPanel";

const row = (over: Partial<CrossUserFeedback>): CrossUserFeedback => ({
    id: "f1",
    sessionId: "s",
    taskId: "task-1",
    userId: "user-123",
    rating: "down",
    comment: "gave me last year's limit",
    shareTaskContent: false,
    subjectId: "a-1",
    subjectType: "agent",
    subjectName: "Procurement Intake Router",
    entrypointType: "slack",
    createdAt: "2026-08-25T09:42:00.000Z",
    ...over,
});

function renderPanel(feedback: CrossUserFeedback, onClose = vi.fn(), onStartActivityChat = vi.fn(), canStartActivityChat = true) {
    render(
        <StoryProvider shouldIsolateQueryClient>
            <FeedbackDetailPanel feedback={feedback} onClose={onClose} onSelect={vi.fn()} onViewMore={vi.fn()} onStartActivityChat={onStartActivityChat} canStartActivityChat={canStartActivityChat} />
        </StoryProvider>
    );
    return { onClose, onStartActivityChat };
}

beforeEach(() => {});

afterEach(() => vi.restoreAllMocks());

describe("FeedbackDetailPanel", () => {
    it.each([
        ["agent", "lucide-bot"],
        ["workflow", "lucide-workflow"],
    ] as const)("marks a %s subject in the heading", (subjectType, glyph) => {
        renderPanel(row({ subjectType, subjectName: "Subject One" }));

        expect(screen.getByRole("heading", { level: 2 }).querySelector(`.${glyph}`)).toBeInTheDocument();
    });

    it("leaves an unattributed subject unglyphed", () => {
        renderPanel(row({ subjectType: "unknown", subjectName: "" }));

        const heading = screen.getByRole("heading", { level: 2 });
        expect(heading).toHaveTextContent("Unknown");
        expect(heading.querySelector("svg")).not.toBeInTheDocument();
    });

    beforeEach(() => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 6, count: 0, totalPages: 1, nextPage: null } } });
    });

    afterEach(() => vi.restoreAllMocks());

    it("titles the panel with the subject name", () => {
        renderPanel(row({}));
        expect(screen.getByRole("heading", { level: 2, name: "Procurement Intake Router" })).toBeInTheDocument();
    });

    it("falls back to the subject id when the name is unresolved", () => {
        renderPanel(row({ subjectName: "" }));
        expect(screen.getByRole("heading", { level: 2, name: "a-1" })).toBeInTheDocument();
    });

    it("titles an unattributable row Unknown", () => {
        renderPanel(row({ subjectType: "unknown", subjectId: "unknown", subjectName: "" }));
        expect(screen.getByRole("heading", { level: 2, name: "Unknown" })).toBeInTheDocument();
    });

    it("shows user, time, rating and entrypoint", () => {
        renderPanel(row({}));
        const details = screen.getByRole("region", { name: /Feedback Details/ });
        expect(within(details).getByText("user-123")).toBeInTheDocument();
        expect(within(details).getByText(/Aug 25/)).toBeInTheDocument();
        expect(within(details).getByLabelText("Negative")).toBeInTheDocument();
        expect(within(details).getByText("Slack", { selector: "div" })).toBeInTheDocument();
        expect(within(details).getByTestId("entrypoint-icon")).toBeInTheDocument();
    });

    it("shows Unknown with no icon for an unmapped entrypoint", () => {
        renderPanel(row({ entrypointType: "unknown" }));
        const details = screen.getByRole("region", { name: /Feedback Details/ });
        expect(within(details).getByText("Unknown")).toBeInTheDocument();
        expect(within(details).queryByTestId("entrypoint-icon")).not.toBeInTheDocument();
    });

    it("keeps the whole comment in the DOM with line breaks preserved", () => {
        const long = `first line ${"x".repeat(600)}\nsecond line`;
        renderPanel(row({ comment: long }));
        const comment = screen.getByTestId("feedback-comment");
        expect(comment.textContent).toBe(long);
        expect(comment).toHaveClass("whitespace-pre-wrap");
        expect(comment).not.toHaveClass("truncate");
    });

    it("renders an em-dash for a missing comment", () => {
        renderPanel(row({ comment: null }));
        expect(screen.getByTestId("feedback-comment")).toHaveTextContent("—");
    });

    it.each([true, false])("shows the trace button whenever a task id exists (shareTaskContent=%s)", share => {
        renderPanel(row({ shareTaskContent: share }));
        expect(screen.getByRole("button", { name: "Ask Activity Monitor" })).toBeInTheDocument();
    });

    it("hides the trace button without a task id", () => {
        renderPanel(row({ taskId: "" }));
        expect(screen.queryByRole("button", { name: "Ask Activity Monitor" })).not.toBeInTheDocument();
    });

    it("calls onClose from the close button", async () => {
        const user = userEvent.setup();
        const { onClose } = renderPanel(row({}));
        await user.click(screen.getByRole("button", { name: "Close feedback details" }));
        expect(onClose).toHaveBeenCalled();
    });

    it("hosts the recent feedback section", async () => {
        renderPanel(row({}));
        expect(await screen.findByRole("heading", { name: "Recent Feedback (7 Days)" })).toBeInTheDocument();
    });

    it("carries the year on the time's hover title, not in the field", () => {
        vi.stubEnv("TZ", "UTC");

        renderPanel(row({ createdAt: "2026-08-25T09:42:00.000Z" }));

        const time = screen.getByTitle(/2026/);
        expect(time).toBeInTheDocument();
        expect(time.textContent).not.toContain("2026");

        vi.unstubAllEnvs();
    });
});

describe("FeedbackDetailPanel long comments", () => {
    function simulateOverflow(isOverflowing: boolean) {
        vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(isOverflowing ? 400 : 100);
        vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(100);
    }

    beforeEach(() => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 6, count: 0, totalPages: 1, nextPage: null } } });
        vi.stubGlobal(
            "ResizeObserver",
            class {
                observe() {}
                disconnect() {}
            }
        );
    });

    afterEach(() => {
        vi.restoreAllMocks();
        vi.unstubAllGlobals();
    });

    it("offers no toggle when the comment fits", () => {
        simulateOverflow(false);
        renderPanel(row({}));
        expect(screen.queryByRole("button", { name: "Read More" })).not.toBeInTheDocument();
    });

    it("clamps an overflowing comment and expands and collapses it", async () => {
        const user = userEvent.setup();
        simulateOverflow(true);
        renderPanel(row({ comment: "para one\n\npara two\n\npara three" }));
        const comment = screen.getByTestId("feedback-comment");
        expect(comment).toHaveClass("line-clamp-6");

        await user.click(screen.getByRole("button", { name: "Read More" }));
        expect(comment).not.toHaveClass("line-clamp-6");
        expect(screen.getByRole("button", { name: "Read Less" })).toHaveAttribute("aria-expanded", "true");

        await user.click(screen.getByRole("button", { name: "Read Less" }));
        expect(comment).toHaveClass("line-clamp-6");
    });

    it("collapses again when a different feedback is shown", async () => {
        const user = userEvent.setup();
        simulateOverflow(true);
        const { rerender } = render(
            <StoryProvider shouldIsolateQueryClient>
                <FeedbackDetailPanel feedback={row({})} onClose={vi.fn()} onSelect={vi.fn()} onViewMore={vi.fn()} onStartActivityChat={vi.fn()} canStartActivityChat />
            </StoryProvider>
        );
        await user.click(screen.getByRole("button", { name: "Read More" }));
        expect(screen.getByTestId("feedback-comment")).not.toHaveClass("line-clamp-6");

        rerender(
            <StoryProvider shouldIsolateQueryClient>
                <FeedbackDetailPanel feedback={row({ id: "f2", comment: "another long one" })} onClose={vi.fn()} onSelect={vi.fn()} onViewMore={vi.fn()} onStartActivityChat={vi.fn()} canStartActivityChat />
            </StoryProvider>
        );
        expect(screen.getByTestId("feedback-comment")).toHaveClass("line-clamp-6");
        expect(screen.getByRole("button", { name: "Read More" })).toBeInTheDocument();
    });

    it("offers no toggle for a missing comment", () => {
        simulateOverflow(true);
        renderPanel(row({ comment: null }));
        expect(screen.queryByRole("button", { name: "Read More" })).not.toBeInTheDocument();
    });
});

describe("FeedbackDetailPanel Activity Monitor action", () => {
    it("starts the chat when the caller may read the task behind the row", async () => {
        const { onStartActivityChat } = renderPanel(row({}));
        const button = await screen.findByRole("button", { name: "Ask Activity Monitor" });
        expect(button).toBeEnabled();
        await userEvent.click(button);
        expect(onStartActivityChat).toHaveBeenCalledTimes(1);
    });

    it("renders nothing for a row with no task", () => {
        renderPanel(row({ taskId: "" }));
        expect(screen.queryByRole("button", { name: "Ask Activity Monitor" })).not.toBeInTheDocument();
    });

    it("withholds the button entirely when the page says this caller cannot use the handoff", async () => {
        renderPanel(row({}), vi.fn(), vi.fn(), false);
        await screen.findByTestId("feedback-comment");
        expect(screen.queryByRole("button", { name: "Ask Activity Monitor" })).not.toBeInTheDocument();
    });

    it("explains nothing in place of the withheld button", async () => {
        renderPanel(row({}), vi.fn(), vi.fn(), false);
        await screen.findByTestId("feedback-comment");
        expect(screen.queryByText(/activity access/i)).not.toBeInTheDocument();
    });
});
