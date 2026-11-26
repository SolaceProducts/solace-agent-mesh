// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { CrossUserFeedback, FeedbackHistoryResponse } from "@/api/feedback";
import * as service from "@/api/feedback/service";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { RECENT_FETCH_SIZE, RecentFeedbackList } from "./RecentFeedbackList";

const DAY_MS = 24 * 60 * 60 * 1000;

afterEach(() => {
    vi.restoreAllMocks();
});

const row = (over: Partial<CrossUserFeedback>): CrossUserFeedback => ({
    id: "open",
    sessionId: "s",
    taskId: "t",
    userId: "u",
    rating: "down",
    comment: "open comment",
    shareTaskContent: false,
    subjectId: "a-1",
    subjectType: "agent",
    subjectName: "Agent One",
    entrypointType: "webui",
    createdAt: "2026-08-25T09:42:00.000Z",
    ...over,
});

const page = (data: CrossUserFeedback[]): FeedbackHistoryResponse => ({
    data,
    meta: { pagination: { pageNumber: 1, pageSize: RECENT_FETCH_SIZE, count: data.length, totalPages: 1, nextPage: null } },
});

function renderList(feedback: CrossUserFeedback, handlers: Partial<{ onSelect: (r: CrossUserFeedback) => void; onViewMore: () => void }> = {}) {
    const onSelect = handlers.onSelect ?? vi.fn();
    const onViewMore = handlers.onViewMore ?? vi.fn();
    const utils = render(
        <StoryProvider shouldIsolateQueryClient>
            <RecentFeedbackList feedback={feedback} onSelect={onSelect} onViewMore={onViewMore} />
        </StoryProvider>
    );
    return { ...utils, onSelect, onViewMore };
}

