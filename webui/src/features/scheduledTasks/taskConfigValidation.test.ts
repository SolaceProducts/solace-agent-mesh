// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { TaskConfig } from "./task-config";
import { validateTaskConfig } from "./taskConfigValidation";

const valid: TaskConfig = {
    name: "Report",
    description: "",
    scheduleType: "interval",
    scheduleExpression: "30m",
    targetType: "agent",
    targetAgentName: "Orchestrator",
    taskMessage: "Summarise last week",
    timezone: "UTC",
    enabled: true,
};

const tripSchema = { properties: { destination: { type: "string" } }, required: ["destination"] };

describe("validateTaskConfig", () => {
    beforeEach(() => {
        vi.useFakeTimers();
        vi.setSystemTime(new Date("2026-10-19T12:00:00Z"));
    });
    afterEach(() => vi.useRealTimers());

    it("accepts a complete form", () => {
        expect(validateTaskConfig(valid, undefined)).toEqual({});
    });

    it("names every empty required field at once, in form order", () => {
        const errors = validateTaskConfig({ ...valid, name: " ", scheduleExpression: "", timezone: "", targetAgentName: "", taskMessage: "" }, undefined);
        expect(Object.keys(errors)).toEqual(["name", "scheduleExpression", "timezone", "targetAgentName", "taskMessage"]);
        expect(errors.taskMessage).toBe("Instructions are required");
    });

    it("leaves a cron expression to the schedule builder", () => {
        expect(validateTaskConfig({ ...valid, scheduleType: "cron", scheduleExpression: "0 9 * * *" }, undefined)).toEqual({});
    });

    it.each([
        ["30m", undefined],
        ["45", "Interval must be a positive number followed by s, m, h, or d (e.g. 30m)"],
        ["30s", "Interval must be at least 60 seconds"],
        ["400d", "Interval must be at most 1 year"],
    ])("interval %s", (expression, wantError) => {
        expect(validateTaskConfig({ ...valid, scheduleExpression: expression }, undefined).scheduleExpression).toBe(wantError);
    });

    it.each([
        ["2026-10-20T09:00:00", undefined],
        ["2026-10-20 09:00", "Invalid date & time — use format YYYY-MM-DDTHH:MM:SS"],
        ["2026-13-45T09:00:00", "Invalid date & time"],
        ["2026-10-18T09:00:00", "Scheduled time must be in the future"],
    ])("one-time %s", (expression, wantError) => {
        expect(validateTaskConfig({ ...valid, scheduleType: "one_time", scheduleExpression: expression }, undefined).scheduleExpression).toBe(wantError);
    });

    it("checks the instructions against the workflow's input schema", () => {
        expect(validateTaskConfig({ ...valid, taskMessage: "plan a trip" }, tripSchema).taskMessage).toContain("enter a JSON object");
        expect(validateTaskConfig({ ...valid, taskMessage: '{"destination":"Porto"}' }, tripSchema)).toEqual({});
    });

    it("does not check the instructions of a target that declares no input shape", () => {
        expect(validateTaskConfig({ ...valid, taskMessage: "plan a trip" }, undefined)).toEqual({});
    });
});
