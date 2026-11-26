// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import {
    createClaimMappingV2,
    createRoleV2,
    deleteClaimMappingV2,
    deleteRoleV2,
    getDefaultRolesV2,
    getRoleV2,
    getUserRoleGrantsV2,
    getUserV2,
    listClaimMappingsV2,
    listComponentAccessUsersV2,
    listRolesV2,
    listSystemUsersV2,
    listUsersV2,
    setDefaultRolesV2,
    setUserRoleGrantsV2,
    updateClaimMappingV2,
    updateRoleV2,
} from "./v2";

const page = (data: unknown[]) => ({ data, meta: { pagination: { pageNumber: 2, count: data.length, pageSize: 20, nextPage: null, totalPages: 2 } } });

describe("component access on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("maps each user's userId onto the id the table keys on", async () => {
        fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(page([{ userId: "u1", displayName: "Ada", userName: "ada", email: "ada@example.com", isSystemUser: false }]))));

        const got = await listComponentAccessUsersV2({ agentId: "a1" }, { pageNumber: 2, pageSize: 20, search: " ada " });

        expect(fetchMock.mock.calls[0][0]).toBe("/api/v2/platform/agents/a1/access?pageNumber=2&pageSize=20&search=ada");
        expect(got.data).toEqual([{ id: "u1", displayName: "Ada", userName: "ada", email: "ada@example.com", isSystemUser: false }]);
        expect(got.meta.pagination.totalPages).toBe(2);
    });

    test("a name v2 has no record of comes back empty, as on v1", async () => {
        fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(page([{ userId: "svc", displayName: null, userName: null, email: null, isSystemUser: true }]))));

        const got = await listComponentAccessUsersV2({ agentId: "a1" }, { pageNumber: 1, pageSize: 20 });

        expect(got.data).toEqual([{ id: "svc", displayName: "", userName: "", email: "", isSystemUser: true }]);
    });

    test("a workflow's access is read from the workflow", async () => {
        fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(page([]))));

        await listComponentAccessUsersV2({ workflowId: "w 1" }, { pageNumber: 1, pageSize: 20 });

        expect(fetchMock.mock.calls[0][0]).toBe("/api/v2/platform/workflows/w%201/access?pageNumber=1&pageSize=20");
    });
});

const json = (body: unknown, status = 200, headers?: HeadersInit) => new Response(status === 204 ? null : JSON.stringify(body), { status, headers });
const listPage = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: nextPage ? 2 : 1 } } });

