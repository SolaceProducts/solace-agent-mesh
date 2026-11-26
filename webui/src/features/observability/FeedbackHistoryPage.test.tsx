// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useRef } from "react";
import { act, createEvent, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation, useNavigate, useNavigationType } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { CrossUserFeedback, FeedbackHistoryParams, FeedbackHistoryResponse, FeedbackSummariesParams, FeedbackSummariesResponse, FeedbackSummaryRow } from "@/api/feedback";
import * as itemService from "@/api/feedback/item";
import * as service from "@/api/feedback/service";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { formatDay } from "./dashboardFilters";
import { FeedbackDashboardPage } from "./FeedbackDashboardPage";
import { FeedbackFilterMemoryProvider } from "./feedbackFilterMemory";
import { FeedbackHistoryPage } from "./FeedbackHistoryPage";

const { mockUseConfigContext } = vi.hoisted(() => ({ mockUseConfigContext: vi.fn(() => ({}) as Record<string, unknown>) }));
vi.mock("@/providers/contexts", async importOriginal => ({
    ...(await importOriginal<typeof import("@/providers/contexts")>()),
    useConfigContext: mockUseConfigContext,
}));
import { RECENT_FETCH_SIZE } from "./RecentFeedbackList";
import { retentionFloorDate } from "./retentionLimit";

const navigateSpy = vi.fn();
const { mockUseAgentCards } = vi.hoisted(() => ({ mockUseAgentCards: vi.fn(() => ({ data: [] as unknown[] })) }));
vi.mock("@/api/agent-cards/hooks", () => ({ useAgentCards: mockUseAgentCards }));
const peopleSearch = vi.hoisted(() => ({ available: true }));
vi.mock("@/hooks/useIsPeopleSearchAvailable", () => ({ useIsPeopleSearchAvailable: () => peopleSearch.available }));
vi.mock("react-router-dom", async () => {
    const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
    return {
        ...actual,
        useNavigate: () => {
            const inRouter = actual.useInRouterContext();
            const realNavigate = inRouter ? actual.useNavigate() : null;
            return (...args: Parameters<ReturnType<typeof actual.useNavigate>>) => {
                navigateSpy(...args);
                return realNavigate?.(...args);
            };
        },
    };
});

const emptyPage = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 20, count: 0, totalPages: 1, nextPage: null } } };
const DAY_MS = 24 * 60 * 60 * 1000;

const fb = (over: Partial<CrossUserFeedback>): CrossUserFeedback => ({
    id: "1",
    sessionId: "s1",
    taskId: "t1",
    userId: "u1",
    rating: "up",
    comment: "first comment",
    shareTaskContent: false,
    subjectId: "agent-1",
    subjectType: "agent",
    subjectName: "Agent One",
    entrypointType: "slack",
    createdAt: new Date().toISOString(),
    ...over,
});

const pageOf = (data: CrossUserFeedback[], pagination: Partial<FeedbackHistoryResponse["meta"]["pagination"]> = {}): FeedbackHistoryResponse => ({
    data,
    meta: { pagination: { pageNumber: 1, pageSize: 20, count: data.length, totalPages: 1, nextPage: null, ...pagination } },
});

const isRecentRequest = (p: FeedbackHistoryParams) => p.pageSize === RECENT_FETCH_SIZE;

function finishPanelTransition() {
    const panel = screen.getByRole("complementary", { name: "Feedback details" });
    const rail = panel.parentElement!.parentElement!;
    const event = createEvent.transitionEnd(rail);
    Object.defineProperty(event, "propertyName", { value: "width" });
    fireEvent(rail, event);
}

const MAX_LOCATION_KEYS = 20;

function LocationProbe({ onChange }: { onChange?: (key: string) => void }) {
    const location = useLocation();
    const navigationType = useNavigationType();
    const seenKeys = useRef<Set<string>>(new Set());
    seenKeys.current.add(location.key);
    if (seenKeys.current.size > MAX_LOCATION_KEYS) {
        throw new Error(`LocationProbe saw ${seenKeys.current.size} distinct location keys: the page is navigating in a loop.`);
    }
    onChange?.(location.key);
    return (
        <div data-testid="location-search" data-navigation-type={navigationType}>
            {location.search}
        </div>
    );
}

function JumpTo({ path }: { path: string }) {
    const navigate = useNavigate();
    return (
        <button type="button" onClick={() => navigate(path)}>
            jump elsewhere
        </button>
    );
}

function renderPageWithJump(jumpPath: string) {
    return render(
        <StoryProvider shouldIsolateQueryClient>
            <FeedbackFilterMemoryProvider>
                <MemoryRouter initialEntries={["/feedback/history"]}>
                    <Routes>
                        <Route
                            path="/feedback/history"
                            element={
                                <>
                                    <FeedbackHistoryPage />
                                    <JumpTo path={jumpPath} />
                                </>
                            }
                        />
                    </Routes>
                </MemoryRouter>
            </FeedbackFilterMemoryProvider>
        </StoryProvider>
    );
}

function renderPage(initialPath = "/feedback/history") {
    return render(
        <StoryProvider shouldIsolateQueryClient>
            <FeedbackFilterMemoryProvider>
                <MemoryRouter initialEntries={[initialPath]}>
                    <Routes>
                        <Route
                            path="/feedback/history"
                            element={
                                <>
                                    <FeedbackHistoryPage />
                                    <LocationProbe />
                                </>
                            }
                        />
                    </Routes>
                </MemoryRouter>
            </FeedbackFilterMemoryProvider>
        </StoryProvider>
    );
}

const renderPageAt = renderPage;

function renderPageWithProbe(initialPath = "/feedback/history") {
    const keys: string[] = [];
    render(
        <StoryProvider shouldIsolateQueryClient>
            <FeedbackFilterMemoryProvider>
                <MemoryRouter initialEntries={[initialPath]}>
                    <Routes>
                        <Route
                            path="/feedback/history"
                            element={
                                <>
                                    <FeedbackHistoryPage />
                                    <LocationProbe onChange={key => keys.push(key)} />
                                </>
                            }
                        />
                    </Routes>
                </MemoryRouter>
            </FeedbackFilterMemoryProvider>
        </StoryProvider>
    );
    return keys;
}

