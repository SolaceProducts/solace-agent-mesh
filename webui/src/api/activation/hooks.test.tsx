// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { useActivation, useFreeModelStatus, useSignIn } from "./hooks";
import { activationKeys } from "./keys";
import { rereadFreeModelAfterReply } from "./reread";
import * as activationService from "./service";
import type { Activation, FreeModelStatus } from "./types";

const agreements = { eulaClickwrapId: "eula-1" };

function status(signingIn: boolean): Activation {
    return { state: "signed_out", signingIn, agreements, freeModelAvailable: true, freeModelChosen: false };
}

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useSignIn", () => {
    afterEach(() => vi.restoreAllMocks());

    test("a status read still in flight cannot replace the sign-in's answer", async () => {
        let answerStaleRead: (value: Activation) => void = () => {};
        const getActivation = vi
            .spyOn(activationService, "getActivation")
            .mockResolvedValueOnce(status(false))
            .mockImplementationOnce(() => new Promise(resolve => (answerStaleRead = resolve)));
        vi.spyOn(activationService, "signIn").mockResolvedValue(status(true));
        const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });

        const { result } = renderHook(() => ({ activation: useActivation(true), signIn: useSignIn() }), { wrapper: wrapper(client) });
        await waitFor(() => expect(result.current.activation.data?.signingIn).toBe(false));

        act(() => {
            void result.current.activation.refetch();
        });
        await waitFor(() => expect(getActivation).toHaveBeenCalledTimes(2));
        await act(async () => {
            await result.current.signIn.mutateAsync();
        });
        await act(async () => {
            answerStaleRead(status(false));
            await new Promise(resolve => setTimeout(resolve, 0));
        });

        expect(client.getQueryData<Activation>(activationKeys.status())?.signingIn).toBe(true);
        await waitFor(() => expect(result.current.activation.data?.signingIn).toBe(true));
    });
});

describe("rereadFreeModelAfterReply", () => {
    afterEach(() => vi.useRealTimers());

    test("reads the credits again once LiteLLM has written the reply's spend", () => {
        vi.useFakeTimers();
        const client = new QueryClient();
        const invalidate = vi.spyOn(client, "invalidateQueries").mockResolvedValue();

        rereadFreeModelAfterReply(client);
        expect(invalidate).toHaveBeenCalledTimes(1);
        expect(invalidate).toHaveBeenLastCalledWith({ queryKey: activationKeys.freeModel() });

        vi.advanceTimersByTime(9_999);
        expect(invalidate).toHaveBeenCalledTimes(1);
        vi.advanceTimersByTime(1);
        expect(invalidate).toHaveBeenCalledTimes(2);
        expect(invalidate).toHaveBeenLastCalledWith({ queryKey: activationKeys.freeModel() });

        vi.advanceTimersByTime(49_999);
        expect(invalidate).toHaveBeenCalledTimes(2);
        vi.advanceTimersByTime(1);
        expect(invalidate).toHaveBeenCalledTimes(3);
        expect(invalidate).toHaveBeenLastCalledWith({ queryKey: activationKeys.freeModel() });
    });
});

describe("useFreeModelStatus", () => {
    afterEach(() => {
        vi.useRealTimers();
        vi.restoreAllMocks();
    });

    test("reads the credits again every five minutes while the free model is in use", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const available: FreeModelStatus = { status: "available", remainingPercent: 80, models: [] };
        const getFreeModel = vi.spyOn(activationService, "getFreeModel").mockResolvedValue(available);
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

        renderHook(() => useFreeModelStatus(true), { wrapper: wrapper(client) });
        await waitFor(() => expect(getFreeModel).toHaveBeenCalledTimes(1));

        await act(async () => {
            await vi.advanceTimersByTimeAsync(4 * 60_000);
        });
        expect(getFreeModel).toHaveBeenCalledTimes(1);
        await act(async () => {
            await vi.advanceTimersByTimeAsync(60_000);
        });
        expect(getFreeModel).toHaveBeenCalledTimes(2);
    });

    test("reads the credits again when a reader that asks to read on open mounts, even while the last reading is fresh", async () => {
        const available: FreeModelStatus = { status: "available", remainingPercent: 80, models: [] };
        const getFreeModel = vi.spyOn(activationService, "getFreeModel").mockResolvedValue(available);
        const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 5 * 60_000 } } });

        renderHook(() => useFreeModelStatus(true), { wrapper: wrapper(client) });
        await waitFor(() => expect(getFreeModel).toHaveBeenCalledTimes(1));

        renderHook(() => useFreeModelStatus(true), { wrapper: wrapper(client) });
        await waitFor(() => expect(client.isFetching()).toBe(0));
        expect(getFreeModel).toHaveBeenCalledTimes(1);

        renderHook(() => useFreeModelStatus(true, { readOnOpen: true }), { wrapper: wrapper(client) });
        await waitFor(() => expect(getFreeModel).toHaveBeenCalledTimes(2));
    });
});
