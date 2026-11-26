// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { dedupeConsecutiveUpdates, filterActivityUpdates, hasLiveActivityUpdates } from "./activityIndicator";

const msg = (over: Partial<{ isUser: boolean; isComplete: boolean; isStatusBubble: boolean; taskId: string; parts: { kind?: string; data?: unknown }[]; progressUpdates: { type: string }[] }> = {}) => ({
    isUser: false,
    isComplete: false,
    taskId: "task-1",
    progressUpdates: [] as { type: string }[],
    ...over,
});

const dataPart = (type: string) => ({ kind: "data", data: { type } });

describe("filterActivityUpdates", () => {
    const updates = [
        { type: "status", text: "a" },
        { type: "thinking", text: "b" },
        { type: "tool_call", text: "c" },
    ];

    test("drops thinking items when show-thinking is off", () => {
        expect(filterActivityUpdates(updates, false).map(u => u.type)).toEqual(["status", "tool_call"]);
    });

    test("keeps thinking items when show-thinking is on", () => {
        expect(filterActivityUpdates(updates, true)).toHaveLength(3);
    });

    test("treats undefined as empty", () => {
        expect(filterActivityUpdates(undefined, false)).toEqual([]);
    });
});

describe("hasLiveActivityUpdates drops the trailing dot only while a step is actually animating", () => {
    test("false with no updates yet, so the dot bridges the initial gap", () => {
        expect(hasLiveActivityUpdates(msg({ progressUpdates: [] }), false)).toBe(false);
    });

    test("true once a step lands", () => {
        expect(hasLiveActivityUpdates(msg({ progressUpdates: [{ type: "status" }] }), false)).toBe(true);
    });

    test("false for thinking-only updates when show-thinking is off", () => {
        expect(hasLiveActivityUpdates(msg({ progressUpdates: [{ type: "thinking" }] }), false)).toBe(false);
    });

    test("true for thinking-only updates when show-thinking is on", () => {
        expect(hasLiveActivityUpdates(msg({ progressUpdates: [{ type: "thinking" }] }), true)).toBe(true);
    });

    test("false for a user message", () => {
        expect(hasLiveActivityUpdates(msg({ isUser: true, progressUpdates: [{ type: "status" }] }), false)).toBe(false);
    });

    test("false once the message completes, so a finished turn stops claiming activity", () => {
        expect(hasLiveActivityUpdates(msg({ isComplete: true, progressUpdates: [{ type: "status" }] }), false)).toBe(false);
    });

    test("false when there is no message at all", () => {
        expect(hasLiveActivityUpdates(undefined, false)).toBe(false);
    });

    test("false without a taskId, since isActive needs isLastWithTaskId", () => {
        expect(hasLiveActivityUpdates(msg({ taskId: undefined, progressUpdates: [{ type: "status" }] }), false)).toBe(false);
    });

    test("false for a status bubble, which ChatMessage renders as nothing", () => {
        expect(hasLiveActivityUpdates(msg({ isStatusBubble: true, progressUpdates: [{ type: "status" }] }), false)).toBe(false);
    });

    test("false when a compaction notification renders in place of the timeline", () => {
        expect(hasLiveActivityUpdates(msg({ parts: [dataPart("compaction_notification")], progressUpdates: [{ type: "status" }] }), false)).toBe(false);
    });

    test("false when a deep-research plan renders in place of the timeline", () => {
        expect(hasLiveActivityUpdates(msg({ parts: [dataPart("deep_research_plan")], progressUpdates: [{ type: "status" }] }), false)).toBe(false);
    });

    test("true for deep-research progress, which animates its own cue", () => {
        expect(hasLiveActivityUpdates(msg({ parts: [dataPart("deep_research_progress")], progressUpdates: [{ type: "status" }] }), false)).toBe(true);
    });

    test("true for an ordinary text part", () => {
        expect(hasLiveActivityUpdates(msg({ parts: [{ kind: "text" }], progressUpdates: [{ type: "status" }] }), false)).toBe(true);
    });
});

describe("dedupeConsecutiveUpdates", () => {
    const u = (text: string, timestamp: number, type = "status") => ({ type, text, timestamp });

    test("keeps the first of a consecutive run, so the latest entry stops moving", () => {
        const deduped = dedupeConsecutiveUpdates([u("Applying edits...", 1), u("Applying edits...", 2), u("Applying edits...", 3)]);

        expect(deduped).toEqual([u("Applying edits...", 1)]);
    });

    test("keeps a repeat that is not consecutive", () => {
        const deduped = dedupeConsecutiveUpdates([u("Fixing errors...", 1), u("Validating...", 2), u("Fixing errors...", 3)]);

        expect(deduped.map(d => d.timestamp)).toEqual([1, 2, 3]);
    });

    test("never drops thinking items — identical headers can carry different content", () => {
        const deduped = dedupeConsecutiveUpdates([u("Thinking", 1, "thinking"), u("Thinking", 2, "thinking")]);

        expect(deduped).toHaveLength(2);
    });

    test("returns an empty list unchanged", () => {
        expect(dedupeConsecutiveUpdates([])).toEqual([]);
    });
});
