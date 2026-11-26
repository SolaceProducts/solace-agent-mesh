// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { CrossUserFeedback } from "@/api/feedback";

import { buildFeedbackTracePrefill } from "./feedbackTracePrefill";

const row = (over: Partial<CrossUserFeedback>): CrossUserFeedback => ({
    id: "f1",
    sessionId: "session-abc",
    taskId: "task-xyz",
    userId: "user-123",
    rating: "down",
    comment: null,
    shareTaskContent: false,
    subjectId: "a-1",
    subjectType: "agent",
    subjectName: "Procurement Intake Router",
    entrypointType: "slack",
    createdAt: "2026-08-25T09:42:00.000Z",
    ...over,
});

describe("buildFeedbackTracePrefill", () => {
    it("asks about a negative rating by its feedback id", () => {
        expect(buildFeedbackTracePrefill(row({ rating: "down" }))).toBe("Why was this response rated negative? Feedback ID f1");
    });

    it("asks about a positive rating the same way", () => {
        expect(buildFeedbackTracePrefill(row({ rating: "up" }))).toBe("Why was this response rated positive? Feedback ID f1");
    });

    it("leaves out the comment and the session and task ids", () => {
        const text = buildFeedbackTracePrefill(row({ comment: "gave me last year's limit" }));
        for (const omitted of ["gave me last year's limit", "session-abc", "task-xyz"]) {
            expect(text).not.toContain(omitted);
        }
    });
});
