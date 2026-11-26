// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { FeedbackTrendPoint } from "@/api/feedback";

import { trendDirectionOf } from "./trendDirection";

const series = (...ratios: number[]): FeedbackTrendPoint[] =>
    ratios.map((negativeRatio, i) => ({
        date: `2026-09-0${i + 1}`,
        totalActivities: 100,
        positiveFeedback: 0,
        negativeFeedback: Math.round(negativeRatio * 100),
        negativeRatio,
    }));

describe("trendDirectionOf", () => {
    it("reads a clearly rising negative rate as worsening", () => {
        expect(trendDirectionOf(series(0.01, 0.01, 0.05, 0.06))).toBe("worsening");
    });

    it("reads a clearly falling negative rate as improving", () => {
        expect(trendDirectionOf(series(0.06, 0.05, 0.01, 0.01))).toBe("improving");
    });

    it("reads a flat series as steady", () => {
        expect(trendDirectionOf(series(0.03, 0.03, 0.03, 0.03))).toBe("steady");
    });

    it("treats a change under the threshold as steady, not as noise worth flagging", () => {
        expect(trendDirectionOf(series(0.04, 0.04, 0.042, 0.042))).toBe("steady");
    });

    it("flags a change above the threshold", () => {
        expect(trendDirectionOf(series(0.04, 0.04, 0.046, 0.046))).toBe("worsening");
    });

    it("drops the middle point when the count is odd, so neither half is skewed by it", () => {
        expect(trendDirectionOf(series(0.01, 0.99, 0.05))).toBe("worsening");
    });

    it("treats an all-zero series as steady rather than dividing by zero", () => {
        expect(trendDirectionOf(series(0, 0, 0, 0))).toBe("steady");
    });

    it("treats a series rising from zero as worsening", () => {
        expect(trendDirectionOf(series(0, 0, 0.02, 0.02))).toBe("worsening");
    });

    it("treats a series too short to compare as steady", () => {
        expect(trendDirectionOf(series(0.05))).toBe("steady");
        expect(trendDirectionOf([])).toBe("steady");
    });
});
