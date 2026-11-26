// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { usageAgentRows, usageDetailFor, usageModelRows, usageSummary, usageUserRows, usageWorkflowRows } from "@/testing/data/usage";

import { ApiV2Error } from "../common";

import type { UsageSavings, UsageSelf, UsageWorkflowDetail } from "./types";
import { getSelfUsageSavingsV2, getSelfUsageV2, getUsageDetailV2, getUsageSavingsV2, getUsageSummaryV2, listUsageV2 } from "./v2";

const ID = "01a10c9e-3333-7e77-be6a-3cdc8d0b9301";
const WINDOW = { period: "this_month" } as const;

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 25, nextPage, totalPages: 1 } } });

describe("usage on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown, headers?: HeadersInit) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status, headers }));
    const url = (n: number) => fetchMock.mock.calls[n][0];

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("reads the tenant's and the caller's own savings, unwrapped from {data}", async () => {
        const savings: UsageSavings = {
            scope: "tenant",
            dataAsOf: null,
            historyComplete: true,
            window: usageSummary.window,
            currency: "USD",
            avoidedInputTokens: 1750,
            avoidedCost: "0.0700000000",
            calls: 4,
            measuredCalls: 3,
            unpricedCalls: 1,
        };
        respond(200, { data: savings });
        respond(200, { data: { ...savings, scope: "self" } });

        await expect(getUsageSavingsV2(WINDOW)).resolves.toEqual(savings);
        await expect(getSelfUsageSavingsV2(WINDOW)).resolves.toEqual({ ...savings, scope: "self" });
        expect(url(0)).toBe("/api/v2/platform/usage/savings?period=this_month");
        expect(url(1)).toBe("/api/v2/platform/userUsage/savings?period=this_month");
    });

    test("reads the summary, unwrapped from {data}", async () => {
        respond(200, { data: usageSummary });

        await expect(getUsageSummaryV2({ startDate: "2026-09-01", endDate: "2026-09-30" })).resolves.toEqual(usageSummary);
        expect(url(0)).toBe("/api/v2/platform/usage/summary?startDate=2026-09-01&endDate=2026-09-30");
    });

    test("lists with the name filter sent as search, dropping each row's type", async () => {
        const [row] = usageAgentRows;
        respond(200, page([{ ...row, type: "agentUsage" }]));

        const result = await listUsageV2("agents", { window: WINDOW, pageNumber: 2, pageSize: 25, sortBy: "cost", sortDir: "desc", name: " code ", population: "spenders", expandDaily: true });

        expect(url(0)).toBe("/api/v2/platform/usage/agents?period=this_month&pageNumber=2&pageSize=25&sort=cost%3Adesc&search=code&population=spenders&expand=daily");
        expect(result.data).toEqual([row]);
    });

    test("a users row, which has no bookedName on v2, gets v1's null", async () => {
        const { bookedName: _bookedName, ...row } = usageUserRows[0];
        respond(200, page([{ ...row, type: "userUsage" }]));

        const result = await listUsageV2("users", { window: WINDOW, pageNumber: 1, pageSize: 25, sortBy: "budgetUsage", sortDir: "asc", budgetId: "b-1" });

        expect(url(0)).toBe("/api/v2/platform/usage/users?period=this_month&pageNumber=1&pageSize=25&sort=budgetUsage%3Aasc&budgetId=b-1");
        expect(result.data).toEqual([usageUserRows[0]]);
        expect(result.data[0].bookedName).toBeNull();
    });

    test("a models row keeps its served models", async () => {
        const [row] = usageModelRows;
        respond(200, page([{ ...row, type: "modelUsage" }]));

        const result = await listUsageV2("models", { window: WINDOW, pageNumber: 1, pageSize: 25, sortBy: "model", sortDir: "asc" });

        expect(result.data).toEqual([row]);
    });

    test("a workflow's nodes carry v2's key as nodeId", async () => {
        const detail = usageDetailFor("workflows", usageWorkflowRows[0]) as UsageWorkflowDetail;
        const wire = { ...detail, type: "workflowUsage", nodes: detail.nodes.map(({ nodeId, ...node }) => ({ ...node, key: nodeId })) };
        respond(200, { data: wire });

        const result = await getUsageDetailV2("workflows", { id: ID }, WINDOW);

        expect(url(0)).toBe(`/api/v2/platform/usage/workflows/${ID}?period=this_month`);
        expect(result).toEqual(detail);
    });

    test("a subject with no id is read by its booked name", async () => {
        const detail = usageDetailFor("agents", usageAgentRows[0]);
        respond(200, { data: { ...detail, type: "agentUsage" } });

        const result = await getUsageDetailV2("agents", { bookedName: "yaml agent" }, WINDOW);

        expect(url(0)).toBe("/api/v2/platform/usage/agents/byName?period=this_month&name=yaml+agent");
        expect(result).toEqual(detail);
    });

    test("an id that is not a UUID is refused before any request", async () => {
        await expect(getUsageDetailV2("users", { id: "nope" }, WINDOW)).rejects.toThrow("A usage subject id is a UUID");
        expect(fetchMock).not.toHaveBeenCalled();
    });

    test("self usage is read from /userUsage, its userId becoming id", async () => {
        const self = { ...(usageDetailFor("users", usageUserRows[0]) as UsageSelf), scope: "self", months: [] };
        const { id, ...rest } = self;
        respond(200, { data: { ...rest, userId: id } });

        const result = await getSelfUsageV2(WINDOW);

        expect(url(0)).toBe("/api/v2/platform/userUsage?period=this_month");
        expect(result).toEqual(self);
    });

    test("a slow read rejects with 503, which the hooks never retry", async () => {
        respond(503, { message: "Usage figures took too long to read; retry shortly, or choose a shorter date range", errorId: "e1" }, { "Retry-After": "30" });

        const error = await getUsageSummaryV2(WINDOW).catch((e: unknown) => e);

        expect(error).toBeInstanceOf(ApiV2Error);
        expect(error).toMatchObject({ status: 503 });
    });

    test("window problems fold into the message under their v2 names", async () => {
        respond(422, { message: "Invalid query parameter", errorId: "e2", validationDetails: { endDate: ["must not be before startDate"] } });

        await expect(getSelfUsageV2({ startDate: "2026-09-30", endDate: "2026-09-01" })).rejects.toThrow("Invalid query parameter: endDate: must not be before startDate");
    });
});
