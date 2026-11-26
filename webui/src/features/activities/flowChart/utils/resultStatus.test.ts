// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { isFailedResultStatus, isPlaceholderToolResult } from "./resultStatus";

describe("isPlaceholderToolResult", () => {
    test.each([
        ["a sign-in placeholder as published, with the marker redacted", { status: "pending", _pending_auth_marker: "<REDACTED>", _pending_auth_function_call_id: "fc-1" }],
        ["a sign-in placeholder with the marker intact", { status: "pending", _pending_auth_marker: true }],
        ["a peer delegation placeholder", { status: "pending", sub_task_id: "sub-1", peer_name: "Researcher" }],
        ["a workflow placeholder published before it stated its status", { sub_task_id: "sub-1", workflow_name: "triage", pending_kind: "workflow" }],
        ["a human-approval placeholder", { hil_request_id: "hil-1", pending_kind: "hil_approval" }],
        ["a human-approval placeholder that states its status", { hil_request_id: "hil-1", status: "pending" }],
    ])("recognises %s", (_, resultData) => {
        expect(isPlaceholderToolResult(resultData)).toBe(true);
    });

    test.each([
        ["a tool's own result that says pending", { status: "pending", job_id: "job-7" }],
        ["a peer's final result", { status: "success", sub_task_id: "sub-1", peer_name: "Researcher", response: "done" }],
        ["an approval's final result", { status: "approved", approver: "reviewer" }],
        ["a result that names an approval but no longer says pending", { hil_request_id: "hil-1", status: "approved" }],
        ["a sign-in marker with any other value", { status: "pending", _pending_auth_marker: "yes" }],
        ["a text result", "pending"],
        ["no result", null],
    ])("does not treat %s as a placeholder", (_, resultData) => {
        expect(isPlaceholderToolResult(resultData)).toBe(false);
    });
});

describe("isFailedResultStatus", () => {
    test.each(["error", "failed", "timed_out", "failure"])("%s is a failure", status => {
        expect(isFailedResultStatus(status)).toBe(true);
    });

    test.each(["success", "pending", "skipped", undefined])("%s is not a failure", status => {
        expect(isFailedResultStatus(status)).toBe(false);
    });
});
