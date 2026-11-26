// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { subjectQuery, usageListQuery, windowQuery } from "./service";

describe("windowQuery", () => {
    test("sends a preset period alone", () => {
        expect(windowQuery({ period: "last_7_days" }).toString()).toBe("period=last_7_days");
    });

    test("sends a custom range as startDate and endDate, never with a period", () => {
        expect(windowQuery({ startDate: "2026-09-01", endDate: "2026-09-30" }).toString()).toBe("startDate=2026-09-01&endDate=2026-09-30");
    });
});

describe("usageListQuery", () => {
    test("sends paging, sort, a trimmed search, the population and the budget", () => {
        const query = usageListQuery({ window: { period: "this_month" }, pageNumber: 2, pageSize: 25, sortBy: "budgetUsage", sortDir: "desc", name: "  par ", population: "all", budgetId: "b1" });
        expect(query.toString()).toBe("period=this_month&pageNumber=2&pageSize=25&sort=budgetUsage%3Adesc&search=par&population=all&budgetId=b1");
    });

    test("omits an empty search and an unset population", () => {
        const query = usageListQuery({ window: { period: "this_month" }, pageNumber: 1, pageSize: 5, sortBy: "cost", sortDir: "asc", name: "  " });
        expect(query.toString()).toBe("period=this_month&pageNumber=1&pageSize=5&sort=cost%3Aasc");
    });
});

describe("subjectQuery", () => {
    test("addresses a subject with an id by id", () => {
        expect(subjectQuery("workflows", { id: "01a10c9e-3333-7e77-be6a-3cdc8d0b9301" }, { period: "this_month" })).toBe("workflows/01a10c9e-3333-7e77-be6a-3cdc8d0b9301?period=this_month");
    });

    test("addresses a subject with no id by its booked name, in the name query parameter", () => {
        expect(subjectQuery("models", { bookedName: "openai/gpt 4.1" }, { period: "this_month" })).toBe("models/byName?period=this_month&name=openai%2Fgpt+4.1");
    });

    test("sends a dot-segment name as it is, since a query parameter is never resolved away", () => {
        expect(subjectQuery("agents", { bookedName: ".." }, { startDate: "2026-09-01T00:00:00.000Z", endDate: "2026-09-30T00:00:00.000Z" })).toBe("agents/byName?startDate=2026-09-01T00%3A00%3A00.000Z&endDate=2026-09-30T00%3A00%3A00.000Z&name=..");
    });

    test.each([
        ["an id that is not a UUID", { id: "." }],
        ["an empty name", { bookedName: "" }],
    ])("refuses %s rather than address a different route", (_what, ref) => {
        expect(() => subjectQuery("agents", ref, { period: "this_month" })).toThrow();
    });

    test("refuses a user by name, which has no route", () => {
        expect(() => subjectQuery("users", { bookedName: "google-oauth2|1" }, { period: "this_month" })).toThrow();
    });
});
