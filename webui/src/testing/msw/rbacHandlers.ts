// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { http, HttpResponse } from "msw";

import { db, findRole, findUser, type RoleRecord } from "@/api/rbac/store";

import { uuid } from "@/utils/uuid";

import { pageFromRequest, paginatedResponse } from "@/testing/helpers/handlers";
import type { ClaimMapping, User } from "@/types";

const ROLES = "*/api/v2/platform/rbac/roles";
const DEFAULT_ROLES = "*/api/v2/platform/rbac/defaultRoles";
const CLAIM_MAPPINGS = "*/api/v2/platform/rbac/claimMappings";
const SYSTEM_USERS = "*/api/v2/platform/rbac/serviceAccounts";
const USERS = "*/api/v2/platform/users";

const CLAIM_MAPPING_LIST_PARAMS = new Set(["pageNumber", "pageSize", "sort", "oidcProvider", "origin", "roleId", "search"]);

const USER_LIST_PARAMS = new Set(["pageNumber", "pageSize", "sort", "search", "roleId", "group", "oidcProvider", "budgetId", "expand"]);

function addAmounts(a: string, b: string): string {
    const cents = (amount: string) => {
        const [whole, frac = ""] = amount.split(".");
        return BigInt(whole) * 100n + BigInt(frac.padEnd(2, "0"));
    };
    const sum = cents(a) + cents(b);
    return `${sum / 100n}.${String(sum % 100n).padStart(2, "0")}`;
}

function withAppliedBudget(user: User): User {
    const assigned = db.budgetByUser[user.id];
    const id = assigned ?? db.defaultBudgetId;
    const budget = id ? db.budgets[id] : undefined;
    const topUp = db.topUpByUser[user.id];
    const budgetTopUp = topUp ?? null;
    if (!id || !budget) return { ...user, budget: null, budgetTopUp, effectiveBudgetAmount: null };
    return {
        ...user,
        budget: { id, name: budget.name, amount: budget.amount, source: assigned ? "assigned" : "default", assignmentId: assigned ? `assignment-${user.id}` : null },
        budgetTopUp,
        effectiveBudgetAmount: effectiveAmount(budget.amount, topUp?.amount),
    };
}

function effectiveAmount(budgetAmount: string | null, topUpAmount: string | undefined): string | null {
    if (budgetAmount === null || topUpAmount === undefined) return budgetAmount;
    return addAmounts(budgetAmount, topUpAmount);
}

const expandBudget = (url: URL) => url.searchParams.get("expand") === "budget";
const badExpand = (url: URL) => url.searchParams.has("expand") && !expandBudget(url);

const USER_SORT_FIELDS: Record<string, "displayName" | "email" | "lastLoginAt" | "createdAt" | "updatedAt"> = {
    displayName: "displayName",
    email: "email",
    lastLoginTime: "lastLoginAt",
    createdTime: "createdAt",
    updatedTime: "updatedAt",
};

const CLAIM_MAPPING_SORT_FIELDS: Record<string, keyof ClaimMapping> = {
    name: "name",
    oidcProvider: "oidcProvider",
    claimValue: "claimValue",
    createdTime: "createdAt",
    updatedTime: "updatedAt",
};

const unknownParam = (url: URL, allowed: Set<string>) => [...url.searchParams.keys()].find(key => !allowed.has(key));

const byNameOrder = (a: string, b: string) => {
    const [x, y] = [a.toLowerCase(), b.toLowerCase()];
    return x < y ? -1 : Number(x > y);
};

const byCodeUnit = (a: string, b: string) => (a < b ? -1 : Number(a > b));

interface RoleWriteBody {
    name?: string;
    description?: string;
    scopes?: string[];
    inherits?: string[];
}

function nowIso(): string {
    return new Date().toISOString();
}

function toInstant(value: string | undefined): string | null {
    if (!value) return null;
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? null : date.toISOString();
}

const orNull = (value: string | undefined) => value || null;

