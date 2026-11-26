// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Skill } from "@/types";

function buildQueryWrapper() {
    const client = new QueryClient({
        defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

function makeSkill(status: Skill["discoveryStatus"]): Skill {
    return {
        id: "skill-1",
        name: "staging-ops",
        description: "test ops check",
        tags: null,
        origin: "user",
        discoveryStatus: status,
        hasReferences: false,
        hasAssets: false,
        hasTools: false,
        resourceCount: 0,
        referenceCount: 0,
        assetCount: 0,
        agents: [],
        createdBy: "morgan.reed@example.org",
        updatedBy: "morgan.reed@example.org",
        createdAt: "2026-07-06T20:19:29.000Z",
        updatedAt: "2026-07-06T20:19:29.000Z",
    };
}

describe("isSkillProcessing", () => {
    let isSkillProcessing: typeof import("./hooks").isSkillProcessing;

    beforeEach(async () => {
        vi.resetModules();
        ({ isSkillProcessing } = await import("./hooks"));
    });

    test.each([
        ["created", true],
        ["pending", true],
        ["ready", false],
        ["failed", false],
        ["removed", false],
    ] as const)("returns %s -> %s", (status, expected) => {
        expect(isSkillProcessing(status)).toBe(expected);
    });

    test("returns false for undefined", () => {
        expect(isSkillProcessing(undefined)).toBe(false);
    });
});

describe("useSkill polling", () => {
    type AsyncFn = ((...args: unknown[]) => Promise<unknown>) & {
        mockResolvedValue: (v: unknown) => AsyncFn;
        mockResolvedValueOnce: (v: unknown) => AsyncFn;
    };
    let getSkillMock: AsyncFn;
    let useSkill: typeof import("./hooks").useSkill;

    beforeEach(async () => {
        vi.resetModules();
        vi.useFakeTimers();
        getSkillMock = vi.fn() as unknown as AsyncFn;
        vi.doMock("./versioned", async importOriginal => {
            const { skillsApi } = await importOriginal<typeof import("./versioned")>();
            return { skillsApi: { ...skillsApi, get: (...args: unknown[]) => getSkillMock(...args) } };
        });
        ({ useSkill } = await import("./hooks"));
    });

    afterEach(() => {
        vi.useRealTimers();
        vi.doUnmock("./versioned");
    });

    test("keeps polling while the skill is processing and stops once it settles", async () => {
        getSkillMock.mockResolvedValueOnce(makeSkill("pending")).mockResolvedValueOnce(makeSkill("pending")).mockResolvedValue(makeSkill("ready"));

        const { result } = renderHook(() => useSkill("skill-1"), { wrapper: buildQueryWrapper() });

        await vi.waitFor(() => expect(result.current.data?.discoveryStatus).toBe("pending"));
        expect(getSkillMock).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getSkillMock).toHaveBeenCalledTimes(2);

        await vi.advanceTimersByTimeAsync(5_000);
        await vi.waitFor(() => expect(result.current.data?.discoveryStatus).toBe("ready"));
        expect(getSkillMock).toHaveBeenCalledTimes(3);

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getSkillMock).toHaveBeenCalledTimes(3);
    });

    test("does not poll when the skill is already ready", async () => {
        getSkillMock.mockResolvedValue(makeSkill("ready"));

        const { result } = renderHook(() => useSkill("skill-1"), { wrapper: buildQueryWrapper() });

        await vi.waitFor(() => expect(result.current.data?.discoveryStatus).toBe("ready"));
        expect(getSkillMock).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getSkillMock).toHaveBeenCalledTimes(1);
    });
});

describe("useSkills polling", () => {
    type AsyncFn = ((...args: unknown[]) => Promise<unknown>) & {
        mockResolvedValue: (v: unknown) => AsyncFn;
        mockResolvedValueOnce: (v: unknown) => AsyncFn;
    };
    let getSkillsMock: AsyncFn;
    let useSkills: typeof import("./hooks").useSkills;

    beforeEach(async () => {
        vi.resetModules();
        vi.useFakeTimers();
        getSkillsMock = vi.fn() as unknown as AsyncFn;
        vi.doMock("./versioned", async importOriginal => {
            const { skillsApi } = await importOriginal<typeof import("./versioned")>();
            return { skillsApi: { ...skillsApi, getAll: (...args: unknown[]) => getSkillsMock(...args) } };
        });
        ({ useSkills } = await import("./hooks"));
    });

    afterEach(() => {
        vi.useRealTimers();
        vi.doUnmock("./versioned");
    });

    test("keeps polling while any skill in the list is processing and stops once all settle", async () => {
        const pendingList = [makeSkill("ready"), { ...makeSkill("pending"), id: "skill-2" }];
        const settledList = [makeSkill("ready"), { ...makeSkill("ready"), id: "skill-2" }];
        getSkillsMock.mockResolvedValueOnce(pendingList).mockResolvedValueOnce(pendingList).mockResolvedValue(settledList);

        const { result } = renderHook(() => useSkills(), { wrapper: buildQueryWrapper() });

        await vi.waitFor(() => expect(result.current.data).toHaveLength(2));
        expect(getSkillsMock).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getSkillsMock).toHaveBeenCalledTimes(2);

        await vi.advanceTimersByTimeAsync(5_000);
        await vi.waitFor(() => expect(result.current.data?.every(s => s.discoveryStatus === "ready")).toBe(true));
        expect(getSkillsMock).toHaveBeenCalledTimes(3);

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getSkillsMock).toHaveBeenCalledTimes(3);
    });

    test("does not poll when every skill is already terminal", async () => {
        getSkillsMock.mockResolvedValue([makeSkill("ready"), { ...makeSkill("failed"), id: "skill-2" }]);

        const { result } = renderHook(() => useSkills(), { wrapper: buildQueryWrapper() });

        await vi.waitFor(() => expect(result.current.data).toHaveLength(2));
        expect(getSkillsMock).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getSkillsMock).toHaveBeenCalledTimes(1);
    });
});

