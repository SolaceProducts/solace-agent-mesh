// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useRef } from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation, useNavigationType } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { FeedbackSummariesParams, FeedbackSummariesResponse, FeedbackSummaryRow } from "@/api/feedback";
import * as service from "@/api/feedback/service";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import * as dashboardFilters from "./dashboardFilters";
import { FeedbackDashboardPage } from "./FeedbackDashboardPage";
import { FeedbackFilterMemoryProvider } from "./feedbackFilterMemory";
import * as retentionLimit from "./retentionLimit";

const navigateSpy = vi.fn();
const { mockUseConfigContext } = vi.hoisted(() => ({ mockUseConfigContext: vi.fn(() => ({}) as Record<string, unknown>) }));
vi.mock("@/providers/contexts", async importOriginal => ({
    ...(await importOriginal<typeof import("@/providers/contexts")>()),
    useConfigContext: mockUseConfigContext,
}));

vi.mock("react-router-dom", async () => ({
    ...(await vi.importActual<typeof import("react-router-dom")>("react-router-dom")),
    useNavigate: () => navigateSpy,
}));

const { mockUseAgentCards } = vi.hoisted(() => ({ mockUseAgentCards: vi.fn(() => ({ data: [] as unknown[] })) }));
vi.mock("@/api/agent-cards/hooks", () => ({ useAgentCards: mockUseAgentCards }));

vi.mock("./NegativeTrendChart", () => ({
    NegativeTrendChart: ({
        rows,
        isLoading,
        error,
        granularity,
        onSelectSubject,
    }: {
        rows?: { subject?: { id: string; name: string; type: string } }[];
        isLoading?: boolean;
        error?: Error | null;
        granularity?: string;
        onSelectSubject?: (subject: { id: string; name: string; type: string }) => void;
    }) => (
        <div data-testid="trend-chart" data-loading={String(Boolean(isLoading))} data-error={error?.message ?? ""} data-granularity={granularity} data-selectable={String(Boolean(onSelectSubject))}>
            {(rows ?? []).map(r => r.subject?.id).join(",")}
            {onSelectSubject &&
                (rows ?? []).map(r =>
                    r.subject ? (
                        <button key={r.subject.id} type="button" onClick={() => onSelectSubject(r.subject!)}>
                            select {r.subject.name}
                        </button>
                    ) : null
                )}
        </div>
    ),
}));

const tenantRow: FeedbackSummaryRow = { totalActivities: 12406, positiveFeedback: 145, negativeFeedback: 14, negativeRatio: 0.00113, trend: [] };

const subjectRow = (id: string, name: string): FeedbackSummaryRow => ({
    subject: { id, name, type: "agent" },
    totalActivities: 10,
    positiveFeedback: 1,
    negativeFeedback: 0,
    negativeRatio: 0,
    trend: [],
});

const pageOf = (rows: FeedbackSummaryRow[], count?: number): FeedbackSummariesResponse => ({
    data: rows,
    meta: { pagination: { pageNumber: 1, pageSize: 100, count: count ?? rows.length, totalPages: 1, nextPage: null } },
});

const TABLE_QUERY_PAGE_SIZE = 20;

function mockSummaries(options?: { count?: number }) {
    return vi.spyOn(service, "fetchFeedbackSummaries").mockImplementation(async (params: FeedbackSummariesParams) => {
        if (params.groupBy === "tenant") return pageOf([tenantRow]);
        const rows = [subjectRow("a1", "Procurement Intake Router")];
        if (params.pageSize === TABLE_QUERY_PAGE_SIZE && options?.count !== undefined) {
            const pageSize = params.pageSize ?? rows.length;
            return {
                data: rows,
                meta: { pagination: { pageNumber: params.pageNumber ?? 1, pageSize, count: options.count, totalPages: Math.ceil(options.count / pageSize), nextPage: null } },
            };
        }
        return pageOf(rows);
    });
}