function toRoleWire(record: RoleRecord) {
    return {
        id: record.id,
        type: "role",
        name: record.name,
        description: orNull(record.description),
        scopes: [...record.scopes],
        inherits: record.inherits ? [...record.inherits] : [],
        origin: record.origin,
        createdBy: orNull(record.createdBy),
        updatedBy: orNull(record.updatedBy),
        createdTime: toInstant(record.createdAt),
        updatedTime: toInstant(record.updatedAt),
    };
}

function toClaimMappingWire(record: ClaimMapping) {
    return {
        id: record.id,
        type: "claimMapping",
        name: orNull(record.name),
        oidcProvider: orNull(record.oidcProvider),
        claimKey: orNull(record.claimKey),
        claimValue: record.claimValue,
        roleIds: [...record.roleIds],
        origin: record.origin,
        createdBy: orNull(record.createdBy),
        updatedBy: orNull(record.updatedBy),
        createdTime: toInstant(record.createdAt),
        updatedTime: toInstant(record.updatedAt),
    };
}

function toUserWire(user: User, expand: boolean) {
    const applied = expand ? withAppliedBudget(user) : { budget: null, budgetTopUp: null, effectiveBudgetAmount: null };
    return {
        id: user.id,
        type: "user",
        displayName: orNull(user.displayName),
        userName: orNull(user.userName),
        email: orNull(user.email),
        issuer: orNull(user.issuer),
        sub: orNull(user.sub),
        oidcProvider: orNull(user.oidcProvider),
        provisionedVia: user.provisionedVia ?? "",
        active: user.active ?? true,
        groups: user.groups ?? [],
        roles: user.roles.map(({ roleId, source }) => ({ roleId, source, manageable: source === "direct" })),
        lastLoginTime: toInstant(user.lastLoginAt),
        createdBy: orNull(user.createdBy),
        updatedBy: orNull(user.updatedBy),
        createdTime: toInstant(user.createdAt),
        updatedTime: toInstant(user.updatedAt),
        budget: applied.budget ?? null,
        budgetTopUp: applied.budgetTopUp ?? null,
        effectiveBudgetAmount: applied.effectiveBudgetAmount ?? null,
    };
}

const resource = (data: unknown, init?: ResponseInit) => HttpResponse.json({ data }, init);

function errorEnvelope(message: string) {
    return { message, errorId: uuid() };
}

interface ClaimMappingWriteBody {
    name?: string;
    oidcProvider?: string;
    claimKey?: string;
    claimValue?: string;
    roleIds?: string[];
}

function claimMappingWriteError(body: ClaimMappingWriteBody) {
    if (!(body.name ?? "").trim()) return HttpResponse.json(errorEnvelope("name is required"), { status: 400 });
    if (!(body.claimValue ?? "").trim()) return HttpResponse.json(errorEnvelope("claimValue is required"), { status: 400 });
    const roleIds = body.roleIds ?? [];
    if (roleIds.length === 0) return HttpResponse.json(errorEnvelope("at least one role is required"), { status: 400 });
    const unknown = roleIds.find(id => !findRole(id));
    if (unknown !== undefined) {
        return HttpResponse.json(
            {
                ...errorEnvelope("Invalid roleId"),
                validationDetails: { roleId: [`"${unknown}" is not a known role id; system roles are defined in your configuration and cannot be targeted through the API`] },
            },
            { status: 422 }
        );
    }
    return null;
}

const defaultRolesResponse = () => resource({ roleIds: [...db.defaultRoleIds], origin: db.defaultRoleIds.length > 0 ? "custom" : "yaml" });

const yamlMappingWriteError = (record: ClaimMapping) => (record.origin === "yaml" ? HttpResponse.json(errorEnvelope("invalid id: must be a UUID"), { status: 400 }) : null);

