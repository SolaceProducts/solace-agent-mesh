// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { CrossUserFeedback } from "@/api/feedback";

import { isRowWithinFilters, pageOfRank, rankQueryParams, type TableFilters } from "./feedbackRowPage";

const row = (over: Partial<CrossUserFeedback> = {}): CrossUserFeedback => ({
    id: "f1",
    sessionId: "s1",
    taskId: "t1",
    userId: "someone@example.com",
    rating: "down",
    comment: "Wrong answer",
    shareTaskContent: false,
    subjectId: "a1",
    subjectType: "agent",
    subjectName: "Procurement Intake Router",
    entrypointType: "webui",
    createdAt: "2026-10-10T12:00:00.000Z",
    ...over,
});

const filters = (over: Partial<TableFilters> = {}): TableFilters => ({
    subjectKey: "all",
    rating: "all",
    startDate: "2026-10-01T00:00:00.000Z",
    ...over,
});

describe("pageOfRank", () => {
    it.each([
        [1, 1],
        [20, 1],
        [21, 2],
        [40, 2],
        [41, 3],
    ])("puts rank %i on page %i at a page size of 20", (rank, expected) => {
        expect(pageOfRank(rank, 20)).toBe(expected);
    });

    it("never returns a page below one", () => {
        expect(pageOfRank(0, 20)).toBe(1);
    });

    it("respects a page size other than the table's", () => {
        expect(pageOfRank(6, 5)).toBe(2);
    });
});

describe("isRowWithinFilters", () => {
    it("accepts a row that every active filter admits", () => {
        expect(isRowWithinFilters(row(), filters())).toBe(true);
    });

    it("rejects a row whose rating the table filters out", () => {
        expect(isRowWithinFilters(row({ rating: "down" }), filters({ rating: "up" }))).toBe(false);
    });

    it("admits a row matching an active rating filter", () => {
        expect(isRowWithinFilters(row({ rating: "up" }), filters({ rating: "up" }))).toBe(true);
    });

    it("rejects a row belonging to another subject", () => {
        expect(isRowWithinFilters(row({ subjectType: "agent", subjectId: "a1" }), filters({ subjectKey: "agent:a2" }))).toBe(false);
    });

    it("rejects a row whose subject kind differs from the filter's", () => {
        expect(isRowWithinFilters(row({ subjectType: "workflow", subjectId: "a1" }), filters({ subjectKey: "agent:a1" }))).toBe(false);
    });

    it("admits a row matching an active subject filter", () => {
        expect(isRowWithinFilters(row({ subjectType: "agent", subjectId: "a1" }), filters({ subjectKey: "agent:a1" }))).toBe(true);
    });

    it("rejects a row older than the window the table is showing", () => {
        expect(isRowWithinFilters(row({ createdAt: "2026-09-30T23:59:59.000Z" }), filters({ startDate: "2026-10-01T00:00:00.000Z" }))).toBe(false);
    });

    it("admits a row sitting exactly on the window's lower bound", () => {
        expect(isRowWithinFilters(row({ createdAt: "2026-10-01T00:00:00.000Z" }), filters({ startDate: "2026-10-01T00:00:00.000Z" }))).toBe(true);
    });

    it("rejects a row that one filter admits and another excludes", () => {
        expect(isRowWithinFilters(row({ rating: "up", subjectId: "a9" }), filters({ rating: "up", subjectKey: "agent:a1" }))).toBe(false);
    });

    it("rejects a row newer than the window the table is showing", () => {
        expect(isRowWithinFilters(row({ createdAt: "2026-10-11T00:00:00.000Z" }), filters({ endDate: "2026-10-10T23:59:59.999Z" }))).toBe(false);
    });

    it("admits a row sitting exactly on the window's upper bound", () => {
        expect(isRowWithinFilters(row({ createdAt: "2026-10-10T23:59:59.999Z" }), filters({ endDate: "2026-10-10T23:59:59.999Z" }))).toBe(true);
    });

    it("admits a row with no upper bound set, which is what a preset range means", () => {
        expect(isRowWithinFilters(row({ createdAt: "2030-01-01T00:00:00.000Z" }), filters())).toBe(true);
    });
});

describe("rankQueryParams", () => {
    it("counts from the row's own timestamp to the window's upper bound", () => {
        const params = rankQueryParams(row({ createdAt: "2026-10-05T00:00:00.000Z" }), filters({ endDate: "2026-10-10T23:59:59.999Z" }));

        expect(params.startDate).toBe("2026-10-05T00:00:00.000Z");
        expect(params.endDate).toBe("2026-10-10T23:59:59.999Z");
    });

    it("leaves the upper bound unset when the table has none", () => {
        expect(rankQueryParams(row(), filters()).endDate).toBeUndefined();
    });

    it("carries the table's other narrowing so the count describes the same list", () => {
        const params = rankQueryParams(row(), filters({ subjectKey: "agent:a1", rating: "down" }));

        expect(params.agentIds).toEqual(["a1"]);
        expect(params.rating).toBe("down");
    });
});

describe("isRowWithinFilters — user", () => {
    it("rejects a row submitted by someone the table is not showing", () => {
        expect(isRowWithinFilters(row({ userId: "olive@example.com" }), filters({ userId: "blake@example.com" }))).toBe(false);
    });

    it("admits a row matching an active user filter", () => {
        expect(isRowWithinFilters(row({ userId: "olive@example.com" }), filters({ userId: "olive@example.com" }))).toBe(true);
    });

    it("admits any submitter when no user filter is set", () => {
        expect(isRowWithinFilters(row({ userId: "anyone@example.com" }), filters())).toBe(true);
    });
});

describe("rankQueryParams — user", () => {
    it("carries the user filter into the rank query", () => {
        expect(rankQueryParams(row(), filters({ userId: "olive@example.com" })).userIds).toEqual(["olive@example.com"]);
    });

    it("omits userIds entirely when no user filter is set", () => {
        expect(rankQueryParams(row(), filters())).not.toHaveProperty("userIds");
    });
});
