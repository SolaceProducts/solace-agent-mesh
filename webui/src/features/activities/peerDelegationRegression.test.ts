// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { A2AEventSSEPayload, TaskFE } from "@/types";

import fixture from "./__fixtures__/peerDelegationRegression.fixture.json";
import { processSteps } from "./flowChart/utils/layoutEngine";
import type { LayoutNode } from "./flowChart/utils/types";
import { processTaskForVisualization } from "./taskVisualizerProcessor";

const PARENT_ID = "019e954d-f81e-7a15-8d0e-688748fb4d27";
const SUB_ID = "019e954e-5df3-79c1-b570-8014d23570fe";

const EXPECTED = { agents: 3, chartTools: 4, pythonTools: 2, totalTools: 6 };

type BackfillEntry = { taskId: string; events: A2AEventSSEPayload[] };

function buildMonitoredTasks(entries: BackfillEntry[]): Record<string, TaskFE> {
    const tasks: Record<string, TaskFE> = {};
    for (const entry of entries) {
        const events = entry.events ?? [];
        tasks[entry.taskId] = {
            taskId: entry.taskId,
            initialRequestText: "test",
            events,
            firstSeen: new Date(events[0]?.timestamp ?? 0),
            lastUpdated: new Date(events[events.length - 1]?.timestamp ?? 0),
        };
    }
    return tasks;
}

function flatten(nodes: LayoutNode[], out: LayoutNode[] = []): LayoutNode[] {
    for (const n of nodes) {
        out.push(n);
        if (n.children?.length) flatten(n.children, out);
        for (const branch of n.parallelBranches ?? []) flatten(branch, out);
    }
    return out;
}

const labelOf = (n: LayoutNode): string => (n.data as any)?.label ?? "";

function counts(nodes: LayoutNode[]): { agents: number; chartTools: number; pythonTools: number; totalTools: number } {
    const all = flatten(nodes);
    const tools = all.filter(n => n.type === "tool");
    return {
        agents: all.filter(n => n.type === "agent").length,
        chartTools: tools.filter(n => labelOf(n).includes("create_chart")).length,
        pythonTools: tools.filter(n => labelOf(n) === "execute_python_code").length,
        totalTools: tools.length,
    };
}

function renderCounts(entries: BackfillEntry[]): ReturnType<typeof counts> {
    const monitored = buildMonitoredTasks(entries);
    const viz = processTaskForVisualization(monitored[PARENT_ID].events, monitored, monitored[PARENT_ID]);
    expect(viz).not.toBeNull();
    return counts(processSteps(viz!.steps).nodes);
}

const allEntries = (fixture as { data: BackfillEntry[] }).data;
const parentOnly = allEntries.filter(e => e.taskId === PARENT_ID);

describe("ChatSidePanel activity graph — peer/tool delegation (rc task 019e954d)", () => {
    test("BACKFILL (parent + sub-task both monitored): exact delegation subtree", () => {
        expect(renderCounts(allEntries)).toEqual(EXPECTED);
    });

    test("LIVE (only parent monitored): subtree reconstructed from forwarded events", () => {
        expect(renderCounts(parentOnly)).toEqual(EXPECTED);
    });

    test("LIVE-PARTIAL (sub-task monitored, request event missing): subtree repaired", () => {
        const entries = allEntries.map(e => (e.taskId === SUB_ID ? { ...e, events: e.events.filter(ev => ev.direction !== "request") } : e));
        expect(renderCounts(entries)).toEqual(EXPECTED);
    });

    test("all delivery states produce an identical graph", () => {
        const partial = allEntries.map(e => (e.taskId === SUB_ID ? { ...e, events: e.events.filter(ev => ev.direction !== "request") } : e));
        const backfill = renderCounts(allEntries);
        expect(renderCounts(parentOnly)).toEqual(backfill);
        expect(renderCounts(partial)).toEqual(backfill);
    });

    test("malformed forwarded events (no original_peer_event_taskId) degrade gracefully", () => {
        const stripped = parentOnly.map(e => ({
            ...e,
            events: e.events.map(ev => {
                const meta = (ev.full_payload as any)?.result?.status?.message?.metadata;
                if (meta?.forwarded_from_peer) {
                    const clone = JSON.parse(JSON.stringify(ev));
                    delete clone.full_payload.result.status.message.metadata.original_peer_event_taskId;
                    return clone as A2AEventSSEPayload;
                }
                return ev;
            }),
        }));
        const monitored = buildMonitoredTasks(stripped);
        const viz = processTaskForVisualization(monitored[PARENT_ID].events, monitored, monitored[PARENT_ID]);
        expect(viz).not.toBeNull();
        const c = counts(processSteps(viz!.steps).nodes);
        expect(c.agents).toBe(1);
        expect(c.chartTools).toBe(0);
    });
});
