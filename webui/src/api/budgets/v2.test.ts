// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "../common";

import {
    cancelBudgetTopUpV2,
    createBudgetAssignmentV2,
    createBudgetV2,
    deleteBudgetAssignmentV2,
    deleteBudgetV2,
    DuplicateBudgetNameError,
    findDefaultBudgetV2,
    getBudgetV2,
    grantBudgetTopUpV2,
    listBudgetAssignmentsV2,
    listBudgetSubjectsV2,
    listBudgetsV2,
    makeDefaultBudgetV2,
    moveBudgetAssignmentV2,
    updateBudgetAssignmentsV2,
    updateBudgetV2,
} from "./v2";

const budget = (overrides: Record<string, unknown> = {}) => ({
    id: "b-1",
    type: "budget",
    name: "Engineering",
    subjectType: "user",
    amount: "250.00",
    warningPercentage: 80,
    isDefault: false,
    assignedCount: 3,
    hiddenAssignedCount: 0,
    pendingCount: 1,
    createdBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedBy: "u2",
    updatedTime: null,
    ...overrides,
});

const assignment = (overrides: Record<string, unknown> = {}) => ({
    id: "a-1",
    type: "budgetAssignment",
    state: "concrete",
    subject: { type: "user", id: "u-1", displayName: "Ada", user: { email: "ada@example.com", isSystemUser: false, emailShared: null, sub: null, issuer: null }, identity: null },
    budget: { id: "b-1", name: "Engineering" },
    createdBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedBy: "u1",
    updatedTime: "2026-01-02T03:04:05.678Z",
    ...overrides,
});

const topUp = (overrides: Record<string, unknown> = {}) => ({
    amount: "50.00",
    expiresTime: "2026-02-01T00:00:00.000Z",
    createdBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedBy: "u1",
    updatedTime: "2026-01-02T03:04:05.678Z",
    ...overrides,
});

const page = (data: unknown[], nextPage: number | null = null, pageNumber = 1) => ({ data, meta: { pagination: { pageNumber, count: data.length, pageSize: 100, nextPage, totalPages: nextPage ? 2 : 1 } } });

