// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider, type QueryKey } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { FULL_RBAC_SEED } from "@/testing/data/rbac";
import { paginatedResponse } from "@/testing/helpers/handlers";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";

import {
    useAssignableRoles,
    useClaimMappings,
    useCreateClaimMappingMutation,
    useCreateRoleMutation,
    useDefaultRoles,
    useDeleteClaimMappingMutation,
    useDeleteRoleMutation,
    useRole,
    useRoles,
    useSetDefaultRolesMutation,
    useSetUserRoleGrantsMutation,
    useSystemUsers,
    useUpdateClaimMappingMutation,
    useUpdateRoleMutation,
    useUser,
    useUserRoleGrants,
    useUsers,
} from "./hooks";
import { rbacKeys } from "./keys";
import { resetStore, seedStore } from "./store";
import { rbacApi } from "./versioned";

const server = setupServer(...rbacHandlers);
beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

beforeEach(() => {
    resetStore();
    seedStore(FULL_RBAC_SEED);
});

function setup<T>(useHook: () => T) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result } = renderHook(useHook, { wrapper });
    const prime = (keys: QueryKey[]) => keys.forEach(key => client.setQueryData(key, []));
    const isInvalidated = (key: QueryKey) => client.getQueryState(key)?.isInvalidated === true;
    return { client, result, prime, isInvalidated };
}

describe("useDeleteRoleMutation invalidation", () => {
    test("invalidates roles, users, and claim mappings", async () => {
        const { result, prime, isInvalidated } = setup(useDeleteRoleMutation);
        prime([rbacKeys.roles.list(), rbacKeys.users.list(), rbacKeys.claimMappings.list()]);

        await result.current.mutateAsync("role-hr-agent-user");

        await waitFor(() => {
            expect(isInvalidated(rbacKeys.roles.list())).toBe(true);
            expect(isInvalidated(rbacKeys.users.list())).toBe(true);
            expect(isInvalidated(rbacKeys.claimMappings.list())).toBe(true);
        });
    });
});

describe("role write mutations invalidate roles broadly (list + all details)", () => {
    test("creating a role invalidates every role list and detail", async () => {
        const { result, prime, isInvalidated } = setup(useCreateRoleMutation);
        prime([rbacKeys.roles.list(), rbacKeys.roles.detail("role-consumer")]);

        await result.current.mutateAsync({ name: "New Role", description: "", scopes: ["rbac:_:read"] });

        await waitFor(() => {
            expect(isInvalidated(rbacKeys.roles.list())).toBe(true);
            expect(isInvalidated(rbacKeys.roles.detail("role-consumer"))).toBe(true);
        });
    });

    test("updating a role invalidates every role detail", async () => {
        const { result, prime, isInvalidated } = setup(useUpdateRoleMutation);
        prime([rbacKeys.roles.list(), rbacKeys.roles.detail("role-consumer"), rbacKeys.roles.detail("role-hr-agent-user")]);

        await result.current.mutateAsync({ id: "role-hr-agent-user", changes: { name: "HR Agent User", description: "x", scopes: ["agent:foo:invoke"] } });

        await waitFor(() => {
            expect(isInvalidated(rbacKeys.roles.detail("role-consumer"))).toBe(true);
            expect(isInvalidated(rbacKeys.roles.detail("role-hr-agent-user"))).toBe(true);
        });
    });
});

describe("useSetDefaultRolesMutation invalidation", () => {
    test("invalidates the default-roles query", async () => {
        const { result, prime, isInvalidated } = setup(useSetDefaultRolesMutation);
        prime([rbacKeys.defaultRoles.get()]);

        await result.current.mutateAsync(["role-hr-agent-user"]);

        await waitFor(() => {
            expect(isInvalidated(rbacKeys.defaultRoles.get())).toBe(true);
        });
    });
});

describe("claim mapping mutations invalidate mappings and users", () => {
    const CLAIM_WRITE = { name: "Engineering", oidcProvider: "azure", claimKey: "groups", claimValue: "grp-eng", roleIds: ["role-editor"] };

    test("creating a claim mapping refreshes the users list", async () => {
        const { result, prime, isInvalidated } = setup(useCreateClaimMappingMutation);
        prime([rbacKeys.claimMappings.list(), rbacKeys.users.list()]);

        await result.current.mutateAsync(CLAIM_WRITE);

        await waitFor(() => {
            expect(isInvalidated(rbacKeys.claimMappings.list())).toBe(true);
            expect(isInvalidated(rbacKeys.users.list())).toBe(true);
        });
    });

    test("updating a claim mapping refreshes the users list", async () => {
        const { result, prime, isInvalidated } = setup(useUpdateClaimMappingMutation);
        prime([rbacKeys.claimMappings.list(), rbacKeys.users.list()]);

        await result.current.mutateAsync({ id: "claim-engineering", changes: CLAIM_WRITE });

        await waitFor(() => {
            expect(isInvalidated(rbacKeys.claimMappings.list())).toBe(true);
            expect(isInvalidated(rbacKeys.users.list())).toBe(true);
        });
    });

    test("deleting a claim mapping refreshes the users list", async () => {
        const { result, prime, isInvalidated } = setup(useDeleteClaimMappingMutation);
        prime([rbacKeys.claimMappings.list(), rbacKeys.users.list()]);

        await result.current.mutateAsync("claim-engineering");

        await waitFor(() => {
            expect(isInvalidated(rbacKeys.claimMappings.list())).toBe(true);
            expect(isInvalidated(rbacKeys.users.list())).toBe(true);
        });
    });
});

