// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

describe("useAgentOptions", () => {
    let useAgentOptions: typeof import("@/features/entrypoints/hooks/useAgentOptions").useAgentOptions;
    let mockAgents: { name: string; instanceName?: string; isEphemeral?: boolean }[];
    let mockRemoteAgents: { name: string; instanceName?: string }[];
    let mockCards: { name: string; displayName?: string; isWorkflow?: boolean; isEphemeral?: boolean }[];

    beforeEach(async () => {
        vi.resetModules();

        mockAgents = [];
        mockRemoteAgents = [];
        mockCards = [];

        vi.doMock("@/api/agents", () => ({
            useAgents: () => ({ data: mockAgents }),
        }));
        vi.doMock("@/api/remote-agents", () => ({
            useRemoteAgents: () => ({ data: mockRemoteAgents }),
        }));
        vi.doMock("@/api/agent-cards", () => ({
            useAgentCards: () => ({ data: mockCards }),
        }));

        const mod = await import("@/features/entrypoints/hooks/useAgentOptions");
        useAgentOptions = mod.useAgentOptions;
    });

    test("returns empty list when no agents are registered — no synthetic Orchestrator fallback", () => {
        const { result } = renderHook(() => useAgentOptions());
        expect(result.current).toEqual([]);
    });

    test("includes DB-backed agents", () => {
        mockAgents = [{ name: "Orchestrator" }, { name: "Builder" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([
            { name: "Builder", displayName: "Builder" },
            { name: "Orchestrator", displayName: "Orchestrator" },
        ]);
    });

    test("DB agents with instanceName !== name use the broker name as target", () => {
        mockAgents = [{ name: "My Custom Agent", instanceName: "agent_019e_uuid" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([{ name: "agent_019e_uuid", displayName: "My Custom Agent" }]);
    });

    test("DB agents without instanceName fall back to name on both fields", () => {
        mockAgents = [{ name: "Orchestrator" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([{ name: "Orchestrator", displayName: "Orchestrator" }]);
    });

    test("DB agent dedupes against discovered card sharing the broker instanceName", () => {
        mockAgents = [{ name: "My Custom Agent", instanceName: "agent_019e_uuid" }];
        mockCards = [{ name: "agent_019e_uuid", displayName: "From Discovery" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([{ name: "agent_019e_uuid", displayName: "My Custom Agent" }]);
    });

    test("skips ephemeral DB agents", () => {
        mockAgents = [{ name: "Orchestrator" }, { name: "EphemeralTest", isEphemeral: true }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current.map(o => o.name)).toEqual(["Orchestrator"]);
    });

    test("remote agents publish under instanceName, displayed by name", () => {
        mockRemoteAgents = [{ name: "Friendly Label", instanceName: "broker-side-id" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([{ name: "broker-side-id", displayName: "Friendly Label" }]);
    });

    test("remote agents without instanceName fall back to name on both fields", () => {
        mockRemoteAgents = [{ name: "Standalone" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([{ name: "Standalone", displayName: "Standalone" }]);
    });

    test("includes broker-discovered cards and prefers card displayName when set", () => {
        mockCards = [{ name: "discovered-1", displayName: "Pretty Name" }, { name: "discovered-2" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([
            { name: "discovered-2", displayName: "discovered-2" },
            { name: "discovered-1", displayName: "Pretty Name" },
        ]);
    });

    test("filters out workflow and ephemeral discovered cards", () => {
        mockCards = [{ name: "Real" }, { name: "WorkflowA", isWorkflow: true }, { name: "TestWrapper", isEphemeral: true }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current.map(o => o.name)).toEqual(["Real"]);
    });

    test("dedupes by broker name across sources, DB winning over discovery", () => {
        mockAgents = [{ name: "Orchestrator" }];
        mockCards = [{ name: "Orchestrator", displayName: "From Discovery" }, { name: "OnlyDiscovered" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([
            { name: "OnlyDiscovered", displayName: "OnlyDiscovered" },
            { name: "Orchestrator", displayName: "Orchestrator" },
        ]);
    });

    test("DB agent wins over a remote agent sharing the same broker name", () => {
        mockAgents = [{ name: "shared-id" }];
        mockRemoteAgents = [{ name: "Friendly", instanceName: "shared-id" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([{ name: "shared-id", displayName: "shared-id" }]);
    });

    test("remote takes precedence over discovery on the same broker name", () => {
        mockRemoteAgents = [{ name: "Friendly", instanceName: "shared-id" }];
        mockCards = [{ name: "shared-id", displayName: "From Discovery" }];

        const { result } = renderHook(() => useAgentOptions());

        expect(result.current).toEqual([{ name: "shared-id", displayName: "Friendly" }]);
    });

    test("returns a referentially stable list across re-renders while all sources are loading", () => {
        mockAgents = undefined as never;
        mockRemoteAgents = undefined as never;
        mockCards = undefined as never;

        const { result, rerender } = renderHook(() => useAgentOptions());
        const first = result.current;
        rerender();
        rerender();

        expect(result.current).toBe(first);
        expect(result.current).toEqual([]);
    });

    test("stays referentially stable when a slower source is still loading", () => {
        mockAgents = [{ name: "Orchestrator" }];
        mockRemoteAgents = undefined as never;
        mockCards = undefined as never;

        const { result, rerender } = renderHook(() => useAgentOptions());
        const first = result.current;
        rerender();

        expect(result.current).toBe(first);
        expect(result.current).toEqual([{ name: "Orchestrator", displayName: "Orchestrator" }]);
    });
});
