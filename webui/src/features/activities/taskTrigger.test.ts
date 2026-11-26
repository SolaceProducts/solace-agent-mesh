// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { TaskSubject, TaskTrigger } from "@/types";

import { taskOriginLine } from "./taskTrigger";

const workflow: TaskSubject = { id: "workflow_01a1", type: "workflow", name: "Dependency update" };
const webhook: TaskTrigger = { kind: "event", channel: "webhook", entrypointName: "github-deps", event: "check_suite.completed" };

describe("taskOriginLine", () => {
    test("a top-level task names its workflow and what started it", () => {
        expect(taskOriginLine({ subject: workflow, trigger: webhook })).toBe("Dependency update · webhook · github-deps · check_suite.completed");
    });

    test("a delegated task names only what handled it", () => {
        expect(taskOriginLine({ subject: { id: "Fixer", type: "agent", name: "Fixer" }, trigger: { channel: "webhook", entrypointName: "github-deps" }, parentTaskId: "task-root" })).toBe("Fixer");
    });

    test("falls back to the trigger kind when no channel was recorded", () => {
        expect(taskOriginLine({ trigger: { kind: "scheduled" } })).toBe("scheduled");
    });

    test("is empty for a row logged before triggers were recorded", () => {
        expect(taskOriginLine({})).toBeUndefined();
    });
});