const paramsOf = (fetch: ReturnType<typeof mockSummaries>) => fetch.mock.calls.map(call => call[0]);

const DEFAULT_ROUTE = "/feedback";

const pageAt = (route: string) => (
    <StoryProvider shouldIsolateQueryClient>
        <FeedbackFilterMemoryProvider>
            <MemoryRouter initialEntries={[route]}>
                <FeedbackDashboardPage />
                <LocationProbe />
            </MemoryRouter>
        </FeedbackFilterMemoryProvider>
    </StoryProvider>
);

const renderPage = (route: string = DEFAULT_ROUTE) => render(pageAt(route));

const renderPageWithScopes = (scopes: readonly string[]) =>
    render(
        <StoryProvider shouldIsolateQueryClient userContextValues={{ scopes }}>
            <FeedbackFilterMemoryProvider>
                <MemoryRouter initialEntries={[DEFAULT_ROUTE]}>
                    <FeedbackDashboardPage />
                    <LocationProbe />
                </MemoryRouter>
            </FeedbackFilterMemoryProvider>
        </StoryProvider>
    );

beforeEach(() => {
    vi.restoreAllMocks();
});

describe("FeedbackDashboardPage", () => {
    it("shows the tenant KPIs and asks for them without a trend", async () => {
        const fetch = mockSummaries();
        renderPage();

        expect(await screen.findByText("12,406")).toBeInTheDocument();
        expect(screen.getByText("(0.113%)")).toBeInTheDocument();

        const tenantCall = paramsOf(fetch).find(params => params.groupBy === "tenant");
        expect(tenantCall?.trendGranularity).toBe("none");
        expect(tenantCall?.startDate).toBeTruthy();
        expect(tenantCall?.endDate).toBeTruthy();
    });

    it("names the subjects table and keeps that name when the table has no rows", async () => {
        vi.spyOn(service, "fetchFeedbackSummaries").mockImplementation(async (params: FeedbackSummariesParams) => (params.groupBy === "tenant" ? pageOf([tenantRow]) : pageOf([])));
        renderPage();

        const section = await screen.findByRole("region", { name: "Agents & Workflows" });
        expect(within(section).getByRole("heading", { name: "Agents & Workflows" })).toBeInTheDocument();
        await waitFor(() => expect(within(section).queryByRole("table")).not.toBeInTheDocument());
    });

    it("populates the subject filter from the summaries endpoint, not the agent registry", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        const optionsCall = paramsOf(fetch).find(params => params.groupBy === "subject");
        expect(optionsCall).toMatchObject({ groupBy: "subject", sortBy: "name", sortDir: "asc", pageSize: 100, trendGranularity: "none" });
        expect(optionsCall?.agentIds).toBeUndefined();

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));
        expect(await screen.findByRole("option", { name: "Procurement Intake Router" })).toBeInTheDocument();
    });

    it("narrows the KPI request to the chosen subject", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));
        await userEvent.click(await screen.findByRole("option", { name: "Procurement Intake Router" }));

        await waitFor(() => {
            const narrowed = paramsOf(fetch).find(params => params.groupBy === "tenant" && params.agentIds?.length);
            expect(narrowed?.agentIds).toEqual(["a1"]);
        });
    });

    it("keeps the filter menu listing every subject after one is selected", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));
        await userEvent.click(await screen.findByRole("option", { name: "Procurement Intake Router" }));

        await waitFor(() => {
            const optionsCalls = paramsOf(fetch).filter(params => params.groupBy === "subject" && params.sortBy === "name");
            expect(optionsCalls.every(params => params.agentIds === undefined)).toBe(true);
        });
    });

    it("changes the window when the preset changes", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");
        const before = paramsOf(fetch).find(params => params.groupBy === "tenant")!.startDate!;

        await userEvent.click(screen.getByLabelText("Date Range"));
        await userEvent.click(await screen.findByText("Last 7 days"));

        await waitFor(() => {
            const after = paramsOf(fetch)
                .filter(params => params.groupBy === "tenant")
                .at(-1)!.startDate!;
            expect(Date.parse(after)).toBeGreaterThan(Date.parse(before));
        });
    });

    it("resets back to the default window", async () => {
        mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Date Range"));
        await userEvent.click(await screen.findByText("Last 7 days"));
        await userEvent.click(await screen.findByRole("button", { name: /reset/i }));

        await waitFor(() => expect(screen.queryByRole("button", { name: /reset/i })).not.toBeInTheDocument());
    });

    it("says so when more subjects exist than the menu lists", async () => {
        vi.spyOn(service, "fetchFeedbackSummaries").mockImplementation(async (params: FeedbackSummariesParams) => (params.groupBy === "tenant" ? pageOf([tenantRow]) : pageOf([subjectRow("a1", "Procurement Intake Router")], 250)));
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));

        expect(await screen.findByText(/first 100/i)).toBeInTheDocument();
    });

    it("surfaces a failed summaries request instead of leaving the screen blank", async () => {
        vi.spyOn(service, "fetchFeedbackSummaries").mockRejectedValue(new Error("date range must not exceed 366 days"));
        renderPage();

        expect(await screen.findByText(/Couldn't load feedback summary: date range must not exceed 366 days/i)).toBeInTheDocument();
        expect(await screen.findByText("Couldn't Load Agents & Workflows")).toBeInTheDocument();
    });

    it("keeps the trigger labelled when a window change drops the held subject from the options", async () => {
        const sevenDaysAgo = Date.now() - 7 * 24 * 60 * 60 * 1000;
        vi.spyOn(service, "fetchFeedbackSummaries").mockImplementation(async (params: FeedbackSummariesParams) => {
            if (params.groupBy === "tenant") return pageOf([tenantRow]);
            const isNarrowedWindow = Date.parse(params.startDate!) > sevenDaysAgo;
            return isNarrowedWindow ? pageOf([]) : pageOf([subjectRow("a1", "Procurement Intake Router")]);
        });
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));
        await userEvent.click(await screen.findByRole("option", { name: "Procurement Intake Router" }));
        expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Procurement Intake Router");

        await userEvent.click(screen.getByLabelText("Date Range"));
        await userEvent.click(await screen.findByText("Last 24 hours"));

        await waitFor(() => expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Procurement Intake Router"));
    });

    it("renders the access-denied view when the grant was revoked mid-session", async () => {
        const denied = Object.assign(new Error("feedback read access required"), { status: 403 });
        vi.spyOn(service, "fetchFeedbackSummaries").mockRejectedValue(denied);
        renderPage();

        expect(await screen.findByText("Access Denied")).toBeInTheDocument();
    });

    it("asks for the top five subjects by negative ratio, bucketed for the default 90-day range", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await waitFor(() => {
            const chartCall = paramsOf(fetch).find(params => params.sortBy === "negativeRatio" && params.pageSize === 5);
            expect(chartCall).toMatchObject({
                groupBy: "subject",
                sortBy: "negativeRatio",
                sortDir: "desc",
                pageSize: 5,
                trendGranularity: "week",
            });
        });
    });

    it("asks for the granularity the range implies", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Date Range"));
        await userEvent.click(await screen.findByText("Last 24 hours"));

        await waitFor(() => {
            const granularities = paramsOf(fetch)
                .filter(params => params.sortBy === "negativeRatio")
                .map(params => params.trendGranularity);
            expect(granularities).toContain("hour");
            expect(granularities.at(-1)).not.toBe("day");
        });

        await waitFor(() => expect(screen.getByTestId("trend-chart")).toHaveAttribute("data-granularity", "hour"));
    });

    it("hands the chart the rows it fetched", async () => {
        mockSummaries();
        renderPage();

        const chart = await screen.findByTestId("trend-chart");
        await waitFor(() => expect(chart).toHaveTextContent("a1"));
    });

    it("narrows the whole dashboard to a subject selected in the chart", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(await screen.findByRole("button", { name: "select Procurement Intake Router" }));

        await waitFor(() => expect(screen.getByLabelText("Agent / Workflow")).toHaveTextContent("Procurement Intake Router"));
        await waitFor(() => {
            const narrowedKpi = paramsOf(fetch).find(params => params.groupBy === "tenant" && params.agentIds?.length);
            expect(narrowedKpi?.agentIds).toEqual(["a1"]);
        });
    });

    it("withdraws the chart's selection affordance once a single subject is already filtered", async () => {
        mockSummaries();
        renderPage();
        await screen.findByText("12,406");
        expect(await screen.findByTestId("trend-chart")).toHaveAttribute("data-selectable", "true");

        await userEvent.click(await screen.findByRole("button", { name: "select Procurement Intake Router" }));

        await waitFor(() => expect(screen.getByTestId("trend-chart")).toHaveAttribute("data-selectable", "false"));
    });

    it("narrows the chart to the chosen subject alongside the KPIs", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Agent / Workflow"));
        await userEvent.click(await screen.findByRole("option", { name: "Procurement Intake Router" }));

        await waitFor(() => {
            const narrowedChart = paramsOf(fetch).find(params => params.sortBy === "negativeRatio" && params.pageSize === 5 && params.agentIds?.length);
            expect(narrowedChart?.agentIds).toEqual(["a1"]);
        });
    });

    it("asks for a paginated, week-bucketed page of subjects sorted worst first", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await waitFor(() => {
            const tableCall = paramsOf(fetch).find(params => params.pageSize === TABLE_QUERY_PAGE_SIZE);
            expect(tableCall).toMatchObject({
                groupBy: "subject",
                sortBy: "negativeRatio",
                sortDir: "desc",
                pageSize: 20,
                pageNumber: 1,
                trendGranularity: "week",
            });
        });
    });

    it("buckets the table by the selected range instead of a fixed week", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Date Range"));
        await userEvent.click(await screen.findByText("Last 30 days"));

        await waitFor(() => {
            const tableCall = paramsOf(fetch)
                .filter(params => params.pageSize === TABLE_QUERY_PAGE_SIZE)
                .at(-1);
            expect(tableCall?.trendGranularity).toBe("day");
        });
    });

    it("scrolls the subjects table inside a capped region, with pagination outside it", async () => {
        mockSummaries({ count: 40 });
        renderPage();
        await screen.findByText("12,406");

        const table = await screen.findByRole("table");
        const scroller = table.closest("div[class*='max-h-']");

        expect(scroller).not.toBeNull();
        expect(scroller!.className).toContain("overflow-auto");
        expect(scroller!.contains(screen.getByText("2"))).toBe(false);
        expect(scroller!.contains(screen.getByText(/Showing 1–/))).toBe(false);
    });

    it("re-requests with the chosen column when a header is clicked", async () => {
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByRole("button", { name: /Total Activities/i }));

        await waitFor(() => {
            const sorted = paramsOf(fetch).find(params => params.sortBy === "totalActivities");
            expect(sorted).toMatchObject({ sortBy: "totalActivities", sortDir: "asc", trendGranularity: "week" });
        });
    });

    it("keeps the sort when the page changes", async () => {
        const fetch = mockSummaries({ count: 40 });
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByRole("button", { name: /Total Activities/i }));
        await waitFor(() => expect(paramsOf(fetch).some(params => params.sortBy === "totalActivities")).toBe(true));

        await userEvent.click(screen.getByText("2"));

        await waitFor(() => {
            const page2 = paramsOf(fetch).find(params => params.pageNumber === 2);
            expect(page2).toMatchObject({ pageNumber: 2, sortBy: "totalActivities" });
        });
    });

    it("returns to page 1 when the date range changes", async () => {
        const fetch = mockSummaries({ count: 40 });
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByText("2"));
        await waitFor(() => expect(paramsOf(fetch).some(params => params.pageNumber === 2)).toBe(true));
        const page2StartDate = paramsOf(fetch).find(params => params.pageNumber === 2)!.startDate!;

        await userEvent.click(screen.getByLabelText("Date Range"));
        await userEvent.click(await screen.findByText("Last 7 days"));

        await waitFor(() => {
            const narrowed = paramsOf(fetch).filter(params => params.pageSize === TABLE_QUERY_PAGE_SIZE && params.startDate !== page2StartDate);
            expect(narrowed.length).toBeGreaterThan(0);
            expect(narrowed.every(params => params.pageNumber === 1)).toBe(true);
        });
    });

    it("opens history filtered to a clicked subject, carrying its display name", async () => {
        mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(await screen.findByText("Procurement Intake Router"));

        expect(navigateSpy).toHaveBeenCalledWith({ pathname: "/feedback/history", search: "?subject=agent%3Aa1&name=Procurement+Intake+Router" });
    });

    it("carries the current Date Range along when opening a subject's history", async () => {
        mockSummaries();
        renderPage(`${DEFAULT_ROUTE}?range=7d`);
        await screen.findByText("12,406");

        await userEvent.click(await screen.findByText("Procurement Intake Router"));

        expect(navigateSpy).toHaveBeenCalledWith({ pathname: "/feedback/history", search: "?range=7d&subject=agent%3Aa1&name=Procurement+Intake+Router" });
    });

    it("dims the table, without blanking it, while a refetch for a new window is in flight", async () => {
        mockSummaries();
        renderPage();
        await screen.findByText("Procurement Intake Router");

        let releaseRefetch: () => void = () => {};
        const pending = new Promise<void>(resolve => {
            releaseRefetch = resolve;
        });
        vi.spyOn(service, "fetchFeedbackSummaries").mockImplementation(async (params: FeedbackSummariesParams) => {
            if (params.groupBy === "tenant") return pageOf([tenantRow]);
            if (params.pageSize === TABLE_QUERY_PAGE_SIZE) await pending;
            return pageOf([subjectRow("a1", "Procurement Intake Router")]);
        });

        await userEvent.click(screen.getByLabelText("Date Range"));
        await userEvent.click(await screen.findByText("Last 7 days"));

        await waitFor(() => {
            const wrapper = screen.getByText("Procurement Intake Router").closest("tr")!.closest("[aria-busy]");
            expect(wrapper).toHaveAttribute("aria-busy", "true");
        });
        expect(screen.getByText("Procurement Intake Router")).toBeInTheDocument();

        releaseRefetch();

        await waitFor(() => {
            const wrapper = screen.getByText("Procurement Intake Router").closest("tr")!.closest("[aria-busy]");
            expect(wrapper).toHaveAttribute("aria-busy", "false");
        });
    });
});