function renderPageAsReviewer({ scopes, username }: { scopes: readonly string[]; username?: string }) {
    return render(
        <StoryProvider shouldIsolateQueryClient userContextValues={{ scopes }} authContextValues={{ userInfo: username === undefined ? null : { username } }}>
            <FeedbackFilterMemoryProvider>
                <MemoryRouter initialEntries={["/feedback/history"]}>
                    <FeedbackHistoryPage />
                </MemoryRouter>
            </FeedbackFilterMemoryProvider>
        </StoryProvider>
    );
}

function mockHistory() {
    const fetch = vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(emptyPage);
    return fetch;
}

describe("FeedbackHistoryPage", () => {
    afterEach(() => vi.restoreAllMocks());

    it("requests the last 90 days on first load", async () => {
        const fetchSpy = vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(emptyPage);
        renderPage();

        await waitFor(() => expect(fetchSpy).toHaveBeenCalled());
        const startDate = fetchSpy.mock.calls[0][0].startDate;
        expect(startDate).toBeDefined();
        const daysAgo = (Date.now() - new Date(startDate as string).getTime()) / DAY_MS;
        expect(daysAgo).toBeGreaterThan(89);
        expect(daysAgo).toBeLessThan(91);
    });

    it("sends the rating filter and resets to page 1, without discarding an explicit page change", async () => {
        const user = userEvent.setup();
        const fetchSpy = vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue({
            ...emptyPage,
            meta: { pagination: { pageNumber: 3, pageSize: 20, count: 100, totalPages: 5, nextPage: 4 } },
        });
        renderPage();
        await waitFor(() => expect(fetchSpy).toHaveBeenCalled());
        await waitFor(() => expect(screen.getByText("3")).toBeInTheDocument());

        await user.click(screen.getByText("3"));
        await waitFor(() => {
            const last = fetchSpy.mock.calls[fetchSpy.mock.calls.length - 1][0];
            expect(last.pageNumber).toBe(3);
        });

        await user.click(screen.getByLabelText("Filter by rating"));
        await user.click(screen.getByText("Negative"));

        await waitFor(() => {
            const last = fetchSpy.mock.calls[fetchSpy.mock.calls.length - 1][0];
            expect(last.rating).toBe("down");
            expect(last.pageNumber).toBe(1);
        });
    });

    it("distinguishes an unfiltered empty result from a filtered one", async () => {
        const user = userEvent.setup();
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(emptyPage);
        renderPage();

        await waitFor(() => expect(screen.getByText(/No Feedback Found/i)).toBeInTheDocument());

        await user.click(screen.getByLabelText("Filter by rating"));
        await user.click(screen.getByText("Negative"));

        await waitFor(() => expect(screen.getByText(/no .*match/i)).toBeInTheDocument());
    });

    it("renders an error state rather than an empty table when the request fails", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockRejectedValue(new Error("boom"));
        renderPage();

        await waitFor(() => expect(screen.getByText(/couldn't load/i)).toBeInTheDocument());
    });

    it("renders access-denied rather than a generic error on a 403", async () => {
        const forbidden = new Error("forbidden") as Error & { status?: number };
        forbidden.status = 403;
        vi.spyOn(service, "fetchFeedbackHistory").mockRejectedValue(forbidden);
        renderPage();

        await waitFor(() => expect(screen.getByText("Access Denied")).toBeInTheDocument());
        expect(screen.queryByText(/couldn't load/i)).not.toBeInTheDocument();
    });

    it("keeps a previously seen subject selectable after a filter narrows the response to empty", async () => {
        const user = userEvent.setup();
        const feedbackRow = (overrides: Partial<CrossUserFeedback>): CrossUserFeedback => ({
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
            ...overrides,
        });
        const withRows = {
            data: [feedbackRow({}), feedbackRow({ id: "2", subjectId: "agent-2", subjectName: "Agent Two", rating: "down" })],
            meta: { pagination: { pageNumber: 1, pageSize: 20, count: 2, totalPages: 1, nextPage: null } },
        };
        const fetchSpy = vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(withRows);
        renderPage();
        await waitFor(() => expect(fetchSpy).toHaveBeenCalled());

        await user.click(screen.getByLabelText("Agent / Workflow"));
        await user.click(screen.getByRole("option", { name: "Agent One" }));

        fetchSpy.mockResolvedValue({ ...withRows, data: [withRows.data[0]], meta: { pagination: { pageNumber: 1, pageSize: 20, count: 1, totalPages: 1, nextPage: null } } });
        await waitFor(() => {
            const last = fetchSpy.mock.calls[fetchSpy.mock.calls.length - 1][0];
            expect(last.agentIds).toEqual(["agent-1"]);
        });

        expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Agent One");

        await user.click(screen.getByLabelText("Agent / Workflow"));
        expect(screen.getByRole("option", { name: "Agent Two" })).toBeInTheDocument();
    });

    it("starts filtered to a subject handed over in the url", async () => {
        const fetch = mockHistory();

        renderPageAt("/feedback/history?subject=agent:a1");

        await waitFor(() => {
            const call = fetch.mock.calls.at(-1)![0];
            expect(call.agentIds).toEqual(["a1"]);
        });
    });

    it("hands a workflow row off as a workflow filter, not an agent one", async () => {
        const fetch = mockHistory();

        renderPageAt("/feedback/history?subject=workflow:w1");

        await waitFor(() => {
            const call = fetch.mock.calls.at(-1)![0];
            expect(call.workflowIds).toEqual(["w1"]);
            expect(call.agentIds).toBeUndefined();
        });
    });

    it("parses a subject key whose id contains a colon", async () => {
        const fetch = mockHistory();

        renderPageAt("/feedback/history?subject=agent:tenant:a1");

        await waitFor(() => {
            const call = fetch.mock.calls.at(-1)![0];
            expect(call.agentIds).toEqual(["tenant:a1"]);
        });
    });

    it("ignores an unparseable subject parameter rather than filtering by nothing", async () => {
        const fetch = mockHistory();

        renderPageAt("/feedback/history?subject=garbage");

        await waitFor(() => {
            const call = fetch.mock.calls.at(-1)![0];
            expect(call.agentIds).toBeUndefined();
            expect(call.workflowIds).toBeUndefined();
        });
    });

    it("labels the filter from the handoff link even when the filtered response is empty", async () => {
        mockHistory();

        renderPageAt("/feedback/history?subject=agent:a1&name=Procurement%20Intake%20Router");

        await waitFor(() => expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Procurement Intake Router"));
    });

    it("falls back to the subject id when the handoff link carries no name", async () => {
        mockHistory();

        renderPageAt("/feedback/history?subject=agent:a1");

        await waitFor(() => expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("a1"));
    });

    it("carries a clicked subject's key and name from the Dashboard into History's own filter", async () => {
        const subjectRow: FeedbackSummaryRow = {
            subject: { id: "tenant:a1", name: "Procurement Intake Router", type: "agent" },
            totalActivities: 10,
            positiveFeedback: 1,
            negativeFeedback: 0,
            negativeRatio: 0,
            trend: [],
        };
        const tenantRow: FeedbackSummaryRow = { totalActivities: 10, positiveFeedback: 1, negativeFeedback: 0, negativeRatio: 0, trend: [] };
        const summariesPageOf = (rows: FeedbackSummaryRow[]): FeedbackSummariesResponse => ({
            data: rows,
            meta: { pagination: { pageNumber: 1, pageSize: 100, count: rows.length, totalPages: 1, nextPage: null } },
        });
        vi.spyOn(service, "fetchFeedbackSummaries").mockImplementation(async (params: FeedbackSummariesParams) => (params.groupBy === "tenant" ? summariesPageOf([tenantRow]) : summariesPageOf([subjectRow])));

        const dashboard = render(
            <StoryProvider shouldIsolateQueryClient>
                <FeedbackFilterMemoryProvider>
                    <MemoryRouter initialEntries={["/feedback"]}>
                        <FeedbackDashboardPage />
                    </MemoryRouter>
                </FeedbackFilterMemoryProvider>
            </StoryProvider>
        );
        await dashboard.findByText("Procurement Intake Router");
        await userEvent.click(dashboard.getByText("Procurement Intake Router"));

        expect(navigateSpy).toHaveBeenCalledWith(expect.objectContaining({ pathname: "/feedback/history" }));
        const { pathname, search } = navigateSpy.mock.calls[0][0] as { pathname: string; search: string };
        const capturedUrl = `${pathname}${search}`;
        dashboard.unmount();

        const fetch = mockHistory();
        renderPageAt(capturedUrl);

        await waitFor(() => {
            const call = fetch.mock.calls.at(-1)![0];
            expect(call.agentIds).toEqual(["tenant:a1"]);
        });
        expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Procurement Intake Router");
    });

    describe("detail panel", () => {
        const rowA = fb({});
        const rowB = fb({ id: "2", subjectId: "agent-2", subjectName: "Agent Two", comment: "second comment", rating: "down" });

        it("opens on row click, swaps on another row, and closes from the close button", async () => {
            const user = userEvent.setup();
            vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([]) : pageOf([rowA, rowB])));
            renderPage();

            await user.click(await screen.findByText("first comment"));
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            expect(within(panel).getByRole("heading", { level: 2, name: "Agent One" })).toBeInTheDocument();

            await user.click(screen.getByText("first comment", { selector: "td" }));
            expect(await screen.findByRole("complementary", { name: "Feedback details" })).toBeInTheDocument();
            expect(within(panel).getByRole("heading", { level: 2, name: "Agent One" })).toBeInTheDocument();

            await user.click(screen.getByText("second comment", { selector: "td" }));
            await waitFor(() => expect(within(panel).getByRole("heading", { level: 2, name: "Agent Two" })).toBeInTheDocument());

            await user.click(within(panel).getByRole("button", { name: "Close feedback details" }));
            finishPanelTransition();
            await waitFor(() => expect(screen.queryByRole("complementary", { name: "Feedback details" })).not.toBeInTheDocument());
            expect(screen.getByText("first comment").closest("tr")).toHaveAttribute("aria-selected", "false");
            expect(screen.getByText("second comment").closest("tr")).toHaveAttribute("aria-selected", "false");
        });

        it("opens from the keyboard", async () => {
            const user = userEvent.setup();
            vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([]) : pageOf([rowA])));
            renderPage();

            (await screen.findByText("first comment")).closest("tr")!.focus();
            await user.keyboard("{Enter}");
            expect(await screen.findByRole("complementary", { name: "Feedback details" })).toBeInTheDocument();
        });

        it("keeps showing the selected feedback after paging away from it", async () => {
            const user = userEvent.setup();
            const other = fb({ id: "3", comment: "page two comment" });
            vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => {
                if (isRecentRequest(p)) return pageOf([]);
                return p.pageNumber === 2 ? pageOf([other], { pageNumber: 2, count: 40, totalPages: 2 }) : pageOf([rowA], { count: 40, totalPages: 2 });
            });
            renderPage();

            await user.click(await screen.findByText("first comment"));
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            await user.click(screen.getByText("2"));

            await waitFor(() => expect(screen.getByText("page two comment")).toBeInTheDocument());
            expect(within(panel).getByTestId("feedback-comment")).toHaveTextContent("first comment");
        });

        it("requests recent feedback without the table's rating filter", async () => {
            const user = userEvent.setup();
            const spy = vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([]) : pageOf([rowB])));
            renderPage();

            await user.click(screen.getByLabelText("Filter by rating"));
            await user.click(screen.getByText("Negative"));
            await user.click(await screen.findByText("second comment"));

            await waitFor(() => expect(spy.mock.calls.some(([p]) => isRecentRequest(p))).toBe(true));
            const recent = spy.mock.calls.map(([p]) => p).find(isRecentRequest)!;
            expect(recent.rating).toBeUndefined();
            expect(recent.agentIds).toEqual(["agent-2"]);
            const daysAgo = (Date.now() - new Date(recent.startDate as string).getTime()) / DAY_MS;
            expect(daysAgo).toBeLessThan(7.1);
        });

        it("Filter by Agent filters the table to the open subject", async () => {
            const user = userEvent.setup();
            const spy = vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([rowA, fb({ id: "9", comment: "older" })]) : pageOf([rowA, rowB])));
            renderPage();

            await user.click(await screen.findByText("first comment", { selector: "td" }));
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            await user.click(await within(panel).findByRole("button", { name: "Filter by Agent" }));

            await waitFor(() => {
                const tableCalls = spy.mock.calls.map(([p]) => p).filter(p => !isRecentRequest(p));
                expect(tableCalls[tableCalls.length - 1].agentIds).toEqual(["agent-1"]);
            });
            expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Agent One");
            expect(screen.getByRole("complementary", { name: "Feedback details" })).toBeInTheDocument();
        });

        it("retires the filter action once the table already filters to that subject", async () => {
            const user = userEvent.setup();
            vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([rowA, fb({ id: "9", comment: "older" })]) : pageOf([rowA, rowB])));
            renderPage();

            await user.click(await screen.findByText("first comment", { selector: "td" }));
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            await user.click(await within(panel).findByRole("button", { name: "Filter by Agent" }));

            await waitFor(() => expect(within(panel).queryByRole("button", { name: "Filter by Agent" })).not.toBeInTheDocument());
            expect(within(panel).getByText("older")).toBeInTheDocument();
        });

        it("swaps the panel to a recent item when clicked", async () => {
            const user = userEvent.setup();
            const older = fb({ id: "9", comment: "older comment" });
            vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([rowA, older]) : pageOf([rowA])));
            renderPage();

            await user.click(await screen.findByText("first comment", { selector: "td" }));
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            await user.click(await within(panel).findByRole("button", { name: /older comment/ }));

            await waitFor(() => expect(within(panel).getByTestId("feedback-comment")).toHaveTextContent("older comment"));
        });
    });

    describe("URL state", () => {
        afterEach(() => vi.restoreAllMocks());

        beforeEach(() => {});

        it("writes the rating filter to the URL and drops the page", async () => {
            vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(pageOf([fb({})], { totalPages: 3 }));
            renderPage("/feedback/history?page=2");
            await screen.findByText("first comment");
            await userEvent.click(screen.getByRole("combobox", { name: "Filter by rating" }));
            await userEvent.click(await screen.findByRole("option", { name: "Negative" }));
            await waitFor(() => expect(screen.getByTestId("location-search")).toHaveTextContent("?rating=down"));
        });

        it("selects a row through the URL", async () => {
            vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(pageOf([fb({ id: "f7" })]));
            renderPage();
            await userEvent.click(await screen.findByText("first comment"));
            expect(screen.getByTestId("location-search")).toHaveTextContent("selected=f7");
            expect(screen.getByRole("complementary", { name: "Feedback details" })).toBeInTheDocument();
        });

        it("opens the panel for a selected row that is on the page", async () => {
            vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(pageOf([fb({ id: "f7", comment: "deep linked" })]));
            renderPage("/feedback/history?selected=f7");
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            expect(within(panel).getByTestId("feedback-comment")).toHaveTextContent("deep linked");
        });

        it("fetches a selected row that is not on the page", async () => {
            vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(pageOf([fb({ id: "other" })]));
            const item = vi.spyOn(itemService, "fetchFeedbackItem").mockResolvedValue(fb({ id: "f9", comment: "from the item route" }));
            renderPage("/feedback/history?selected=f9");
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            expect(within(panel).getByTestId("feedback-comment")).toHaveTextContent("from the item route");
            expect(item).toHaveBeenCalledWith("f9");
        });

        it("does not keep showing the previous row when the URL moves to an unfetchable id outside a click", async () => {
            const user = userEvent.setup();
            vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(pageOf([fb({ id: "f7" })]));
            vi.spyOn(itemService, "fetchFeedbackItem").mockRejectedValue(Object.assign(new Error("not found"), { status: 404 }));
            renderPageWithJump("/feedback/history?selected=ghost");

            await user.click(await screen.findByText("first comment"));
            const openedPanel = await screen.findByRole("complementary", { name: "Feedback details" });
            expect(within(openedPanel).getByTestId("feedback-comment")).toHaveTextContent("first comment");

            await user.click(screen.getByRole("button", { name: "jump elsewhere" }));

            await waitFor(() => {
                const panel = screen.getByRole("complementary", { name: "Feedback details" });
                expect(within(panel).queryByTestId("feedback-comment")).not.toBeInTheDocument();
            });
            expect(within(screen.getByRole("complementary", { name: "Feedback details" })).getByText("This feedback cannot be found.")).toBeInTheDocument();
        });
    });
});