describe("RecentFeedbackList", () => {
    it("requests this subject's last 7 days, both ratings", async () => {
        const spy = vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([]));
        renderList(row({}));

        await waitFor(() => expect(spy).toHaveBeenCalled());
        const params = spy.mock.calls[0][0];
        expect(params.agentIds).toEqual(["a-1"]);
        expect(params.workflowIds).toBeUndefined();
        expect(params.rating).toBeUndefined();
        expect(params.pageNumber).toBe(1);
        expect(params.pageSize).toBe(RECENT_FETCH_SIZE);
        const daysAgo = (Date.now() - new Date(params.startDate as string).getTime()) / DAY_MS;
        expect(daysAgo).toBeGreaterThan(6.9);
        expect(daysAgo).toBeLessThan(7.1);
    });

    it("filters a workflow subject by workflowIds", async () => {
        const spy = vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([]));
        renderList(row({ subjectType: "workflow", subjectId: "wf-1" }));

        await waitFor(() => expect(spy).toHaveBeenCalled());
        expect(spy.mock.calls[0][0].workflowIds).toEqual(["wf-1"]);
        expect(spy.mock.calls[0][0].agentIds).toBeUndefined();
    });

    it("excludes the open item and caps the list at five", async () => {
        const others = [1, 2, 3, 4, 5].map(n => row({ id: `r${n}`, comment: `other ${n}` }));
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([row({}), ...others]));
        renderList(row({}));

        await waitFor(() => expect(screen.getByText("other 5")).toBeInTheDocument());
        expect(screen.queryByText("open comment")).not.toBeInTheDocument();
        expect(screen.getAllByRole("button", { name: /other \d/ })).toHaveLength(5);
    });

    it("caps at five when the open item is not in the response", async () => {
        const others = [1, 2, 3, 4, 5, 6].map(n => row({ id: `r${n}`, comment: `other ${n}` }));
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page(others));
        renderList(row({}));

        await waitFor(() => expect(screen.getByText("other 1")).toBeInTheDocument());
        expect(screen.getAllByRole("button", { name: /other \d/ })).toHaveLength(5);
    });

    it("renders an em-dash for an item with no comment", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([row({ id: "r1", comment: null })]));
        renderList(row({}));

        await waitFor(() => expect(screen.getByRole("button", { name: /—/ })).toBeInTheDocument());
    });

    it("carries the year on each item's time title, not in the stamp", async () => {
        vi.stubEnv("TZ", "UTC");
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([row({ id: "r1", comment: "other 1", createdAt: "2026-08-25T09:42:00.000Z" })]));
        renderList(row({}));

        const stamp = await screen.findByTitle(/2026/);
        expect(stamp.textContent).not.toContain("2026");

        vi.unstubAllEnvs();
    });

    it("shows the empty state when nothing else arrived in the window", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([row({})]));
        renderList(row({}));

        await waitFor(() => expect(screen.getByText("No other feedback in the last 7 days")).toBeInTheDocument());
    });

    it("shows an inline error and no list when the request fails", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockRejectedValue(new Error("boom"));
        renderList(row({}));

        await waitFor(() => expect(screen.getByText("Couldn't load recent feedback")).toBeInTheDocument());
    });

    it("renders nothing for an unknown subject and never fetches", () => {
        const spy = vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([]));
        renderList(row({ subjectType: "unknown", subjectId: "unknown" }));

        expect(screen.queryByText("Recent Feedback (7 Days)")).not.toBeInTheDocument();
        expect(spy).not.toHaveBeenCalled();
    });

    it("hands the clicked item to onSelect", async () => {
        const user = userEvent.setup();
        const other = row({ id: "r1", comment: "other 1" });
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([other]));
        const { onSelect } = renderList(row({}));

        await user.click(await screen.findByRole("button", { name: /other 1/ }));
        expect(onSelect).toHaveBeenCalledWith(other);
    });

    it("selects the item when its time or rating is clicked", async () => {
        const user = userEvent.setup();
        const other = row({ id: "r1", comment: "other 1", rating: "up" });
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([other]));
        const { onSelect } = renderList(row({}));

        const item = await screen.findByRole("button", { name: /other 1/ });
        await user.click(within(item).getByLabelText("Positive"));
        expect(onSelect).toHaveBeenCalledWith(other);
        expect(item).toHaveClass("cursor-pointer");
    });

    it("calls onViewMore", async () => {
        const user = userEvent.setup();
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([row({ id: "r1", comment: "other 1" })]));
        const { onViewMore } = renderList(row({}));

        await user.click(await screen.findByRole("button", { name: "Filter by Agent" }));
        expect(onViewMore).toHaveBeenCalled();
    });

    it("draws the filter action as a button, not as link text", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([row({ id: "r1", comment: "other 1" })]));
        renderList(row({}));

        const action = await screen.findByRole("button", { name: "Filter by Agent" });
        expect(action.className).toContain("border");
        expect(action.className).not.toMatch(/\bunderline\b/);
    });

    it("names the subject's own kind in the action", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([row({ id: "r1", comment: "other 1", subjectType: "workflow" })]));
        renderList(row({ subjectType: "workflow" }));

        expect(await screen.findByRole("button", { name: "Filter by Workflow" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Filter by Agent" })).not.toBeInTheDocument();
    });

    it("drops the action when there is no handler for it", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(page([row({ id: "r1", comment: "other 1" })]));
        render(
            <StoryProvider shouldIsolateQueryClient>
                <RecentFeedbackList feedback={row({})} onSelect={vi.fn()} />
            </StoryProvider>
        );

        expect(await screen.findByText("other 1")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /^Filter by/ })).not.toBeInTheDocument();
    });

    it("shows a skeleton rather than the previous subject's items while switching", async () => {
        let resolveB: (v: FeedbackHistoryResponse) => void = () => {};
        vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(params => (params.agentIds?.[0] === "a-1" ? Promise.resolve(page([row({ id: "r1", comment: "from A" })])) : new Promise(resolve => (resolveB = resolve))));
        const { rerender } = renderList(row({}));
        await waitFor(() => expect(screen.getByText("from A")).toBeInTheDocument());

        rerender(
            <StoryProvider shouldIsolateQueryClient>
                <RecentFeedbackList feedback={row({ id: "openB", subjectId: "a-2", subjectName: "Agent Two" })} onSelect={vi.fn()} onViewMore={vi.fn()} />
            </StoryProvider>
        );

        await waitFor(() => expect(screen.queryByText("from A")).not.toBeInTheDocument());
        expect(screen.getByTestId("recent-feedback-loading")).toBeInTheDocument();
        resolveB(page([row({ id: "r2", subjectId: "a-2", comment: "from B" })]));
        await waitFor(() => expect(screen.getByText("from B")).toBeInTheDocument());
    });
});
