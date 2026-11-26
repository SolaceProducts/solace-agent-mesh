// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import type { Skill } from "@/types";

import { useBuiltinSkills, useSkill, useSkillFiles, useSkills } from "./hooks";
import { skillKeys } from "./keys";
import { skillsApi } from "./versioned";

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const readySkill = { id: "sk-1", name: "order-lookup", discoveryStatus: "ready" } as Skill;
const noFiles = { references: [], assets: [] };

const cases = [
    { hook: "useSkills", use: () => useSkills(), method: "getAll", key: skillKeys.list, result: [] },
    { hook: "useBuiltinSkills", use: () => useBuiltinSkills(), method: "getBuiltin", key: skillKeys.builtins, result: [] },
    { hook: "useSkill", use: () => useSkill("sk-1"), method: "get", key: () => skillKeys.detail("sk-1"), result: readySkill },
    { hook: "useSkillFiles", use: () => useSkillFiles("sk-1"), method: "getFiles", key: () => skillKeys.files("sk-1"), result: noFiles },
] as const;

describe("skill hooks", () => {
    afterEach(() => vi.restoreAllMocks());

    test.each(cases)("$hook calls the skills API and caches under its key", async ({ use, method, key, result }) => {
        const called = vi.spyOn(skillsApi, method).mockResolvedValue(result as never);
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

        const { result: hook } = renderHook(() => use(), { wrapper: wrapper(client) });

        await waitFor(() => expect(hook.current.isSuccess).toBe(true));
        expect(called).toHaveBeenCalledTimes(1);
        expect(client.getQueryData(key())).toEqual(result);
    });
});

describe("built-in skills and the lists() prefix", () => {
    afterEach(() => vi.restoreAllMocks());

    test("invalidating lists() refreshes the built-in skills, so their Agent Usage is current", async () => {
        const getBuiltin = vi.spyOn(skillsApi, "getBuiltin").mockResolvedValue([]);
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const { result } = renderHook(() => useBuiltinSkills(), { wrapper: wrapper(client) });
        await waitFor(() => expect(result.current.isSuccess).toBe(true));

        await client.invalidateQueries({ queryKey: skillKeys.lists() });

        await waitFor(() => expect(getBuiltin).toHaveBeenCalledTimes(2));
    });
});
