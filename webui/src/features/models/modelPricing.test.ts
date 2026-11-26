// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { ModelPricing } from "@/api/models/types";

import {
    cacheRateRequiredBecauseOf,
    cacheRatesBlockedByMissingRequiredRates,
    formatRate,
    formatRateForInput,
    isUnpriced,
    PRICE_DECIMAL_PLACES,
    PRICE_MAX_AMOUNT,
    pricingFormValues,
    rateDisplay,
    rateFormatError,
    rateRequiredBecauseOf,
    readPricingFromForm,
} from "./modelPricing";

describe("rateFormatError", () => {
    test("accepts a blank rate, which is the unpriced case rather than a fault", () => {
        expect(rateFormatError("")).toBeNull();
        expect(rateFormatError("   ")).toBeNull();
        expect(rateFormatError(undefined)).toBeNull();
    });

    test("accepts the decimal forms an operator copies off a pricing page", () => {
        for (const value of ["0", "3", "3.00", "0.0237", "15.5", "1000"]) {
            expect(rateFormatError(value), value).toBeNull();
        }
    });

    test("rejects anything that is not a plain decimal", () => {
        for (const value of ["abc", "-1", "1,000", "1e5", "$3", "1.2.3"]) {
            expect(rateFormatError(value), value).not.toBeNull();
        }
    });

    test(`rejects more than ${PRICE_DECIMAL_PLACES} decimal places rather than silently truncating`, () => {
        expect(rateFormatError("0.0001")).toBeNull();
        expect(rateFormatError("0.00001")).toContain("decimal places");
    });
});

describe("conditional requirement", () => {
    test("neither rate is required while both are blank", () => {
        expect(rateRequiredBecauseOf("input", {})).toBeNull();
        expect(rateRequiredBecauseOf("output", {})).toBeNull();
    });

    test("each rate becomes required once its counterpart is filled", () => {
        expect(rateRequiredBecauseOf("output", { input: "3" })?.label).toBe("Input");
        expect(rateRequiredBecauseOf("input", { output: "15" })?.label).toBe("Output");
    });

    test("a whitespace-only counterpart does not make the other required", () => {
        expect(rateRequiredBecauseOf("output", { input: "   " })).toBeNull();
    });

    test("the cache rates require each other the same way", () => {
        expect(cacheRateRequiredBecauseOf("cacheWrite", {})).toBeNull();
        expect(cacheRateRequiredBecauseOf("cacheWrite", { cacheRead: "0.3" })?.label).toBe("Cache Read");
        expect(cacheRateRequiredBecauseOf("cacheRead", { cacheWrite: "3.75" })?.label).toBe("Cache Write");
    });
});

