// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterAll, describe, expect, test, vi } from "vitest";

import { USAGE_BUDGETS, usageModelRows, usageUserRows } from "@/testing/data/usage";

import { byNameParamsErrors, hasComparisonWindow, listParamsErrors, listRowsFromRequest, unknownParamsErrors, windowParamsError } from "./usageHandlers";

vi.useFakeTimers({ toFake: ["Date"] });
vi.setSystemTime(new Date("2026-10-16T12:00:00.000Z"));
afterAll(() => {
    vi.useRealTimers();
});

const at = (query: string) => new Request(`http://localhost/api/v2/platform/usage/summary${query}`);

const EXPECTED = {
    "both forms": { field: "period" },
    "an unknown period": { field: "period" },
    "half a range": { field: "endDate" },
    "an inverted range": { field: "endDate", message: "endDate is before startDate" },
    "a start before the retained history": { field: "startDate", message: "startDate is before the retained usage history" },
    "a start in the future": { field: "startDate", message: "startDate is in the future" },
};

describe("windowParamsError", () => {
    test("reads an offset date-time as the UTC day containing it, as the server does", () => {
        const day = new Date(Date.now() - 5 * 86_400_000).toISOString().slice(0, 10);
        const previous = new Date(Date.now() - 6 * 86_400_000).toISOString().slice(0, 10);
        expect(windowParamsError(at(`?startDate=${day}T00:00:00.000Z&endDate=${encodeURIComponent(`${day}T00:00:00+02:00`)}`))).toMatchObject({ field: "endDate", message: "endDate is before startDate" });
        expect(windowParamsError(at(`?startDate=${previous}T00:00:00Z&endDate=${day}`))).toBeNull();
    });

    const today = new Date().toISOString().slice(0, 10);
    const daysAgo = (n: number) => new Date(Date.now() - n * 86_400_000).toISOString().slice(0, 10);

    test.each(["", "?period=this_month", `?startDate=${daysAgo(10)}T00:00:00.000Z&endDate=${daysAgo(3)}T00:00:00.000Z`, `?startDate=${today}T00:00:00.000Z&endDate=${today}T00:00:00.000Z`])("accepts %s", query => {
        expect(windowParamsError(at(query))).toBeNull();
    });

    test.each([
        ["both forms", "?period=this_month&startDate=2026-09-01T00:00:00.000Z&endDate=2026-10-01T00:00:00.000Z"],
        ["an unknown period", "?period=yesterday"],
        ["half a range", "?startDate=2026-09-01T00:00:00.000Z"],
        ["an inverted range", `?startDate=${daysAgo(3)}T00:00:00.000Z&endDate=${daysAgo(10)}T00:00:00.000Z`],
        ["a start before the retained history", `?startDate=${daysAgo(120)}T00:00:00.000Z&endDate=${daysAgo(3)}T00:00:00.000Z`],
        ["a start in the future", `?startDate=${daysAgo(-2)}T00:00:00.000Z&endDate=${daysAgo(-4)}T00:00:00.000Z`],
    ])("refuses %s", (_what, query) => {
        expect(windowParamsError(at(query))).toMatchObject(EXPECTED[_what as keyof typeof EXPECTED] ?? {});
    });
});

const list = (kind: string, query: string) => new Request(`http://localhost/api/v2/platform/usage/${kind}${query}`);
const fieldsOf = (problems: ReadonlyArray<{ field: string }>) => problems.map(problem => problem.field);

