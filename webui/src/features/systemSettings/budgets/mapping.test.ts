// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { Budget } from "@/api/budgets";

import { mockBudgetRows } from "@/testing/data";

import { toBudgetRow, toCreateRequest, toUpdateRequest } from "./mapping";

const engineering = mockBudgetRows.find(b => b.id === "budget-engineering")!;

describe("toBudgetRow", () => {
    test("keeps the displayed fields and the listed assignee count, and reduces the hidden and pending counts to whether any exist", () => {
        const budget: Budget = {
            id: "b1",
            type: "budget",
            name: "Engineering",
            subjectType: "user",
            amount: null,
            warningPercentage: null,
            isDefault: true,
            assignedCount: 7,
            hiddenAssignedCount: 1,
            pendingCount: 2,
            createdBy: "amy@example.com",
            updatedBy: "eve@example.com",
            createdTime: "2026-09-01T00:00:00.000Z",
            updatedTime: "2026-09-02T00:00:00.000Z",
        };
        expect(toBudgetRow(budget)).toEqual({ id: "b1", name: "Engineering", scope: "user", amount: null, warnPercentage: null, isDefault: true, assignedCount: 7, hasUnlistedAssignments: true });
    });
});

describe("toCreateRequest", () => {
    test("creates a user-tier budget, keeping nulls", () => {
        expect(toCreateRequest({ name: "Exemption", scope: "user", amount: null, warnPercentage: null })).toEqual({ name: "Exemption", subjectType: "user", amount: null, warningPercentage: null });
    });
});

describe("toUpdateRequest", () => {
    test("is empty when nothing changed", () => {
        expect(toUpdateRequest(engineering, { name: "Engineering", scope: "user", amount: "2000.00", warnPercentage: 90 })).toEqual({});
    });

    test("includes only the changed fields", () => {
        expect(toUpdateRequest(engineering, { name: "Engineering", scope: "user", amount: "2500", warnPercentage: 90 })).toEqual({ amount: "2500" });
        expect(toUpdateRequest(engineering, { name: "Platform", scope: "user", amount: "2000.00", warnPercentage: 90 })).toEqual({ name: "Platform" });
    });

    test("sends explicit nulls when a budget becomes unlimited", () => {
        expect(toUpdateRequest(engineering, { name: "Engineering", scope: "user", amount: null, warnPercentage: null })).toEqual({ amount: null, warningPercentage: null });
    });

    test("sends a null threshold with the cleared limit when the budget had no threshold", () => {
        const noThreshold = { ...engineering, warnPercentage: null };
        expect(toUpdateRequest(noThreshold, { name: "Engineering", scope: "user", amount: null, warnPercentage: null })).toEqual({ amount: null, warningPercentage: null });
    });

    test("a budget that was already unlimited sends only what changed", () => {
        const unlimited = { ...engineering, amount: null, warnPercentage: null };
        expect(toUpdateRequest(unlimited, { name: "Engineering", scope: "user", amount: null, warnPercentage: null })).toEqual({});
        expect(toUpdateRequest(unlimited, { name: "Platform", scope: "user", amount: null, warnPercentage: null })).toEqual({ name: "Platform" });
    });

    test("sends a null threshold when the threshold is cleared", () => {
        expect(toUpdateRequest(engineering, { name: "Engineering", scope: "user", amount: "2000.00", warnPercentage: null })).toEqual({ warningPercentage: null });
    });
});
