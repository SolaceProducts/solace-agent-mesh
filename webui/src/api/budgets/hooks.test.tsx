// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { rbacKeys } from "@/api/rbac/keys";
import { usageKeys } from "@/api/usage/keys";

import { paginatedResponse } from "@/testing/helpers/handlers";
import { platformFetch } from "@/testing/helpers/platformFetch";
import { v2Error } from "@/testing/helpers/platformResponses";

import {
    useBudgetAssignments,
    useBudgetSubjects,
    useCreateBudgetAssignmentMutation,
    useCreateBudgetMutation,
    useDeleteBudgetAssignmentMutation,
    useDeleteBudgetMutation,
    useDeleteBudgetTopUpMutation,
    useGrantBudgetTopUpMutation,
    useMakeDefaultBudgetMutation,
    useUpdateBudgetAssignmentsMutation,
    useUpdateBudgetMutation,
} from "./hooks";
import { budgetKeys } from "./keys";
import type { BudgetSubject, ListBudgetSubjectsParams } from "./types";

const platform = { get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), delete: vi.fn() };

const subject = (id: string): BudgetSubject => ({ id, type: "budgetSubject", subjectType: "user", displayName: id, user: { email: null, isSystemUser: false }, budget: null, topUp: null, effectiveAmount: null });

function renderSubjects(initialProps: { params: ListBudgetSubjectsParams; enabled?: boolean }) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => (
        <OpenFeatureTestProvider flagValueMap={{}}>
            <QueryClientProvider client={client}>{children}</QueryClientProvider>
        </OpenFeatureTestProvider>
    );
    return renderHook(({ params, enabled }) => useBudgetSubjects(params, { enabled }), { initialProps, wrapper });
}

beforeEach(() => {
    vi.stubGlobal("fetch", platformFetch(platform));
    platform.get.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("useBudgetSubjects", () => {
    test("the paged list keeps the previous page while the next one loads", async () => {
        platform.get.mockImplementation((url: string) => (url.includes("pageNumber=1") ? Promise.resolve(paginatedResponse([subject("u-a")])) : new Promise(() => {})));
        const { result, rerender } = renderSubjects({ params: { subjectType: "user", pageNumber: 1, pageSize: 20 } });
        await waitFor(() => expect(result.current.data?.data.map(s => s.id)).toEqual(["u-a"]));

        rerender({ params: { subjectType: "user", pageNumber: 2, pageSize: 20 } });
        await waitFor(() => expect(platform.get).toHaveBeenCalledTimes(2));
        expect(result.current.data?.data.map(s => s.id)).toEqual(["u-a"]);
    });
});

function renderWithClient<T>(client: QueryClient, hook: () => T): T {
    const wrapper = ({ children }: { children: ReactNode }) => (
        <OpenFeatureTestProvider flagValueMap={{}}>
            <QueryClientProvider client={client}>{children}</QueryClientProvider>
        </OpenFeatureTestProvider>
    );
    return renderHook(hook, { wrapper }).result.current;
}

function expectEveryListRefreshed(invalidate: ReturnType<typeof vi.spyOn>) {
    expect(invalidate).toHaveBeenCalledWith({ queryKey: budgetKeys.all });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: rbacKeys.users.all() });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: usageKeys.all });
}

describe("budget mutations", () => {
    test.each([
        { name: "removing an assignment", run: (client: QueryClient) => renderWithClient(client, useDeleteBudgetAssignmentMutation).mutateAsync("a-1") },
        { name: "updating assignments", run: (client: QueryClient) => renderWithClient(client, useUpdateBudgetAssignmentsMutation).mutateAsync({ budgetId: "b-eng", changes: { subjectType: "user", add: [], remove: ["u-a"] } }) },
        { name: "granting a top-up", run: (client: QueryClient) => renderWithClient(client, useGrantBudgetTopUpMutation).mutateAsync({ subjectType: "user", subjectId: "u-ada", amount: "50" }) },
        { name: "cancelling a top-up", run: (client: QueryClient) => renderWithClient(client, useDeleteBudgetTopUpMutation).mutateAsync("u-ada") },
        { name: "making a budget the default", run: (client: QueryClient) => renderWithClient(client, useMakeDefaultBudgetMutation).mutateAsync("b-ops") },
        { name: "updating a budget", run: (client: QueryClient) => renderWithClient(client, useUpdateBudgetMutation).mutateAsync({ id: "b-eng", changes: { name: "Engineering" } }) },
        { name: "creating a budget", run: (client: QueryClient) => renderWithClient(client, useCreateBudgetMutation).mutateAsync({ name: "Agent Mesh", subjectType: "mesh", amount: "5000" }) },
        { name: "deleting a budget", run: (client: QueryClient) => renderWithClient(client, useDeleteBudgetMutation).mutateAsync("b-eng") },
    ])("$name refreshes the users lists and usage as well as every budget query", async ({ run }) => {
        platform.post.mockResolvedValue({ data: { id: "b-new", createdTime: null, updatedTime: null } });
        platform.patch.mockResolvedValue({ data: { id: "b-eng", createdTime: null, updatedTime: null } });
        platform.put.mockResolvedValue({ data: { id: "b-ops", createdTime: null, updatedTime: null, added: [], moved: [], removed: [], unchanged: [] } });
        platform.delete.mockResolvedValue(undefined);
        const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
        const invalidate = vi.spyOn(client, "invalidateQueries");
        await run(client);
        expectEveryListRefreshed(invalidate);
    });

    test("a failed one still refreshes them", async () => {
        platform.post.mockImplementation(async () => v2Error(409, "This user already has a budget assignment."));
        const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
        const invalidate = vi.spyOn(client, "invalidateQueries");
        await expect(renderWithClient(client, useCreateBudgetAssignmentMutation).mutateAsync({ subjectType: "user", subjectId: "u-a", budgetId: "b-eng" })).rejects.toThrow("already has");
        expectEveryListRefreshed(invalidate);
    });
});

describe("useBudgetAssignments", () => {
    test("loads only concrete assignments, a page at a time, flattened, with the total", async () => {
        const assignment = (id: string) => ({ id, subject: { id }, createdTime: null, updatedTime: null });
        platform.get.mockImplementation((url: string) =>
            Promise.resolve(
                url.includes("pageNumber=1")
                    ? { data: [assignment("a-1")], meta: { pagination: { pageNumber: 1, count: 2, pageSize: 1, nextPage: 2, totalPages: 2 } } }
                    : { data: [assignment("a-2")], meta: { pagination: { pageNumber: 2, count: 2, pageSize: 1, nextPage: null, totalPages: 2 } } }
            )
        );
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const wrapper = ({ children }: { children: ReactNode }) => (
            <OpenFeatureTestProvider flagValueMap={{}}>
                <QueryClientProvider client={client}>{children}</QueryClientProvider>
            </OpenFeatureTestProvider>
        );
        const { result } = renderHook(() => useBudgetAssignments({ budgetId: "b-eng" }), { wrapper });
        await waitFor(() => expect(result.current.assignments.map(a => a.id)).toEqual(["a-1"]));
        expect(platform.get).toHaveBeenCalledWith(expect.stringContaining("state=concrete"));
        expect(result.current.total).toBe(2);
        expect(result.current.hasNextPage).toBe(true);
        await result.current.fetchNextPage();
        await waitFor(() => expect(result.current.assignments.map(a => a.id)).toEqual(["a-1", "a-2"]));
        expect(result.current.hasNextPage).toBe(false);
    });
});