describe("retention-capped date ranges", () => {
    afterEach(() => {
        mockUseConfigContext.mockReturnValue({});
    });

    it("does not offer a range that reaches past retention", async () => {
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 90 } });
        mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Date Range"));

        expect(await screen.findByRole("button", { name: "Last 30 days" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Last 90 days" })).not.toBeInTheDocument();
    });

    it("falls back from a default the window cannot serve", async () => {
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 30 } });
        const fetch = mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await waitFor(() => {
            const windows = paramsOf(fetch).filter(params => params.startDate !== undefined);
            const last = windows.at(-1)!;
            const spanDays = (Date.parse(last.endDate!) - Date.parse(last.startDate!)) / 86400000;
            expect(Math.round(spanDays)).toBe(30);
        });
    });

    it("offers every range when retention is disabled", async () => {
        mockUseConfigContext.mockReturnValue({});
        mockSummaries();
        renderPage();
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Date Range"));

        expect(await screen.findByRole("button", { name: "Last 90 days" })).toBeInTheDocument();
    });
});

describe("Ask AI", () => {
    beforeEach(() => {
        mockUseAgentCards.mockReturnValue({ data: [] });
        navigateSpy.mockClear();
    });

    afterEach(() => {
        mockUseConfigContext.mockReturnValue({});
    });

    it("is not offered when the Activity Monitor is not among the agent cards", async () => {
        mockUseAgentCards.mockReturnValue({ data: [{ name: "Orchestrator" }] });
        mockSummaries();
        renderPage();

        expect(await screen.findByText("12,406")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /ask ai/i })).not.toBeInTheDocument();
    });

    it("is offered to a feedback reader without the tenant-wide activity read", async () => {
        mockUseAgentCards.mockReturnValue({ data: [{ name: "7c21-uuid", displayName: "Activity Monitor" }] });
        mockSummaries();
        renderPageWithScopes(["agent:a-1:read_feedback"]);

        expect(await screen.findByRole("button", { name: /ask ai/i })).toBeInTheDocument();
    });

    it("is offered to a holder of the tenant-wide activity read", async () => {
        mockUseAgentCards.mockReturnValue({ data: [{ name: "7c21-uuid", displayName: "Activity Monitor" }] });
        mockSummaries();
        renderPageWithScopes(["activity:*:read"]);

        expect(await screen.findByRole("button", { name: /ask ai/i })).toBeInTheDocument();
    });

    it("carries the window and the agent's wire name to a new chat", async () => {
        mockUseAgentCards.mockReturnValue({ data: [{ name: "7c21-uuid", displayName: "Activity Monitor" }] });
        mockSummaries();
        renderPage();

        await userEvent.click(await screen.findByRole("button", { name: /ask ai/i }));

        expect(navigateSpy).toHaveBeenCalledWith("/chat", {
            state: {
                promptText: "Tell me about feedback given in the last 90 days",
                groupId: "ask-ai",
                groupName: "Ask AI",
                agentName: "7c21-uuid",
            },
        });
    });

    it("carries the selected subject's display name alongside the window", async () => {
        mockUseAgentCards.mockReturnValue({ data: [{ name: "Activity Monitor" }] });
        mockSummaries();
        renderPage();

        await userEvent.click(await screen.findByLabelText("Agent / Workflow"));
        await userEvent.click(await screen.findByRole("option", { name: /Procurement Intake Router/ }));
        await userEvent.click(await screen.findByRole("button", { name: /ask ai/i }));

        expect(navigateSpy).toHaveBeenCalledWith(
            "/chat",
            expect.objectContaining({
                state: expect.objectContaining({
                    promptText: "Tell me about feedback given to Procurement Intake Router in the last 90 days",
                }),
            })
        );
    });

    it("follows the date preset the reviewer picked", async () => {
        mockUseAgentCards.mockReturnValue({ data: [{ name: "Activity Monitor" }] });
        mockSummaries();
        renderPage();

        await userEvent.click(await screen.findByLabelText("Date Range"));
        await userEvent.click(await screen.findByRole("button", { name: "Last 7 days" }));
        await userEvent.click(await screen.findByRole("button", { name: /ask ai/i }));

        expect(navigateSpy).toHaveBeenCalledWith("/chat", expect.objectContaining({ state: expect.objectContaining({ promptText: "Tell me about feedback given in the last 7 days" }) }));
    });

    it("falls back to the preset's own label when it briefly isn't in the retention-capped list", async () => {
        mockUseAgentCards.mockReturnValue({ data: [{ name: "Activity Monitor" }] });
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 7, feedbackDays: 7 } });
        vi.spyOn(retentionLimit, "rangeWithinRetention").mockImplementation(({ range }) => range);
        mockSummaries();
        renderPage(`${DEFAULT_ROUTE}?range=90d`);

        await userEvent.click(await screen.findByRole("button", { name: /ask ai/i }));

        expect(navigateSpy).toHaveBeenCalledWith("/chat", expect.objectContaining({ state: expect.objectContaining({ promptText: "Tell me about feedback given in the last 90 days" }) }));
    });
});

