// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { useGatewayDeployment } from "./useGatewayDeployment";

describe("useGatewayDeployment on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body: unknown, headers?: HeadersInit) => fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(body), { status, headers }));
    const record = (status: string, message?: string) => ({
        data: { id: "d1", entrypointId: "gw-1", action: "deploy", status, failure: message ? { reason: "runtime_error", message, errorId: "e0" } : null, createdBy: "u", createdTime: null, updatedTime: null },
    });

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("runs onSuccess", async () => {
        respond(201, record("success"));
        const onSuccess = vi.fn();

        const { result } = renderHook(() => useGatewayDeployment());
        await act(async () => {
            await result.current.deploy({ gatewayId: "gw-1", action: "deploy", onSuccess });
        });

        expect(onSuccess).toHaveBeenCalledTimes(1);
    });

    test("falls back to a generic error message when the failed record carries none", async () => {
        respond(201, record("failed"));

        const { result } = renderHook(() => useGatewayDeployment());
        await act(async () => {
            await result.current.deploy({ gatewayId: "gw-3", action: "undeploy" });
        });

        await waitFor(() => expect(result.current.error).toBe("Deployment reported status: failed"));
    });

    test("a 502 shows failure.message from the record it points at", async () => {
        respond(502, { message: "Deployment failed", errorId: "e1" }, { Location: "/api/v2/platform/entrypointDeployments/d1" });
        respond(200, record("failed", "The entrypoint runtime did not complete the change."));
        const onSuccess = vi.fn();

        const { result } = renderHook(() => useGatewayDeployment());
        await act(async () => {
            await result.current.deploy({ gatewayId: "gw-1", action: "deploy", onSuccess });
        });

        expect(onSuccess).not.toHaveBeenCalled();
        await waitFor(() => expect(result.current.error).toBe("The entrypoint runtime did not complete the change."));
    });

    test("any other error shows its message", async () => {
        respond(404, { message: "Entrypoint not found", errorId: "e2" });

        const { result } = renderHook(() => useGatewayDeployment());
        await act(async () => {
            await result.current.deploy({ gatewayId: "gw-1", action: "deploy" });
        });

        await waitFor(() => expect(result.current.error).toBe("Entrypoint not found"));
    });

    test("surfaces transport errors via setError", async () => {
        fetchMock.mockRejectedValueOnce(new Error("Network down"));

        const { result } = renderHook(() => useGatewayDeployment());
        await act(async () => {
            await result.current.deploy({ gatewayId: "gw-4", action: "deploy" });
        });

        await waitFor(() => expect(result.current.error).toMatch(/Network down/));
    });
});
