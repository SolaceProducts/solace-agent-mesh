// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { NotificationItem } from "@/api/notifications";

import { pendingChatRequests } from "./usePendingChatRequests";

const surface = { surfaceId: "s", catalogId: "", components: [], dataModel: {} };
const request = (id: string, sessionId: string | undefined, promptState: "pending" | "resolved" = "pending"): NotificationItem => ({
    id,
    title: "Your input is requested",
    body: "",
    status: "unread",
    createdAt: "2026-06-01T10:00:00.000Z",
    requestId: `req-${id}`,
    surface,
    promptState,
    ...(sessionId && { action: { label: "", sessionId } }),
});

describe("pendingChatRequests", () => {
    const items = [request("a", "chat-1"), request("b", "chat-2"), request("c", "chat-1", "resolved"), request("d", undefined), request("e", "chat-1")];

    it("keeps the requests still waiting in this chat", () => {
        expect(pendingChatRequests(items, "chat-1", new Set()).map(n => n.id)).toEqual(["a", "e"]);
    });

    it("leaves out a request the chat already shows from its own stream", () => {
        expect(pendingChatRequests(items, "chat-1", new Set(["req-a"])).map(n => n.id)).toEqual(["e"]);
    });

    it("shows nothing when no chat is open", () => {
        expect(pendingChatRequests(items, undefined, new Set())).toEqual([]);
    });

    it("ignores ordinary notifications that point at the chat", () => {
        const plain: NotificationItem = { id: "p", title: "Report ready", body: "", status: "unread", createdAt: "2026-06-01T10:00:00.000Z", action: { label: "", sessionId: "chat-1" } };
        expect(pendingChatRequests([plain], "chat-1", new Set())).toEqual([]);
    });
});
