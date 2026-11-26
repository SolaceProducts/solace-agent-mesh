// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { calculateRunGroupProgress, runGroupAggregateStatusBadge, runGroupDisplayStatus, type RunGroupProgress } from "@/features/evaluations/components/runGroupStatus";

import { makeEvalRun } from "@/testing/helpers/evaluations";

describe("calculateRunGroupProgress", () => {
    test("returns zeros for empty runs array", () => {
        const result = calculateRunGroupProgress([]);
        expect(result).toEqual({ current: 0, total: 0 });
    });

    test("returns progress from single run", () => {
        const runs = [makeEvalRun({ progressCurrent: 5, progressTotal: 10 })];
        const result = calculateRunGroupProgress(runs);
        expect(result).toEqual({ current: 5, total: 10 });
    });

    test("returns sum of progress across multiple runs", () => {
        const runs = [makeEvalRun({ progressCurrent: 8, progressTotal: 10 }), makeEvalRun({ progressCurrent: 5, progressTotal: 10 }), makeEvalRun({ progressCurrent: 7, progressTotal: 10 })];
        const result = calculateRunGroupProgress(runs);
        expect(result.current).toBe(20);
        expect(result.total).toBe(30);
    });
});

describe("runGroupDisplayStatus", () => {
    test("maps failed with some successful runs to partial", () => {
        const runs = [makeEvalRun({ status: "failed" }), makeEvalRun({ status: "completed" })];
        expect(runGroupDisplayStatus("failed", runs)).toBe("partial");
    });

    test("maps failed with completed_with_warnings run to partial", () => {
        const runs = [makeEvalRun({ status: "failed" }), makeEvalRun({ status: "completed_with_warnings" })];
        expect(runGroupDisplayStatus("failed", runs)).toBe("partial");
    });

    test("maps failed with all failed runs to failed", () => {
        const runs = [makeEvalRun({ status: "failed" }), makeEvalRun({ status: "failed" })];
        expect(runGroupDisplayStatus("failed", runs)).toBe("failed");
    });
});

describe("runGroupAggregateStatusBadge", () => {
    test("renders badge with correct label for completed status", () => {
        const { container } = render(runGroupAggregateStatusBadge("completed", []));
        expect(container.textContent).toBe("Complete");
    });

    test("renders badge with progress when in_progress and progress provided", () => {
        const progress: RunGroupProgress = { current: 5, total: 10 };
        const { container } = render(runGroupAggregateStatusBadge("running", [], progress));
        expect(container.textContent).toContain("5/10 completed");
    });

    test("renders default label when in_progress but progress total is 0", () => {
        const progress: RunGroupProgress = { current: 0, total: 0 };
        const { container } = render(runGroupAggregateStatusBadge("running", [], progress));
        expect(container.textContent).toBe("In Progress");
    });

    test("renders default label when in_progress but no progress provided", () => {
        const { container } = render(runGroupAggregateStatusBadge("running", []));
        expect(container.textContent).toBe("In Progress");
    });

    test("renders pulse indicator for in_progress status", () => {
        render(runGroupAggregateStatusBadge("running", []));
        expect(screen.getByTestId("pulse-indicator")).toBeTruthy();
    });

    test("does not render pulse indicator for completed status", () => {
        render(runGroupAggregateStatusBadge("completed", []));
        expect(screen.queryByTestId("pulse-indicator")).toBeNull();
    });
});
