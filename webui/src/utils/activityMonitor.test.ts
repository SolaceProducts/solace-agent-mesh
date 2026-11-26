// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { AgentCardInfo } from "@/types";

import { ACTIVITY_MONITOR_DISPLAY_NAME, canActivityMonitorReadTask, hasTenantOrTraceReach, resolveActivityMonitor } from "./activityMonitor";

const card = (over: Partial<AgentCardInfo>): AgentCardInfo => ({ name: "x", description: "", url: "", ...over }) as AgentCardInfo;

describe("resolveActivityMonitor", () => {
    it("matches on displayName and hands back the card carrying the wire name", () => {
        const found = resolveActivityMonitor([card({ name: "a7f3c1e2-9b4d", displayName: ACTIVITY_MONITOR_DISPLAY_NAME })]);
        expect(found?.name).toBe("a7f3c1e2-9b4d");
    });

    it("matches on the wire name for a card built without going through transformAgentCard", () => {
        const found = resolveActivityMonitor([card({ name: ACTIVITY_MONITOR_DISPLAY_NAME })]);
        expect(found?.name).toBe(ACTIVITY_MONITOR_DISPLAY_NAME);
    });

    it("returns undefined when no Activity Monitor is present", () => {
        expect(resolveActivityMonitor([card({ name: "Orchestrator" }), card({ name: "b2", displayName: "Sentinel" })])).toBeUndefined();
    });

    it("returns undefined for an empty or missing list", () => {
        expect(resolveActivityMonitor([])).toBeUndefined();
        expect(resolveActivityMonitor(undefined)).toBeUndefined();
    });

    it("does not match an agent whose name merely contains the words", () => {
        expect(resolveActivityMonitor([card({ name: "c3", displayName: "Activity Monitor (staging)" })])).toBeUndefined();
    });
});

describe("canActivityMonitorReadTask", () => {
    it("lets the task's owner read it without any activity grant", () => {
        expect(canActivityMonitorReadTask("u1", "u1", [])).toBe(true);
    });

    it("lets a tenant-wide activity reader read another user's task", () => {
        expect(canActivityMonitorReadTask("u1", "someone-else", ["activity:*:read"])).toBe(true);
    });

    it("lets a trace reader hand off another user's task, since the server opens it by the rule the Activities list uses", () => {
        expect(canActivityMonitorReadTask("u1", "someone-else", ["agent:a1:read_trace"])).toBe(true);
        expect(canActivityMonitorReadTask("u1", "someone-else", ["agent:*:read_trace"])).toBe(true);
    });

    it("does not accept a partial-glob trace grant, since the server grants nothing for one", () => {
        expect(canActivityMonitorReadTask("u1", "someone-else", ["a*:*:read_trace"])).toBe(false);
    });

    it("does not accept a partial-glob activity grant as tenant-wide read", () => {
        expect(canActivityMonitorReadTask("u1", "someone-else", ["activity:**:read"])).toBe(false);
    });

    it("refuses when the caller's username is unknown and no grant is held", () => {
        expect(canActivityMonitorReadTask(undefined, undefined, [])).toBe(false);
        expect(canActivityMonitorReadTask(undefined, null, [])).toBe(false);
    });
});

describe("hasTenantOrTraceReach", () => {
    it("is true for a tenant-wide activity reader or any trace reach", () => {
        expect(hasTenantOrTraceReach(["activity:*:read"])).toBe(true);
        expect(hasTenantOrTraceReach(["agent:a1:read_trace"])).toBe(true);
    });

    it("is false without either, and for a partial glob", () => {
        expect(hasTenantOrTraceReach([])).toBe(false);
        expect(hasTenantOrTraceReach(["agent:*:invoke"])).toBe(false);
        expect(hasTenantOrTraceReach(["a*:*:read_trace"])).toBe(false);
    });
});