describe("URL state", () => {
    it("restores the date range from the URL on a fresh mount", async () => {
        mockSummaries();
        render(
            <StoryProvider shouldIsolateQueryClient>
                <MemoryRouter initialEntries={["/feedback?range=24h"]}>
                    <FeedbackDashboardPage />
                </MemoryRouter>
            </StoryProvider>
        );

        await waitFor(() => expect(screen.getByLabelText("Date Range")).toHaveTextContent("Last 24 hours"));
    });
});

const MAX_LOCATION_KEYS = 20;

function LocationProbe({ onChange }: { onChange?: (entry: { key: string; type: string }) => void }) {
    const location = useLocation();
    const type = useNavigationType();
    const seenKeys = useRef<Set<string>>(new Set());
    seenKeys.current.add(location.key);
    if (seenKeys.current.size > MAX_LOCATION_KEYS) {
        throw new Error(`LocationProbe saw ${seenKeys.current.size} distinct location keys: the page is navigating in a loop.`);
    }
    onChange?.({ key: location.key, type });
    return <div data-testid="location">{location.search}</div>;
}

function renderWithProbe(route: string) {
    const entries: { key: string; type: string }[] = [];
    render(
        <StoryProvider shouldIsolateQueryClient>
            <MemoryRouter initialEntries={[route]}>
                <LocationProbe onChange={entry => entries.push(entry)} />
                <FeedbackDashboardPage />
            </MemoryRouter>
        </StoryProvider>
    );
    return entries;
}