describe("retention-capped date ranges", () => {
    afterEach(() => {
        mockUseConfigContext.mockReturnValue({});
    });

    it("caps on feedback retention alone, not the shorter of the two clocks", async () => {
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 90 } });
        mockHistory();
        renderPageAt();

        await userEvent.click(screen.getByLabelText("Date Range"));

        expect(await screen.findByRole("button", { name: "Last 90 days" })).toBeInTheDocument();
    });

    it("never offers an All time option", async () => {
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 90 } });
        mockHistory();
        renderPageAt();

        await userEvent.click(screen.getByLabelText("Date Range"));
        await screen.findByRole("button", { name: "Last 90 days" });

        expect(screen.queryByRole("button", { name: /all time/i })).not.toBeInTheDocument();
    });
});

describe("filter bar styling", () => {
    it("matches the Dashboard's aria-labels, widths and button copy", async () => {
        mockHistory();
        renderPage();
        await screen.findByLabelText("Agent / Workflow");

        expect(screen.getByLabelText("Agent / Workflow")).toHaveClass("w-64");
        expect(screen.getByLabelText("Date Range")).toHaveClass("w-64");
        expect(screen.queryByLabelText("Filter by agent or workflow")).not.toBeInTheDocument();
        expect(screen.queryByLabelText("Filter by date range")).not.toBeInTheDocument();
        expect(screen.getByLabelText("Filter by rating")).toBeInTheDocument();
    });

    it("shows Reset Filters instead of Clear filters once a filter is active", async () => {
        const user = userEvent.setup();
        mockHistory();
        renderPage();
        await screen.findByLabelText("Agent / Workflow");

        await user.click(screen.getByLabelText("Filter by rating"));
        await user.click(await screen.findByText("Negative"));

        expect(await screen.findByRole("button", { name: /Reset Filters/ })).toBeInTheDocument();
        expect(screen.queryByText("Clear filters")).not.toBeInTheDocument();
    });
});

