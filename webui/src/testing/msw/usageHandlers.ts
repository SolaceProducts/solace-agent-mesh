// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { http, HttpResponse, type RequestHandler } from "msw";

import { USAGE_PERIODS, type UsageDay, type UsageKind, type UsageModelRow, type UsageRow, type UsageSavings, type UsageSummary, type UsageUserRow } from "@/api/usage";

import { compareUsageMoney } from "@/utils/usageMoney";

import { usageAgentRows, usageDetailFor, usageGoldens, usageModelRows, usageRowDaily, usageSummary, usageUserRows, usageWorkflowRows } from "@/testing/data/usage";
import { pageFromRequest } from "@/testing/helpers/handlers";

const USAGE = "*/api/v2/platform/usage";
const SELF_USAGE = "*/api/v2/platform/userUsage";
const SELF_USAGE_SAVINGS = `${SELF_USAGE}/savings`;
const SAVINGS_READ_LAG_MS = 7;
const WINDOW_ROUTES = ["summary", "savings"];

type AnyRow = UsageRow | UsageUserRow | UsageModelRow;

const MONEY_SORTS: Record<string, (row: AnyRow) => string | null> = {
    cost: row => row.cost,
    costPerActivity: row => row.costPerActivity,
    uncachedInputCost: row => row.costByBucket.uncachedInput,
    outputCost: row => row.costByBucket.output,
    cacheReadCost: row => row.costByBucket.cacheRead,
    cacheWriteCost: row => row.costByBucket.cacheWrite,
};

const NUMBER_SORTS: Record<string, (row: AnyRow) => number | null> = {
    activities: row => row.activities,
    shareOfSpend: row => row.shareOfSpend,
};

const BUDGET_SORTS = ["budget", "budgetUsage"] as const;
const LIST_SORTS = ["name", ...Object.keys(MONEY_SORTS), ...Object.keys(NUMBER_SORTS)];
const KIND_SORTS: Partial<Record<UsageKind, readonly string[]>> = { users: BUDGET_SORTS, models: ["model"] };

const byName = (a: AnyRow, b: AnyRow) => (a.name ?? "").localeCompare(b.name ?? "");

function compareNulls(x: unknown, y: unknown): number | null {
    if (x !== null && y !== null) return null;
    return Number(y === null) - Number(x === null);
}

function compareMoney(field: string, a: AnyRow, b: AnyRow): number {
    const [x, y] = [MONEY_SORTS[field](a), MONEY_SORTS[field](b)];
    return compareNulls(x, y) ?? compareUsageMoney(x ?? "0", y ?? "0");
}

function compareNumbers(field: string, a: AnyRow, b: AnyRow): number {
    const [x, y] = [NUMBER_SORTS[field]?.(a) ?? null, NUMBER_SORTS[field]?.(b) ?? null];
    return compareNulls(x, y) ?? (x ?? 0) - (y ?? 0);
}

function compareRows(field: string, a: AnyRow, b: AnyRow): number {
    if (field === "name") return byName(a, b);
    return field in MONEY_SORTS ? compareMoney(field, a, b) : compareNumbers(field, a, b);
}

const BudgetGroup = { Budgeted: 0, Unlimited: 1, NoBudget: 2, NoUser: 3 } as const;
type BudgetGroup = (typeof BudgetGroup)[keyof typeof BudgetGroup];

function budgetRank(row: AnyRow, field: (typeof BUDGET_SORTS)[number]): { group: BudgetGroup; amount?: string | null; percent?: number } {
    const budget = "budget" in row ? row.budget : null;
    if (!budget) return { group: row.id === null ? BudgetGroup.NoUser : BudgetGroup.NoBudget };
    if (field === "budget") return { group: BudgetGroup.Budgeted, amount: budget.effectiveAmount };
    if (budget.effectiveAmount === null) return { group: BudgetGroup.Unlimited };
    return { group: BudgetGroup.Budgeted, percent: (row as UsageUserRow).budgetUsage?.usagePercent ?? 0 };
}