describe("explicit date range", () => {
    afterEach(() => {
        mockUseConfigContext.mockReturnValue({});
        mockUseAgentCards.mockReturnValue({ data: [] });
    });

    const customRoute = `${DEFAULT_ROUTE}?range=2026-08-19..2026-09-17`;

    it("scopes its queries to an explicit range from the URL", async () => {
        const fetch = mockSummaries();
        renderPage(customRoute);
        await screen.findByText("12,406");

        const startDate = new Date(2026, 7, 19, 0, 0, 0, 0).toISOString();
        const endDate = new Date(2026, 8, 17, 23, 59, 59, 999).toISOString();
        const windows = paramsOf(fetch).filter(params => params.startDate !== undefined);
        expect(windows.length).toBeGreaterThan(0);
        windows.forEach(params => {
            expect(params.startDate).toBe(startDate);
            expect(params.endDate).toBe(endDate);
        });
    });

    it("reads the explicit range back as dates on the trigger", async () => {
        mockSummaries();
        renderPage(customRoute);

        expect(await screen.findByLabelText("Date Range")).toHaveTextContent(dashboardFilters.formatDay("2026-08-19"));
        expect(screen.getByLabelText("Date Range")).toHaveTextContent(dashboardFilters.formatDay("2026-09-17"));
    });

    it("buckets a 30-day custom range by day", async () => {
        const fetch = mockSummaries();
        renderPage(customRoute);
        await screen.findByText("12,406");

        const chartCall = paramsOf(fetch).find(params => params.pageSize === 5);
        expect(chartCall?.trendGranularity).toBe("day");
        expect(screen.getByTestId("trend-chart")).toHaveAttribute("data-granularity", "day");
    });

    it("applies a range edited in the custom fields", async () => {
        const fetch = mockSummaries();
        renderPage(customRoute);
        await screen.findByText("12,406");

        await userEvent.click(screen.getByLabelText("Date Range"));
        await userEvent.click(screen.getByRole("button", { name: "Start date" }));
        await userEvent.click(screen.getByRole("button", { name: "5" }));
        await userEvent.click(screen.getByRole("button", { name: /apply/i }));

        await waitFor(() => {
            const last = paramsOf(fetch)
                .filter(params => params.groupBy === "tenant")
                .at(-1)!;
            expect(last.startDate).toBe(new Date(2026, 7, 5, 0, 0, 0, 0).toISOString());
            expect(last.endDate).toBe(new Date(2026, 8, 17, 23, 59, 59, 999).toISOString());
        });
    });

    it("hands Ask AI the explicit range as dates rather than as a duration", async () => {
        mockUseAgentCards.mockReturnValue({ data: [{ name: "Activity Monitor" }] });
        navigateSpy.mockClear();
        mockSummaries();
        renderPage(customRoute);

        await userEvent.click(await screen.findByRole("button", { name: /ask ai/i }));

        const promptText = `Tell me about feedback given between ${dashboardFilters.formatDay("2026-08-19")} and ${dashboardFilters.formatDay("2026-09-17")}`;
        expect(navigateSpy).toHaveBeenCalledWith("/chat", expect.objectContaining({ state: expect.objectContaining({ promptText }) }));
    });

    it("clamps an explicit range that reaches past retention, rather than discarding it", async () => {
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 30 } });
        mockSummaries();
        const floor = retentionLimit.retentionFloorDate(30)!;
        const today = retentionLimit.todayCalendarDay();
        const beyondFloor = retentionLimit.retentionFloorDate(35)!;

        renderWithProbe(`${DEFAULT_ROUTE}?range=${beyondFloor}..${today}`);

        await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent(`?range=${floor}..${today}`));
    });
});