describe("user role grants", () => {
    const GRACE = "1a000000-0000-4000-8000-000000000007";

    test("useUserRoleGrants fetches a user's direct grants", async () => {
        const { result } = setup(() => useUserRoleGrants(GRACE));
        await waitFor(() => expect(result.current.data).toEqual(["role-hr-agent-user", "role-evaluations-analyst"]));
    });

    test("useSetUserRoleGrantsMutation invalidates users and role lists", async () => {
        const { result, prime, isInvalidated } = setup(useSetUserRoleGrantsMutation);
        prime([rbacKeys.users.list(), rbacKeys.roles.list()]);

        await result.current.mutateAsync({ userId: GRACE, roleIds: ["role-hr-agent-user"] });

        await waitFor(() => {
            expect(isInvalidated(rbacKeys.users.list())).toBe(true);
            expect(isInvalidated(rbacKeys.roles.list())).toBe(true);
        });
    });
});

type RBACApiFn = keyof typeof rbacApi;

const PAGE = { pageNumber: 1, pageSize: 20 };

const QUERY_HOOKS: { hook: string; fn: RBACApiFn; use: () => { isSuccess: boolean }; key: QueryKey; value: unknown }[] = [
    { hook: "useRoles", fn: "listRoles", use: useRoles, key: rbacKeys.roles.list(), value: [] },
    { hook: "useAssignableRoles", fn: "listRoles", use: useAssignableRoles, key: rbacKeys.roles.list(), value: [] },
    { hook: "useRole", fn: "getRole", use: () => useRole("r1"), key: rbacKeys.roles.detail("r1"), value: { id: "r1" } },
    { hook: "useDefaultRoles", fn: "getDefaultRoles", use: useDefaultRoles, key: rbacKeys.defaultRoles.get(), value: { roleIds: [], origin: "yaml" } },
    { hook: "useUsers", fn: "listUsers", use: () => useUsers(PAGE), key: rbacKeys.users.list(PAGE), value: paginatedResponse([]) },
    { hook: "useUser", fn: "getUser", use: () => useUser("u1"), key: rbacKeys.users.detail("u1"), value: { id: "u1" } },
    { hook: "useUserRoleGrants", fn: "getUserRoleGrants", use: () => useUserRoleGrants("u1"), key: rbacKeys.users.roleGrants("u1"), value: [] },
    { hook: "useSystemUsers", fn: "listSystemUsers", use: useSystemUsers, key: rbacKeys.systemUsers.list(), value: paginatedResponse([]) },
    { hook: "useClaimMappings", fn: "listClaimMappings", use: () => useClaimMappings(PAGE), key: rbacKeys.claimMappings.list(PAGE), value: paginatedResponse([]) },
];

describe("RBAC hooks reach the API", () => {
    afterEach(() => vi.restoreAllMocks());

    test.each(QUERY_HOOKS)("$hook reads through the RBAC API and caches under its key", async ({ fn, use, key, value }) => {
        const spy = vi.spyOn(rbacApi, fn).mockResolvedValue(value as never);
        const { client, result } = setup(use);

        await waitFor(() => expect(result.current.isSuccess).toBe(true));

        expect(spy).toHaveBeenCalled();
        expect(client.getQueryData(key)).toBeDefined();
    });

    const MUTATIONS: { hook: string; fn: RBACApiFn; run: () => Promise<unknown> }[] = [
        { hook: "useCreateRoleMutation", fn: "createRole", run: () => mutate(useCreateRoleMutation, { name: "New", description: "" }) },
        { hook: "useUpdateRoleMutation", fn: "updateRole", run: () => mutate(useUpdateRoleMutation, { id: "r1", changes: { name: "R", description: "", scopes: [] } }) },
        { hook: "useDeleteRoleMutation", fn: "deleteRole", run: () => mutate(useDeleteRoleMutation, "r1") },
        { hook: "useSetDefaultRolesMutation", fn: "setDefaultRoles", run: () => mutate(useSetDefaultRolesMutation, ["r1"]) },
        { hook: "useSetUserRoleGrantsMutation", fn: "setUserRoleGrants", run: () => mutate(useSetUserRoleGrantsMutation, { userId: "u1", roleIds: ["r1"] }) },
        { hook: "useCreateClaimMappingMutation", fn: "createClaimMapping", run: () => mutate(useCreateClaimMappingMutation, { name: "M", oidcProvider: "azure", claimKey: "groups", claimValue: "g", roleIds: ["r1"] }) },
        {
            hook: "useUpdateClaimMappingMutation",
            fn: "updateClaimMapping",
            run: () => mutate(useUpdateClaimMappingMutation, { id: "m1", changes: { name: "M", oidcProvider: "azure", claimKey: "groups", claimValue: "g", roleIds: ["r1"] } }),
        },
        { hook: "useDeleteClaimMappingMutation", fn: "deleteClaimMapping", run: () => mutate(useDeleteClaimMappingMutation, "m1") },
    ];

    function mutate<V>(useMutationHook: () => { mutateAsync: (vars: V) => Promise<unknown> }, vars: V) {
        return setup(useMutationHook).result.current.mutateAsync(vars);
    }

    test.each(MUTATIONS)("$hook writes through the RBAC API", async ({ fn, run }) => {
        const spy = vi.spyOn(rbacApi, fn).mockResolvedValue({} as never);

        await run();

        expect(spy).toHaveBeenCalled();
    });
});
