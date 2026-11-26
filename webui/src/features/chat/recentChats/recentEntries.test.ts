// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { SharedWithMeItem } from "@/types/share";

import type { Session } from "@/types";

import { mergeRecentEntries } from "./recentEntries";

const iso = (ms: number) => new Date(ms).toISOString();

const session = (id: string, updatedAtMs: number) => ({ id, name: id, updatedAt: iso(updatedAtMs) }) as unknown as Session;

const shared = (id: string, sharedAtMs: number): SharedWithMeItem => ({
    id,
    title: id,
    ownerEmail: "owner@example.com",
    accessLevel: "resource_viewer",
    sharedAt: iso(sharedAtMs),
    shareUrl: `https://example.com/${id}`,
    sessionId: null,
});

const ids = (entries: ReturnType<typeof mergeRecentEntries>) => entries.map(e => (e.kind === "session" ? e.session.id : e.shared.id));

describe("mergeRecentEntries", () => {
    test("interleaves sessions and shares into one most-recent-first list", () => {
        const entries = mergeRecentEntries([session("s-old", 1_000), session("s-new", 4_000)], [shared("sh-mid", 3_000), shared("sh-older", 2_000)], {
            maxItems: 10,
            includeShared: true,
        });

        expect(ids(entries)).toEqual(["s-new", "sh-mid", "sh-older", "s-old"]);
    });

    test("a freshly shared chat outranks every older owned session", () => {
        const entries = mergeRecentEntries([session("s-1", 1_000), session("s-2", 2_000)], [shared("sh-new", 5_000)], { maxItems: 10, includeShared: true });

        expect(ids(entries)[0]).toBe("sh-new");
    });

    test("omits shares when they are excluded (agent-scoped lists)", () => {
        const entries = mergeRecentEntries([session("s-1", 1_000)], [shared("sh-1", 5_000)], { maxItems: 10, includeShared: false });

        expect(ids(entries)).toEqual(["s-1"]);
    });

    test("truncates after sorting, so the newest items survive the cap", () => {
        const entries = mergeRecentEntries([session("s-old", 1_000), session("s-new", 9_000)], [shared("sh-mid", 5_000)], { maxItems: 2, includeShared: true });

        expect(ids(entries)).toEqual(["s-new", "sh-mid"]);
    });

    test("sorts unparseable timestamps last instead of throwing off the order", () => {
        const broken = { ...shared("sh-broken", 0), sharedAt: "not-a-date" };
        const entries = mergeRecentEntries([session("s-1", 1_000)], [broken], { maxItems: 10, includeShared: true });

        expect(ids(entries)).toEqual(["s-1", "sh-broken"]);
    });

    test("returns an empty list when there is nothing to show", () => {
        expect(mergeRecentEntries([], [], { maxItems: 10, includeShared: true })).toEqual([]);
    });
});