describe("filter glyphs", () => {
    it("marks each subject option with its type", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(pageOf([fb({})]));
        renderPage();
        await screen.findByText("Agent One");

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));

        const option = (await screen.findAllByRole("option")).find(o => o.textContent === "Agent One")!;
        expect(option.querySelector(".lucide-bot")).toBeInTheDocument();
    });

    it("leaves the all-subjects option unglyphed", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(pageOf([fb({})]));
        renderPage();
        await screen.findByText("Agent One");

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));

        const option = (await screen.findAllByRole("option")).find(o => o.textContent === "All Agents & Workflows")!;
        expect(option.querySelector(".lucide-bot")).not.toBeInTheDocument();
        expect(option.querySelector(".lucide-workflow")).not.toBeInTheDocument();
    });

    it("marks the rating options with the same thumbs the badges use", async () => {
        vi.spyOn(service, "fetchFeedbackHistory").mockResolvedValue(pageOf([fb({})]));
        renderPage();
        await screen.findByText("Agent One");

        await userEvent.click(screen.getByLabelText("Filter by rating"));
        const options = await screen.findAllByRole("option");
        const byText = (text: string) => options.find(o => o.textContent === text)!;

        expect(byText("Positive").querySelector(".lucide-thumbs-up")).toBeInTheDocument();
        expect(byText("Negative").querySelector(".lucide-thumbs-down")).toBeInTheDocument();
        expect(byText("Any Rating").querySelector(".lucide-thumbs-up")).not.toBeInTheDocument();
        expect(byText("Any Rating").querySelector(".lucide-thumbs-down")).not.toBeInTheDocument();
    });
});

