// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { formatCollaborativeTimestamp, getUserInitials } from "@/utils/userFormatting";

describe("getUserInitials", () => {
    test("returns two-letter initials from first and last name", () => {
        expect(getUserInitials("John Doe")).toBe("JD");
    });

    test("returns first two chars for single name", () => {
        expect(getUserInitials("Alice")).toBe("AL");
    });

    test("uses first and last name for three-word names", () => {
        expect(getUserInitials("Vincent Van Gogh")).toBe("VG");
    });

    test("returns empty string for empty input", () => {
        expect(getUserInitials("")).toBe("");
    });

    test("returns empty string for whitespace-only input", () => {
        expect(getUserInitials("   ")).toBe("");
    });

    test("uppercases the initials", () => {
        expect(getUserInitials("jane smith")).toBe("JS");
    });
});

describe("formatCollaborativeTimestamp", () => {
    test("formats epoch timestamp as time string", () => {
        const ts = new Date("2024-01-15T14:30:00Z").getTime();
        const result = formatCollaborativeTimestamp(ts);
        expect(result).toMatch(/\d{1,2}:\d{2}\s*(AM|PM|a\.m\.|p\.m\.)/i);
    });
});
