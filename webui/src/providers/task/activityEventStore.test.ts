// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { A2AEventSSEPayload, TaskFE } from "@/types";

import { applyActivityEventFrame, upsertActivityEvent } from "./activityEventStore";

const evt = (over: Partial<A2AEventSSEPayload>): A2AEventSSEPayload =>
    ({
        event_type: "a2a_message",
        timestamp: "2026-01-01T00:00:00.000Z",
        solace_topic: "ns/a2a/v1/agent/status/Agent/t1",
        direction: "status_update",
        source_entity: "Agent",
        target_entity: "",
        full_payload: {},
        task_id: "t1",
        ...over,
    }) as A2AEventSSEPayload;

const requestEvt = (taskId: string, text: string): A2AEventSSEPayload =>
    evt({
        task_id: taskId,
        direction: "request",
        full_payload: { method: "message/send", params: { message: { parts: [{ kind: "text", text }] } } },
    });

describe("upsertActivityEvent", () => {
    it("creates a task entry on the first event for a task_id", () => {
        const out = upsertActivityEvent({}, evt({ task_id: "t1" }));
        expect(Object.keys(out)).toEqual(["t1"]);
        expect(out.t1.events).toHaveLength(1);
        expect(out.t1.taskId).toBe("t1");
    });

    it("appends and sorts by timestamp", () => {
        let tasks: Record<string, TaskFE> = {};
        tasks = upsertActivityEvent(tasks, evt({ timestamp: "2026-01-01T00:00:02.000Z" }));
        tasks = upsertActivityEvent(tasks, evt({ timestamp: "2026-01-01T00:00:01.000Z" }));
        const ts = tasks.t1.events.map(e => e.timestamp);
        expect(ts).toEqual(["2026-01-01T00:00:01.000Z", "2026-01-01T00:00:02.000Z"]);
    });

    it("ignores events with no task_id", () => {
        const out = upsertActivityEvent({}, evt({ task_id: null }));
        expect(out).toEqual({});
    });

    it("captures the contextId as sessionId from result.contextId", () => {
        const out = upsertActivityEvent({}, evt({ full_payload: { result: { contextId: "sess-1" } } }));
        expect(out.t1.sessionId).toBe("sess-1");
    });

    it("captures the contextId as sessionId from params.message.contextId", () => {
        const out = upsertActivityEvent({}, evt({ full_payload: { params: { message: { contextId: "sess-2" } } } }));
        expect(out.t1.sessionId).toBe("sess-2");
    });

    it("captures initialRequestText from a request frame (stripping the gateway prefix)", () => {
        const out = upsertActivityEvent({}, requestEvt("t1", "Request received by gateway at: 2026-01-01\nhello there"));
        expect(out.t1.initialRequestText).toBe("hello there");
    });

    it("fills initialRequestText from a later request frame if the first event lacked it", () => {
        let tasks = upsertActivityEvent({}, evt({ task_id: "t1", direction: "status_update" }));
        expect(tasks.t1.initialRequestText).toBe("");
        tasks = upsertActivityEvent(tasks, requestEvt("t1", "the prompt"));
        expect(tasks.t1.initialRequestText).toBe("the prompt");
    });

    it("does not mutate the input map", () => {
        const input: Record<string, TaskFE> = {};
        const out = upsertActivityEvent(input, evt({}));
        expect(input).toEqual({});
        expect(out).not.toBe(input);
    });

    it("applyActivityEventFrame parses and merges a raw frame", () => {
        const out = applyActivityEventFrame({}, JSON.stringify(evt({ task_id: "t9" })));
        expect(out.t9?.events).toHaveLength(1);
    });

    it("applyActivityEventFrame drops a malformed frame, leaving the store unchanged", () => {
        const input: Record<string, TaskFE> = {};
        const out = applyActivityEventFrame(input, "not json");
        expect(out).toBe(input);
    });

    it("caps the store, dropping the least-recently-updated tasks", () => {
        let tasks: Record<string, TaskFE> = {};
        for (let i = 0; i < 60; i++) {
            const ts = new Date(2026, 0, 1, 0, 0, i).toISOString();
            tasks = upsertActivityEvent(tasks, evt({ task_id: `t${i}`, timestamp: ts }));
        }
        expect(Object.keys(tasks)).toHaveLength(50);
        expect(tasks.t0).toBeUndefined();
        expect(tasks.t59).toBeDefined();
    });
});