describe("filter memory", () => {
    function MemoryHarness({ showPage, path = "/feedback/history" }: Readonly<{ showPage: boolean; path?: string }>) {
        return (
            <StoryProvider shouldIsolateQueryClient>
                <FeedbackFilterMemoryProvider>
                    {showPage ? (
                        <MemoryRouter initialEntries={[path]}>
                            <Routes>
                                <Route
                                    path="/feedback/history"
                                    element={
                                        <>
                                            <FeedbackHistoryPage />
                                            <LocationProbe />
                                        </>
                                    }
                                />
                            </Routes>
                        </MemoryRouter>
                    ) : null}
                </FeedbackFilterMemoryProvider>
            </StoryProvider>
        );
    }

    it("restores the filters it remembered onto a bare URL", async () => {
        mockHistory();
        const { rerender } = render(<MemoryHarness showPage />);
        await waitFor(() => expect(screen.getByLabelText("Filter by rating")).toBeInTheDocument());

        await userEvent.click(screen.getByLabelText("Filter by rating"));
        await userEvent.click(await screen.findByText("Negative"));
        await waitFor(() => expect(screen.getByLabelText("Filter by rating")).toHaveTextContent("Negative"));

        rerender(<MemoryHarness showPage={false} />);
        rerender(<MemoryHarness showPage />);

        await waitFor(() => expect(screen.getByTestId("location-search")).toHaveTextContent("rating=down"));
        expect(screen.getByLabelText("Filter by rating")).toHaveTextContent("Negative");
    });

    it("lets a URL that carries filters outrank what it remembered", async () => {
        mockHistory();
        const { rerender } = render(<MemoryHarness showPage />);
        await waitFor(() => expect(screen.getByLabelText("Filter by rating")).toBeInTheDocument());

        await userEvent.click(screen.getByLabelText("Filter by rating"));
        await userEvent.click(await screen.findByText("Negative"));
        await waitFor(() => expect(screen.getByLabelText("Filter by rating")).toHaveTextContent("Negative"));

        rerender(<MemoryHarness showPage={false} />);
        rerender(<MemoryHarness showPage path="/feedback/history?rating=up" />);

        await waitFor(() => expect(screen.getByLabelText("Filter by rating")).toHaveTextContent("Positive"));
        expect(screen.getByTestId("location-search")).not.toHaveTextContent("rating=down");
    });
});

describe("activity monitor handoff", () => {
    const rowA = fb({});

    beforeEach(() => {
        navigateSpy.mockClear();
    });

    afterEach(() => vi.restoreAllMocks());

    it("carries the feedback's own id, and the agent's wire name, to a new chat", async () => {
        const user = userEvent.setup();
        mockUseAgentCards.mockReturnValue({ data: [{ name: "7c21-uuid", displayName: "Activity Monitor" }] });
        vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([]) : pageOf([rowA])));
        renderPage();

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });
        const button = await within(panel).findByRole("button", { name: "Ask Activity Monitor" });
        await waitFor(() => expect(button).toBeEnabled());
        await user.click(button);

        expect(navigateSpy).toHaveBeenCalledWith("/chat", {
            state: {
                promptText: "Why was this response rated positive? Feedback ID 1",
                groupId: "feedback-trace-1",
                groupName: "Activity Monitor",
                agentName: "7c21-uuid",
            },
        });
    });

    it("withholds the handoff from a reviewer who can read the feedback but not the task", async () => {
        const user = userEvent.setup();
        mockUseAgentCards.mockReturnValue({ data: [{ name: "7c21-uuid", displayName: "Activity Monitor" }] });
        vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([]) : pageOf([rowA])));
        renderPageAsReviewer({ scopes: ["agent:*:read_feedback"], username: "someone-else" });

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });

        expect(within(panel).queryByRole("button", { name: "Ask Activity Monitor" })).not.toBeInTheDocument();
    });

    it("withholds the handoff on the reviewer's own row without a tenant-wide activity grant", async () => {
        const user = userEvent.setup();
        mockUseAgentCards.mockReturnValue({ data: [{ name: "7c21-uuid", displayName: "Activity Monitor" }] });
        vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([]) : pageOf([rowA])));
        renderPageAsReviewer({ scopes: ["agent:*:read_feedback"], username: rowA.userId });

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });

        expect(within(panel).queryByRole("button", { name: "Ask Activity Monitor" })).not.toBeInTheDocument();
    });

    it("offers the handoff to a holder of the tenant-wide activity read", async () => {
        const user = userEvent.setup();
        mockUseAgentCards.mockReturnValue({ data: [{ name: "7c21-uuid", displayName: "Activity Monitor" }] });
        vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([]) : pageOf([rowA])));
        renderPageAsReviewer({ scopes: ["activity:*:read"], username: "someone-else" });

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });

        expect(await within(panel).findByRole("button", { name: "Ask Activity Monitor" })).toBeEnabled();
    });

    it("does not accept a partial-glob activity grant as tenant-wide read", async () => {
        const user = userEvent.setup();
        mockUseAgentCards.mockReturnValue({ data: [{ name: "7c21-uuid", displayName: "Activity Monitor" }] });
        vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([]) : pageOf([rowA])));
        renderPageAsReviewer({ scopes: ["activity:**:read"], username: "someone-else" });

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });

        expect(within(panel).queryByRole("button", { name: "Ask Activity Monitor" })).not.toBeInTheDocument();
    });

    it("withholds the button when the Activity Monitor agent isn't among the agent cards", async () => {
        const user = userEvent.setup();
        mockUseAgentCards.mockReturnValue({ data: [{ name: "Orchestrator" }] });
        vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([]) : pageOf([rowA])));
        renderPage();

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });
        expect(within(panel).queryByRole("button", { name: "Ask Activity Monitor" })).not.toBeInTheDocument();
        expect(navigateSpy).not.toHaveBeenCalledWith("/chat", expect.anything());
    });
});