describe("budgets on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status }));
    const call = (n: number) => ({ url: fetchMock.mock.calls[n][0], method: fetchMock.mock.calls[n][1]?.method ?? "GET", body: JSON.parse((fetchMock.mock.calls[n][1]?.body as string) ?? "null") });

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("lists budgets with the name filter sent as search, and maps unknown times to empty strings", async () => {
        respond(200, page([budget()]));

        const result = await listBudgetsV2({ pageNumber: 2, pageSize: 20, subjectType: "user", name: " eng ", sortBy: "amount", sortDir: "desc" });

        expect(call(0)).toMatchObject({ url: "/api/v2/platform/budgets?pageNumber=2&pageSize=20&subjectType=user&search=eng&sort=amount%3Adesc", method: "GET" });
        expect(result.data).toEqual([{ ...budget(), updatedTime: "" }]);
        expect(result.meta.pagination.count).toBe(1);
    });

    test("finds the tier's default across two pages", async () => {
        respond(200, page([budget()], 2));
        respond(200, page([budget({ id: "b-def", isDefault: true })], null, 2));

        const found = await findDefaultBudgetV2("user");

        expect(found?.id).toBe("b-def");
        expect(call(0).url).toBe("/api/v2/platform/budgets?pageNumber=1&pageSize=100&subjectType=user");
        expect(call(1).url).toBe("/api/v2/platform/budgets?pageNumber=2&pageSize=100&subjectType=user");
    });

    test("a tier with no default finds null", async () => {
        respond(200, page([budget()]));
        await expect(findDefaultBudgetV2("mesh")).resolves.toBeNull();
    });

    test.each([
        ["get", () => getBudgetV2("b 1"), "GET", "/api/v2/platform/budgets/b%201", null],
        ["create", () => createBudgetV2({ name: "Eng", subjectType: "user", amount: null }), "POST", "/api/v2/platform/budgets", { name: "Eng", subjectType: "user", amount: null }],
        ["update", () => updateBudgetV2("b-1", { amount: null }), "PATCH", "/api/v2/platform/budgets/b-1", { amount: null }],
        ["makeDefault", () => makeDefaultBudgetV2("b-1"), "PUT", "/api/v2/platform/budgets/b-1/makeDefault", null],
    ])("%s unwraps the budget from {data}", async (_name, run, method, url, body) => {
        respond(200, { data: budget() });

        const result = await run();

        expect(call(0)).toEqual({ url, method, body });
        expect(result).toMatchObject({ id: "b-1", createdTime: "2026-01-02T03:04:05.678Z", updatedTime: "" });
    });

    test.each([
        ["budget", () => deleteBudgetV2("b-1"), "/api/v2/platform/budgets/b-1"],
        ["assignment", () => deleteBudgetAssignmentV2("a-1"), "/api/v2/platform/budgetAssignments/a-1"],
    ])("deletes a %s", async (_name, run, url) => {
        respond(204);
        await expect(run()).resolves.toBeUndefined();
        expect(call(0)).toMatchObject({ url, method: "DELETE" });
    });

    test("a duplicate name rejects with the v2 conflict sentence and status", async () => {
        respond(409, { message: "A budget with this name already exists.", errorId: "e1" });

        const error = await createBudgetV2({ name: "Eng", subjectType: "user", amount: "1" }).catch((e: unknown) => e);

        expect(error).toBeInstanceOf(DuplicateBudgetNameError);
        expect(error).toMatchObject({ status: 409, message: "A budget with this name already exists." });
    });

    test("any other conflict stays a plain v2 error", async () => {
        respond(409, { message: "The default budget cannot be deleted.", errorId: "e1" });

        const error = await updateBudgetV2("b-1", { name: "Eng" }).catch((e: unknown) => e);

        expect(error).toBeInstanceOf(ApiV2Error);
        expect(error).not.toBeInstanceOf(DuplicateBudgetNameError);
    });

    test("field errors fold into the message under their v2 names", async () => {
        respond(422, { message: "Unable to create budget", errorId: "e2", validationDetails: { amount: ["must be a decimal"] } });
        await expect(createBudgetV2({ name: "Eng", subjectType: "user", amount: "x" })).rejects.toThrow("Unable to create budget: amount: must be a decimal");
    });

    test("lists budget subjects, filling in a missing user", async () => {
        const subject = { id: "u-1", type: "budgetSubject", subjectType: "user", displayName: "Ada", user: null, budget: null, topUp: null, effectiveAmount: null };
        respond(200, page([subject]));

        const result = await listBudgetSubjectsV2({ subjectType: "user", pageNumber: 1, pageSize: 20, search: " ad ", sortDir: "desc" });

        expect(call(0).url).toBe("/api/v2/platform/budgetSubjects?subjectType=user&pageNumber=1&pageSize=20&search=ad&sort=displayName%3Adesc");
        expect(result.data[0].user).toEqual({ email: null, isSystemUser: false });
    });

    test("lists assignments, dropping null user and identity fields", async () => {
        const pending = assignment({ id: "a-2", state: "pending", subject: { type: "user", id: null, displayName: null, user: null, identity: { type: "email", value: "x@example.com", issuer: null } } });
        respond(200, page([assignment(), pending]));

        const result = await listBudgetAssignmentsV2({ budgetId: "b-1", pageNumber: 1, pageSize: 20, search: "ada", state: "concrete" });

        expect(call(0).url).toBe("/api/v2/platform/budgetAssignments?budgetId=b-1&pageNumber=1&pageSize=20&search=ada&state=concrete");
        expect(result.data[0].subject).toEqual({ type: "user", id: "u-1", displayName: "Ada", user: { email: "ada@example.com", isSystemUser: false } });
        expect(result.data[1].subject).toEqual({ type: "user", id: null, displayName: null, identity: { type: "email", value: "x@example.com" } });
    });

    test("creates and moves an assignment", async () => {
        respond(201, { data: assignment() });
        respond(200, { data: assignment({ budget: { id: "b-2", name: "Other" } }) });

        await createBudgetAssignmentV2({ subjectType: "user", subjectId: "u-1", budgetId: "b-1" });
        const moved = await moveBudgetAssignmentV2("a-1", "b-2");

        expect(call(0)).toEqual({ url: "/api/v2/platform/budgetAssignments", method: "POST", body: { subjectType: "user", subjectId: "u-1", budgetId: "b-1" } });
        expect(call(1)).toEqual({ url: "/api/v2/platform/budgetAssignments/a-1", method: "PATCH", body: { budgetId: "b-2" } });
        expect(moved.budget.id).toBe("b-2");
    });

    test("saves a budget's assignments and unwraps the changes", async () => {
        const changes = { added: [{ assignmentId: "a-1", subject: { id: "u-1" } }], moved: [], removed: [], unchanged: [] };
        respond(200, { data: changes });

        const result = await updateBudgetAssignmentsV2("b-1", { subjectType: "user", add: ["u-1"], remove: [] });

        expect(call(0)).toEqual({ url: "/api/v2/platform/budgets/b-1/updateAssignments", method: "PUT", body: { subjectType: "user", add: ["u-1"], remove: [] } });
        expect(result).toEqual(changes);
    });

    describe("a top-up", () => {
        test.each([
            [201, "creates"],
            [200, "replaces"],
        ])("a grant is one PUT to the subject's top-up, which %i answers when it %s it", async status => {
            respond(status, { data: topUp({ amount: "75.00" }) });

            await expect(grantBudgetTopUpV2({ subjectType: "user", subjectId: "u 1", amount: "75" })).resolves.toBeUndefined();

            expect(call(0)).toEqual({ url: "/api/v2/platform/budgetSubjects/u%201/topUp", method: "PUT", body: { amount: "75" } });
            expect(fetchMock).toHaveBeenCalledTimes(1);
        });

        test("a cancel sets the subject's top-up to 0", async () => {
            respond(200, { data: topUp({ amount: null, expiresTime: null, createdBy: null, createdTime: null, updatedBy: null, updatedTime: null }) });

            await expect(cancelBudgetTopUpV2("u-1")).resolves.toBeUndefined();

            expect(call(0)).toEqual({ url: "/api/v2/platform/budgetSubjects/u-1/topUp", method: "PUT", body: { amount: "0" } });
        });

        test("a refusal rejects with its sentence and status, without a retry", async () => {
            const sentence = "The budget that applies to this subject is unlimited, so a top-up would have no effect. Set an amount on it first.";
            respond(409, { message: sentence, errorId: "e5" });

            await expect(grantBudgetTopUpV2({ subjectType: "user", subjectId: "u-1", amount: "75" })).rejects.toMatchObject({ status: 409, message: sentence });
            expect(fetchMock).toHaveBeenCalledTimes(1);
        });

        test("a validation failure names the field", async () => {
            respond(422, { message: "Unable to set budget top-up", errorId: "e7", validationDetails: { amount: ["must be at most 100000.00"] } });

            await expect(grantBudgetTopUpV2({ subjectType: "user", subjectId: "u-1", amount: "100000.01" })).rejects.toThrow("Unable to set budget top-up: amount: must be at most 100000.00");
        });
    });
});