function compareBudget(field: (typeof BUDGET_SORTS)[number], descending: boolean, a: AnyRow, b: AnyRow): number {
    const [x, y] = [budgetRank(a, field), budgetRank(b, field)];
    if (x.group !== y.group) return x.group - y.group;
    if (x.group !== BudgetGroup.Budgeted) return 0;
    let order: number;
    if (field === "budgetUsage") order = (x.percent ?? 0) - (y.percent ?? 0);
    else if (x.amount === null || y.amount === null) order = Number(x.amount === null) - Number(y.amount === null);
    else order = compareUsageMoney(x.amount ?? "0", y.amount ?? "0");
    return descending ? -order : order;
}

const servedModel = (row: AnyRow) => ("models" in row ? (row.models.find(model => model.name !== null)?.name ?? null) : null);

const NULLS_LAST: Record<string, (row: AnyRow) => string | null> = { model: servedModel, name: row => row.name };

function nullsLast(field: string, a: AnyRow, b: AnyRow): number {
    const value = NULLS_LAST[field];
    if (!value) return 0;
    return Number(value(a) === null) - Number(value(b) === null);
}

function compareByField(field: string, descending: boolean, a: AnyRow, b: AnyRow): number {
    if ((BUDGET_SORTS as readonly string[]).includes(field)) return compareBudget(field as (typeof BUDGET_SORTS)[number], descending, a, b);
    const sign = descending ? -1 : 1;
    if (field === "model") return nullsLast(field, a, b) || sign * (servedModel(a) ?? "").toLowerCase().localeCompare((servedModel(b) ?? "").toLowerCase());
    return nullsLast(field, a, b) || sign * compareRows(field, a, b);
}

const onBudget = (row: AnyRow, budgetId: string | null) => !budgetId || ("budget" in row && row.budget?.id === budgetId.toLowerCase());

export function listRowsFromRequest(request: Request, rows: readonly AnyRow[]) {
    const params = new URL(request.url).searchParams;
    const search = params.get("search")?.toLowerCase();
    const [field, dir] = (params.get("sort") ?? "cost:desc").split(":");
    const descending = dir?.toLowerCase() === "desc";
    const filtered = rows
        .filter(row => !search || row.name?.toLowerCase().includes(search))
        .filter(row => params.get("population") !== "spenders" || compareUsageMoney(row.cost, "0") > 0)
        .filter(row => onBudget(row, params.get("budgetId")))
        .sort((a, b) => compareByField(field, descending, a, b) || compareUsageMoney(b.cost, a.cost) || byName(a, b));
    const page = pageFromRequest(request, filtered, 25);
    if (params.get("expand") !== "daily") return page;
    return { ...page, data: page.data.map(row => ({ ...row, daily: row.daily ?? usageRowDaily(row) })) };
}

const WINDOW_PARAMS = ["period", "startDate", "endDate"];
const LIST_PARAMS = [...WINDOW_PARAMS, "pageNumber", "pageSize", "sort", "search", "population", "expand", "budgetId"];
const BY_NAME_PARAMS = [...WINDOW_PARAMS, "name"];
const MAX_PAGE_SIZE = 100;
const MAX_DAILY_PAGE_SIZE = 25;
const MAX_NAME_BYTES = 256;

type Problem = { field: string; message: string };

export type UsageRouteParams = "list" | "byName" | "window";
const ROUTE_PARAMS: Record<UsageRouteParams, readonly string[]> = { list: LIST_PARAMS, byName: BY_NAME_PARAMS, window: WINDOW_PARAMS };

export function unknownParamsErrors(request: Request, route: UsageRouteParams): Problem[] {
    const params = new URL(request.url).searchParams;
    const problems: Problem[] = [];
    for (const key of new Set(params.keys())) {
        if (!ROUTE_PARAMS[route].includes(key)) problems.push({ field: key, message: "is not a query parameter of this operation" });
        else if (params.getAll(key).length > 1 && key !== "sort") problems.push({ field: key, message: "must be given once" });
    }
    return problems;
}

