// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

const h = vi.hoisted(() => ({
    mutateAsync: vi.fn<(...args: unknown[]) => Promise<unknown>>(),
    isPending: false,
    addNotification: vi.fn(),
}));

vi.mock("@/api/workflows", () => ({
    useWorkflowDeploymentMutation: () => ({ mutateAsync: h.mutateAsync, isPending: h.isPending }),
}));

vi.mock("@/providers/contexts", () => ({
    useChatContext: () => ({ addNotification: h.addNotification }),
}));

import { useWorkflowDeployment } from "./useWorkflowDeployment";

describe("useWorkflowDeployment", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        h.isPending = false;
        h.mutateAsync.mockResolvedValue({ status: "success" });
    });

    test("rejects when the deployment mutation rejects", async () => {
        h.mutateAsync.mockRejectedValue(new Error("deployer did not ACK"));
        const { result } = renderHook(() => useWorkflowDeployment());

        await expect(result.current.deploy({ workflowId: "wf-1", action: "deploy" })).rejects.toThrow("deployer did not ACK");
    });

    test("still surfaces the failure to the user before rethrowing", async () => {
        h.mutateAsync.mockRejectedValue(new Error("deployer did not ACK"));
        const { result } = renderHook(() => useWorkflowDeployment());

        await expect(result.current.deploy({ workflowId: "wf-1", action: "deploy" })).rejects.toThrow();
        expect(h.addNotification).toHaveBeenCalledWith(expect.stringContaining("deployer did not ACK"), "warning");
    });

    test("resolves and runs onSuccess when the deployment succeeds", async () => {
        const onSuccess = vi.fn();
        const { result } = renderHook(() => useWorkflowDeployment());

        await expect(result.current.deploy({ workflowId: "wf-1", action: "deploy", onSuccess })).resolves.toBeUndefined();
        expect(onSuccess).toHaveBeenCalledTimes(1);
        expect(h.addNotification).toHaveBeenCalledWith(expect.stringContaining("deployment started"), "info");
    });

    test("rejects rather than reporting success when a deploy is already in flight", async () => {
        h.isPending = true;
        const { result } = renderHook(() => useWorkflowDeployment());

        await expect(result.current.deploy({ workflowId: "wf-1", action: "deploy" })).rejects.toThrow(/already in progress/);
        expect(h.mutateAsync).not.toHaveBeenCalled();
    });
});
