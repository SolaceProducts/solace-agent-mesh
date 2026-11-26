// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { SharedWithMeItem } from "@/types/share";

import { NO_PROJECT_LABEL, NO_PROJECT_VALUE, UNRESOLVED_PROJECT_FILTER } from "@/features/chat/projectFilter";

import type { Session } from "@/types";

import { collectMessageTexts, filterSessions, resolveProjectFilterId, sortSharedChats } from "./recentChatsPageUtils";

const mk = (p: Partial<Session>): Session => p as Session;

describe("collectMessageTexts", () => {
    test("flattens non-empty, trimmed bubble texts across tasks", () => {
        const tasks = [{ messageBubbles: JSON.stringify([{ text: "hello" }, { text: "  " }, { text: " world " }]) }, { messageBubbles: JSON.stringify([{ text: "again" }]) }];
        expect(collectMessageTexts(tasks)).toEqual(["hello", "world", "again"]);
    });

    test("skips a task with unparseable JSON (and warns), keeping the rest", () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
        const tasks = [{ messageBubbles: "{not json" }, { messageBubbles: JSON.stringify([{ text: "ok" }]) }];
        expect(collectMessageTexts(tasks)).toEqual(["ok"]);
        expect(warn).toHaveBeenCalled();
        warn.mockRestore();
    });

    test("skips a task whose JSON is valid but not an array", () => {
        const tasks = [{ messageBubbles: JSON.stringify({ text: "nope" }) }, { messageBubbles: JSON.stringify([{ text: "yes" }]) }];
        expect(collectMessageTexts(tasks)).toEqual(["yes"]);
    });

    test("returns an empty array when no bubble has text", () => {
        expect(collectMessageTexts([{ messageBubbles: JSON.stringify([{ text: "" }, {}]) }])).toEqual([]);
    });
});

describe("filterSessions", () => {
    const sessions = [mk({ id: "1", source: "chat", project: { id: "p1", name: "Alpha" } }), mk({ id: "2", source: "scheduler" }), mk({ id: "3", source: "chat" }), mk({ id: "4", source: "chat", isEphemeralTest: true })];

    test("always drops ephemeral builder Test-mode sessions", () => {
        expect(filterSessions(sessions, "chat", "all", true).map(s => s.id)).not.toContain("4");
    });

    test("scheduler tab keeps only scheduler sessions", () => {
        expect(filterSessions(sessions, "scheduler", "all", true).map(s => s.id)).toEqual(["2"]);
    });

    test("chat tab excludes scheduler sessions", () => {
        expect(filterSessions(sessions, "chat", "all", true).map(s => s.id)).toEqual(["1", "3"]);
    });

    test("filters by a specific project name", () => {
        expect(filterSessions(sessions, "chat", "Alpha", true).map(s => s.id)).toEqual(["1"]);
    });

    test("'(No Project)' keeps only sessions without a project", () => {
        expect(filterSessions(sessions, "chat", "(No Project)", true).map(s => s.id)).toEqual(["3"]);
        expect(NO_PROJECT_LABEL).toBe("(No Project)");
    });

    test("drops Builder sessions when the user lacks Builder access", () => {
        const withBuilder = [mk({ id: "1", source: "chat" }), mk({ id: "b", source: "chat", agentId: "Builder" })];
        expect(filterSessions(withBuilder, "chat", "all", false).map(s => s.id)).toEqual(["1"]);
    });

    test("keeps Builder sessions when the user has Builder access", () => {
        const withBuilder = [mk({ id: "1", source: "chat" }), mk({ id: "b", source: "chat", agentId: "Builder" })];
        expect(filterSessions(withBuilder, "chat", "all", true).map(s => s.id)).toEqual(["1", "b"]);
    });
});

describe("sortSharedChats", () => {
    const shared = (id: string, sharedAtMs: number): SharedWithMeItem => ({
        id,
        title: id,
        ownerEmail: "owner@example.com",
        accessLevel: "resource_viewer",
        sharedAt: new Date(sharedAtMs).toISOString(),
        shareUrl: `https://example.com/${id}`,
        sessionId: null,
    });

    test("orders most recently shared first", () => {
        const items = [shared("oldest", 1_000), shared("newest", 3_000), shared("middle", 2_000)];
        expect(sortSharedChats(items).map(i => i.id)).toEqual(["newest", "middle", "oldest"]);
    });

    test("breaks ties on id so the order is stable", () => {
        const items = [shared("a", 1_000), shared("b", 1_000)];
        expect(sortSharedChats(items).map(i => i.id)).toEqual(["b", "a"]);
    });

    test("does not mutate the input array", () => {
        const items = [shared("oldest", 1_000), shared("newest", 3_000)];
        sortSharedChats(items);
        expect(items.map(i => i.id)).toEqual(["oldest", "newest"]);
    });
});

describe("resolveProjectFilterId", () => {
    const withProject = mk({ id: "1", project: { id: "p1", name: "Alpha" } });
    const withoutProject = mk({ id: "2" });
    const sessions = [withProject, withoutProject];

    test("'all' means no project filter", () => {
        expect(resolveProjectFilterId("all", sessions, [])).toBeNull();
    });

    test("a real project resolves to its id", () => {
        expect(resolveProjectFilterId("Alpha", sessions, [])).toBe("p1");
    });

    test("a project with no chats resolves to its id from the projects list", () => {
        const projects = [{ id: "p2", name: "Chatless", userId: "u1", createdAt: "2026-01-01T00:00:00Z" }];
        expect(resolveProjectFilterId("Chatless", sessions, projects)).toBe("p2");
    });

    test("'(No Project)' resolves to the unassigned sentinel, not null", () => {
        expect(resolveProjectFilterId(NO_PROJECT_LABEL, sessions, [])).toBe(NO_PROJECT_VALUE);
    });

    test("an unresolvable project narrows to nothing rather than dropping the filter", () => {
        expect(resolveProjectFilterId("Nonexistent", sessions, [])).toBe(UNRESOLVED_PROJECT_FILTER);
        expect(resolveProjectFilterId("Nonexistent", sessions, [])).not.toBeNull();
    });

    test("sends the value the gateway recognises", () => {
        expect(NO_PROJECT_VALUE).toBe("__no_project__");
    });
});