function pageParamError(raw: string | null, field: string, limit: number): Problem | null {
    if (raw === null) return null;
    const value = Number(raw);
    return Number.isInteger(value) && value >= 1 && value <= limit && String(value) === raw ? null : { field, message: `${field} must be a whole number from 1 to ${limit}` };
}

const tooLong = (field: string, value: string | null): Problem | null => (value !== null && new TextEncoder().encode(value).length > MAX_NAME_BYTES ? { field, message: `${field} is longer than ${MAX_NAME_BYTES} bytes` } : null);

function sortError(raw: string | null, kind: UsageKind): Problem | null {
    const [field, direction, ...rest] = (raw ?? "cost:desc").split(":");
    if (rest.length > 0 || (direction !== undefined && !["asc", "desc"].includes(direction.toLowerCase()))) return { field: "sort", message: "sort direction must be asc or desc" };
    if (![...LIST_SORTS, ...(KIND_SORTS[kind] ?? [])].includes(field)) return { field: "sort", message: `${kind} cannot sort by ${field}` };
    return null;
}

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function budgetIdError(budgetId: string | null, kind: UsageKind): Problem | null {
    if (budgetId === null) return null;
    if (budgetId === "") return { field: "budgetId", message: "budgetId must not be empty" };
    if (kind !== "users") return { field: "budgetId", message: "budgetId filters users only" };
    return UUID_PATTERN.test(budgetId) ? null : { field: "budgetId", message: "budgetId must be a UUID" };
}

function populationError(population: string | null): Problem | null {
    return population === null || population === "all" || population === "spenders" ? null : { field: "population", message: "population must be all or spenders" };
}

function expandErrors(expand: string | null, pageSize: string | null): Problem[] {
    if (expand === null) return [];
    if (expand.split(",").some(field => field.trim() !== "daily")) return [{ field: "expand", message: "expand takes only daily" }];
    return Number(pageSize ?? MAX_DAILY_PAGE_SIZE) > MAX_DAILY_PAGE_SIZE ? [{ field: "pageSize", message: `with expand=daily, pageSize must be at most ${MAX_DAILY_PAGE_SIZE}` }] : [];
}

export function listParamsErrors(request: Request, kind: UsageKind): Problem[] {
    const params = new URL(request.url).searchParams;
    const pageSize = pageParamError(params.get("pageSize"), "pageSize", MAX_PAGE_SIZE);
    const single = [
        tooLong("search", params.get("search")),
        pageParamError(params.get("pageNumber"), "pageNumber", 2 ** 31 - 1),
        pageSize,
        populationError(params.get("population")),
        budgetIdError(params.get("budgetId"), kind),
        sortError(params.get("sort"), kind),
    ];
    return [...single.filter((problem): problem is Problem => problem !== null), ...(pageSize ? [] : expandErrors(params.get("expand"), params.get("pageSize")))];
}

export function byNameParamsErrors(request: Request): Problem[] {
    const name = new URL(request.url).searchParams.get("name");
    if (!name) return [{ field: "name", message: "name is required" }];
    const problem = tooLong("name", name);
    return problem ? [problem] : [];
}

const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/;
const ISO_TIME = /^\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
function isIsoDateTime(value: string): boolean {
    const [date, time, ...rest] = value.split("T");
    return rest.length === 0 && ISO_DATE.test(date) && (time === undefined || ISO_TIME.test(time));
}

const utcDayOf = (value: string) => new Date(value).toISOString().slice(0, 10);

function windowFormError(period: string | null, startDate: string | null, endDate: string | null): Problem | null {
    if (period !== null && (startDate !== null || endDate !== null)) return { field: "period", message: "send a period or startDate and endDate, not both" };
    if (period !== null && !(USAGE_PERIODS as readonly string[]).includes(period)) return { field: "period", message: "period must be this_month, last_month, last_7_days, last_30_days or last_90_days" };
    if (startDate === null && endDate !== null) return { field: "startDate", message: "startDate and endDate go together" };
    if (startDate !== null && endDate === null) return { field: "endDate", message: "startDate and endDate go together" };
    return null;
}

