// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { AMOUNT_PATTERN, formatBudgetLimit, formatTopUpExpiry, formatUsd, toCents } from "./budgetFormat";

describe("formatBudgetLimit", () => {
    test.each([
        [null, "Unlimited"],
        ["0.00", "$0.00"],
        ["0", "$0.00"],
        ["2000", "$2,000.00"],
        ["2000.5", "$2,000.50"],
        ["2000.10", "$2,000.10"],
        ["999.99", "$999.99"],
        ["1000", "$1,000.00"],
        ["1250000.00", "$1,250,000.00"],
        ["922337203.68", "$922,337,203.68"],
    ])("%s → %s", (amount, expected) => {
        expect(formatBudgetLimit(amount)).toBe(expected);
    });

    test("more than two decimals pass through unrounded", () => {
        expect(formatBudgetLimit("2000.12345678")).toBe("$2,000.12345678");
    });
});

describe("formatUsd", () => {
    test("formats a spend amount the same way as a limit", () => {
        expect(formatUsd("0.00")).toBe("$0.00");
        expect(formatUsd("2400")).toBe("$2,400.00");
    });
});

describe("formatTopUpExpiry", () => {
    test("reads in UTC, in both wire formats", () => {
        expect(formatTopUpExpiry("2026-11-01T00:00:00.000Z")).toBe("Nov 1, 2026, 12:00 AM UTC");
        expect(formatTopUpExpiry("2027-01-01T00:00:00.000000+00:00")).toBe("Jan 1, 2027, 12:00 AM UTC");
    });
});

describe("AMOUNT_PATTERN and toCents", () => {
    test("accept whole amounts and up to two decimals, counted in exact cents", () => {
        expect(["100", "100.5", "100.50", "0"].every(text => AMOUNT_PATTERN.test(text))).toBe(true);
        expect(["100.", ".5", "1.234", "-1", "1e3", " 1"].some(text => AMOUNT_PATTERN.test(text))).toBe(false);
        expect(toCents("100.5")).toBe(10050n);
        expect(toCents("922337203.68")).toBe(92_233_720_368n);
    });
});
