// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { Agent, RuntimeStatusValue } from "@/types";

import { awaitingToolRegistrations, describeToolSourceFailures, hasSessionAgent, toolSourceFailures } from "./toolSourceFailures";

const agentComponent = { name: "Order Helper", type: "agent", origin: "session" };
const connectors = [{ id: "conn-1", name: "Orders MCP" }];

const platformAgent = (overrides: Partial<Agent>): Agent => ({ name: "Order Helper", description: "", ...overrides }) as Agent;
const failed = { toolType: "mcp", connectorId: "conn-1", status: "failed" as const, toolCount: 0 };
const ready = { toolType: "mcp", connectorId: "conn-1", status: "ready" as const, toolCount: 3 };

describe("toolSourceFailures", () => {
    test("names the agent and the connector whose registration failed", () => {
        const agents = [platformAgent({ toolRegistrations: [{ ...failed, error: "refused" }] })];
        expect(toolSourceFailures([agentComponent], agents, connectors)).toEqual([{ agentName: "Order Helper", connectorName: "Orders MCP" }]);
    });

    test("reports nothing when every registration is ready", () => {
        expect(toolSourceFailures([agentComponent], [platformAgent({ toolRegistrations: [ready] })], connectors)).toEqual([]);
    });

    test("matches the platform agent by normalized name", () => {
        const agents = [platformAgent({ name: "order-helper", toolRegistrations: [failed] })];
        expect(toolSourceFailures([agentComponent], agents, connectors)).toHaveLength(1);
    });

    test("matches by the manifest id when the stored name differs from the manifest name", () => {
        const agents = [platformAgent({ id: "p-1", name: "Totally Different", toolRegistrations: [failed] })];
        expect(toolSourceFailures([{ ...agentComponent, id: "p-1" }], agents, connectors)).toHaveLength(1);
    });

    test("prefers the exact-named agent over an earlier look-alike", () => {
        const lookAlike = platformAgent({ id: "p-1", name: "order_helper", toolRegistrations: [failed] });
        const exact = platformAgent({ id: "p-2", name: "Order Helper", toolRegistrations: [ready] });
        expect(toolSourceFailures([agentComponent], [lookAlike, exact], connectors)).toEqual([]);
    });

    test("uses the manifest id to tell apart two agents that normalize to the same name", () => {
        const first = platformAgent({ id: "p-1", name: "order_helper", toolRegistrations: [failed] });
        const second = platformAgent({ id: "p-2", name: "orderhelper", toolRegistrations: [ready] });
        expect(toolSourceFailures([{ ...agentComponent, id: "p-2" }], [first, second], connectors)).toEqual([]);
        expect(toolSourceFailures([{ ...agentComponent, id: "p-1" }], [first, second], connectors)).toHaveLength(1);
    });

    test("ignores another agent's failure and a platform-origin component", () => {
        const other = [platformAgent({ name: "Someone Else", toolRegistrations: [failed] })];
        expect(toolSourceFailures([agentComponent], other, connectors)).toEqual([]);
        const own = [platformAgent({ toolRegistrations: [failed] })];
        expect(toolSourceFailures([{ ...agentComponent, origin: "platform" }], own, connectors)).toEqual([]);
    });

    test("leaves the connector unnamed when it is no longer listed", () => {
        const agents = [platformAgent({ toolRegistrations: [{ ...failed, connectorId: "gone" }] })];
        expect(toolSourceFailures([agentComponent], agents, connectors)).toEqual([{ agentName: "Order Helper", connectorName: undefined }]);
    });

    test("tolerates agents that have not loaded", () => {
        expect(toolSourceFailures([agentComponent], undefined, undefined)).toEqual([]);
        expect(toolSourceFailures(undefined, undefined, undefined)).toEqual([]);
    });
});

describe("awaitingToolRegistrations", () => {
    test.each<[RuntimeStatusValue, boolean]>([
        ["starting", true],
        ["disconnected", true],
        ["running", false],
        ["degraded", false],
        ["stopped", false],
    ])("an agent with runtime status %s -> %s", (status, expected) => {
        const agents = [platformAgent({ runtimeStatus: { status } as Agent["runtimeStatus"] })];
        expect(awaitingToolRegistrations([agentComponent], agents)).toBe(expected);
    });

    test("is false before the platform lists the agent", () => {
        expect(awaitingToolRegistrations([agentComponent], [])).toBe(false);
    });
});

describe("hasSessionAgent", () => {
    test("is true only for a session-origin agent", () => {
        expect(hasSessionAgent([agentComponent])).toBe(true);
        expect(hasSessionAgent([{ ...agentComponent, origin: "platform" }])).toBe(false);
        expect(hasSessionAgent([{ ...agentComponent, type: "skill" }])).toBe(false);
        expect(hasSessionAgent(undefined)).toBe(false);
    });
});

describe("describeToolSourceFailures", () => {
    test("names one agent and connector", () => {
        const text = describeToolSourceFailures([{ agentName: "Order Helper", connectorName: "Orders MCP" }]);
        expect(text).toContain("“Order Helper” could not load tools from the “Orders MCP” connector");
        expect(text).toContain("so it is running without that connector’s tools");
        expect(text).toContain("Check the connector’s configuration, then redeploy");
    });

    test("counts distinct agents and connectors, not failed registrations", () => {
        const oneAgentTwoConnectors = describeToolSourceFailures([
            { agentName: "A", connectorName: "One" },
            { agentName: "A", connectorName: "Two" },
        ]);
        expect(oneAgentTwoConnectors).toContain("so it is running without those connectors’ tools");
        expect(oneAgentTwoConnectors).toContain("Check those connectors’ configuration");

        const twoAgentsOneConnector = describeToolSourceFailures([
            { agentName: "A", connectorName: "One" },
            { agentName: "B", connectorName: "One" },
        ]);
        expect(twoAgentsOneConnector).toContain("so they are running without that connector’s tools");
        expect(twoAgentsOneConnector).toContain("Check the connector’s configuration");
    });

    test("names every affected pair and pluralises the consequence and remedy", () => {
        const text = describeToolSourceFailures([{ agentName: "A", connectorName: "One" }, { agentName: "B" }]);
        expect(text).toContain("“A” could not load tools from the “One” connector");
        expect(text).toContain("“B” could not load tools from a connector");
        expect(text).toContain("so they are running without those connectors’ tools");
        expect(text).toContain("Check those connectors’ configuration, then redeploy");
    });
});
