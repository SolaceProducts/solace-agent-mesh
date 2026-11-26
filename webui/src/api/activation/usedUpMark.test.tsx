// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { useFreeModelStatus, useRefreshFreeModel } from "./hooks";
import { activationKeys } from "./keys";
import * as activationService from "./service";
import type { FreeModelStatus } from "./types";
import { markFreeCreditsUsedUp } from "./usedUpMark";

function available(remainingPercent: number | null): FreeModelStatus {
    return { status: "available", remainingPercent, models: [] };
}

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function renderStatus(firstAnswer: FreeModelStatus) {
    const getFreeModel = vi.spyOn(activationService, "getFreeModel").mockResolvedValue(firstAnswer);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const view = renderHook(() => ({ status: useFreeModelStatus(true), refresh: useRefreshFreeModel() }), { wrapper: wrapper(client) });
    const readAgain = async (answer: FreeModelStatus) => {
        getFreeModel.mockResolvedValue(answer);
        let read: FreeModelStatus | undefined;
        await act(async () => {
            read = (await view.result.current.status.refetch()).data;
        });
        return read;
    };
    return { client, view, readAgain };
}

describe("the used-up mark", () => {
    beforeEach(() => localStorage.clear());
    afterEach(() => {
        vi.restoreAllMocks();
        localStorage.clear();
    });

    test("a refused chat shows the credits used up until the budget is raised", async () => {
        const { client, view, readAgain } = renderStatus(available(13));
        await waitFor(() => expect(view.result.current.status.data?.remainingPercent).toBe(13));

        await act(() => markFreeCreditsUsedUp(client));
        await waitFor(() => expect(view.result.current.status.data?.remainingPercent).toBe(0));

        expect((await readAgain(available(11.5)))?.remainingPercent).toBe(0);
        expect((await readAgain(available(13)))?.remainingPercent).toBe(0);
        expect((await readAgain(available(60)))?.remainingPercent).toBe(60);
        expect((await readAgain(available(13)))?.remainingPercent).toBe(13);
    });

    test("holds through a pause or an expired sign-in", async () => {
        const { client, view, readAgain } = renderStatus(available(13));
        await waitFor(() => expect(view.result.current.status.data?.remainingPercent).toBe(13));
        await act(() => markFreeCreditsUsedUp(client));

        expect(await readAgain({ status: "reauthorization_required", remainingPercent: null, models: [] })).toMatchObject({ status: "reauthorization_required", remainingPercent: null });
        expect((await readAgain(available(13)))?.remainingPercent).toBe(0);
    });

    test("a second refusal keeps the usage the first one recorded", async () => {
        const { client, view, readAgain } = renderStatus(available(13));
        await waitFor(() => expect(view.result.current.status.data?.remainingPercent).toBe(13));
        await act(() => markFreeCreditsUsedUp(client));
        await act(() => markFreeCreditsUsedUp(client));

        expect((await readAgain(available(12)))?.remainingPercent).toBe(0);
    });

    test("an allowance with no limit clears the mark", async () => {
        const { client, view, readAgain } = renderStatus(available(13));
        await waitFor(() => expect(view.result.current.status.data?.remainingPercent).toBe(13));
        await act(() => markFreeCreditsUsedUp(client));

        expect((await readAgain(available(null)))?.remainingPercent).toBeNull();
        expect((await readAgain(available(13)))?.remainingPercent).toBe(13);
    });

    test("the credits refresh keeps the mark", async () => {
        const { client, view } = renderStatus(available(13));
        await waitFor(() => expect(view.result.current.status.data?.remainingPercent).toBe(13));
        await act(() => markFreeCreditsUsedUp(client));
        vi.spyOn(activationService, "refreshFreeModel").mockResolvedValue(available(12));

        await act(async () => {
            await view.result.current.refresh.mutateAsync();
        });

        expect(client.getQueryData<FreeModelStatus>(activationKeys.freeModel())?.remainingPercent).toBe(0);
    });

    test("the mark comes from the allowance at the refusal, not the last poll", async () => {
        const { client, view, readAgain } = renderStatus(available(99));
        await waitFor(() => expect(view.result.current.status.data?.remainingPercent).toBe(99));
        vi.mocked(activationService.getFreeModel).mockResolvedValue(available(40));

        await act(() => markFreeCreditsUsedUp(client));

        expect(client.getQueryData<FreeModelStatus>(activationKeys.freeModel())?.remainingPercent).toBe(0);
        expect((await readAgain(available(40)))?.remainingPercent).toBe(0);
        expect((await readAgain(available(53.7)))?.remainingPercent).toBe(53.7);
    });

    test("keeps the last poll's mark when the allowance can't be read at the refusal", async () => {
        const { client, view, readAgain } = renderStatus(available(13));
        await waitFor(() => expect(view.result.current.status.data?.remainingPercent).toBe(13));
        vi.mocked(activationService.getFreeModel).mockRejectedValue(new Error("offline"));

        await act(() => markFreeCreditsUsedUp(client));

        expect(client.getQueryData<FreeModelStatus>(activationKeys.freeModel())?.remainingPercent).toBe(0);
        expect((await readAgain(available(12)))?.remainingPercent).toBe(0);
        expect((await readAgain(available(14)))?.remainingPercent).toBe(14);
    });

    test("sets nothing before the credits have been read", async () => {
        const client = new QueryClient();

        await markFreeCreditsUsedUp(client);

        expect(localStorage.length).toBe(0);
    });
});
