// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { actionLabel } from "./notificationAction";

describe("actionLabel", () => {
    test("uses the producer's label for an in-app path", () => {
        expect(actionLabel({ label: "Review budget", path: "/budgets" })).toBe("Review budget");
    });

    test("falls back to a generic label when the producer's is blank or only whitespace", () => {
        expect(actionLabel({ label: "   ", path: "/budgets" })).toBe("Open");
        expect(actionLabel({ label: "", path: "/budgets" })).toBe("Open");
    });

    test("says View Chat for an action that opens a chat, whatever its label", () => {
        expect(actionLabel({ label: "Review budget", sessionId: "s1" })).toBe("View Chat");
        expect(actionLabel({ label: "", shareId: "sh1" })).toBe("View Chat");
    });

    test("says Respond in Chat for an input request still waiting in its chat, and View Chat once it is no longer", () => {
        const action = { label: "", sessionId: "s1" };
        const base = { id: "h1", title: "Your input is requested", body: "", createdAt: "2026-06-01T10:00:00.000Z", requestId: "req-1", surface: { surfaceId: "req-1", catalogId: "", components: [], dataModel: {} }, action };
        expect(actionLabel(action, { ...base, status: "unread", promptState: "pending" })).toBe("Respond in Chat");
        expect(actionLabel(action, { ...base, status: "read", promptState: "pending" })).toBe("Respond in Chat");
        expect(actionLabel(action, { ...base, status: "read", promptState: "resolved" })).toBe("View Chat");
    });
});
