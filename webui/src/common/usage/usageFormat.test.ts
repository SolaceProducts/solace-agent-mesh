// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { formatDataAsOf, formatFreshness } from "./usageFormat";

const NOW = new Date("2026-10-16T14:10:00Z");
const SEVEN_MIN_AGO = "2026-10-16T14:03:00.000Z";

describe("formatDataAsOf", () => {
    test.each([
        ["not a time", "Update time unknown"],
        ["2026-10-16T14:09:40.000Z", "Updated just now"],
        [SEVEN_MIN_AGO, "Updated 7 min ago"],
        ["2026-10-16T11:00:00.000Z", "Updated 3 h ago"],
        ["2026-10-14T09:05:00.000Z", "Updated Oct 14, 09:05 AM UTC"],
    ])("%s → %s", (dataAsOf, expected) => {
        expect(formatDataAsOf(dataAsOf, NOW)).toBe(expected);
    });
});

describe("formatFreshness", () => {
    test.each([
        [SEVEN_MIN_AGO, true, "Updated 7 min ago"],
        [SEVEN_MIN_AGO, false, "Updated 7 min ago. Older usage is still being calculated, so totals may be low."],
        [null, false, "Usage is still being calculated, so totals may be low."],
        [null, true, "Usage is still being calculated, so totals may be low."],
    ])("%s, history complete %s → %s", (dataAsOf, historyComplete, expected) => {
        expect(formatFreshness(dataAsOf, historyComplete, NOW)).toBe(expected);
    });
});
