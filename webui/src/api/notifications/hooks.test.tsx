// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { onlineManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, type Mock, vi } from "vitest";

import { useNotificationMatrix, useSetNotificationCategoryPreference, useSetNotificationPreference, useTurnOffChannel, useUpdateNotificationMatrix } from "./hooks";
import { notificationKeys } from "./keys";
import type { NotificationMatrix } from "./types";

const mockGet = vi.fn();
const mockPut = vi.fn();
const mockPost = vi.fn();
vi.mock("@/api/client", () => ({
    api: { webui: { get: (...args: unknown[]) => mockGet(...args), put: (...args: unknown[]) => mockPut(...args), post: (...args: unknown[]) => mockPost(...args) } },
}));

let queryClient: QueryClient;
let invalidate: ReturnType<typeof vi.spyOn>;

function wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });
    invalidate = vi.spyOn(queryClient, "invalidateQueries");
    mockGet.mockReset();
    mockPut.mockReset();
    mockPut.mockResolvedValue({});
    mockPost.mockReset();
    mockPost.mockResolvedValue({});
});

afterEach(() => queryClient.clear());

describe("notification preference mutations", () => {
    it("refreshes the settings matrix when a category is turned off", async () => {
        const { result } = renderHook(() => useSetNotificationCategoryPreference(), { wrapper });

        await act(async () => result.current.mutateAsync({ category: "scheduled_task", enabled: false }));

        await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.categoryPreferences() }));
        expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.matrix() });
    });

    it("refreshes the settings matrix when a channel preference changes", async () => {
        const { result } = renderHook(() => useSetNotificationPreference(), { wrapper });

        await act(async () => result.current.mutateAsync({ channelType: "webhook", enabled: false }));

        await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.preferences() }));
        expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.matrix() });
    });

    it("leaves the settings matrix alone when the change fails", async () => {
        mockPut.mockRejectedValue(new Error("boom"));
        const { result } = renderHook(() => useSetNotificationCategoryPreference(), { wrapper });

        await act(async () => {
            await result.current.mutateAsync({ category: "scheduled_task", enabled: false }).catch(() => undefined);
        });

        expect(invalidate).not.toHaveBeenCalledWith({ queryKey: notificationKeys.matrix() });
    });
});

describe("useTurnOffChannel", () => {
    it("turns the channel off and refreshes the settings matrix and the channel list", async () => {
        const { result } = renderHook(() => useTurnOffChannel(), { wrapper });

        await act(async () => result.current.mutateAsync("slack"));

        expect(mockPost).toHaveBeenCalledWith("/api/v1/notify/channels/slack/disable");
        await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.channels() }));
        expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.matrix() });
    });

    it("leaves the settings matrix alone when turning the channel off fails", async () => {
        mockPost.mockRejectedValue(new Error("boom"));
        const { result } = renderHook(() => useTurnOffChannel(), { wrapper });

        await act(async () => {
            await result.current.mutateAsync("slack").catch(() => undefined);
        });

        await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.channels() }));
        expect(invalidate).not.toHaveBeenCalledWith({ queryKey: notificationKeys.matrix() });
    });
});

function statusError(status: number | undefined) {
    return Object.assign(new Error("failed"), { status });
}

describe("useNotificationMatrix", () => {
    const loaded = { categories: [], channels: [], cells: [] };

    it("loads the matrix", async () => {
        mockGet.mockResolvedValue(loaded);
        const { result } = renderHook(() => useNotificationMatrix(), { wrapper });

        await waitFor(() => expect(result.current.data).toEqual(loaded));
        expect(mockGet).toHaveBeenCalledWith("/api/v1/notify/preferences/matrix");
    });

    it.each([400, 404, 503])("does not retry a %s", async status => {
        mockGet.mockRejectedValue(statusError(status));
        const { result } = renderHook(() => useNotificationMatrix(), { wrapper });

        await waitFor(() => expect(result.current.isError).toBe(true));
        expect(mockGet).toHaveBeenCalledTimes(1);
    });

    it.each([408, 429, 500, undefined])("retries a %s up to the attempt limit", async status => {
        mockGet.mockRejectedValue(statusError(status));
        const { result } = renderHook(() => useNotificationMatrix(), { wrapper });

        await waitFor(() => expect(result.current.isError).toBe(true));
        expect(mockGet).toHaveBeenCalledTimes(4);
    });
});

function deferred<T>() {
    let resolve: (value: T) => void = () => undefined;
    let reject: (error: Error) => void = () => undefined;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return { promise, resolve, reject };
}

