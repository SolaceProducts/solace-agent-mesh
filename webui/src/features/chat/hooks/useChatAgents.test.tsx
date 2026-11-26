// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQueryClient } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { agentCardKeys, useAgentCards } from "@/api/agent-cards";
import { remoteAgentKeys, useRemoteAgentCards } from "@/api/remote-agents";

import { useChatAgents } from "./useChatAgents";

vi.mock("@/api/agent-cards", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/agent-cards")>()), useAgentCards: vi.fn() }));
vi.mock("@/api/remote-agents", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/remote-agents")>()), useRemoteAgentCards: vi.fn() }));
vi.mock("@tanstack/react-query", () => ({ useQueryClient: vi.fn() }));

const mockYaml = (isLoading: boolean, isFetching = false, isError = false) => (useAgentCards as unknown as ReturnType<typeof vi.fn>).mockReturnValue({ isLoading, isFetching, isError });
const mockRemote = (isLoading: boolean, isFetching = false, isError = false) => (useRemoteAgentCards as unknown as ReturnType<typeof vi.fn>).mockReturnValue({ isLoading, isFetching, isError });
const invalidateQueries = vi.fn();

describe("useChatAgents", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        (useQueryClient as unknown as ReturnType<typeof vi.fn>).mockReturnValue({ invalidateQueries });
    });

    it("is loading while the yaml (discovery) query is loading", () => {
        mockYaml(true);
        mockRemote(false);
        const { result } = renderHook(() => useChatAgents());
        expect(result.current.isLoading).toBe(true);
    });

    it("is loading while the remote-agents query is loading", () => {
        mockYaml(false);
        mockRemote(true);
        const { result } = renderHook(() => useChatAgents());
        expect(result.current.isLoading).toBe(true);
    });

    it("is idle (not loading / refetching / error) once both sources have resolved cleanly", () => {
        mockYaml(false);
        mockRemote(false);
        const { result } = renderHook(() => useChatAgents());
        expect(result.current.isLoading).toBe(false);
        expect(result.current.isRefetching).toBe(false);
        expect(result.current.isError).toBe(false);
    });

    it("is refetching when the yaml source is refetching in the background", () => {
        mockYaml(false, true);
        mockRemote(false, false);
        const { result } = renderHook(() => useChatAgents());
        expect(result.current.isRefetching).toBe(true);
    });

    it("is refetching when the remote source is refetching in the background", () => {
        mockYaml(false, false);
        mockRemote(false, true);
        const { result } = renderHook(() => useChatAgents());
        expect(result.current.isRefetching).toBe(true);
    });

    it("is in error when the yaml source failed to load", () => {
        mockYaml(false, false, true);
        mockRemote(false, false, false);
        const { result } = renderHook(() => useChatAgents());
        expect(result.current.isError).toBe(true);
    });

    it("is in error when the remote source failed to load", () => {
        mockYaml(false, false, false);
        mockRemote(false, false, true);
        const { result } = renderHook(() => useChatAgents());
        expect(result.current.isError).toBe(true);
    });

    it("invalidates both agent-card query lists on refetch", () => {
        mockYaml(false);
        mockRemote(false);
        const { result } = renderHook(() => useChatAgents());
        result.current.refetch();
        expect(invalidateQueries).toHaveBeenCalledTimes(2);
        expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: agentCardKeys.lists() });
        expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: remoteAgentKeys.lists() });
    });
});
