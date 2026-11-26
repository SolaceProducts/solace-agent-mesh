// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { type BudgetFormInput, budgetFormSchema, formInputToValues } from "./budgetForm.schema";

const valid: BudgetFormInput = { name: "Engineering", scope: "user", unlimited: false, amount: "500", warnPercentage: "75" };

function errorsFor(input: Partial<BudgetFormInput>) {
    const result = budgetFormSchema.safeParse({ ...valid, ...input });
    if (result.success) return {};
    return Object.fromEntries(result.error.issues.map(i => [i.path.join("."), i.message]));
}

describe("budgetFormSchema — name", () => {
    test("accepts a valid form", () => {
        expect(budgetFormSchema.safeParse(valid).success).toBe(true);
    });

    test.each([
        ["", "Use at least 3 characters"],
        ["ab", "Use at least 3 characters"],
        ["   ab   ", "Use at least 3 characters"],
        ["x".repeat(256), "Use 255 characters or fewer"],
    ])("rejects %j", (name, message) => {
        expect(errorsFor({ name }).name).toBe(message);
    });

    test("accepts 3 and 255 characters", () => {
        expect(errorsFor({ name: "abc" }).name).toBeUndefined();
        expect(errorsFor({ name: "x".repeat(255) }).name).toBeUndefined();
    });

    test("counts an emoji as one character", () => {
        expect(errorsFor({ name: "a😀" }).name).toBe("Use at least 3 characters");
        expect(errorsFor({ name: "😀".repeat(255) }).name).toBeUndefined();
        expect(errorsFor({ name: "😀".repeat(256) }).name).toBe("Use 255 characters or fewer");
    });

    test.each(["Engi\tneering", "Engi\nneering", "Engi\u0000neering"])("rejects the control character in %j", name => {
        expect(errorsFor({ name }).name).toBe("Remove tabs, line breaks and other control characters");
    });

    test("trims surrounding whitespace before checking for control characters", () => {
        expect(errorsFor({ name: "\tEngineering\n" }).name).toBeUndefined();
    });
});

describe("budgetFormSchema — limit", () => {
    test.each(["0", "500", "500.5", "500.50", "922337203.68"])("accepts %s", amount => {
        expect(errorsFor({ amount }).amount).toBeUndefined();
    });

    test("an empty limit asks for a value or Unlimited", () => {
        expect(errorsFor({ amount: "" }).amount).toBe("Enter a limit, or select Unlimited");
    });

    test.each(["500.123", "-5", "5e3", "$500", "1,000", "abc", ".5", "500."])("rejects %s", amount => {
        expect(errorsFor({ amount }).amount).toBe("Enter an amount such as 500 or 500.50");
    });

    test.each(["922337203.69", "922337204", "92233720368.54"])("rejects %s, above the storable maximum", amount => {
        expect(errorsFor({ amount }).amount).toBe("Enter a smaller amount");
    });

    test("an unlimited budget needs no amount and ignores bad values", () => {
        expect(errorsFor({ unlimited: true, amount: "", warnPercentage: "" })).toEqual({});
        expect(errorsFor({ unlimited: true, amount: "junk", warnPercentage: "999" })).toEqual({});
    });
});

describe("budgetFormSchema — alert threshold", () => {
    test.each(["", "1", "75", "100"])("accepts %j", warnPercentage => {
        expect(errorsFor({ warnPercentage }).warnPercentage).toBeUndefined();
    });

    test.each(["0", "101", "75.5", "-1", "abc"])("rejects %s", warnPercentage => {
        expect(errorsFor({ warnPercentage }).warnPercentage).toBe("Enter a whole number from 1 to 100");
    });
});

describe("form mapping", () => {
    test("submits a limited budget with a threshold", () => {
        expect(formInputToValues(budgetFormSchema.parse({ name: "  Engineering ", scope: "user", unlimited: false, amount: " 500.50", warnPercentage: "75 " }))).toEqual({ name: "Engineering", scope: "user", amount: "500.50", warnPercentage: 75 });
    });

    test("submits a blank threshold as null", () => {
        expect(formInputToValues({ ...valid, warnPercentage: "" }).warnPercentage).toBeNull();
    });

    test("submits unlimited as a null amount and null threshold", () => {
        expect(formInputToValues({ ...valid, unlimited: true })).toEqual({ name: "Engineering", scope: "user", amount: null, warnPercentage: null });
    });
});
