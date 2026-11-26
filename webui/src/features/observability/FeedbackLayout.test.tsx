// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { CrossUserFeedback, FeedbackHistoryResponse, FeedbackSummariesParams, FeedbackSummariesResponse, FeedbackSummaryRow } from "@/api/feedback";
import * as service from "@/api/feedback/service";

import type { ConfigContextValue } from "@/providers/contexts/ConfigContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { formatDay } from "./dashboardFilters";
import { FeedbackDashboardPage } from "./FeedbackDashboardPage";
import { FeedbackHistoryPage } from "./FeedbackHistoryPage";
import { FeedbackLayout } from "./FeedbackLayout";
import { retentionFloorDate } from "./retentionLimit";

const emptyHistoryPage: FeedbackHistoryResponse = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 20, count: 0, totalPages: 1, nextPage: null } } };

const historyRow: CrossUserFeedback = {
    id: "1",
    sessionId: "s1",
    taskId: "t1",
    userId: "u1",
    rating: "up",
    comment: null,
    shareTaskContent: false,
    subjectId: "agent-1",
    subjectType: "agent",
    subjectName: "Agent One",
    entrypointType: "chat",
    createdAt: new Date().toISOString(),
};

const tenantRow: FeedbackSummaryRow = { totalActivities: 12406, positiveFeedback: 145, negativeFeedback: 14, negativeRatio: 0.00113, trend: [] };

const summaryPageOf = (rows: FeedbackSummaryRow[]): FeedbackSummariesResponse => ({
    data: rows,
    meta: { pagination: { pageNumber: 1, pageSize: 100, count: rows.length, totalPages: 1, nextPage: null } },
});