function customRangeError(startDate: string, endDate: string): Problem | null {
    if (!isIsoDateTime(startDate)) return { field: "startDate", message: "startDate is an ISO 8601 date-time" };
    if (!isIsoDateTime(endDate)) return { field: "endDate", message: "endDate is an ISO 8601 date-time" };
    const [startDay, endDay] = [utcDayOf(startDate), utcDayOf(endDate)];
    if (startDay > endDay) return { field: "endDate", message: "endDate is before startDate" };
    if (startDay > utcToday()) return { field: "startDate", message: "startDate is in the future" };
    if (startDay < retentionFloorDay()) return { field: "startDate", message: "startDate is before the retained usage history" };
    return null;
}

export function windowParamsError(request: Request): Problem | null {
    const params = new URL(request.url).searchParams;
    const [period, startDate, endDate] = [params.get("period"), params.get("startDate"), params.get("endDate")];
    const formError = windowFormError(period, startDate, endDate);
    if (formError || startDate === null || endDate === null) return formError;
    return customRangeError(startDate, endDate);
}

const RETENTION_DAYS = 90;
const MS_PER_DAY = 86_400_000;
const utcToday = () => new Date().toISOString().slice(0, 10);
function retentionFloor(now = Date.now()): number {
    const cutoff = now - RETENTION_DAYS * MS_PER_DAY;
    const dayStart = cutoff - (cutoff % MS_PER_DAY);
    return dayStart < cutoff ? dayStart + MS_PER_DAY : dayStart;
}
const retentionFloorDay = () => new Date(retentionFloor()).toISOString().slice(0, 10);

const startOfDay = (ms: number) => ms - (ms % MS_PER_DAY);
const monthStart = (ms: number) => Date.UTC(new Date(ms).getUTCFullYear(), new Date(ms).getUTCMonth(), 1);
const nextMonthStart = (ms: number) => Date.UTC(new Date(ms).getUTCFullYear(), new Date(ms).getUTCMonth() + 1, 1);
const LAST_DAYS: Record<string, number> = { last_7_days: 7, last_30_days: 30, last_90_days: 90 };

interface ResolvedWindow {
    start: number;
    end: number;
    dayEnd: number;
}

function resolveWindow(params: URLSearchParams, now: number): ResolvedWindow {
    const [startDate, endDate] = [params.get("startDate"), params.get("endDate")];
    if (startDate !== null && endDate !== null) {
        const dayEnd = startOfDay(Date.parse(endDate)) + MS_PER_DAY;
        return { start: startOfDay(Date.parse(startDate)), end: Math.min(dayEnd, now), dayEnd };
    }
    const period = params.get("period") ?? "this_month";
    if (period === "last_month") {
        const thisMonth = monthStart(now);
        return { start: monthStart(thisMonth - 1), end: thisMonth, dayEnd: thisMonth };
    }
    if (LAST_DAYS[period]) {
        const today = startOfDay(now);
        return { start: today - (LAST_DAYS[period] - 1) * MS_PER_DAY, end: now, dayEnd: today + MS_PER_DAY };
    }
    return { start: monthStart(now), end: now, dayEnd: nextMonthStart(now) };
}

function daysForWindow(pattern: readonly UsageDay[], window: ResolvedWindow): UsageDay[] {
    if (pattern.length === 0) return [];
    const patternStart = Date.parse(`${pattern[0].date}T00:00:00.000Z`);
    const count = Math.ceil((window.end - window.start) / MS_PER_DAY);
    return Array.from({ length: count }, (_, index) => {
        const at = window.start + index * MS_PER_DAY;
        const offset = Math.round((at - patternStart) / MS_PER_DAY);
        const source = pattern[((offset % pattern.length) + pattern.length) % pattern.length];
        return { ...source, date: new Date(at).toISOString().slice(0, 10) };
    });
}

function requestedWindow(summary: UsageSummary, request: Request): ResolvedWindow {
    return resolveWindow(new URL(request.url).searchParams, Date.parse(summary.window.endTime));
}