describe("retention clamp navigation", () => {
    afterEach(() => {
        mockUseConfigContext.mockReturnValue({});
    });

    it("writes the clamped range as a replacement, not a new history entry", async () => {
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 30 } });
        mockSummaries();
        const entries = renderWithProbe(`${DEFAULT_ROUTE}?range=90d`);

        await waitFor(() => expect(screen.getByTestId("location")).not.toHaveTextContent("range="));

        expect(entries.at(-1)?.type).toBe("REPLACE");
    });

    it("settles after one write instead of re-entering the clamp on its own update", async () => {
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 30 } });
        mockSummaries();
        const entries = renderWithProbe(`${DEFAULT_ROUTE}?range=90d`);

        await waitFor(() => expect(screen.getByTestId("location")).not.toHaveTextContent("range="));
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 50));
        });

        expect(new Set(entries.map(entry => entry.key)).size).toBe(2);
    });

    it("writes nothing at all for a bare URL whose default retention already allows", async () => {
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 30, feedbackDays: 30 } });
        mockSummaries();
        const entries = renderWithProbe(DEFAULT_ROUTE);

        await screen.findByText("12,406");
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 50));
        });

        expect(screen.getByTestId("location")).not.toHaveTextContent("range=");
        expect(new Set(entries.map(entry => entry.key)).size).toBe(1);
    });

    it("leaves a range that retention can serve untouched", async () => {
        mockUseConfigContext.mockReturnValue({ dataRetention: { taskDays: 365, feedbackDays: 365 } });
        mockSummaries();
        const entries = renderWithProbe(`${DEFAULT_ROUTE}?range=7d`);
        await screen.findByText("12,406");

        expect(new Set(entries.map(entry => entry.key)).size).toBe(1);
    });
});

describe("query window stability", () => {
    it("resolves the window once across re-renders of an unchanged range", async () => {
        const resolve = vi.spyOn(dashboardFilters, "resolveWindow");
        mockSummaries();
        const { rerender } = renderPage();
        await screen.findByText("12,406");
        const resolvedAfterSettling = resolve.mock.calls.length;

        rerender(pageAt(DEFAULT_ROUTE));
        rerender(pageAt(DEFAULT_ROUTE));

        expect(resolve.mock.calls.length).toBe(resolvedAfterSettling);
    });
});
