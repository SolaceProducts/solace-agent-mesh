// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { RemoteAgent } from "@/types";

const addNotification = vi.fn();
const remoteAgentsRefetch = vi.fn().mockResolvedValue(undefined);
const deployRemoteAgentMock = vi.fn();

describe("useRemoteAgentDeployment", () => {
    let ChatContext: typeof import("@/providers/contexts/ChatContext").ChatContext;
    let useRemoteAgentDeployment: typeof import("./useRemoteAgentDeployment").useRemoteAgentDeployment;
    let wrapper: ({ children }: { children: ReactNode }) => ReactNode;

    beforeEach(async () => {
        vi.resetModules();

        addNotification.mockClear();
        remoteAgentsRefetch.mockClear();
        deployRemoteAgentMock.mockReset();

        vi.doMock("@/api/remote-agents", () => ({
            remoteAgentsApi: { deploy: deployRemoteAgentMock },
            useRemoteAgents: () => ({ refetch: remoteAgentsRefetch }),
        }));

        const chatCtxMod = await import("@/providers/contexts/ChatContext");
        const hookMod = await import("./useRemoteAgentDeployment");
        ChatContext = chatCtxMod.ChatContext;
        useRemoteAgentDeployment = hookMod.useRemoteAgentDeployment;

        wrapper = ({ children }: { children: ReactNode }) => {
            const value = { addNotification } as unknown as React.ContextType<typeof ChatContext>;
            return <ChatContext.Provider value={value}>{children}</ChatContext.Provider>;
        };
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    describe("deployRemoteAgent", () => {
        test("treats 2xx + status:'success' as success", async () => {
            deployRemoteAgentMock.mockResolvedValue({
                id: "d-1",
                agentId: "ra-1",
                status: "success",
                action: "deploy",
            });
            const onSuccess = vi.fn();

            const { result } = renderHook(() => useRemoteAgentDeployment(), { wrapper });

            let ok = false;
            await act(async () => {
                ok = await result.current.deployRemoteAgent({
                    agentId: "ra-1",
                    action: "deploy",
                    onSuccess,
                });
            });

            expect(ok).toBe(true);
            expect(onSuccess).toHaveBeenCalledTimes(1);
            await waitFor(() => expect(result.current.error).toBeNull());
        });

        test("treats 2xx + status:'failed' as a failure (Go sync-deploy contract)", async () => {
            deployRemoteAgentMock.mockResolvedValue({
                id: "d-2",
                agentId: "ra-2",
                status: "failed",
                action: "deploy",
                errorMessage: "AWE timeout",
            });
            const onSuccess = vi.fn();

            const { result } = renderHook(() => useRemoteAgentDeployment(), { wrapper });

            let ok = true;
            await act(async () => {
                ok = await result.current.deployRemoteAgent({
                    agentId: "ra-2",
                    action: "deploy",
                    onSuccess,
                });
            });

            expect(ok).toBe(false);
            expect(onSuccess).not.toHaveBeenCalled();
            await waitFor(() => expect(result.current.error).toBe("AWE timeout"));
            expect(addNotification).toHaveBeenCalledWith("Deployment failed: AWE timeout", "warning");
        });

        test("surfaces transport errors as a warning notification", async () => {
            deployRemoteAgentMock.mockRejectedValue(new Error("network down"));

            const { result } = renderHook(() => useRemoteAgentDeployment(), { wrapper });

            let ok = true;
            await act(async () => {
                ok = await result.current.deployRemoteAgent({
                    agentId: "ra-3",
                    action: "undeploy",
                });
            });

            expect(ok).toBe(false);
            await waitFor(() => expect(result.current.error).toBe("network down"));
            expect(addNotification).toHaveBeenCalledWith("Undeployment failed: network down", "warning");
        });
    });

    describe("handleDeployRemoteAgent", () => {
        test("returns false when the agent has no id", async () => {
            const { result } = renderHook(() => useRemoteAgentDeployment(), { wrapper });
            const ok = await result.current.handleDeployRemoteAgent({} as RemoteAgent, "deploy");
            expect(ok).toBe(false);
            expect(deployRemoteAgentMock).not.toHaveBeenCalled();
        });

        test("on success refetches remote-agents and shows a success toast", async () => {
            deployRemoteAgentMock.mockResolvedValue({
                id: "d-4",
                agentId: "ra-4",
                status: "success",
                action: "deploy",
            });

            const { result } = renderHook(() => useRemoteAgentDeployment(), { wrapper });

            let ok = false;
            await act(async () => {
                ok = await result.current.handleDeployRemoteAgent({ id: "ra-4" } as RemoteAgent, "deploy");
            });

            expect(ok).toBe(true);
            await waitFor(() => expect(remoteAgentsRefetch).toHaveBeenCalled());
            expect(addNotification).toHaveBeenCalledWith("Deployed successfully.", "success");
        });

        test("on 2xx + failed, no success toast fires (the bug we fixed)", async () => {
            deployRemoteAgentMock.mockResolvedValue({
                id: "d-5",
                agentId: "ra-5",
                status: "failed",
                action: "deploy",
                errorMessage: "broker queue full",
            });

            const { result } = renderHook(() => useRemoteAgentDeployment(), { wrapper });

            let ok = true;
            await act(async () => {
                ok = await result.current.handleDeployRemoteAgent({ id: "ra-5" } as RemoteAgent, "deploy");
            });

            expect(ok).toBe(false);
            expect(addNotification).not.toHaveBeenCalledWith("Deployed successfully.", "success");
            expect(addNotification).toHaveBeenCalledWith("Deployment failed: broker queue full", "warning");
        });
    });
});
