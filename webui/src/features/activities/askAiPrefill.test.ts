// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { buildTaskTracePrefill } from "./askAiPrefill";

describe("buildTaskTracePrefill", () => {
    it("names the task and its session", () => {
        expect(buildTaskTracePrefill({ taskId: "task-xyz", sessionId: "session-abc" })).toBe("Look up Task ID task-xyz in Session ID session-abc and tell me what happened.");
    });

    it("names only the task when it ran outside any session", () => {
        expect(buildTaskTracePrefill({ taskId: "task-xyz", sessionId: undefined })).toBe("Look up Task ID task-xyz and tell me what happened.");
        expect(buildTaskTracePrefill({ taskId: "task-xyz", sessionId: "" })).toBe("Look up Task ID task-xyz and tell me what happened.");
    });
});