function renderAt(path: string, configContextValues?: Partial<ConfigContextValue>) {
    return render(
        <StoryProvider configContextValues={configContextValues}>
            <MemoryRouter initialEntries={[path]}>
                <Routes>
                    <Route path="/feedback" element={<FeedbackLayout />}>
                        <Route index element={<FeedbackDashboardPage />} />
                        <Route path="history" element={<FeedbackHistoryPage />} />
                    </Route>
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("FeedbackLayout", () => {
    afterEach(() => vi.restoreAllMocks());

    it("renders both tabs under a Feedback title", () => {
        renderAt("/feedback");

        expect(screen.getByText("Feedback")).toBeInTheDocument();
        expect(screen.getByText("Dashboard")).toBeInTheDocument();
        expect(screen.getByText("Feedback History")).toBeInTheDocument();
    });

    it("shows the dashboard filters on the index route", () => {
        renderAt("/feedback");

        expect(screen.getByLabelText("Agent / Workflow")).toBeInTheDocument();
    });

    it("shows the history table view on the history route", () => {
        renderAt("/feedback/history");

        expect(screen.getByTestId("feedback-history-page")).toBeInTheDocument();
    });

    it("navigates to the history tab when it is clicked", async () => {
        const user = userEvent.setup();
        renderAt("/feedback");

        await user.click(screen.getByText("Feedback History"));

        expect(screen.getByTestId("feedback-history-page")).toBeInTheDocument();
    });

    it("marks the dashboard tab selected on the index route", () => {
        renderAt("/feedback");

        expect(screen.getByRole("tab", { name: "Dashboard" })).toHaveAttribute("aria-selected", "true");
        expect(screen.getByRole("tab", { name: "Feedback History" })).toHaveAttribute("aria-selected", "false");
    });

    it("marks the history tab selected on the history route", () => {
        renderAt("/feedback/history");

        expect(screen.getByRole("tab", { name: "Feedback History" })).toHaveAttribute("aria-selected", "true");
        expect(screen.getByRole("tab", { name: "Dashboard" })).toHaveAttribute("aria-selected", "false");
    });

    it("forwards the current query string when switching tabs", async () => {
        const user = userEvent.setup();
        renderAt("/feedback?subject=agent:a-1&name=Agent+One&range=7d");

        await user.click(screen.getByText("Feedback History"));

        expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Agent One");
        expect(screen.getByLabelText("Date Range")).toHaveTextContent("Last 7 days");
    });

    it("carries Rating through the Dashboard round trip via History's private filter memory, without exposing it on Dashboard", async () => {
        const user = userEvent.setup();
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(emptyHistoryPage);
        renderAt("/feedback/history");

        await user.click(screen.getByLabelText("Filter by rating"));
        await user.click(await screen.findByText("Negative"));
        await waitFor(() => expect(screen.getByLabelText("Filter by rating")).toHaveTextContent("Negative"));

        await user.click(screen.getByText("Dashboard"));
        expect(screen.queryByLabelText("Filter by rating")).not.toBeInTheDocument();

        await user.click(screen.getByText("Feedback History"));
        await waitFor(() => expect(screen.getByLabelText("Filter by rating")).toHaveTextContent("Negative"));
    });

    it("carries a real Agent/Workflow selection and a Date Range selection made on History over to Dashboard", async () => {
        const user = userEvent.setup();
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue({ ...emptyHistoryPage, data: [historyRow], meta: { pagination: { ...emptyHistoryPage.meta.pagination, count: 1 } } });
        renderAt("/feedback/history");

        await user.click(screen.getByLabelText("Date Range"));
        await user.click(await screen.findByRole("button", { name: "Last 7 days" }));
        await waitFor(() => expect(screen.getByLabelText("Date Range")).toHaveTextContent("Last 7 days"));

        await user.click(screen.getByLabelText("Agent / Workflow"));
        await user.click(await screen.findByRole("option", { name: "Agent One" }));
        await waitFor(() => expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Agent One"));

        await user.click(screen.getByText("Dashboard"));

        await waitFor(() => expect(screen.getByLabelText("Date Range")).toHaveTextContent("Last 7 days"));
        await waitFor(() => expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Agent One"));
    });

    it("carries a real Agent/Workflow selection made on Dashboard over to History", async () => {
        const user = userEvent.setup();
        vi.spyOn(service, "fetchFeedbackSummaries").mockImplementation(async (params: FeedbackSummariesParams) => {
            if (params.groupBy === "tenant") return summaryPageOf([tenantRow]);
            return summaryPageOf([{ subject: { id: "a1", name: "Agent One", type: "agent" }, totalActivities: 10, positiveFeedback: 1, negativeFeedback: 0, negativeRatio: 0, trend: [] }]);
        });
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(emptyHistoryPage);
        renderAt("/feedback");
        await screen.findByText("12,406");

        await user.click(screen.getByLabelText("Agent / Workflow"));
        await user.click(await screen.findByRole("option", { name: "Agent One" }));
        await waitFor(() => expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Agent One"));

        await user.click(screen.getByText("Feedback History"));

        await waitFor(() => expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Agent One"));
    });

    describe("carrying a custom date range between tabs", () => {
        const from = retentionFloorDate(10)!;
        const to = retentionFloorDate(3)!;
        const customRange = `range=${from}..${to}`;

        const expectTriggerToRead = (first: string, last: string) => {
            const trigger = screen.getByLabelText("Date Range");
            expect(trigger).toHaveTextContent(formatDay(first));
            expect(trigger).toHaveTextContent(formatDay(last));
        };

        const mockBothTabs = () => {
            vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(emptyHistoryPage);
            vi.spyOn(service, "fetchFeedbackSummaries").mockImplementation(async (params: FeedbackSummariesParams) => {
                if (params.groupBy === "tenant") return summaryPageOf([tenantRow]);
                return summaryPageOf([{ subject: { id: "a1", name: "Agent One", type: "agent" }, totalActivities: 10, positiveFeedback: 1, negativeFeedback: 0, negativeRatio: 0, trend: [] }]);
            });
        };

        it("carries a custom range set on History over to Dashboard", async () => {
            const user = userEvent.setup();
            mockBothTabs();
            renderAt(`/feedback/history?${customRange}`);
            expectTriggerToRead(from, to);

            await user.click(screen.getByText("Dashboard"));

            await waitFor(() => expect(screen.queryByTestId("feedback-history-page")).not.toBeInTheDocument());
            expectTriggerToRead(from, to);
        });

        it("carries a custom range set on Dashboard over to History", async () => {
            const user = userEvent.setup();
            mockBothTabs();
            renderAt(`/feedback?${customRange}`);
            expectTriggerToRead(from, to);

            await user.click(screen.getByText("Feedback History"));

            expect(await screen.findByTestId("feedback-history-page")).toBeInTheDocument();
            expectTriggerToRead(from, to);
        });

        it("keeps the custom range when a Subjects-table row opens that subject's History", async () => {
            const user = userEvent.setup();
            mockBothTabs();
            renderAt(`/feedback?${customRange}`);

            await user.click(await screen.findByRole("row", { name: /Agent One/ }));

            expect(await screen.findByTestId("feedback-history-page")).toBeInTheDocument();
            expectTriggerToRead(from, to);
            expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Agent One");
        });

        it("clamps to Dashboard's shorter retention floor when History's range reaches further back", async () => {
            const user = userEvent.setup();
            mockBothTabs();
            const historyFrom = retentionFloorDate(60)!;
            const dashboardFloor = retentionFloorDate(30)!;
            renderAt(`/feedback/history?range=${historyFrom}..${to}`, { dataRetention: { taskDays: 30, feedbackDays: 90 } });
            expectTriggerToRead(historyFrom, to);

            await user.click(screen.getByText("Dashboard"));

            await waitFor(() => expect(screen.getByLabelText("Date Range")).toHaveTextContent(formatDay(dashboardFloor)));
            expectTriggerToRead(dashboardFloor, to);
            expect(within(screen.getByLabelText("Date Range")).queryByText(formatDay(historyFrom), { exact: false })).not.toBeInTheDocument();
        });
    });
});