describe("saving the notification matrix", () => {
    const web = { channelType: "web", destinationKey: "", destination: "" };
    const cell = (category: string, enabled: boolean, extra: Partial<NotificationMatrix["cells"][number]> = {}) => ({ category, channelType: "web", destinationKey: "", enabled, locked: false, ...extra });
    const categories = ["scheduled_task", "agent_message", "chat_share"].map(category => ({ category, label: category, group: "Agents and Tasks", description: "" }));
    const three = (on: [boolean, boolean, boolean]): NotificationMatrix => ({ categories, channels: [web], cells: categories.map((c, i) => cell(c.category, on[i])) });
    const change = (category: string, enabled: boolean) => [{ category, channelType: "web", destinationKey: "", enabled }];

    const mount = () => renderHook(() => ({ matrix: useNotificationMatrix(), update: useUpdateNotificationMatrix({ onSaveError }) }), { wrapper });
    const shown = (result: { current: { matrix: { data?: NotificationMatrix } } }) => result.current.matrix.data?.cells.map(c => c.enabled);
    const cached = () => queryClient.getQueryData<NotificationMatrix>(notificationKeys.matrix());
    const matrixInvalidations = () => invalidate.mock.calls.filter((call: unknown[]) => JSON.stringify((call[0] as { queryKey: unknown }).queryKey) === JSON.stringify(notificationKeys.matrix())).length;
    let onSaveError: Mock<(error: Error) => void>;

    beforeEach(() => {
        onSaveError = vi.fn<(error: Error) => void>();
        mockGet.mockResolvedValue(three([true, true, true]));
    });

    it("shows a change before its save returns, without touching what the service held", async () => {
        const put = deferred<NotificationMatrix>();
        mockPut.mockReturnValue(put.promise);
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toEqual([true, true, true]));

        act(() => result.current.update.mutate(change("scheduled_task", false)));

        await waitFor(() => expect(shown(result)).toEqual([false, true, true]));
        expect(cached()?.cells.map(c => c.enabled)).toEqual([true, true, true]);
        put.resolve(three([false, true, true]));
    });

    it("never shows a locked cell as off", async () => {
        mockGet.mockResolvedValue({ categories, channels: [web], cells: [cell("scheduled_task", true), cell("agent_message", true, { locked: true }), cell("chat_share", true)] });
        mockPut.mockReturnValue(new Promise(() => undefined));
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toEqual([true, true, true]));

        act(() => result.current.update.mutate(change("agent_message", false)));

        await waitFor(() => expect(mockPut).toHaveBeenCalled());
        expect(shown(result)).toEqual([true, true, true]);
    });

    it("changes only the cell that matches in category, channel type and destination", async () => {
        const at = (channelType: string, destinationKey: string) => cell("scheduled_task", true, { channelType, destinationKey });
        mockGet.mockResolvedValue({ categories, channels: [web], cells: [at("webhook", "wh_1"), at("webhook", "wh_2"), at("slack", "wh_2"), at("web", "")] });
        mockPut.mockReturnValue(new Promise(() => undefined));
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toHaveLength(4));

        act(() => result.current.update.mutate([{ category: "scheduled_task", channelType: "webhook", destinationKey: "wh_2", enabled: false }]));

        await waitFor(() => expect(shown(result)).toEqual([true, false, true, true]));
    });

    it("caches the matrix a save returns, and refreshes the settings it supersedes", async () => {
        queryClient.setQueryData(notificationKeys.matrix(), three([true, true, true]));
        const returned = three([false, true, false]);
        mockPut.mockResolvedValue(returned);
        const { result } = renderHook(() => useUpdateNotificationMatrix(), { wrapper });

        await act(async () => result.current.mutateAsync(change("scheduled_task", false)));

        expect(cached()).toEqual(returned);
        expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.categoryPreferences() });
        expect(invalidate).toHaveBeenCalledWith({ queryKey: notificationKeys.preferences() });
        expect(matrixInvalidations()).toBe(1);
    });

    it("drops a failed change at once, and reports it", async () => {
        mockPut.mockRejectedValue(new Error("boom"));
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toEqual([true, true, true]));

        await act(async () => {
            await result.current.update.mutateAsync(change("scheduled_task", false)).catch(() => undefined);
        });

        expect(shown(result)).toEqual([true, true, true]);
        expect(onSaveError).toHaveBeenCalledTimes(1);
    });

    it("sends overlapping saves one at a time, in order, and reads the matrix again only after the last", async () => {
        const first = deferred<NotificationMatrix>();
        const second = deferred<NotificationMatrix>();
        mockPut.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toEqual([true, true, true]));

        act(() => {
            result.current.update.mutate(change("scheduled_task", false));
            result.current.update.mutate(change("scheduled_task", true));
        });
        await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(1));
        expect(shown(result)).toEqual([true, true, true]);

        await act(async () => first.resolve(three([false, true, true])));
        await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(2));
        expect(matrixInvalidations()).toBe(0);

        await act(async () => second.resolve(three([true, true, true])));
        await waitFor(() => expect(matrixInvalidations()).toBe(1));
        expect(mockPut.mock.calls.map(call => (call[1] as { cells: Array<{ enabled: boolean }> }).cells[0].enabled)).toEqual([false, true]);
    });

    it("shows what the service holds when an earlier save fails and a later one succeeds, even if reading it again fails", async () => {
        const a = deferred<NotificationMatrix>();
        const b = deferred<NotificationMatrix>();
        mockPut.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toEqual([true, true, true]));
        mockGet.mockRejectedValue(statusError(400));

        act(() => {
            result.current.update.mutate(change("scheduled_task", false));
            result.current.update.mutate(change("agent_message", false));
        });
        await waitFor(() => expect(shown(result)).toEqual([false, false, true]));
        await act(async () => a.reject(new Error("a")));
        await waitFor(() => expect(shown(result)).toEqual([true, false, true]));
        await act(async () => b.resolve(three([true, false, true])));

        await waitFor(() => expect(result.current.matrix.isError).toBe(true));
        expect(shown(result)).toEqual([true, false, true]);
        expect(onSaveError).toHaveBeenCalledTimes(1);
    });

    it("shows what the service holds when every save fails, even if reading it again fails", async () => {
        const a = deferred<NotificationMatrix>();
        const b = deferred<NotificationMatrix>();
        mockPut.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toEqual([true, true, true]));
        mockGet.mockRejectedValue(statusError(400));

        act(() => {
            result.current.update.mutate(change("scheduled_task", false));
            result.current.update.mutate(change("agent_message", false));
        });
        await act(async () => a.reject(new Error("first")));
        await act(async () => b.reject(new Error("second")));

        await waitFor(() => expect(result.current.matrix.isError).toBe(true));
        expect(shown(result)).toEqual([true, true, true]);
        expect(onSaveError.mock.calls.map(([error]) => (error as Error).message)).toEqual(["first", "second"]);
    });

    it("keeps a saved change on screen when a read that started before the save lands while a later save waits", async () => {
        const a = deferred<NotificationMatrix>();
        const b = deferred<NotificationMatrix>();
        mockPut.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toEqual([true, true, true]));
        const staleRead = deferred<NotificationMatrix>();
        mockGet.mockReturnValueOnce(staleRead.promise);
        act(() => {
            void queryClient.refetchQueries({ queryKey: notificationKeys.matrix() });
        });
        await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(2));

        act(() => {
            result.current.update.mutate(change("scheduled_task", false));
            result.current.update.mutate(change("agent_message", false));
        });
        await act(async () => a.resolve(three([false, true, true])));
        await act(async () => staleRead.resolve(three([true, true, true])));

        await waitFor(() => expect(result.current.matrix.isFetching).toBe(false));
        expect(shown(result)).toEqual([false, false, true]);
        await act(async () => b.resolve(three([false, false, true])));
    });

    it("fails and reports a save made while the network is down, instead of leaving it waiting", async () => {
        mockPut.mockRejectedValue(new Error("offline"));
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toEqual([true, true, true]));
        onlineManager.setOnline(false);
        try {
            act(() => result.current.update.mutate(change("scheduled_task", false)));

            await waitFor(() => expect(onSaveError).toHaveBeenCalledTimes(1));
            expect(mockPut).toHaveBeenCalledTimes(1);
            expect(shown(result)).toEqual([true, true, true]);
        } finally {
            onlineManager.setOnline(true);
        }
    });

    it("gives each save its own 15 second limit, and a save that runs out of time fails and releases the one behind it", async () => {
        const stalled = new AbortController();
        const timeout = vi.spyOn(AbortSignal, "timeout").mockReturnValueOnce(stalled.signal);
        try {
            mockPut
                .mockImplementationOnce((_path: string, _body: unknown, options: { signal: AbortSignal }) => new Promise((_resolve, reject) => options.signal.addEventListener("abort", () => reject(options.signal.reason))))
                .mockResolvedValueOnce(three([true, false, true]));
            const { result } = mount();
            await waitFor(() => expect(shown(result)).toEqual([true, true, true]));

            act(() => {
                result.current.update.mutate(change("scheduled_task", false));
                result.current.update.mutate(change("agent_message", false));
            });
            await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(1));
            expect(timeout).toHaveBeenCalledWith(15_000);
            expect((mockPut.mock.calls[0][2] as { signal: AbortSignal }).signal).toBe(stalled.signal);

            act(() => stalled.abort(new DOMException("signal timed out", "TimeoutError")));

            await waitFor(() => expect(onSaveError).toHaveBeenCalledTimes(1));
            expect(onSaveError.mock.calls[0][0].name).toBe("TimeoutError");
            await waitFor(() => expect(mockPut).toHaveBeenCalledTimes(2));
            expect((mockPut.mock.calls[1][2] as { signal: AbortSignal }).signal).not.toBe(stalled.signal);
        } finally {
            timeout.mockRestore();
        }
    });

    it("keeps the changes in flight on screen when a read that started earlier lands while they save", async () => {
        const put = deferred<NotificationMatrix>();
        mockPut.mockReturnValue(put.promise);
        const { result } = mount();
        await waitFor(() => expect(shown(result)).toEqual([true, true, true]));
        const read = deferred<NotificationMatrix>();
        mockGet.mockReturnValueOnce(read.promise);

        act(() => {
            void queryClient.refetchQueries({ queryKey: notificationKeys.matrix() });
        });
        await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(2));
        act(() => result.current.update.mutate(change("scheduled_task", false)));
        await waitFor(() => expect(shown(result)).toEqual([false, true, true]));
        await act(async () => read.resolve(three([true, true, true])));

        await waitFor(() => expect(result.current.matrix.isFetching).toBe(false));
        expect(shown(result)).toEqual([false, true, true]);
        put.resolve(three([false, true, true]));
    });
});
