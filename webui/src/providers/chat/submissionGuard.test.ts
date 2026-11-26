// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { isSubmissionDropped, type SubmissionState } from "./submissionGuard";

const sendable: SubmissionState = { hasContent: true, isResponding: false, isCancelling: false, selectedAgentName: "Builder" };

describe("isSubmissionDropped", () => {
    test("a submit with content, no running turn and an agent goes out", () => {
        expect(isSubmissionDropped(sendable)).toBe(false);
    });

    test.each<[string, Partial<SubmissionState>]>([
        ["nothing was typed", { hasContent: false }],
        ["a turn is running", { isResponding: true }],
        ["a turn is being cancelled", { isCancelling: true }],
        ["no agent is selected", { selectedAgentName: "" }],
    ])("a submit is dropped when %s", (_, over) => {
        expect(isSubmissionDropped({ ...sendable, ...over })).toBe(true);
    });
});