describe("list parameters", () => {
    test("refuses an expand other than daily, and a sort the kind has no such field for, as the server does", () => {
        expect(fieldsOf(listParamsErrors(list("models", "?expand=nodes"), "models"))).toEqual(["expand"]);
        expect(fieldsOf(listParamsErrors(list("agents", "?sort=model:asc"), "agents"))).toEqual(["sort"]);
        expect(fieldsOf(listParamsErrors(list("models", "?sort=budget:desc"), "models"))).toEqual(["sort"]);
        expect(listParamsErrors(list("models", "?sort=model:desc&expand=daily"), "models")).toEqual([]);
    });

    test("adds each row's days only with expand=daily", () => {
        expect(listRowsFromRequest(list("models", ""), usageModelRows).data.every(row => row.daily === null)).toBe(true);
        expect(listRowsFromRequest(list("models", "?expand=daily"), usageModelRows).data.every(row => row.daily?.length === 16)).toBe(true);
    });

    test("sorts by the provider model that served most of each row, rows with none last either way", () => {
        const names = (query: string) => listRowsFromRequest(list("models", query), [...usageModelRows, { ...usageModelRows[0], id: null, name: "unserved", bookedName: "unserved", models: [] }]).data.map(row => row.name);
        expect(names("?sort=model:asc")).toEqual(["General", "gpt-4.1-mini", "Image Generation", "Planning", "unserved"]);
        expect(names("?sort=model:desc")).toEqual(["Planning", "Image Generation", "gpt-4.1-mini", "General", "unserved"]);
    });

    test("a budgetId keeps the users the budget applies to, and the default keeps the bare subjects too", () => {
        const names = (budgetId: string) => listRowsFromRequest(list("users", `?budgetId=${budgetId}`), usageUserRows).data.map(row => row.name);
        expect(names(USAGE_BUDGETS.engineering.id)).toEqual(["Dana Finance", "Build Bot"]);
        expect(names(USAGE_BUDGETS.default.id)).toEqual(["Riley Legal", "Jordan Marketing", null]);
        expect(names("0b0d6e70-0000-4000-8000-000000000999")).toEqual([]);
    });

    test("never matches a nameless users row by name, and sorts it last under name", () => {
        expect(listRowsFromRequest(list("users", "?search=google"), usageUserRows).data).toHaveLength(0);
        expect(listRowsFromRequest(list("users", "?sort=name:desc"), usageUserRows).data.at(-1)?.name).toBeNull();
        expect(listRowsFromRequest(list("users", "?sort=name:asc"), usageUserRows).data.at(-1)?.name).toBeNull();
    });
});

describe("budget sorts, as budgets.go's SortUsers orders them", () => {
    const nobody = { ...usageUserRows[1], id: "01a10c9e-0000-7e77-be6a-3cdc8d0b9999", name: "Nobody", budget: null, budgetUsage: null };
    const bareWithout = { ...usageUserRows.at(-1)!, cost: "1.0000000000" as const, budget: null, budgetUsage: null };
    const keys = (query: string) => listRowsFromRequest(list("users", query), [...usageUserRows, nobody, bareWithout]).data.map(row => row.name ?? `${row.id === null ? "bare" : "unknown"} $${Number(row.cost)}`);

    test("by budget, unlimited ranks as the largest and follows the direction; a bare subject ranks by the default it stands on", () => {
        expect(keys("?sort=budget:desc")[0]).toBe("Casey Unlimited");
        expect(keys("?sort=budget:desc").slice(-5)).toEqual(["Jordan Marketing", "bare $3.1", "Nobody", "unknown $6.2", "bare $1"]);
        expect(keys("?sort=budget:asc").slice(0, 3)).toEqual(["Riley Legal", "Jordan Marketing", "bare $3.1"]);
        expect(keys("?sort=budget:asc").slice(-4)).toEqual(["Casey Unlimited", "Nobody", "unknown $6.2", "bare $1"]);
    });

    test("by budget usage, unlimited, no budget and a bare subject with none come after every budgeted row in both directions", () => {
        expect(keys("?sort=budgetUsage:asc")[0]).toBe("bare $3.1");
        expect(keys("?sort=budgetUsage:asc").slice(-4)).toEqual(["Casey Unlimited", "Nobody", "unknown $6.2", "bare $1"]);
        expect(keys("?sort=budgetUsage:desc").slice(-5)).toEqual(["bare $3.1", "Casey Unlimited", "Nobody", "unknown $6.2", "bare $1"]);
        expect(keys("?sort=budgetUsage:desc")[0]).toBe("Parminder Procurement");
    });
});