function asUsageWindow(window: ResolvedWindow): UsageSummary["window"] {
    return { startTime: new Date(window.start).toISOString() as UsageSummary["window"]["startTime"], endTime: new Date(window.end).toISOString() as UsageSummary["window"]["endTime"] };
}

function summaryForRequest(summary: UsageSummary, request: Request): UsageSummary {
    const window = requestedWindow(summary, request);
    return { ...summary, window: asUsageWindow(window), daily: daysForWindow(summary.daily, window) };
}

export function hasComparisonWindow(request: Request, now = Date.now()): boolean {
    const w = resolveWindow(new URL(request.url).searchParams, now);
    const [start, dayEnd] = [new Date(w.start), new Date(w.dayEnd)];
    const whole = start.getUTCDate() === 1 && dayEnd.getUTCDate() === 1 && dayEnd.getUTCHours() === 0;
    const months = (dayEnd.getUTCFullYear() - start.getUTCFullYear()) * 12 + (dayEnd.getUTCMonth() - start.getUTCMonth());
    let comparisonStart: number;
    if (whole && months === 1 && w.end < w.dayEnd) comparisonStart = monthStart(w.start - 1);
    else if (whole && w.end === w.dayEnd) comparisonStart = Date.UTC(start.getUTCFullYear(), start.getUTCMonth() - months, 1);
    else comparisonStart = w.start - (w.dayEnd - w.start);
    return comparisonStart >= retentionFloor(now);
}

function routeParamsOf(request: Request): UsageRouteParams {
    const segments = new URL(request.url).pathname.split("/platform/usage/")[1]?.split("/") ?? [];
    if (segments.length === 2 && segments[1] === "byName") return "byName";
    return segments.length === 1 && !WINDOW_ROUTES.includes(segments[0]) ? "list" : "window";
}

function invalidQuery(problems: readonly Problem[], status: 400 | 422) {
    const details: Record<string, string[]> = {};
    for (const problem of problems) details[problem.field] = [...(details[problem.field] ?? []), problem.message];
    return HttpResponse.json({ message: "Invalid query parameter", errorId: `mock-${status}`, validationDetails: details }, { status });
}

function refusal(request: Request, extra: Problem[] = []) {
    const unknown = unknownParamsErrors(request, routeParamsOf(request));
    if (unknown.length > 0) return invalidQuery(unknown, 400);
    const windowError = windowParamsError(request);
    const problems = [...extra, ...(windowError ? [windowError] : [])];
    return problems.length > 0 ? invalidQuery(problems, 422) : undefined;
}

function findRow(rows: readonly AnyRow[], key: "id" | "bookedName", value: string) {
    return rows.find(row => row[key] === value);
}

const USAGE_TYPE: Record<UsageKind, string> = { agents: "agentUsage", workflows: "workflowUsage", models: "modelUsage", users: "userUsage" };

function rowV2(kind: UsageKind, row: AnyRow) {
    const { bookedName, ...rest } = row;
    return { ...(kind === "users" ? rest : { ...rest, bookedName }), type: USAGE_TYPE[kind] };
}

function detailV2(kind: UsageKind, detail: ReturnType<typeof usageDetailFor>) {
    if (kind !== "workflows" || !("nodes" in detail)) return { ...detail, type: USAGE_TYPE[kind] };
    const { nodes, ...rest } = detail;
    return { ...rest, nodes: nodes.map(({ nodeId, ...node }) => ({ ...node, key: nodeId })), type: USAGE_TYPE[kind] };
}

const data = (body: unknown) => HttpResponse.json({ data: body });
const notFound = () => HttpResponse.json({ message: "Not found", errorId: "mock" }, { status: 404 });

export interface UsageDataset {
    summary: UsageSummary;
    rows: Record<UsageKind, readonly AnyRow[]>;
    savings?: UsageSavings;
}

export const defaultUsageDataset: UsageDataset = {
    summary: usageSummary,
    rows: { workflows: usageWorkflowRows, agents: usageAgentRows, users: usageUserRows, models: usageModelRows },
};

