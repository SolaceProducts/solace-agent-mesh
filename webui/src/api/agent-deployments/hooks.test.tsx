// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { agentKeys } from "../agents/keys";

import { useAgentDeploymentMutation, useDeploymentRunner } from "./hooks";
import { agentDeploymentKeys } from "./keys";

let postSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    postSpy = vi.spyOn(api.platform, "post");
});

afterEach(() => {
    postSpy.mockRestore();
});

function buildClient(): QueryClient {
    return new QueryClient({
        defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
        },
    });
}

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function deploymentReply(fields: { id: string; agentId: string; status: string; failure?: { reason: string; message: string; errorId: string | null } }) {
    const deployment = { action: "deploy", failure: null, createdBy: "u", createdTime: null, updatedTime: null, ...fields };
    return new Response(JSON.stringify({ data: deployment }), { status: 201 });
}

describe("useAgentDeploymentMutation", () => {
    test("invalidates agents and agentDeployments query keys on success", async () => {
        postSpy.mockResolvedValue(deploymentReply({ id: "dep-1", agentId: "agent-1", status: "success" }));
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useAgentDeploymentMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await result.current.mutateAsync({ agentId: "agent-1", action: "deploy" });
        });

        await waitFor(() => expect(result.current.isSuccess).toBe(true));

        expect(postSpy).toHaveBeenCalledWith("/api/v2/platform/agentDeployments", { agentId: "agent-1", action: "deploy" }, expect.anything());
        expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: agentKeys.all });
        expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: agentDeploymentKeys.all });
    });

    test("does not invalidate caches when the mutation fails (HTTP error)", async () => {
        postSpy.mockRejectedValue(new Error("boom"));
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useAgentDeploymentMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await expect(result.current.mutateAsync({ agentId: "agent-1", action: "undeploy" })).rejects.toThrow("boom");
        });

        expect(invalidateSpy).not.toHaveBeenCalled();
    });

    test("treats 2xx with status: 'failed' as a mutation error", async () => {
        postSpy.mockResolvedValue(deploymentReply({ id: "dep-2", agentId: "agent-2", status: "failed", failure: { reason: "awe_timeout", message: "AWE did not acknowledge", errorId: null } }));
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useAgentDeploymentMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await expect(result.current.mutateAsync({ agentId: "agent-2", action: "deploy" })).rejects.toThrow("AWE did not acknowledge");
        });

        expect(invalidateSpy).not.toHaveBeenCalled();
    });
});

describe("useDeploymentRunner", () => {
    const addNotification = vi.fn();

    beforeEach(() => {
        addNotification.mockClear();
    });

    test("returns true and fires onSuccess when status:'success'", async () => {
        const apiCall = vi.fn().mockResolvedValue({ status: "success" });
        const { result } = renderHook(() => useDeploymentRunner<"deploy">(apiCall, { resourceLabel: "test", addNotification }));
        const onSuccess = vi.fn();
        let ok = false;
        await act(async () => {
            ok = await result.current.run({ agentId: "a-1", action: "deploy", onSuccess });
        });
        expect(ok).toBe(true);
        expect(onSuccess).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(result.current.error).toBeNull());
    });

    test("returns false and surfaces a warning toast when status:'failed'", async () => {
        const apiCall = vi.fn().mockResolvedValue({
            status: "failed",
            errorMessage: "AWE not responding",
        });
        const { result } = renderHook(() => useDeploymentRunner<"deploy">(apiCall, { resourceLabel: "test", addNotification }));
        const onSuccess = vi.fn();
        let ok = true;
        await act(async () => {
            ok = await result.current.run({ agentId: "a-2", action: "deploy", onSuccess });
        });
        expect(ok).toBe(false);
        expect(onSuccess).not.toHaveBeenCalled();
        await waitFor(() => expect(result.current.error).toBe("AWE not responding"));
        expect(addNotification).toHaveBeenCalledWith("Deployment failed: AWE not responding", "warning");
    });

    test("transport error → resourceLabel-specific fallback message", async () => {
        const apiCall = vi.fn().mockRejectedValue(new Error("boom"));
        const { result } = renderHook(() => useDeploymentRunner<"undeploy">(apiCall, { resourceLabel: "remote agent deployment", addNotification }));
        await act(async () => {
            await result.current.run({ agentId: "a-3", action: "undeploy" });
        });
        await waitFor(() => expect(result.current.error).toBe("boom"));
        expect(addNotification).toHaveBeenCalledWith("Undeployment failed: boom", "warning");
    });

    test("non-Error rejection uses the resourceLabel fallback string", async () => {
        const apiCall = vi.fn().mockRejectedValue("opaque-string-error");
        const { result } = renderHook(() => useDeploymentRunner<"deploy">(apiCall, { resourceLabel: "agent deployment", addNotification }));
        await act(async () => {
            await result.current.run({ agentId: "a-4", action: "deploy" });
        });
        await waitFor(() => expect(result.current.error).toBe("Failed to update agent deployment."));
    });

    test("setError lets callers clear the error from outside", async () => {
        const apiCall = vi.fn().mockRejectedValue(new Error("first failure"));
        const { result } = renderHook(() => useDeploymentRunner<"deploy">(apiCall, { resourceLabel: "test", addNotification }));
        await act(async () => {
            await result.current.run({ agentId: "a-5", action: "deploy" });
        });
        await waitFor(() => expect(result.current.error).toBe("first failure"));
        act(() => {
            result.current.setError(null);
        });
        await waitFor(() => expect(result.current.error).toBeNull());
    });
});
