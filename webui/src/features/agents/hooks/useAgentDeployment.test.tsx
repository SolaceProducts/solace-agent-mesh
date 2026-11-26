// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Agent } from "@/types";

const addNotification = vi.fn();
const userAgentsRefetch = vi.fn().mockResolvedValue(undefined);
const yamlAgentCardsRefetch = vi.fn().mockResolvedValue(undefined);

const RECORD_PATH = "/api/v2/platform/agentDeployments/d-1";

const json = (body: unknown, status: number, headers: Record<string, string> = {}) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });

const record = (status: "success" | "failed", message?: string) => ({
    data: { id: "d-1", type: "agentDeployment", agentId: "a-1", status, action: "deploy", failure: message ? { reason: "runtime_error", message, errorId: null } : null, createdBy: "u", createdTime: null, updatedTime: null },
});

describe("useAgentDeployment", () => {
    let api: typeof import("@/api/client").api;
    let ChatContext: typeof import("@/providers/contexts/ChatContext").ChatContext;
    let useAgentDeployment: typeof import("./useAgentDeployment").useAgentDeployment;
    let postSpy: ReturnType<typeof vi.spyOn>;
    let getSpy: ReturnType<typeof vi.spyOn>;
    const deploySucceeds = () => postSpy.mockResolvedValue(json(record("success"), 201));
    const deployFails = (message?: string) => {
        postSpy.mockResolvedValue(json({ message: "Deployment failed", errorId: "e-1" }, 502, { Location: RECORD_PATH }));
        getSpy.mockResolvedValue(json(record("failed", message), 200));
    };
    let wrapper: ({ children }: { children: ReactNode }) => ReactNode;

    beforeEach(async () => {
        vi.resetModules();

        addNotification.mockClear();
        userAgentsRefetch.mockClear();
        yamlAgentCardsRefetch.mockClear();

        vi.doMock("@/api/agents", () => ({
            useAgents: () => ({ refetch: userAgentsRefetch }),
        }));
        vi.doMock("@/api/agent-cards", () => ({
            useAgentCards: () => ({ refetch: yamlAgentCardsRefetch }),
        }));

        const clientMod = await import("@/api/client");
        const chatCtxMod = await import("@/providers/contexts/ChatContext");
        const hookMod = await import("./useAgentDeployment");
        api = clientMod.api;
        ChatContext = chatCtxMod.ChatContext;
        useAgentDeployment = hookMod.useAgentDeployment;

        postSpy = vi.spyOn(api.platform, "post");
        getSpy = vi.spyOn(api.platform, "get");

        wrapper = ({ children }: { children: ReactNode }) => {
            const value = { addNotification } as unknown as React.ContextType<typeof ChatContext>;
            return <ChatContext.Provider value={value}>{children}</ChatContext.Provider>;
        };
    });

    afterEach(() => {
        postSpy.mockRestore();
        getSpy.mockRestore();
    });

    describe("deploy", () => {
        test("treats a successful deploy as success and runs onSuccess", async () => {
            deploySucceeds();
            const onSuccess = vi.fn();

            const { result } = renderHook(() => useAgentDeployment(), { wrapper });

            let ok = false;
            await act(async () => {
                ok = await result.current.deploy({ agentId: "a-1", action: "deploy", onSuccess });
            });

            expect(ok).toBe(true);
            expect(onSuccess).toHaveBeenCalledTimes(1);
            await waitFor(() => expect(result.current.error).toBeNull());
            expect(addNotification).not.toHaveBeenCalledWith(expect.stringContaining("failed"), expect.any(String));
        });

        test("treats a failed deployment record as a failure", async () => {
            deployFails("AWE did not ACK");
            const onSuccess = vi.fn();

            const { result } = renderHook(() => useAgentDeployment(), { wrapper });

            let ok = true;
            await act(async () => {
                ok = await result.current.deploy({ agentId: "a-2", action: "deploy", onSuccess });
            });

            expect(ok).toBe(false);
            expect(onSuccess).not.toHaveBeenCalled();
            await waitFor(() => expect(result.current.error).toBe("AWE did not ACK"));
            expect(addNotification).toHaveBeenCalledWith("Deployment failed: AWE did not ACK", "warning");
        });

        test("falls back to a generic message when the record carries no failure message", async () => {
            deployFails();

            const { result } = renderHook(() => useAgentDeployment(), { wrapper });

            await act(async () => {
                await result.current.deploy({ agentId: "a-3", action: "undeploy" });
            });

            await waitFor(() => expect(result.current.error).toBe("Undeployment reported status: failed"));
            expect(addNotification).toHaveBeenCalledWith("Undeployment failed: Undeployment reported status: failed", "warning");
        });

        test("surfaces transport errors as a notification", async () => {
            postSpy.mockRejectedValue(new Error("boom"));

            const { result } = renderHook(() => useAgentDeployment(), { wrapper });

            let ok = true;
            await act(async () => {
                ok = await result.current.deploy({ agentId: "a-4", action: "deploy" });
            });

            expect(ok).toBe(false);
            await waitFor(() => expect(result.current.error).toBe("boom"));
            expect(addNotification).toHaveBeenCalledWith("Deployment failed: boom", "warning");
        });
    });

    describe("handleDeploy", () => {
        test("returns false for an agent without an id", async () => {
            const { result } = renderHook(() => useAgentDeployment(), { wrapper });
            const ok = await result.current.handleDeploy({} as Agent, "deploy");
            expect(ok).toBe(false);
            expect(postSpy).not.toHaveBeenCalled();
        });

        test("on success, refetches both registries and emits a success toast", async () => {
            deploySucceeds();

            const { result } = renderHook(() => useAgentDeployment(), { wrapper });

            let ok = false;
            await act(async () => {
                ok = await result.current.handleDeploy({ id: "a-6" } as Agent, "deploy");
            });

            expect(ok).toBe(true);
            await waitFor(() => {
                expect(userAgentsRefetch).toHaveBeenCalled();
                expect(yamlAgentCardsRefetch).toHaveBeenCalled();
            });
            expect(addNotification).toHaveBeenCalledWith("Deployed successfully.", "success");
        });

        test("on a failed deployment record, does NOT emit a success toast", async () => {
            deployFails("snapshot build error");

            const { result } = renderHook(() => useAgentDeployment(), { wrapper });

            let ok = true;
            await act(async () => {
                ok = await result.current.handleDeploy({ id: "a-7" } as Agent, "deploy");
            });

            expect(ok).toBe(false);
            await waitFor(() => expect(result.current.error).toBe("snapshot build error"));
            expect(addNotification).not.toHaveBeenCalledWith("Deployed successfully.", "success");
            expect(addNotification).toHaveBeenCalledWith("Deployment failed: snapshot build error", "warning");
        });
    });
});
