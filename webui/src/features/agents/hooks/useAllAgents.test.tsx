// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { RemoteAgent } from "@/types";

const remoteAgent = (overrides: Partial<RemoteAgent>): RemoteAgent =>
    ({
        id: "ra-1",
        name: "RemoteAgent",
        agentUrl: "https://example.com/agent",
        description: "desc",
        agentAuthType: "none",
        createdBy: "user@example.com",
        updatedBy: "system",
        createdAt: "2026-01-29T10:00:00.000000+00:00",
        updatedAt: "2026-01-29T10:00:00.000000+00:00",
        ...overrides,
    }) as RemoteAgent;

const query = <T,>(data: T) => ({ data, isLoading: false, isError: false, error: null });

function mockHooks(remoteAgents: RemoteAgent[], discoveredNames: string[] = []) {
    vi.doMock("@/api/agents", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/agents")>()), useAgents: () => query([]) }));
    vi.doMock("@/api/remote-agents", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/remote-agents")>()), useRemoteAgents: () => query(remoteAgents) }));
    vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => query(discoveredNames.map(name => ({ name }))) }));
    vi.doMock("@/api/toolsets", () => ({ useBuiltinToolsets: () => query([]), useCustomToolsets: () => query([]) }));
    vi.doMock("@/api/connectors", () => ({ useConnectors: () => query([]) }));
}

async function runUseAllAgents(remoteAgents: RemoteAgent[], discoveredNames: string[] = []) {
    mockHooks(remoteAgents, discoveredNames);
    const { useAllAgents } = await import("./useAllAgents");
    const { result } = renderHook(() => useAllAgents());
    return result.current.agentCards;
}

describe("useAllAgents remote runtime status", () => {
    beforeEach(() => vi.resetModules());

    test("no-card deployed agent with no live heartbeat stays 'deployed' (Registered), never disconnected/stopped", async () => {
        const cards = await runUseAllAgents([remoteAgent({ agentCardLocation: "none", deploymentStatus: "deployed", runtimeStatus: { status: "disconnected" } })]);
        expect(cards.find(c => c.name === "RemoteAgent")?.status).toBe("deployed");
    });

    test("card-having agent reporting running but absent from discovery is downgraded to 'disconnected'", async () => {
        const cards = await runUseAllAgents([remoteAgent({ agentCardLocation: "well_known", deploymentStatus: "deployed", runtimeStatus: { status: "running" } })], []);
        expect(cards.find(c => c.name === "RemoteAgent")?.status).toBe("disconnected");
    });

    test("card-having agent present in discovery (live heartbeat) stays 'deployed'", async () => {
        const cards = await runUseAllAgents([remoteAgent({ agentCardLocation: "well_known", deploymentStatus: "deployed", runtimeStatus: { status: "running" } })], ["RemoteAgent"]);
        expect(cards.find(c => c.name === "RemoteAgent")?.status).toBe("deployed");
    });

    test("no-card agent stays 'deployed' even when absent from discovery — the downgrade guard exempts it", async () => {
        const cards = await runUseAllAgents([remoteAgent({ agentCardLocation: "none", deploymentStatus: "deployed", runtimeStatus: { status: "running" } })], []);
        expect(cards.find(c => c.name === "RemoteAgent")?.status).toBe("deployed");
    });

    test("card-having agent reporting degraded but absent from discovery is downgraded to 'disconnected', problems cleared", async () => {
        const problems = [{ id: "queue:x", severity: "degraded" as const, detail: "boom", since: "" }];
        const cards = await runUseAllAgents([remoteAgent({ agentCardLocation: "well_known", deploymentStatus: "deployed", runtimeStatus: { status: "degraded", problems } })], []);
        const card = cards.find(c => c.name === "RemoteAgent");
        expect(card?.status).toBe("disconnected");
        expect(card?.runtimeProblems).toBeUndefined();
    });

    test("card-having agent reporting degraded and present in discovery stays 'degraded' with its problems intact", async () => {
        const problems = [{ id: "queue:x", severity: "degraded" as const, detail: "boom", since: "" }];
        const cards = await runUseAllAgents([remoteAgent({ agentCardLocation: "well_known", deploymentStatus: "deployed", runtimeStatus: { status: "degraded", problems } })], ["RemoteAgent"]);
        const card = cards.find(c => c.name === "RemoteAgent");
        expect(card?.status).toBe("degraded");
        expect(card?.runtimeProblems).toEqual(problems);
    });
});
