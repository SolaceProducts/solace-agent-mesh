// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { NotificationItem } from "@/api/notifications";

import { isHilPending, isResolvedHil, resolvedHilLabel } from "./hilState";

const surface = { surfaceId: "s", components: [] } as unknown as NotificationItem["surface"];

function item(fields: Partial<NotificationItem>): NotificationItem {
    return { id: "n", title: "t", body: "", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", ...fields } as NotificationItem;
}

describe("isHilPending", () => {
    it.each([
        ["an unread prompt with routing off", item({ requestId: "r", surface }), true],
        ["an unread pending prompt", item({ requestId: "r", surface, promptState: "pending" }), true],
        ["a read pending prompt", item({ requestId: "r", surface, status: "read", promptState: "pending" }), true],
        ["a read prompt with routing off", item({ requestId: "r", surface, status: "read" }), false],
        ["an unread resolved prompt", item({ requestId: "r", surface, promptState: "resolved" }), false],
        ["a read resolved prompt", item({ requestId: "r", surface, status: "read", promptState: "resolved" }), false],
        ["a pending prompt without a form", item({ requestId: "r", promptState: "pending" }), false],
        ["a notification that is not an input request", item({ surface, promptState: "pending" }), false],
    ])("%s → %s", (_name, n, want) => {
        expect(isHilPending(n)).toBe(want);
    });
});

describe("isResolvedHil", () => {
    it.each([
        ["a resolved prompt", item({ requestId: "r", promptState: "resolved" }), true],
        ["a pending prompt", item({ requestId: "r", surface, promptState: "pending" }), false],
        ["a resolved state on a notification that is not an input request", item({ promptState: "resolved" }), false],
    ])("%s → %s", (_name, n, want) => {
        expect(isResolvedHil(n)).toBe(want);
    });
});

describe("resolvedHilLabel", () => {
    const resolved = (fields: Partial<NotificationItem>) => item({ requestId: "r", promptState: "resolved", ...fields });
    it.each([
        ["an approval answered in Slack", resolved({ promptOutcome: "approved", promptAnsweredBy: "Dana Kim" }), "Approved by Dana Kim"],
        ["a declined approval", resolved({ promptOutcome: "declined", promptAnsweredBy: "Dana Kim" }), "Declined by Dana Kim"],
        ["an answer with no yes or no decision", resolved({ promptOutcome: "answered" }), "Answered"],
        ["a prompt asked of one person, which carries no outcome", resolved({ promptAnsweredBy: undefined }), "Expired"],
        ["a prompt nobody answered in time", resolved({ promptOutcome: "expired" }), "Expired"],
        ["a prompt whose run ended", resolved({ promptOutcome: "withdrawn" }), "No longer needed"],
        ["a prompt no longer on record", resolved({}), "Expired"],
    ])("%s → %s", (_name, n, want) => {
        expect(resolvedHilLabel(n)).toBe(want);
    });
});
