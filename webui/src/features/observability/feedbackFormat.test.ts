// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { formatCount, formatNegativeRatio } from "./feedbackFormat";

describe("formatNegativeRatio", () => {
    it("renders a percentage with three decimals", () => {
        expect(formatNegativeRatio(0.00113)).toBe("0.113%");
        expect(formatNegativeRatio(0.0197)).toBe("1.970%");
    });

    it("renders an exact zero as a zero, never as a blank", () => {
        expect(formatNegativeRatio(0)).toBe("0.000%");
    });

    it("keeps three decimals at magnitudes where fewer would collapse distinct agents", () => {
        expect(formatNegativeRatio(0.00088)).toBe("0.088%");
        expect(formatNegativeRatio(0.00056)).toBe("0.056%");
    });
});

describe("formatCount", () => {
    it("groups thousands so a large count does not misread", () => {
        expect(formatCount(12406)).toBe("12,406");
        expect(formatCount(1234567)).toBe("1,234,567");
    });

    it("leaves small counts alone", () => {
        expect(formatCount(0)).toBe("0");
        expect(formatCount(145)).toBe("145");
    });
});
