// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { creditsTone, freeCreditsSummary, percentUsed } from "./freeCredits";

describe("free credits", () => {
    test.each([
        [0, "ok"],
        [79, "ok"],
        [80, "low"],
        [99, "low"],
        [100, "out"],
    ] as const)("%i%% used is %s", (used, tone) => {
        expect(creditsTone(used)).toBe(tone);
    });

    test("turns what the free LLM reports left into the share used, rounded and bounded, and keeps no limit as none", () => {
        expect(percentUsed(85.2)).toBe(15);
        expect(percentUsed(-3)).toBe(100);
        expect(percentUsed(0)).toBe(100);
        expect(percentUsed(104)).toBe(0);
        expect(percentUsed(null)).toBeNull();
    });

    test("never reads as used up while any allowance is left", () => {
        expect(percentUsed(0.4)).toBe(99);
    });

    test.each([
        [
            { status: "available", remainingPercent: 80 },
            { used: 20, tone: "ok", label: "20% of free credits used" },
        ],
        [
            { status: "available", remainingPercent: 15 },
            { used: 85, tone: "low", label: "85% of free credits used" },
        ],
        [
            { status: "available", remainingPercent: 0 },
            { used: 100, tone: "out", label: "No free credits left" },
        ],
        [
            { status: "reauthorization_required", remainingPercent: null },
            { used: 0, tone: "low", label: "Sign in again" },
        ],
        [
            { status: "paused", remainingPercent: null },
            { used: 0, tone: "out", label: "The free model is paused" },
        ],
    ])("summarises %o for the navigation", (status, summary) => {
        expect(freeCreditsSummary({ ...status, models: [] })).toEqual(summary);
    });

    test.each([
        { status: "available", remainingPercent: null },
        { status: "unavailable", remainingPercent: null },
    ])("shows nothing in the navigation for %o", status => {
        expect(freeCreditsSummary({ ...status, models: [] })).toBeNull();
    });
});
