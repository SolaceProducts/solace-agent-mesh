// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { http, HttpResponse } from "msw";

import type { Budget } from "@/api/budgets";
import { db } from "@/api/rbac/store";

import { pageFromRequest } from "@/testing/helpers/handlers";

const BUDGETS = "*/api/v2/platform/budgets";

export const budgetsListErrorHandler = http.get(BUDGETS, () => HttpResponse.json({ message: "Service unavailable" }, { status: 500 }));

const byAmount = (a: Budget, b: Budget) => (a.amount === null ? Infinity : Number(a.amount)) - (b.amount === null ? Infinity : Number(b.amount)) || 0;
const byName = (a: Budget, b: Budget) => a.name.localeCompare(b.name);

export function budgetsListHandler(budgets: readonly Budget[]) {
    return http.get(BUDGETS, ({ request }) => {
        const params = new URL(request.url).searchParams;
        const subjectType = params.get("subjectType");
        const search = params.get("search")?.toLowerCase();
        const [field, dir] = (params.get("sort") ?? "name:asc").split(":");
        const compare = field === "amount" ? byAmount : byName;
        const listed = budgets.filter(budget => (!subjectType || budget.subjectType === subjectType) && (!search || budget.name.toLowerCase().includes(search))).sort((a, b) => (dir === "desc" ? compare(b, a) : compare(a, b)));
        return HttpResponse.json(pageFromRequest(request, listed));
    });
}

export function budgetHandler(budgets: readonly Budget[]) {
    return http.get(`${BUDGETS}/:id`, ({ params }) => {
        const budget = budgets.find(candidate => candidate.id === params.id);
        return budget ? HttpResponse.json({ data: budget }) : HttpResponse.json({ message: "Budget not found", errorId: "mock-404" }, { status: 404 });
    });
}

const TOP_UP = "*/api/v2/platform/budgetSubjects/:id/topUp";

export const budgetTopUpHandlers = [
    http.put(TOP_UP, async ({ params, request }) => {
        const subjectId = String(params.id);
        const { amount: requested } = (await request.json()) as { amount: string };
        if (Number(requested) === 0) {
            delete db.topUpByUser[subjectId];
            return HttpResponse.json({ data: { amount: null, expiresTime: null, createdBy: null, createdTime: null, updatedBy: null, updatedTime: null } });
        }
        const budgetId = db.budgetByUser[subjectId] ?? db.defaultBudgetId;
        const budget = budgetId ? db.budgets[budgetId] : undefined;
        if (budget?.amount === null) {
            return HttpResponse.json({ message: "The budget that applies to this subject is unlimited, so a top-up would have no effect. Set an amount on it first.", errorId: "mock-409" }, { status: 409 });
        }
        const now = new Date();
        const expiresTime = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1)).toISOString();
        const [whole, frac = ""] = requested.split(".");
        const amount = `${Number(whole)}.${frac.padEnd(2, "0")}`;
        const created = !db.topUpByUser[subjectId];
        db.topUpByUser[subjectId] = { amount, expiresTime };
        const stamp = now.toISOString();
        return HttpResponse.json({ data: { amount, expiresTime, createdBy: "admin", createdTime: stamp, updatedBy: "admin", updatedTime: stamp } }, { status: created ? 201 : 200 });
    }),
];
