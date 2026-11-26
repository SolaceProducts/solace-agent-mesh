// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { groupTasksBySession, subtreeTokenUsage, taskEventCount, taskTokenUsage } from "./sessionGrouping";
import type { HierarchicalTaskItem } from "./TaskListPanel";

const task = (taskId: string, overrides: Partial<HierarchicalTaskItem> = {}): HierarchicalTaskItem => ({
    taskId,
    initialRequestText: `request ${taskId}`,
    events: [],
    firstSeen: new Date("2026-01-01T00:00:00Z"),
    lastUpdated: new Date("2026-01-01T00:00:05Z"),
    parentTaskId: null,
    children: [],
    nestingLevel: 0,
    ...overrides,
});

describe("groupTasksBySession", () => {
    it("groups root tasks that share a sessionId, newest first, keyed by the session", () => {
        const groups = groupTasksBySession([task("t3", { sessionId: "s1" }), task("t2", { sessionId: "s2" }), task("t1", { sessionId: "s1" })]);

        expect(groups.map(g => g.key)).toEqual(["s1", "s2"]);
        expect(groups[0].tasks.map(t => t.taskId)).toEqual(["t3", "t1"]);
        expect(groups[0].latestTask.taskId).toBe("t3");
        expect(groups[0].turnCount).toBe(2);
        expect(groups[1].turnCount).toBe(1);
    });

    it("orders sessions by their most recent task, following the list order", () => {
        const groups = groupTasksBySession([task("new", { sessionId: "s2" }), task("older", { sessionId: "s1" }), task("oldest", { sessionId: "s2" })]);
        expect(groups.map(g => g.sessionId)).toEqual(["s2", "s1"]);
    });

    it("gives a task with no sessionId a session of its own", () => {
        const groups = groupTasksBySession([task("a"), task("b", { sessionId: "" })]);
        expect(groups).toHaveLength(2);
        expect(groups[0]).toMatchObject({ key: "task:a", sessionId: null, turnCount: 1 });
        expect(groups[1]).toMatchObject({ key: "task:b", sessionId: null, turnCount: 1 });
    });

    it("takes the session name from whichever task carries it", () => {
        const groups = groupTasksBySession([task("t2", { sessionId: "s1" }), task("t1", { sessionId: "s1", sessionName: "Weather in Ottawa" }), task("t0", { sessionId: "s2" })]);
        expect(groups[0].name).toBe("Weather in Ottawa");
        expect(groups[1].name).toBeNull();
    });

    it("sums the event counts of the session's root tasks", () => {
        const groups = groupTasksBySession([task("t1", { sessionId: "s1", eventCount: 4 }), task("t2", { sessionId: "s1", eventCount: 6 })]);
        expect(groups[0].eventCount).toBe(10);
    });
});

describe("taskEventCount", () => {
    it("prefers the loaded events over the persisted count", () => {
        expect(taskEventCount({ events: [], eventCount: 7 })).toBe(7);
        expect(taskEventCount({ events: [{} as never, {} as never], eventCount: 7 })).toBe(2);
        expect(taskEventCount({ events: [] })).toBe(0);
    });
});

describe("token usage", () => {
    it("is null for a task that reported no usage, and zero-filled when one side is missing", () => {
        expect(taskTokenUsage(task("a"))).toBeNull();
        expect(taskTokenUsage(task("b", { inputTokens: 120 }))).toEqual({ input: 120, output: 0 });
        expect(taskTokenUsage(task("c", { inputTokens: 0, outputTokens: 0 }))).toEqual({ input: 0, output: 0 });
    });

    it("sums a session's turns and their sub-tasks, and is null when nothing reported", () => {
        const subTask = task("sub", { inputTokens: 50, outputTokens: 5, nestingLevel: 1 });
        const turn1 = task("t1", { sessionId: "s1", inputTokens: 1000, outputTokens: 100, children: [subTask] });
        const turn2 = task("t2", { sessionId: "s1", inputTokens: 200, outputTokens: 20 });
        const silent = task("t3", { sessionId: "s2" });

        expect(subtreeTokenUsage([turn1, turn2])).toEqual({ input: 1250, output: 125 });
        expect(subtreeTokenUsage([silent])).toBeNull();

        const groups = groupTasksBySession([turn1, turn2, silent]);
        expect(groups[0].tokenUsage).toEqual({ input: 1250, output: 125 });
        expect(groups[1].tokenUsage).toBeNull();
    });
});