describe("readPricingFromForm", () => {
    test("an untouched form clears the price rather than leaving the old one", () => {
        expect(readPricingFromForm(undefined)).toEqual({ input: null, output: null, cacheRead: null, cacheWrite: null });
    });

    test("sends the required pair and leaves cache null when it is not broken out", () => {
        const pricing = readPricingFromForm({ input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75", breakOutCacheRates: false });
        expect(pricing).toEqual({ input: "3", output: "15", cacheRead: null, cacheWrite: null });
    });

    test("sends the cache rates once they are broken out", () => {
        const pricing = readPricingFromForm({ input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75", breakOutCacheRates: true });
        expect(pricing).toEqual({ input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75" });
    });

    test("trims what the operator typed", () => {
        expect(readPricingFromForm({ input: " 3 ", output: "15", cacheRead: "", cacheWrite: "", breakOutCacheRates: false }).input).toBe("3");
    });
});

describe("pricingFormValues", () => {
    test("an unpriced model produces an empty form with cache collapsed", () => {
        expect(pricingFormValues(null)).toEqual({ input: "", output: "", cacheRead: "", cacheWrite: "", breakOutCacheRates: false });
    });

    test("a price without cache rates leaves the breakdown collapsed", () => {
        const values = pricingFormValues({ input: "3", output: "15", cacheRead: null, cacheWrite: null });
        expect(values.breakOutCacheRates).toBe(false);
        expect(values.cacheRead).toBe("");
    });

    test("a price with cache rates opens the breakdown so they are visible", () => {
        const values = pricingFormValues({ input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75" });
        expect(values.breakOutCacheRates).toBe(true);
    });

    test("loads rates already padded to the cent, so a blur is a no-op", () => {
        const values = pricingFormValues({ input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75" });
        expect(values).toMatchObject({ input: "3.00", output: "15.00", cacheRead: "0.30", cacheWrite: "3.75" });
    });
});

describe("isUnpriced", () => {
    test("treats an absent price and an all-null price alike", () => {
        expect(isUnpriced(null)).toBe(true);
        expect(isUnpriced(undefined)).toBe(true);
        expect(isUnpriced({ input: null, output: null, cacheRead: null, cacheWrite: null })).toBe(true);
    });

    test("a model priced at zero is priced, not unpriced", () => {
        expect(isUnpriced({ input: "0", output: "0", cacheRead: null, cacheWrite: null })).toBe(false);
    });
});

describe("rateDisplay", () => {
    const priced: ModelPricing = { input: "3", output: "15", cacheRead: null, cacheWrite: null };

    test("an unpriced model has nothing to show for any rate", () => {
        for (const key of ["input", "output", "cacheRead", "cacheWrite"] as const) {
            expect(rateDisplay(null, key), key).toBeNull();
        }
    });

    test("a stated rate is shown as currency", () => {
        expect(rateDisplay(priced, "input")).toEqual({ amount: "$3.00", resolved: false });
        expect(rateDisplay(priced, "output")).toEqual({ amount: "$15.00", resolved: false });
    });

    test("an unstated cache rate resolves to the input rate and is marked resolved", () => {
        expect(rateDisplay(priced, "cacheRead")).toEqual({ amount: "$3.00", resolved: true });
        expect(rateDisplay(priced, "cacheWrite")).toEqual({ amount: "$3.00", resolved: true });
    });

    test("a stated cache rate wins over the input rate", () => {
        const withCache: ModelPricing = { input: "3", output: "15", cacheRead: "0.3", cacheWrite: "3.75" };
        expect(rateDisplay(withCache, "cacheRead")).toEqual({ amount: "$0.30", resolved: false });
        expect(rateDisplay(withCache, "cacheWrite")).toEqual({ amount: "$3.75", resolved: false });
    });
});

describe("formatRate", () => {
    test("pads to the cent so a rate reads as money", () => {
        expect(formatRate("2")).toBe("$2.00");
        expect(formatRate("0.1")).toBe("$0.10");
        expect(formatRate("0")).toBe("$0.00");
        expect(formatRate("15.5")).toBe("$15.50");
    });

    test("keeps sub-cent precision rather than rounding a rate to zero", () => {
        expect(formatRate("0.0237")).toBe("$0.0237");
        expect(formatRate("0.002")).toBe("$0.002");
        expect(formatRate("0.0001")).toBe("$0.0001");
    });

    test("groups thousands", () => {
        expect(formatRate("1000")).toBe("$1,000.00");
    });
});

describe("formatRateForInput", () => {
    test("pads to the cent, keeping any finer precision entered", () => {
        expect(formatRateForInput("3")).toBe("3.00");
        expect(formatRateForInput("0.1")).toBe("0.10");
        expect(formatRateForInput("0")).toBe("0.00");
        expect(formatRateForInput(".5")).toBe("0.50");
        expect(formatRateForInput("0.0237")).toBe("0.0237");
        expect(formatRateForInput("0.002")).toBe("0.002");
    });

    test("leaves a blank field blank rather than pricing the model at zero", () => {
        expect(formatRateForInput("")).toBe("");
        expect(formatRateForInput("   ")).toBe("");
        expect(formatRateForInput(undefined)).toBe("");
    });

    test("leaves a value that would not validate exactly as typed", () => {
        expect(formatRateForInput("abc")).toBe("abc");
        expect(formatRateForInput("-1")).toBe("-1");
        expect(formatRateForInput("0.00001")).toBe("0.00001");
    });

    test("adds no currency symbol and no thousands separators", () => {
        expect(formatRateForInput("1000")).toBe("1000.00");
        expect(rateFormatError(formatRateForInput("1000"))).toBeNull();
    });
});

describe("rateFormatError upper bound", () => {
    test("refuses a rate past the largest the platform accepts", () => {
        expect(rateFormatError(String(PRICE_MAX_AMOUNT + 1))).not.toBeNull();
        expect(rateFormatError("99999999")).not.toBeNull();
    });

    test("accepts the largest rate the platform accepts", () => {
        expect(rateFormatError(String(PRICE_MAX_AMOUNT))).toBeNull();
    });
});

describe("cacheRatesBlockedByMissingRequiredRates", () => {
    test("blocks a cache rate while either required rate is blank", () => {
        expect(cacheRatesBlockedByMissingRequiredRates({})).toBe(true);
        expect(cacheRatesBlockedByMissingRequiredRates({ input: "3" })).toBe(true);
        expect(cacheRatesBlockedByMissingRequiredRates({ output: "15" })).toBe(true);
    });

    test("allows a cache rate once both required rates are priced", () => {
        expect(cacheRatesBlockedByMissingRequiredRates({ input: "3", output: "15" })).toBe(false);
    });

    test("treats a zero rate as priced", () => {
        expect(cacheRatesBlockedByMissingRequiredRates({ input: "0", output: "0" })).toBe(false);
    });
});

describe("rateDisplay resolved vs stated", () => {
    test("a cache rate equal to the input rate is still a stated rate", () => {
        const pricing: ModelPricing = { input: "3", output: "15", cacheRead: "3", cacheWrite: null };

        expect(rateDisplay(pricing, "cacheRead")).toStrictEqual({ amount: "$3.00", resolved: false });
    });
});