export function usageHandlers(dataset: UsageDataset = defaultUsageDataset): RequestHandler[] {
    const detailFor = (kind: UsageKind, row: AnyRow, request: Request) => {
        const detail = usageDetailFor(kind, row, dataset.summary);
        if (row.daily) return detail;
        const window = requestedWindow(dataset.summary, request);
        return { ...detail, window: asUsageWindow(window), daily: daysForWindow(detail.daily, window) };
    };

    const savingsForRequest = (request: Request, scope: UsageSavings["scope"]): UsageSavings => {
        const window = requestedWindow(dataset.summary, request);
        const endsNow = window.end < window.dayEnd;
        return {
            ...(dataset.savings ?? usageGoldens.savings),
            scope,
            dataAsOf: dataset.summary.dataAsOf,
            historyComplete: dataset.summary.historyComplete,
            window: asUsageWindow(endsNow ? { ...window, end: window.end + SAVINGS_READ_LAG_MS } : window),
        };
    };

    return [
        http.get(`${USAGE}/*`, ({ request }) => {
            const route = routeParamsOf(request);
            const kind = new URL(request.url).pathname.split("/platform/usage/")[1]?.split("/")[0] as UsageKind;
            if (route === "list") return refusal(request, listParamsErrors(request, kind));
            return refusal(request, route === "byName" ? byNameParamsErrors(request) : []);
        }),
        ...[SELF_USAGE, SELF_USAGE_SAVINGS].map(path =>
            http.get(path, ({ request }) => {
                const unknown = unknownParamsErrors(request, "window");
                if (unknown.length > 0) return invalidQuery(unknown, 400);
                const windowError = windowParamsError(request);
                return windowError ? invalidQuery([windowError], 422) : undefined;
            })
        ),
        http.get(`${USAGE}/summary`, ({ request }) => {
            const noComparison = !dataset.summary.historyComplete || !hasComparisonWindow(request, Date.parse(dataset.summary.window.endTime));
            const summary = summaryForRequest(dataset.summary, request);
            return data(noComparison ? { ...summary, comparison: null, comparisonWindow: null } : summary);
        }),
        http.get(`${USAGE}/savings`, ({ request }) => data(savingsForRequest(request, "tenant"))),
        http.get(SELF_USAGE_SAVINGS, ({ request }) => data(savingsForRequest(request, "self"))),
        http.get(`${USAGE}/:kind/byName`, ({ request, params }) => {
            const kind = params.kind as UsageKind;
            const row = findRow(dataset.rows[kind] ?? [], "bookedName", new URL(request.url).searchParams.get("name") ?? "");
            return row ? data(detailV2(kind, detailFor(kind, row, request))) : notFound();
        }),
        http.get(`${USAGE}/:kind/:id`, ({ request, params }) => {
            const kind = params.kind as UsageKind;
            const row = findRow(dataset.rows[kind] ?? [], "id", String(params.id));
            return row ? data(detailV2(kind, detailFor(kind, row, request))) : notFound();
        }),
        http.get(`${USAGE}/:kind`, ({ request, params }) => {
            const kind = params.kind as UsageKind;
            const page = listRowsFromRequest(request, dataset.rows[kind] ?? []);
            return HttpResponse.json({ ...page, data: page.data.map(row => rowV2(kind, row)) });
        }),
        http.get(SELF_USAGE, () => {
            const { id, ...self } = usageGoldens.self;
            return data({ ...self, userId: id });
        }),
    ];
}

export const usageForbiddenHandler = http.get(`${USAGE}/*`, () => HttpResponse.json({ message: "Reading every user's usage requires the *:*:read_usage scope", errorId: "mock-403" }, { status: 403 }));
export const usageSlowReadHandler = http.get(`${USAGE}/*`, () =>
    HttpResponse.json({ message: "Usage figures took too long to read; retry shortly, or choose a shorter date range", errorId: "mock-503" }, { status: 503, headers: { "Retry-After": "30" } })
);
export const usageErrorHandler = http.get(`${USAGE}/*`, () => HttpResponse.json({ message: "Service unavailable", errorId: "mock-500" }, { status: 500 }));