describe("selecting feedback the table is not currently showing", () => {
    afterEach(() => vi.restoreAllMocks());

    const isRankRequest = (p: FeedbackHistoryParams) => p.pageSize === 1;
    const paramsOf = (fetch: { mock: { calls: [FeedbackHistoryParams][] } }) => fetch.mock.calls.map(call => call[0]);

    const onPage1 = fb({ id: "1", comment: "first comment", createdAt: "2026-10-12T10:00:00.000Z" });
    const furtherDown = fb({ id: "99", comment: "older comment", createdAt: "2026-10-11T10:00:00.000Z" });

    it("moves the table to the page holding the row, and asks the server where that is", async () => {
        const user = userEvent.setup();
        const fetch = vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => {
            if (isRankRequest(p)) return pageOf([], { count: 25, pageSize: 1, totalPages: 25 });
            if (isRecentRequest(p)) return pageOf([furtherDown]);
            return p.pageNumber === 2 ? pageOf([furtherDown], { pageNumber: 2, count: 25, totalPages: 2 }) : pageOf([onPage1], { count: 25, totalPages: 2 });
        });
        vi.spyOn(itemService, "fetchFeedbackItem").mockResolvedValue(furtherDown);
        renderPage();

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });
        await user.click(await within(panel).findByText("older comment"));

        await waitFor(() => expect(paramsOf(fetch).find(isRankRequest)).toMatchObject({ pageNumber: 1, pageSize: 1, startDate: furtherDown.createdAt }));
        await waitFor(() => expect(paramsOf(fetch).some(p => p.pageSize === 20 && p.pageNumber === 2)).toBe(true));
    });

    it("does not drag the reviewer back when they page away afterwards", async () => {
        const user = userEvent.setup();
        const fetch = vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => {
            if (isRankRequest(p)) return pageOf([], { count: 25, pageSize: 1, totalPages: 25 });
            if (isRecentRequest(p)) return pageOf([furtherDown]);
            return p.pageNumber === 2 ? pageOf([furtherDown], { pageNumber: 2, count: 60, totalPages: 3 }) : pageOf([onPage1], { pageNumber: p.pageNumber ?? 1, count: 60, totalPages: 3 });
        });
        vi.spyOn(itemService, "fetchFeedbackItem").mockResolvedValue(furtherDown);
        renderPage();

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });
        await user.click(await within(panel).findByText("older comment"));
        await waitFor(() => expect(paramsOf(fetch).some(p => p.pageSize === 20 && p.pageNumber === 2)).toBe(true));

        await user.click(screen.getByText("3"));

        await waitFor(() => expect(paramsOf(fetch).some(p => p.pageSize === 20 && p.pageNumber === 3)).toBe(true));
        await waitFor(() => expect(screen.getByTestId("location-search").textContent).toContain("page=3"));
    });

    it("says so, and asks for no rank, when the table's own filters exclude the row", async () => {
        const user = userEvent.setup();
        const negativeOnPage = fb({ id: "1", comment: "first comment", rating: "down" });
        const positiveElsewhere = fb({ id: "99", comment: "praise", rating: "up" });
        const fetch = vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([positiveElsewhere]) : pageOf([negativeOnPage])));
        vi.spyOn(itemService, "fetchFeedbackItem").mockResolvedValue(positiveElsewhere);
        renderPage("/feedback/history?rating=down");

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });
        await user.click(await within(panel).findByText("praise"));

        expect(await within(panel).findByTestId("outside-table-filters")).toBeInTheDocument();
        expect(paramsOf(fetch).some(isRankRequest)).toBe(false);
    });

    it("leaves the panel unannotated for a row the filters do admit", async () => {
        const user = userEvent.setup();
        vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => {
            if (isRankRequest(p)) return pageOf([], { count: 1, pageSize: 1, totalPages: 1 });
            if (isRecentRequest(p)) return pageOf([furtherDown]);
            return pageOf([onPage1, furtherDown], { count: 2 });
        });
        renderPage();

        await user.click(await screen.findByText("first comment"));
        const panel = await screen.findByRole("complementary", { name: "Feedback details" });
        await user.click(await within(panel).findByText("older comment"));

        await waitFor(() => expect(within(panel).getByRole("heading", { level: 2, name: "Agent One" })).toBeInTheDocument());
        expect(within(panel).queryByTestId("outside-table-filters")).not.toBeInTheDocument();
    });
});

