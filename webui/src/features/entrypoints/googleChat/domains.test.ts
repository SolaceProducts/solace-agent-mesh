// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { allowedDomainsSchema, normalizeDomain, parseDomainEntry } from "./domains";

describe("normalizeDomain", () => {
    test("normalizes case, @ and full addresses", () => {
        for (const input of ["Example.COM", "@example.com"]) {
            expect(normalizeDomain(input)).toEqual({ domain: "example.com" });
        }
        expect(normalizeDomain("someone@example.com")).toEqual({ domain: "example.com" });
    });

    test("gmail gives the whole-domain message", () => {
        expect(normalizeDomain("gmail")).toEqual({ problem: "Invalid entry. Enter only email domains." });
    });

    test("returns null for an empty entry", () => {
        expect(normalizeDomain("  ")).toBeNull();
    });
});

describe("allowedDomainsSchema", () => {
    test("accepts valid domains", () => {
        expect(allowedDomainsSchema.safeParse(["a.com", "b.org"]).success).toBe(true);
    });

    test("reports the whole-domain message for the first bad entry", () => {
        const result = allowedDomainsSchema.safeParse(["a.com", "gmail", "x"]);
        expect(result.success).toBe(false);
        if (!result.success) expect(result.error.issues[0].message).toBe("Invalid entry. Enter only email domains.");
    });
});

describe("parseDomainEntry", () => {
    test("keeps a bad entry as typed, trimmed, with its problem", () => {
        expect(parseDomainEntry("  gmail ")).toEqual({ value: "gmail", problem: "Invalid entry. Enter only email domains." });
        expect(parseDomainEntry("Example.COM")).toEqual({ value: "example.com" });
        expect(parseDomainEntry(" ")).toBeNull();
    });
});
