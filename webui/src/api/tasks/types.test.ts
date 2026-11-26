// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { isTaskRunning, type PersistedTaskRow, recentTasksPath, rowToTaskFE } from "./types";

describe("isTaskRunning", () => {
    it("is false for no task", () => {
        expect(isTaskRunning(undefined)).toBe(false);
    });

    it("is TRUE for a real in-flight task: no status, no endTime", () => {
        expect(isTaskRunning({})).toBe(true);
        expect(isTaskRunning({ status: undefined, endTime: undefined })).toBe(true);
    });

    it("is true for a non-terminal status with no endTime", () => {
        expect(isTaskRunning({ status: "working" })).toBe(true);
        expect(isTaskRunning({ status: "submitted" })).toBe(true);
        expect(isTaskRunning({ status: "input-required" })).toBe(true);
    });

    it("is false once a terminal status + endTime are recorded", () => {
        for (const status of ["completed", "failed", "canceled", "rejected", "timeout"]) {
            expect(isTaskRunning({ status, endTime: "2026-01-01T00:00:05.000Z" })).toBe(false);
        }
    });

    it("is false when an endTime is present even without a terminal status", () => {
        expect(isTaskRunning({ endTime: "2026-01-01T00:00:05.000Z" })).toBe(false);
    });
});

describe("recentTasksPath", () => {
    it("asks for the newest root tasks with their session names", () => {
        expect(recentTasksPath(100)).toBe("/api/v1/tasks?pageSize=100&sort=lastActivityTime%3Adesc&kind=task&expand=sessionName");
    });

    it("adds the owner and search filters only when set", () => {
        expect(recentTasksPath(50, { userId: "a@b.com", search: " 01a1 " })).toBe("/api/v1/tasks?pageSize=50&sort=lastActivityTime%3Adesc&kind=task&expand=sessionName&userId=a%40b.com&search=+01a1+");
        expect(recentTasksPath(50, { userId: "", search: undefined })).not.toContain("userId");
        expect(recentTasksPath(50, { sessionIds: ["s-1", "s-2"] })).toContain("&sessionIds=s-1%2Cs-2");
        expect(recentTasksPath(50, { sessionIds: [] })).not.toContain("sessionIds");
    });
});

describe("rowToTaskFE", () => {
    it("carries status and endTime through so running detection works", () => {
        const running: PersistedTaskRow = { id: "t-run", initialRequestText: "go", startTime: "2026-01-01T00:00:00Z" };
        const fe = rowToTaskFE(running);
        expect(fe.status).toBeUndefined();
        expect(fe.endTime).toBeUndefined();
        expect(isTaskRunning(fe)).toBe(true);

        const done: PersistedTaskRow = { id: "t-done", status: "completed", endTime: "2026-01-01T00:00:05Z" };
        const feDone = rowToTaskFE(done);
        expect(feDone.status).toBe("completed");
        expect(feDone.endTime).toBe("2026-01-01T00:00:05Z");
        expect(isTaskRunning(feDone)).toBe(false);
    });

    it("carries the session name through when the list expanded it", () => {
        expect(rowToTaskFE({ id: "t-1", sessionId: "s-1", sessionName: "Weather in Ottawa" }).sessionName).toBe("Weather in Ottawa");
        expect(rowToTaskFE({ id: "t-2", sessionId: "s-2" }).sessionName).toBeUndefined();
    });

    it("carries the eager eventCount through while events stay empty for lazy hydration", () => {
        const row: PersistedTaskRow = { id: "t-1", eventCount: 7 };
        const fe = rowToTaskFE(row);
        expect(fe.eventCount).toBe(7);
        expect(fe.events).toEqual([]);
        expect(fe.needsHistoricalHydration).toBe(true);
    });

    it("carries the handling workflow and what started the task", () => {
        const row: PersistedTaskRow = {
            id: "t-hook",
            subject: { id: "workflow_01a1", type: "workflow", name: "Dependency update" },
            trigger: { kind: "event", channel: "webhook", entrypointId: "ep-1", entrypointName: "github-deps", route: "deps", event: "check_suite.completed", deliveryId: "d-1" },
        };
        const fe = rowToTaskFE(row);
        expect(fe.subject).toEqual(row.subject);
        expect(fe.trigger).toEqual(row.trigger);
    });
});

describe("rowToTaskFE token totals", () => {
    it("carries the list's token totals and leaves them unset when the row has none", () => {
        const row: PersistedTaskRow = { id: "t1", totalInputTokens: 1200, totalOutputTokens: 300 };
        expect(rowToTaskFE(row)).toMatchObject({ inputTokens: 1200, outputTokens: 300 });
        expect(rowToTaskFE({ id: "t2" })).toMatchObject({ inputTokens: undefined, outputTokens: undefined });
    });
});