const roleV2 = (overrides: Record<string, unknown> = {}) => ({
    id: "r1",
    type: "role",
    name: "Operators",
    description: null,
    scopes: ["agent:*:invoke"],
    inherits: [],
    origin: "custom",
    nameConflict: false,
    createdBy: "ada@example.com",
    updatedBy: null,
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const userV2 = (overrides: Record<string, unknown> = {}) => ({
    id: "u1",
    type: "user",
    displayName: "Ada",
    userName: null,
    email: "ada@example.com",
    issuer: "https://idp.example.com",
    sub: "ada",
    oidcProvider: null,
    provisionedVia: "jit",
    active: true,
    groups: [],
    roles: [{ roleId: "r1", source: "direct", manageable: true }],
    lastLoginTime: null,
    createdBy: null,
    updatedBy: null,
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: "2026-01-03T00:00:00.000Z",
    budget: null,
    budgetTopUp: null,
    effectiveBudgetAmount: null,
    ...overrides,
});

describe("RBAC and users on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const call = (i = 0) => ({ url: fetchMock.mock.calls[i][0], init: fetchMock.mock.calls[i][1] });
    const body = (i = 0) => JSON.parse(String(call(i).init?.body));

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("listRolesV2 walks every page and maps each role onto the v1 model", async () => {
        fetchMock.mockResolvedValueOnce(json(listPage([roleV2()], 2))).mockResolvedValueOnce(json(listPage([roleV2({ id: "r2", name: "Viewers" })])));

        const roles = await listRolesV2();

        expect(call(0).url).toBe("/api/v2/platform/rbac/roles?pageNumber=1&pageSize=100");
        expect(call(1).url).toBe("/api/v2/platform/rbac/roles?pageNumber=2&pageSize=100");
        expect(roles.map(r => r.id)).toEqual(["r1", "r2"]);
        expect(roles[0]).toEqual({
            id: "r1",
            name: "Operators",
            description: "",
            origin: "custom",
            scopes: ["agent:*:invoke"],
            inherits: [],
            createdBy: "ada@example.com",
            updatedBy: "",
            createdAt: "2026-01-02T03:04:05.678Z",
            updatedAt: "",
        });
    });

    test("role writes send v1's bodies to the v2 paths and unwrap {data}", async () => {
        fetchMock
            .mockResolvedValueOnce(json({ data: roleV2() }, 201))
            .mockResolvedValueOnce(json({ data: roleV2({ name: "Renamed" }) }))
            .mockResolvedValueOnce(json({ data: roleV2() }))
            .mockResolvedValueOnce(json(null, 204));

        await createRoleV2({ name: "Operators", description: "d", scopes: ["agent:*:invoke"] });
        const updated = await updateRoleV2("r 1", { name: "Renamed", description: "d", scopes: [] });
        const got = await getRoleV2("r1");
        await deleteRoleV2("r1");

        expect([call(0).url, call(0).init?.method]).toEqual(["/api/v2/platform/rbac/roles", "POST"]);
        expect(body(0)).toEqual({ name: "Operators", description: "d", scopes: ["agent:*:invoke"], inherits: [] });
        expect([call(1).url, call(1).init?.method]).toEqual(["/api/v2/platform/rbac/roles/r%201", "PUT"]);
        expect(body(1)).toEqual({ name: "Renamed", description: "d", scopes: [], inherits: [] });
        expect(updated.name).toBe("Renamed");
        expect(call(2).url).toBe("/api/v2/platform/rbac/roles/r1");
        expect(got).toMatchObject({ id: "r1", name: "Operators", description: "", createdAt: "2026-01-02T03:04:05.678Z" });
        expect([call(3).url, call(3).init?.method]).toEqual(["/api/v2/platform/rbac/roles/r1", "DELETE"]);
    });

    test("a duplicate name keeps the server's sentence", async () => {
        fetchMock.mockResolvedValueOnce(json({ message: "A role with this name already exists", errorId: "e1" }, 409));

        await expect(createRoleV2({ name: "Operators", description: "" })).rejects.toThrow("A role with this name already exists");
    });

    test("a field error names the field in the message", async () => {
        fetchMock.mockResolvedValueOnce(json({ message: "Request validation failed", errorId: "e2", validationDetails: { name: ["name is required"] } }, 422));

        await expect(createRoleV2({ name: "", description: "" })).rejects.toThrow("Request validation failed: name: name is required");
    });

    test("default roles read and replace the set", async () => {
        fetchMock.mockResolvedValueOnce(json({ data: { roleIds: ["r1"], origin: "custom" } })).mockResolvedValueOnce(json({ data: { roleIds: [], origin: "yaml" } }));

        expect(await getDefaultRolesV2()).toEqual({ roleIds: ["r1"], origin: "custom" });
        expect(await setDefaultRolesV2([])).toEqual({ roleIds: [], origin: "yaml" });
        expect([call(1).url, call(1).init?.method]).toEqual(["/api/v2/platform/rbac/defaultRoles", "PUT"]);
        expect(body(1)).toEqual({ roleIds: [] });
    });

    test("listUsersV2 sends the page's filters and maps each user", async () => {
        fetchMock.mockResolvedValueOnce(json(listPage([userV2()])));

        const page = await listUsersV2({ pageNumber: 1, pageSize: 20, search: " ada ", roleId: "r1", sort: "displayName:desc" });

        expect(call().url).toBe("/api/v2/platform/users?pageNumber=1&pageSize=20&search=ada&roleId=r1&sort=displayName%3Adesc");
        expect(page.data[0]).toEqual({
            id: "u1",
            displayName: "Ada",
            userName: undefined,
            email: "ada@example.com",
            issuer: "https://idp.example.com",
            sub: "ada",
            oidcProvider: undefined,
            provisionedVia: "jit",
            active: true,
            groups: [],
            roles: [{ roleId: "r1", source: "direct" }],
            lastLoginAt: undefined,
            createdBy: undefined,
            updatedBy: undefined,
            createdAt: "2026-01-02T03:04:05.678Z",
            updatedAt: "2026-01-03T00:00:00.000Z",
        });
        expect(page.data[0]).not.toHaveProperty("budget");
    });

    test.each([true, false])("listUsersV2 with expandBudget %s asks for the budget and carries its fields only when expanded", async expandBudget => {
        const budget = { id: "b1", name: "Monthly", amount: "250.00", source: "assigned", assignmentId: "a1" };
        fetchMock.mockResolvedValueOnce(json(listPage([userV2({ budget, effectiveBudgetAmount: "250.00" })])));

        const page = await listUsersV2({ pageNumber: 1, pageSize: 20, budgetId: "b1", expandBudget });

        expect(call().url).toBe(`/api/v2/platform/users?pageNumber=1&pageSize=20&budgetId=b1${expandBudget ? "&expand=budget" : ""}`);
        if (expandBudget) {
            expect([page.data[0].budget, page.data[0].budgetTopUp, page.data[0].effectiveBudgetAmount]).toEqual([budget, null, "250.00"]);
        } else {
            expect(page.data[0]).not.toHaveProperty("budget");
            expect(page.data[0]).not.toHaveProperty("effectiveBudgetAmount");
        }
    });

    test("an expanded user carries the budget fields, null when none applies, as on v1", async () => {
        const budget = { id: "b1", name: "Monthly", amount: "250.00", source: "assigned", assignmentId: "a1" };
        const topUp = { amount: "50.00", expiresTime: "2026-02-01T00:00:00.000Z" };
        fetchMock.mockResolvedValueOnce(json({ data: userV2({ budget, budgetTopUp: topUp, effectiveBudgetAmount: "300.00" }) })).mockResolvedValueOnce(json({ data: userV2() }));

        const expanded = await getUserV2("u1", { expandBudget: true });
        const none = await getUserV2("u1", { expandBudget: true });

        expect(call(0).url).toBe("/api/v2/platform/users/u1?expand=budget");
        expect([expanded.budget, expanded.budgetTopUp, expanded.effectiveBudgetAmount]).toEqual([budget, topUp, "300.00"]);
        expect([none.budget, none.budgetTopUp, none.effectiveBudgetAmount]).toEqual([null, null, null]);
    });

    test("role grants read and replace a user's direct set", async () => {
        fetchMock.mockResolvedValueOnce(json({ data: { roleIds: ["r1"] } })).mockResolvedValueOnce(json({ data: { roleIds: [] } }));

        expect(await getUserRoleGrantsV2("u1")).toEqual(["r1"]);
        expect(await setUserRoleGrantsV2("u1", [])).toEqual([]);
        expect([call(1).url, call(1).init?.method]).toEqual(["/api/v2/platform/users/u1/roleGrants", "PUT"]);
        expect(body(1)).toEqual({ roleIds: [] });
    });

    test("system users come back as the picker's model", async () => {
        fetchMock.mockResolvedValueOnce(json(listPage([{ id: "s1", type: "serviceAccount", name: "bot", sub: "system:bot", displayName: null, roleIds: ["r1"], origin: "custom" }])));

        const page = await listSystemUsersV2();

        expect(call().url).toBe("/api/v2/platform/rbac/serviceAccounts?pageSize=100");
        expect(page.data).toEqual([{ id: "s1", sub: "system:bot", name: "bot", displayName: undefined, roleIds: ["r1"] }]);
    });

    test("claim mappings: a mapping with no name or provider reads as empty strings, and writes keep v1's bodies", async () => {
        const mapping = { id: "m1", type: "claimMapping", name: null, oidcProvider: null, claimKey: null, claimValue: "eng", roleIds: ["r1"], origin: "yaml", createdBy: null, updatedBy: null, createdTime: null, updatedTime: null };
        fetchMock
            .mockResolvedValueOnce(json(listPage([mapping])))
            .mockResolvedValueOnce(json({ data: { ...mapping, name: "Eng", origin: "custom" } }, 201))
            .mockResolvedValueOnce(json({ data: { ...mapping, name: "Eng", origin: "custom" } }));

        const page = await listClaimMappingsV2({ pageNumber: 1, pageSize: 20, sort: "claimValue:asc" });
        await createClaimMappingV2({ name: "Eng", claimKey: "groups", claimValue: "eng", roleIds: ["r1"] });
        await updateClaimMappingV2("m1", { name: "Eng", oidcProvider: "okta", claimKey: "groups", claimValue: "eng", roleIds: ["r1"] });

        expect(call(0).url).toBe("/api/v2/platform/rbac/claimMappings?pageNumber=1&pageSize=20&sort=claimValue%3Aasc");
        expect(page.data[0]).toMatchObject({ id: "m1", name: "", oidcProvider: "", claimKey: "", createdAt: "", origin: "yaml" });
        expect([call(1).url, call(1).init?.method]).toEqual(["/api/v2/platform/rbac/claimMappings", "POST"]);
        expect(body(1)).toEqual({ name: "Eng", claimKey: "groups", claimValue: "eng", roleIds: ["r1"] });
        expect([call(2).url, call(2).init?.method]).toEqual(["/api/v2/platform/rbac/claimMappings/m1", "PUT"]);
        expect(body(2)).toEqual({ name: "Eng", oidcProvider: "okta", claimKey: "groups", claimValue: "eng", roleIds: ["r1"] });
    });

    test("a claim mapping created for a provider names it", async () => {
        fetchMock.mockResolvedValueOnce(
            json({ data: { id: "m2", type: "claimMapping", name: "Eng", oidcProvider: "okta", claimKey: "groups", claimValue: "eng", roleIds: ["r1"], origin: "custom", createdBy: null, updatedBy: null, createdTime: null, updatedTime: null } }, 201)
        );

        const created = await createClaimMappingV2({ name: "Eng", oidcProvider: "okta", claimKey: "groups", claimValue: "eng", roleIds: ["r1"] });

        expect([call().url, call().init?.method]).toEqual(["/api/v2/platform/rbac/claimMappings", "POST"]);
        expect(body()).toEqual({ name: "Eng", oidcProvider: "okta", claimKey: "groups", claimValue: "eng", roleIds: ["r1"] });
        expect(created.oidcProvider).toBe("okta");
    });

    test("the users page's default sort reaches v2 as it is", async () => {
        fetchMock.mockResolvedValueOnce(json(listPage([])));

        await listUsersV2({ pageNumber: 1, pageSize: 20, sort: "displayName:asc" });

        expect(call().url).toBe("/api/v2/platform/users?pageNumber=1&pageSize=20&sort=displayName%3Aasc");
    });

    test("a claim mapping sorted by name keeps its field", async () => {
        fetchMock.mockResolvedValueOnce(json(listPage([])));

        await listClaimMappingsV2({ pageNumber: 1, pageSize: 20, sort: "name:asc" });

        expect(call().url).toBe("/api/v2/platform/rbac/claimMappings?pageNumber=1&pageSize=20&sort=name%3Aasc");
    });

    test("a user who has signed in carries the time", async () => {
        fetchMock.mockResolvedValueOnce(json({ data: userV2({ lastLoginTime: "2026-01-04T05:06:07.890Z" }) }));

        expect((await getUserV2("u1")).lastLoginAt).toBe("2026-01-04T05:06:07.890Z");
    });

    test("deleting a claim mapping sends DELETE to its v2 path", async () => {
        fetchMock.mockResolvedValueOnce(json(null, 204));

        await deleteClaimMappingV2("m 1");

        expect([call().url, call().init?.method]).toEqual(["/api/v2/platform/rbac/claimMappings/m%201", "DELETE"]);
    });
});