describe("useSaveSkillFromConfigMutation", () => {
    let useSaveSkillFromConfigMutation: typeof import("./hooks").useSaveSkillFromConfigMutation;
    let saveSkillFromConfig = vi.fn<(...args: unknown[]) => unknown>();

    beforeEach(async () => {
        vi.resetModules();
        saveSkillFromConfig = vi.fn<(...args: unknown[]) => unknown>();
        vi.doMock("./service", async importOriginal => ({
            ...(await importOriginal<typeof import("./service")>()),
            saveSkillFromConfig: (...args: unknown[]) => saveSkillFromConfig(...args),
        }));
        ({ useSaveSkillFromConfigMutation } = await import("./hooks"));
    });

    afterEach(() => vi.doUnmock("./service"));

    function mountWithCachedSkill() {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        client.setQueryData(["skills", "list"], [makeSkill("ready")]);
        client.setQueryData(["skills", "detail", "skill-1"], makeSkill("ready"));
        const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
        return { client, ...renderHook(() => useSaveSkillFromConfigMutation(), { wrapper }) };
    }
    const stale = (client: QueryClient) => ({ list: client.getQueryState(["skills", "list"])?.isInvalidated, detail: client.getQueryState(["skills", "detail", "skill-1"])?.isInvalidated });
    const request = { config: { name: "staging-ops", description: "d" } };

    test("a skill saved at once refreshes the lists", async () => {
        saveSkillFromConfig.mockResolvedValue({ id: "skill-1", name: "staging-ops", action: "created" });
        const { client, result } = mountWithCachedSkill();

        await result.current.mutateAsync(request);

        await vi.waitFor(() => expect(stale(client).list).toBe(true));
    });

    test("a deferred replacement refreshes the lists and the skill's detail once it is uploaded, not before", async () => {
        const upload = vi.fn().mockResolvedValue(undefined);
        saveSkillFromConfig.mockResolvedValue({ id: "skill-1", name: "staging-ops", action: "replace_deferred", commit: upload });
        const { client, result } = mountWithCachedSkill();

        const saved = await result.current.mutateAsync({ ...request, deferReplace: true });
        client.setQueryData(["skills", "detail", "skill-1"], makeSkill("ready"));
        client.setQueryData(["skills", "list"], [makeSkill("ready")]);
        expect(upload).not.toHaveBeenCalled();

        await (saved as { commit: () => Promise<void> }).commit();

        expect(upload).toHaveBeenCalledTimes(1);
        await vi.waitFor(() => expect(stale(client)).toEqual({ list: true, detail: true }));
    });

    test("a skill replaced inline refreshes the detail too, not only the lists", async () => {
        saveSkillFromConfig.mockResolvedValue({ id: "skill-1", name: "staging-ops", action: "updated" });
        const { client, result } = mountWithCachedSkill();

        await result.current.mutateAsync(request);

        await vi.waitFor(() => expect(stale(client)).toEqual({ list: true, detail: true }));
    });

    test("a deferred upload that fails rejects, and still refreshes, since it may have landed", async () => {
        const failure = new Error("Could not update skill");
        saveSkillFromConfig.mockResolvedValue({ id: "skill-1", name: "staging-ops", action: "replace_deferred", commit: vi.fn().mockRejectedValue(failure) });
        const { client, result } = mountWithCachedSkill();
        const saved = await result.current.mutateAsync({ ...request, deferReplace: true });
        client.setQueryData(["skills", "detail", "skill-1"], makeSkill("ready"));

        await expect((saved as { commit: () => Promise<void> }).commit()).rejects.toBe(failure);

        await vi.waitFor(() => expect(stale(client).detail).toBe(true));
    });
});
