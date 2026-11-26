// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { hasUnseenShare } from "./sessionUnseen";

const iso = (ms: number) => new Date(ms).toISOString();

describe("hasUnseenShare", () => {
    test("an absent viewedAt means the share was never opened", () => {
        expect(hasUnseenShare({ sharedAt: iso(1_000) })).toBe(true);
    });

    test("a null viewedAt means the share was never opened", () => {
        expect(hasUnseenShare({ sharedAt: iso(1_000), viewedAt: null })).toBe(true);
    });

    test("is unread when the owner changed the share after it was last opened", () => {
        expect(hasUnseenShare({ sharedAt: iso(2_000), viewedAt: iso(1_000) })).toBe(true);
    });

    test("is read once opened after the last change", () => {
        expect(hasUnseenShare({ sharedAt: iso(1_000), viewedAt: iso(2_000) })).toBe(false);
    });

    test("equal timestamps read as read, matching the backfill", () => {
        expect(hasUnseenShare({ sharedAt: iso(1_000), viewedAt: iso(1_000) })).toBe(false);
    });

    test("never claims unread from an unparseable sharedAt", () => {
        expect(hasUnseenShare({ sharedAt: "not-a-date" })).toBe(false);
        expect(hasUnseenShare({ sharedAt: "not-a-date", viewedAt: iso(1_000) })).toBe(false);
    });

    test("treats an unparseable viewedAt as never opened", () => {
        expect(hasUnseenShare({ sharedAt: iso(2_000), viewedAt: "not-a-date" })).toBe(true);
    });
});