test("sort=model reads a row's costliest provider model with a recorded name", () => {
    const unrecordedFirst = {
        ...usageModelRows[2],
        name: "Unrecorded First",
        models: [
            { ...usageModelRows[2].models[0], name: null, provider: null },
            { ...usageModelRows[2].models[0], name: "aardvark-1" },
        ],
    };
    expect(listRowsFromRequest(list("models", "?sort=model:asc"), [...usageModelRows, unrecordedFirst]).data[0].name).toBe("Unrecorded First");
});

describe("parameters the server refuses", () => {
    test("a parameter the route does not take, or one sent twice, every one at once", () => {
        expect(fieldsOf(unknownParamsErrors(list("agents", "?period=this_month&foo=1&bar=2"), "list"))).toEqual(["foo", "bar"]);
        expect(fieldsOf(unknownParamsErrors(list("agents", "?period=this_month&period=last_month"), "list"))).toEqual(["period"]);
        expect(fieldsOf(unknownParamsErrors(at("?period=this_month&pageSize=5"), "window"))).toEqual(["pageSize"]);
        expect(fieldsOf(unknownParamsErrors(at("?period=this_month&name=x"), "window"))).toEqual(["name"]);
        expect(unknownParamsErrors(at("?period=this_month&name=x"), "byName")).toEqual([]);
        expect(unknownParamsErrors(list("agents", "?period=this_month&pageSize=5&expand=daily"), "list")).toEqual([]);
    });

    test("every bad list parameter together, as the server names them", () => {
        expect(fieldsOf(listParamsErrors(list("agents", "?pageNumber=0&pageSize=500&sort=cost:up&population=some&budgetId=x"), "agents"))).toEqual(["pageNumber", "pageSize", "population", "budgetId", "sort"]);
        expect(fieldsOf(listParamsErrors(list("users", "?budgetId="), "users"))).toEqual(["budgetId"]);
        expect(fieldsOf(listParamsErrors(list("users", "?budgetId=engineering"), "users"))).toEqual(["budgetId"]);
    });

    test("a byName route's name: required, and no longer than the server keeps", () => {
        expect(byNameParamsErrors(at("?period=this_month"))).toEqual([{ field: "name", message: "name is required" }]);
        expect(fieldsOf(byNameParamsErrors(at(`?name=${"x".repeat(257)}`)))).toEqual(["name"]);
        expect(byNameParamsErrors(at("?name=.."))).toEqual([]);
    });

    test.each([
        ["?pageSize=101", "pageSize"],
        ["?pageSize=0", "pageSize"],
        ["?pageNumber=1.5", "pageNumber"],
        ["?expand=daily&pageSize=26", "pageSize"],
        ["?sort=cost:up", "sort"],
        ["?sort=bogus:asc", "sort"],
        ["?population=some", "population"],
        [`?search=${"x".repeat(257)}`, "search"],
    ])("%s", (query, field) => {
        expect(fieldsOf(listParamsErrors(list("agents", query), "agents"))).toEqual([field]);
    });
});

describe("hasComparisonWindow, as window.go's Comparison", () => {
    test.each([
        ["?period=this_month", true],
        ["?period=last_month", true],
        ["?period=last_30_days", true],
        ["?period=last_90_days", false],
        ["?startDate=2026-09-01&endDate=2026-09-30", true],
        ["?startDate=2026-08-20&endDate=2026-10-10", false],
        ["?startDate=2026-10-10&endDate=2026-10-12", true],
    ])("%s → %s", (query, expected) => {
        expect(hasComparisonWindow(at(query))).toBe(expected);
    });
});