export const rbacHandlers = [
    http.get(ROLES, ({ request }) => HttpResponse.json(pageFromRequest(request, db.roles.map(toRoleWire)))),

    http.get(`${ROLES}/:id`, ({ params }) => {
        const record = findRole(String(params.id));
        return record ? resource(toRoleWire(record)) : HttpResponse.json(errorEnvelope(`Role "${params.id}" not found`), { status: 404 });
    }),

    http.post(ROLES, async ({ request }) => {
        const body = (await request.json()) as RoleWriteBody;
        const name = (body.name ?? "").trim();
        if (!name) return HttpResponse.json(errorEnvelope("Role name is required"), { status: 400 });
        if (db.roles.some(r => r.name.toLowerCase() === name.toLowerCase())) return HttpResponse.json(errorEnvelope(`A role named "${name}" already exists`), { status: 409 });

        const ts = nowIso();
        const record: RoleRecord = {
            id: uuid(),
            name,
            description: body.description ?? "",
            origin: "custom",
            scopes: [...(body.scopes ?? [])],
            inherits: [...(body.inherits ?? [])],
            createdBy: "mock-actor",
            updatedBy: "mock-actor",
            createdAt: ts,
            updatedAt: ts,
        };
        db.roles.push(record);
        return resource(toRoleWire(record), { status: 201 });
    }),

    http.put(`${ROLES}/:id`, async ({ request, params }) => {
        const record = findRole(String(params.id));
        if (!record) return HttpResponse.json(errorEnvelope(`Role "${params.id}" not found`), { status: 404 });
        if (record.origin !== "custom") return HttpResponse.json(errorEnvelope(`Role "${record.name}" is ${record.origin === "builtin" ? "a Solace built-in role" : "a system role defined in your configuration"} and is read-only`), { status: 409 });

        const body = (await request.json()) as RoleWriteBody;
        if (body.name !== undefined) record.name = body.name.trim();
        if (body.description !== undefined) record.description = body.description;
        if (body.scopes !== undefined) record.scopes = [...body.scopes];
        record.inherits = [...(body.inherits ?? [])];
        record.updatedAt = nowIso();
        return resource(toRoleWire(record));
    }),

    http.delete(`${ROLES}/:id`, ({ params }) => {
        const record = findRole(String(params.id));
        if (!record) return HttpResponse.json(errorEnvelope(`Role "${params.id}" not found`), { status: 404 });
        if (record.origin !== "custom") return HttpResponse.json(errorEnvelope(`Role "${record.name}" is ${record.origin === "builtin" ? "a Solace built-in role" : "a system role defined in your configuration"} and is read-only`), { status: 409 });
        db.roles = db.roles.filter(r => r.id !== record.id);
        db.defaultRoleIds = db.defaultRoleIds.filter(id => id !== record.id);
        return new HttpResponse(null, { status: 204 });
    }),

    http.get(DEFAULT_ROLES, () => defaultRolesResponse()),

    http.put(DEFAULT_ROLES, async ({ request }) => {
        const body = (await request.json()) as { roleIds?: string[] };
        const roleIds = body.roleIds ?? [];
        const unknown = roleIds.find(id => !db.roles.some(r => r.id === id && r.origin !== "yaml"));
        if (unknown !== undefined) {
            return HttpResponse.json({ ...errorEnvelope(`Unknown default role id(s): [${unknown}]`), validationDetails: { roleIds: [`Not known role id(s): [${unknown}]`] } }, { status: 422 });
        }
        db.defaultRoleIds = [...roleIds];
        return defaultRolesResponse();
    }),

    http.get(SYSTEM_USERS, () =>
        HttpResponse.json(
            paginatedResponse([
                { id: "6dbe83db-3549-4d99-b4fb-c3aea7b75219", sub: "system:default", name: "default", displayName: "Default", roleIds: ["system_agent_invoke"] },
                { id: "5f1c2d3e-4b5a-6c7d-8e9f-0a1b2c3d4e5f", sub: "system:helpdesk", name: "helpdesk", displayName: "Helpdesk", roleIds: [] },
            ])
        )
    ),

    http.get(CLAIM_MAPPINGS, ({ request }) => {
        const url = new URL(request.url);
        const unknown = unknownParam(url, CLAIM_MAPPING_LIST_PARAMS);
        if (unknown) return HttpResponse.json(errorEnvelope(`Unknown query parameter "${unknown}"`), { status: 400 });

        const search = (url.searchParams.get("search") ?? "").trim().toLowerCase();
        const roleId = url.searchParams.get("roleId");
        const oidcProvider = url.searchParams.get("oidcProvider");
        const origin = url.searchParams.get("origin");

        let matched = db.claimMappings.map(m => ({ ...m, roleIds: [...m.roleIds] }));
        if (oidcProvider) matched = matched.filter(m => m.oidcProvider === oidcProvider);
        if (origin) matched = matched.filter(m => m.origin === origin);
        if (roleId) matched = matched.filter(m => m.roleIds.includes(roleId));
        if (search) matched = matched.filter(m => m.name.toLowerCase().includes(search) || m.claimValue.toLowerCase().includes(search));

        const [field, sortDir = "asc"] = (url.searchParams.get("sort") ?? "").split(":");
        const sortField = field || "name";
        const key = Object.hasOwn(CLAIM_MAPPING_SORT_FIELDS, sortField) ? CLAIM_MAPPING_SORT_FIELDS[sortField] : undefined;
        if (!key) return HttpResponse.json(errorEnvelope(`Unknown sort field "${sortField}"`), { status: 422 });
        const compare = (a: ClaimMapping, b: ClaimMapping) => {
            const byField = key === "name" || key === "claimValue" ? byNameOrder : byCodeUnit;
            return byField(String(a[key] ?? ""), String(b[key] ?? "")) || byCodeUnit(a.id, b.id);
        };
        matched.sort((a, b) => (sortDir === "desc" ? -compare(a, b) : compare(a, b)));

        return HttpResponse.json(pageFromRequest(request, matched.map(toClaimMappingWire)));
    }),

    http.post(CLAIM_MAPPINGS, async ({ request }) => {
        const body = (await request.json()) as ClaimMappingWriteBody;
        const err = claimMappingWriteError(body);
        if (err) return err;
        const ts = nowIso();
        const record: ClaimMapping = {
            id: uuid(),
            name: (body.name ?? "").trim(),
            oidcProvider: (body.oidcProvider ?? "").trim() || db.claimMappings[0]?.oidcProvider || "azure",
            claimKey: (body.claimKey ?? "").trim() || db.claimMappings[0]?.claimKey || "groups",
            claimValue: (body.claimValue ?? "").trim(),
            roleIds: [...(body.roleIds ?? [])],
            origin: "custom",
            createdBy: "mock-actor",
            updatedBy: "mock-actor",
            createdAt: ts,
            updatedAt: ts,
        };
        db.claimMappings.push(record);
        return resource(toClaimMappingWire(record), { status: 201 });
    }),

    http.put(`${CLAIM_MAPPINGS}/:id`, async ({ request, params }) => {
        const record = db.claimMappings.find(m => m.id === String(params.id));
        if (!record) return HttpResponse.json(errorEnvelope(`Claim mapping "${params.id}" not found`), { status: 404 });
        const immutable = yamlMappingWriteError(record);
        if (immutable) return immutable;
        const body = (await request.json()) as ClaimMappingWriteBody;
        const err = claimMappingWriteError(body);
        if (err) return err;
        record.name = (body.name ?? "").trim();
        record.oidcProvider = (body.oidcProvider ?? "").trim() || record.oidcProvider;
        record.claimKey = (body.claimKey ?? "").trim() || record.claimKey;
        record.claimValue = (body.claimValue ?? "").trim();
        record.roleIds = [...(body.roleIds ?? [])];
        record.updatedAt = nowIso();
        return resource(toClaimMappingWire(record));
    }),

    http.delete(`${CLAIM_MAPPINGS}/:id`, ({ params }) => {
        const record = db.claimMappings.find(m => m.id === String(params.id));
        if (!record) return HttpResponse.json(errorEnvelope(`Claim mapping "${params.id}" not found`), { status: 404 });
        const immutable = yamlMappingWriteError(record);
        if (immutable) return immutable;
        db.claimMappings = db.claimMappings.filter(m => m.id !== record.id);
        return new HttpResponse(null, { status: 204 });
    }),

    http.get(USERS, ({ request }) => {
        const url = new URL(request.url);
        const unknown = unknownParam(url, USER_LIST_PARAMS);
        if (unknown) return HttpResponse.json(errorEnvelope(`Unknown query parameter "${unknown}"`), { status: 400 });
        if (badExpand(url)) return HttpResponse.json(errorEnvelope("expand field is not one of: budget"), { status: 422 });

        const search = (url.searchParams.get("search") ?? "").trim().toLowerCase();
        const roleId = url.searchParams.get("roleId");
        const budgetId = url.searchParams.get("budgetId");

        let matched = db.users;
        if (roleId) matched = matched.filter(u => u.roles.some(r => r.roleId === roleId));
        if (budgetId) matched = matched.filter(u => (db.budgetByUser[u.id] ?? db.defaultBudgetId) === budgetId);
        if (search) matched = matched.filter(u => (u.displayName ?? "").toLowerCase().includes(search) || (u.email ?? "").toLowerCase().includes(search));

        const [sortField, sortDir = "asc"] = (url.searchParams.get("sort") ?? "").split(":");
        if (sortField) {
            const field = Object.hasOwn(USER_SORT_FIELDS, sortField) ? USER_SORT_FIELDS[sortField] : undefined;
            if (!field) return HttpResponse.json(errorEnvelope(`Unknown sort field "${sortField}"`), { status: 422 });

            const sortValue = (u: User) => {
                if (field === "displayName") return u.displayName || u.email || "";
                return u[field] ?? "";
            };
            const byField = field === "displayName" || field === "email" ? byNameOrder : byCodeUnit;
            matched = [...matched].sort((a, b) => {
                const cmp = byField(sortValue(a), sortValue(b));
                return (sortDir === "desc" ? -cmp : cmp) || byCodeUnit(b.id, a.id);
            });
        }

        const expand = expandBudget(url);
        return HttpResponse.json(
            pageFromRequest(
                request,
                matched.map(u => toUserWire(u, expand))
            )
        );
    }),

    http.get(`${USERS}/:id`, ({ request, params }) => {
        const url = new URL(request.url);
        if (badExpand(url)) return HttpResponse.json(errorEnvelope("expand field is not one of: budget"), { status: 422 });
        const user = findUser(String(params.id));
        if (!user) return HttpResponse.json(errorEnvelope("user not found"), { status: 404 });
        return resource(toUserWire(user, expandBudget(url)));
    }),

    http.get(`${USERS}/:id/roleGrants`, ({ params }) => {
        const user = findUser(String(params.id));
        if (!user) return HttpResponse.json(errorEnvelope(`User "${params.id}" not found`), { status: 404 });
        return resource({ roleIds: user.roles.filter(r => r.source === "direct").map(r => r.roleId) });
    }),

    http.put(`${USERS}/:id/roleGrants`, async ({ request, params }) => {
        const user = findUser(String(params.id));
        if (!user) return HttpResponse.json(errorEnvelope(`User "${params.id}" not found`), { status: 404 });
        const body = (await request.json()) as { roleIds?: string[] };
        const roleIds = body.roleIds ?? [];
        const unknown = roleIds.find(id => !findRole(id));
        if (unknown !== undefined) {
            return HttpResponse.json({ ...errorEnvelope(`Unknown roleId(s): [${unknown}]`), validationDetails: { roleIds: [`Not known role id(s): [${unknown}]`] } }, { status: 422 });
        }
        const preserved = user.roles.filter(r => r.source !== "direct");
        user.roles = [...roleIds.map(roleId => ({ roleId, source: "direct" as const })), ...preserved];
        return resource({ roleIds });
    }),
];