describe("explicit date ranges", () => {
    afterEach(() => {
        vi.restoreAllMocks();
        mockUseConfigContext.mockReturnValue({});
    });

    const customRoute = "/feedback/history?range=2026-08-19..2026-09-17";
    const customStart = new Date(2026, 7, 19, 0, 0, 0, 0).toISOString();
    const customEnd = new Date(2026, 8, 17, 23, 59, 59, 999).toISOString();
    const paramsOf = (fetch: { mock: { calls: [FeedbackHistoryParams][] } }) => fetch.mock.calls.map(call => call[0]);
    const tableRequests = (fetch: { mock: { calls: [FeedbackHistoryParams][] } }) => paramsOf(fetch).filter(p => p.pageSize === 20 && !isRecentRequest(p));

    it("bounds its request on both ends for an explicit range", async () => {
        const fetch = mockHistory();
        renderPage(customRoute);

        await waitFor(() => expect(fetch).toHaveBeenCalled());
        const params = tableRequests(fetch).at(-1)!;
        expect(params.startDate).toBe(customStart);
        expect(params.endDate).toBe(customEnd);
    });

    it("narrows the request to one submitter when the user filter is set", async () => {
        const fetch = mockHistory();
        renderPage("/feedback/history?user=olive%40example.com");

        await waitFor(() => expect(fetch).toHaveBeenCalled());
        expect(tableRequests(fetch).at(-1)!.userIds).toEqual(["olive@example.com"]);
    });

    it("sends no user filter when none is set", async () => {
        const fetch = mockHistory();
        renderPage("/feedback/history");

        await waitFor(() => expect(fetch).toHaveBeenCalled());
        expect(tableRequests(fetch).at(-1)!).not.toHaveProperty("userIds");
    });

    it("keeps one preset window across re-renders instead of minting a new cache key each time", async () => {
        const fetch = mockHistory();
        renderPage("/feedback/history?range=7d");
        await waitFor(() => expect(fetch).toHaveBeenCalled());
        await screen.findByLabelText("Agent / Workflow");
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 50));
        });

        const starts = new Set(tableRequests(fetch).map(p => p.startDate));
        expect(starts.size).toBe(1);
    });

    it("leaves a preset range open on the right, so its window tracks the clock", async () => {
        const fetch = mockHistory();
        renderPage("/feedback/history?range=7d");

        await waitFor(() => expect(fetch).toHaveBeenCalled());
        const params = tableRequests(fetch).at(-1)!;
        const daysAgo = (Date.now() - new Date(params.startDate as string).getTime()) / DAY_MS;
        expect(daysAgo).toBeGreaterThan(6.9);
        expect(daysAgo).toBeLessThan(7.1);
        expect(params).not.toHaveProperty("endDate");
    });

    it("reads the explicit range back as dates on the trigger", async () => {
        mockHistory();
        renderPage(customRoute);

        const trigger = await screen.findByLabelText("Date Range");
        expect(trigger).toHaveTextContent(formatDay("2026-08-19"));
        expect(trigger).toHaveTextContent(formatDay("2026-09-17"));
    });

    it("applies a range edited in the custom fields and writes it to the URL", async () => {
        const user = userEvent.setup();
        const fetch = mockHistory();
        renderPage(customRoute);
        await waitFor(() => expect(fetch).toHaveBeenCalled());

        await user.click(screen.getByLabelText("Date Range"));
        await user.click(screen.getByRole("button", { name: "Start date" }));
        await user.click(screen.getByRole("button", { name: "5" }));
        await user.click(screen.getByRole("button", { name: /apply/i }));

        await waitFor(() => {
            const last = tableRequests(fetch).at(-1)!;
            expect(last.startDate).toBe(new Date(2026, 7, 5, 0, 0, 0, 0).toISOString());
            expect(last.endDate).toBe(customEnd);
        });
        expect(screen.getByTestId("location-search").textContent).toContain("range=2026-08-05..2026-09-17");
    });

    it("drops a preset's request back to open-ended when the reviewer leaves a custom range", async () => {
        const user = userEvent.setup();
        const fetch = mockHistory();
        renderPage(customRoute);
        await waitFor(() => expect(tableRequests(fetch).at(-1)?.endDate).toBe(customEnd));

        await user.click(screen.getByLabelText("Date Range"));
        await user.click(await screen.findByRole("button", { name: "Last 7 days" }));

        await waitFor(() => expect(tableRequests(fetch).at(-1)?.endDate).toBeUndefined());
        expect(screen.getByTestId("location-search").textContent).toContain("range=7d");
    });

    it("shows Reset Filters for an explicit range, and resets it", async () => {
        const user = userEvent.setup();
        mockHistory();
        renderPage(customRoute);

        await user.click(await screen.findByRole("button", { name: /Reset Filters/ }));

        await waitFor(() => expect(screen.getByTestId("location-search").textContent).not.toContain("range="));
    });

    describe("against feedback retention", () => {
        const navigationType = () => screen.getByTestId("location-search").getAttribute("data-navigation-type");

        it("writes the retention clamp as a replacement, not a new history entry", async () => {
            mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 30 } });
            mockHistory();
            renderPage("/feedback/history?range=90d");

            await waitFor(() => expect(screen.getByTestId("location-search").textContent).toBe(""));
            expect(navigationType()).toBe("REPLACE");
        });

        it("writes nothing at all for a bare URL whose default retention already allows", async () => {
            mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 30 } });
            const fetch = mockHistory();
            const keys = renderPageWithProbe();

            await waitFor(() => expect(fetch).toHaveBeenCalled());
            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 50));
            });

            expect(screen.getByTestId("location-search").textContent).toBe("");
            expect(new Set(keys).size).toBe(1);
        });

        it("leaves a reviewer's own range change as a pushed entry, so Back undoes it", async () => {
            const user = userEvent.setup();
            mockHistory();
            renderPage();

            await user.click(await screen.findByLabelText("Date Range"));
            await user.click(await screen.findByRole("button", { name: "Last 7 days" }));

            await waitFor(() => expect(screen.getByTestId("location-search").textContent).toBe("?range=7d"));
            expect(navigationType()).toBe("PUSH");
        });

        it("keeps a range beyond the task clock but inside the feedback clock", async () => {
            mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 90 } });
            const from = retentionFloorDate(60)!;
            const to = retentionFloorDate(50)!;
            const fetch = mockHistory();
            renderPage(`/feedback/history?range=${from}..${to}`);

            await waitFor(() => expect(fetch).toHaveBeenCalled());
            expect(screen.getByTestId("location-search").textContent).toBe(`?range=${from}..${to}`);
            expect(tableRequests(fetch).at(-1)!.endDate).toBeDefined();
        });

        it("advances the start of a range that reaches behind the floor", async () => {
            mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 90, feedbackDays: 90 } });
            const floor = retentionFloorDate(90)!;
            const to = retentionFloorDate(10)!;
            const fetch = mockHistory();
            renderPage(`/feedback/history?range=${retentionFloorDate(120)}..${to}`);

            await waitFor(() => expect(screen.getByTestId("location-search").textContent).toBe(`?range=${floor}..${to}`));
            const [y, m, d] = floor.split("-").map(Number);
            await waitFor(() => expect(tableRequests(fetch).at(-1)!.startDate).toBe(new Date(y, m - 1, d, 0, 0, 0, 0).toISOString()));
        });

        it("discards a range that lies wholly behind the floor", async () => {
            mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 90, feedbackDays: 90 } });
            const fetch = mockHistory();
            renderPage("/feedback/history?range=2020-01-01..2020-02-01");

            await waitFor(() => expect(screen.getByTestId("location-search").textContent).toBe(""));
            await waitFor(() => expect(tableRequests(fetch).at(-1)!.endDate).toBeUndefined());
        });
    });

    describe("the row-page lookup agrees with the table about the window", () => {
        const isRankRequest = (p: FeedbackHistoryParams) => p.pageSize === 1;
        const insideRange = fb({ id: "1", comment: "first comment", createdAt: new Date(2026, 8, 10, 12).toISOString() });
        const furtherDown = fb({ id: "99", comment: "older comment", createdAt: new Date(2026, 8, 1, 12).toISOString() });
        const afterRange = fb({ id: "77", comment: "too recent", createdAt: new Date(2026, 8, 20, 12).toISOString() });

        it("sends the table's own end bound with the rank request", async () => {
            const user = userEvent.setup();
            const fetch = vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => {
                if (isRankRequest(p)) return pageOf([], { count: 25, pageSize: 1, totalPages: 25 });
                if (isRecentRequest(p)) return pageOf([furtherDown]);
                return pageOf([insideRange], { count: 25, totalPages: 2 });
            });
            vi.spyOn(itemService, "fetchFeedbackItem").mockResolvedValue(furtherDown);
            renderPage(customRoute);

            await user.click(await screen.findByText("first comment"));
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            await user.click(await within(panel).findByText("older comment"));

            await waitFor(() => expect(paramsOf(fetch).some(isRankRequest)).toBe(true));
            const rank = paramsOf(fetch).find(isRankRequest)!;
            expect(rank.endDate).toBe(customEnd);
            expect(rank.endDate).toBe(tableRequests(fetch).at(-1)!.endDate);
            expect(rank.startDate).toBe(furtherDown.createdAt);
        });

        it("calls a row newer than the range's end outside the filters, and asks for no rank", async () => {
            const user = userEvent.setup();
            const fetch = vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => (isRecentRequest(p) ? pageOf([afterRange]) : pageOf([insideRange])));
            vi.spyOn(itemService, "fetchFeedbackItem").mockResolvedValue(afterRange);
            renderPage(customRoute);

            await user.click(await screen.findByText("first comment"));
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            await user.click(await within(panel).findByText("too recent"));

            expect(await within(panel).findByTestId("outside-table-filters")).toBeInTheDocument();
            expect(paramsOf(fetch).some(isRankRequest)).toBe(false);
        });

        it("does not annotate a row inside the range", async () => {
            const user = userEvent.setup();
            vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => {
                if (isRankRequest(p)) return pageOf([], { count: 1, pageSize: 1, totalPages: 1 });
                if (isRecentRequest(p)) return pageOf([furtherDown]);
                return pageOf([insideRange, furtherDown], { count: 2 });
            });
            renderPage(customRoute);

            await user.click(await screen.findByText("first comment"));
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            await user.click(await within(panel).findByText("older comment"));

            await waitFor(() => expect(within(panel).getByRole("heading", { level: 2, name: "Agent One" })).toBeInTheDocument());
            expect(within(panel).queryByTestId("outside-table-filters")).not.toBeInTheDocument();
        });

        it("keeps a preset's rank request open-ended and never calls a fresh row outside it", async () => {
            const user = userEvent.setup();
            const fresh = fb({ id: "1", comment: "first comment", createdAt: new Date().toISOString() });
            const slightlyOlder = fb({ id: "99", comment: "older comment", createdAt: new Date(Date.now() - 60_000).toISOString() });
            const fetch = vi.spyOn(service, "fetchFeedbackHistory").mockImplementation(async p => {
                if (isRankRequest(p)) return pageOf([], { count: 25, pageSize: 1, totalPages: 25 });
                if (isRecentRequest(p)) return pageOf([slightlyOlder]);
                return pageOf([fresh], { count: 25, totalPages: 2 });
            });
            vi.spyOn(itemService, "fetchFeedbackItem").mockResolvedValue(slightlyOlder);
            renderPage("/feedback/history?range=7d");

            await user.click(await screen.findByText("first comment"));
            const panel = await screen.findByRole("complementary", { name: "Feedback details" });
            await user.click(await within(panel).findByText("older comment"));

            await waitFor(() => expect(paramsOf(fetch).some(isRankRequest)).toBe(true));
            expect(paramsOf(fetch).find(isRankRequest)).not.toHaveProperty("endDate");
            expect(within(panel).queryByTestId("outside-table-filters")).not.toBeInTheDocument();
        });
    });
});

describe("user filter availability", () => {
    afterEach(() => {
        peopleSearch.available = true;
        vi.restoreAllMocks();
    });

    it("shows the User filter when people search is available", async () => {
        peopleSearch.available = true;
        mockHistory();
        renderPage();
        await screen.findByLabelText("Agent / Workflow");

        expect(screen.getByRole("button", { name: "User" })).toBeInTheDocument();
    });

    it("hides the User filter and its label when people search is unavailable", async () => {
        peopleSearch.available = false;
        mockHistory();
        renderPage();
        await screen.findByLabelText("Agent / Workflow");

        expect(screen.queryByRole("button", { name: "User" })).not.toBeInTheDocument();
        expect(screen.queryByText("User", { selector: "label" })).not.toBeInTheDocument();
        expect(screen.getByLabelText("Filter by rating")).toBeInTheDocument();
        expect(screen.getByLabelText("Date Range")).toBeInTheDocument();
    });
});
