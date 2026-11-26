// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { UpdateBudgetAssignmentsResponse } from "@/api/budgets";

import { summarizeAssignmentChanges } from "./summary";

const changed = (id: string) => ({ assignmentId: `a-${id}`, subject: { id } });
const moved = (id: string, from: string) => ({ ...changed(id), fromBudget: { id: `b-${from}`, name: from } });
const result = (partial: Partial<UpdateBudgetAssignmentsResponse>): UpdateBudgetAssignmentsResponse => ({ added: [], moved: [], removed: [], unchanged: [], ...partial });

describe("summarizeAssignmentChanges", () => {
    test("names the one budget users moved from", () => {
        expect(summarizeAssignmentChanges(result({ added: [changed("a"), changed("b"), changed("c")], moved: [moved("d", "Engineering"), moved("e", "Engineering")], removed: [changed("f")] }))).toBe("3 added, 2 moved from Engineering, 1 removed");
    });

    test("counts the budgets when users moved from several", () => {
        expect(summarizeAssignmentChanges(result({ moved: [moved("a", "Engineering"), moved("b", "Operations")] }))).toBe("2 moved from 2 budgets");
    });

    test("says so when nothing changed", () => {
        expect(summarizeAssignmentChanges(result({ unchanged: [{ assignmentId: null, subject: { id: "a" }, reason: "not_assigned" }] }))).toBe("No changes");
    });
});
