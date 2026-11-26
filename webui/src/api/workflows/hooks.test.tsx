// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { useDeleteWorkflowMutation, useWorkflowDeploymentMutation, useWorkflowsQuery } from "./hooks";
import { workflowKeys } from "./keys";
import { workflowsApi } from "./versioned";

let deploySpy: ReturnType<typeof vi.spyOn>;
let deleteSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    deploySpy = vi.spyOn(workflowsApi, "deploy");
    deleteSpy = vi.spyOn(workflowsApi, "delete");
});

afterEach(() => {
    deploySpy.mockRestore();
    deleteSpy.mockRestore();
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

describe("useWorkflowDeploymentMutation", () => {
    test("invalidates workflow lists + deployments on success", async () => {
        deploySpy.mockResolvedValue({
            id: "wf-d-1",
            workflowId: "wf-1",
            status: "success",
            action: "deploy",
        });
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useWorkflowDeploymentMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await result.current.mutateAsync({ workflowId: "wf-1", action: "deploy" });
        });

        await waitFor(() => expect(result.current.isSuccess).toBe(true));
        expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: workflowKeys.lists() });
        expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: workflowKeys.deployments() });
    });

    test("treats 2xx + status:'failed' as a mutation error and never invalidates the cache", async () => {
        deploySpy.mockResolvedValue({
            id: "wf-d-2",
            workflowId: "wf-2",
            status: "failed",
            action: "deploy",
            errorMessage: "build failed",
        });
        const onError = vi.fn();
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useWorkflowDeploymentMutation({ onError }), {
            wrapper: wrapper(client),
        });

        await act(async () => {
            await expect(result.current.mutateAsync({ workflowId: "wf-2", action: "deploy" })).rejects.toThrow("build failed");
        });

        expect(onError).toHaveBeenCalled();
        expect(invalidateSpy).not.toHaveBeenCalled();
    });

    test("falls back to a generic message when errorMessage is missing", async () => {
        deploySpy.mockResolvedValue({ status: "failed" });
        const client = buildClient();

        const { result } = renderHook(() => useWorkflowDeploymentMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await expect(result.current.mutateAsync({ workflowId: "wf-3", action: "update" })).rejects.toThrow("Workflow deployment reported status: failed");
        });
    });

    test("does not invalidate caches on a transport error", async () => {
        deploySpy.mockRejectedValue(new Error("network down"));
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useWorkflowDeploymentMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await expect(result.current.mutateAsync({ workflowId: "wf-4", action: "deploy" })).rejects.toThrow("network down");
        });
        expect(invalidateSpy).not.toHaveBeenCalled();
    });
});

describe("useDeleteWorkflowMutation", () => {
    test("DELETEs by id and invalidates the workflow lists on success", async () => {
        deleteSpy.mockResolvedValue(undefined);
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useDeleteWorkflowMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await result.current.mutateAsync("wf-42");
        });

        await waitFor(() => expect(result.current.isSuccess).toBe(true));
        expect(deleteSpy).toHaveBeenCalledWith("wf-42", expect.anything());
        expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: workflowKeys.lists() });
    });

    test("does not invalidate caches when the delete fails", async () => {
        deleteSpy.mockRejectedValue(new Error("boom"));
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useDeleteWorkflowMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await expect(result.current.mutateAsync("wf-99")).rejects.toThrow("boom");
        });
        expect(invalidateSpy).not.toHaveBeenCalled();
    });
});

describe("useWorkflowsQuery", () => {
    test("reads the list under its key", async () => {
        const getAll = vi.spyOn(workflowsApi, "getAll").mockResolvedValue([]);
        const client = buildClient();

        const { result } = renderHook(() => useWorkflowsQuery(), { wrapper: wrapper(client) });

        await waitFor(() => expect(result.current.isSuccess).toBe(true));
        expect(getAll).toHaveBeenCalledTimes(1);
        expect(client.getQueryData(workflowKeys.list())).toEqual([]);
        getAll.mockRestore();
    });
});
