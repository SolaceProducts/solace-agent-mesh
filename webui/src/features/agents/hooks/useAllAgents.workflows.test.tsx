// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

const query = <T,>(data: T) => ({ data, isLoading: false, isError: false, error: null });

interface DiscoveredCardFixture {
    name: string;
    isWorkflow?: boolean;
}

function mockHooks(discoveredCards: DiscoveredCardFixture[], userAgentNames: string[] = []) {
    vi.doMock("@/api/agents", async importOriginal => ({
        ...(await importOriginal<typeof import("@/api/agents")>()),
        useAgents: () => query(userAgentNames.map(name => ({ name, isEphemeral: false }))),
    }));
    vi.doMock("@/api/remote-agents", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/remote-agents")>()), useRemoteAgents: () => query([]) }));
    vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => query(discoveredCards) }));
    vi.doMock("@/api/toolsets", () => ({ useBuiltinToolsets: () => query([]), useCustomToolsets: () => query([]) }));
    vi.doMock("@/api/connectors", () => ({ useConnectors: () => query([]) }));
}

async function runUseAllAgents(discoveredCards: DiscoveredCardFixture[], includeWorkflows?: boolean, userAgentNames: string[] = []) {
    mockHooks(discoveredCards, userAgentNames);
    const { useAllAgents } = await import("./useAllAgents");
    const { result } = renderHook(() => useAllAgents(includeWorkflows === undefined ? undefined : { includeWorkflows }));
    return result.current.agentCards;
}

describe("useAllAgents includeWorkflows", () => {
    beforeEach(() => vi.resetModules());

    test("drops discovered workflow cards by default", async () => {
        const cards = await runUseAllAgents([
            { name: "SomeAgent", isWorkflow: false },
            { name: "SomeWorkflow", isWorkflow: true },
        ]);
        expect(cards.map(c => c.name)).toEqual(["SomeAgent"]);
    });

    test("drops discovered workflow cards when includeWorkflows is explicitly false", async () => {
        const cards = await runUseAllAgents([{ name: "SomeWorkflow", isWorkflow: true }], false);
        expect(cards).toHaveLength(0);
    });

    test("keeps discovered workflow cards when includeWorkflows is true", async () => {
        const cards = await runUseAllAgents(
            [
                { name: "SomeAgent", isWorkflow: false },
                { name: "SomeWorkflow", isWorkflow: true },
            ],
            true
        );
        expect(cards.map(c => c.name).sort()).toEqual(["SomeAgent", "SomeWorkflow"]);
    });

    test("a DB-backed agent still wins over a same-named discovered workflow card regardless of the flag", async () => {
        const cards = await runUseAllAgents([{ name: "SomeName", isWorkflow: true }], true, ["SomeName"]);
        expect(cards).toHaveLength(1);
        expect(cards[0].isWorkflow).toBeFalsy();
    });
});
