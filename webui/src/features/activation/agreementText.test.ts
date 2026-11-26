// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { listSeparator, splitCheckboxText, termsLoadRetryable } from "./agreementText";

describe("splitCheckboxText", () => {
    test("splits SpotDraft's wording around its agreement list", () => {
        expect(splitCheckboxText("I agree to {#agreement_list#} as per laws.")).toEqual(["I agree to ", " as per laws."]);
        expect(splitCheckboxText("{#agreement_list#}")).toEqual(["", ""]);
    });

    test("has nothing to fill in wording without the list", () => {
        expect(splitCheckboxText("")).toBeNull();
        expect(splitCheckboxText("I agree.")).toBeNull();
    });
});

describe("listSeparator", () => {
    test("joins names as a sentence would", () => {
        const join = (names: string[]) => names.map((name, index) => listSeparator(index, names.length) + name).join("");
        expect(join(["A"])).toBe("A");
        expect(join(["A", "B"])).toBe("A and B");
        expect(join(["A", "B", "C"])).toBe("A, B and C");
    });
});

describe("termsLoadRetryable", () => {
    test("offers a retry unless the build links no agreement", () => {
        expect(termsLoadRetryable(Object.assign(new Error("down"), { status: 502 }))).toBe(true);
        expect(termsLoadRetryable(new TypeError("Load failed"))).toBe(true);
        expect(termsLoadRetryable(Object.assign(new Error("unlinked"), { status: 503 }))).toBe(false);
    });
});
