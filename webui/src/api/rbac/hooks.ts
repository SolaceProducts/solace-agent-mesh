// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { ClaimMapping, CreateClaimMappingRequest, CreateRoleRequest, DefaultRolesResponse, RBACRole, UpdateClaimMappingRequest, UpdateRoleRequest } from "@/types";

import { rbacKeys } from "./keys";
import type { ComponentAccessTarget, GetUserParams, ListClaimMappingsParams, ListUsersParams } from "./service";
import { componentAccessApi, rbacApi } from "./versioned";

export function useRoles() {
    return useQuery({
        queryKey: rbacKeys.roles.list(),
        queryFn: rbacApi.listRoles,
    });
}

export function useAssignableRoles() {
    return useQuery({
        queryKey: rbacKeys.roles.list(),
        queryFn: rbacApi.listRoles,
        select: roles => roles.filter(r => r.origin !== "yaml"),
    });
}

export function useRole(id: string, options?: { enabled?: boolean }) {
    return useQuery({
        queryKey: rbacKeys.roles.detail(id),
        queryFn: () => rbacApi.getRole(id),
        enabled: options?.enabled ?? Boolean(id),
    });
}

function invalidateRBACDerived(queryClient: ReturnType<typeof useQueryClient>) {
    queryClient.invalidateQueries({ queryKey: rbacKeys.users.all() });
    queryClient.invalidateQueries({ queryKey: rbacKeys.componentAccess.all() });
}

export function useCreateRoleMutation() {
    const queryClient = useQueryClient();
    return useMutation<RBACRole, Error, CreateRoleRequest>({
        mutationFn: rbacApi.createRole,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: rbacKeys.roles.all() });
        },
    });
}

export function useUpdateRoleMutation() {
    const queryClient = useQueryClient();
    return useMutation<RBACRole, Error, { id: string; changes: UpdateRoleRequest }>({
        mutationFn: ({ id, changes }) => rbacApi.updateRole(id, changes),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: rbacKeys.roles.all() });
            invalidateRBACDerived(queryClient);
        },
    });
}

export function useDeleteRoleMutation() {
    const queryClient = useQueryClient();
    return useMutation<void, Error, string>({
        mutationFn: rbacApi.deleteRole,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: rbacKeys.roles.all() });
            queryClient.invalidateQueries({ queryKey: rbacKeys.users.all() });
            queryClient.invalidateQueries({ queryKey: rbacKeys.claimMappings.all() });
            queryClient.invalidateQueries({ queryKey: rbacKeys.componentAccess.all() });
        },
    });
}

export function useDefaultRoles() {
    return useQuery({
        queryKey: rbacKeys.defaultRoles.get(),
        queryFn: rbacApi.getDefaultRoles,
    });
}

export function useSetDefaultRolesMutation() {
    const queryClient = useQueryClient();
    return useMutation<DefaultRolesResponse, Error, string[]>({
        mutationFn: rbacApi.setDefaultRoles,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: rbacKeys.defaultRoles.all() });
            invalidateRBACDerived(queryClient);
        },
    });
}

export function useUsers(params: ListUsersParams) {
    return useQuery({
        queryKey: rbacKeys.users.list(params),
        queryFn: () => rbacApi.listUsers(params),
        placeholderData: keepPreviousData,
    });
}

export function useUser(userId: string, params: GetUserParams = {}, options?: { enabled?: boolean }) {
    return useQuery({
        queryKey: rbacKeys.users.detail(userId, params),
        queryFn: () => rbacApi.getUser(userId, params),
        enabled: options?.enabled ?? true,
    });
}

export function useUserRoleGrants(userId: string, options?: { enabled?: boolean }) {
    return useQuery({
        queryKey: rbacKeys.users.roleGrants(userId),
        queryFn: () => rbacApi.getUserRoleGrants(userId),
        enabled: options?.enabled ?? Boolean(userId),
    });
}

export function useSetUserRoleGrantsMutation() {
    const queryClient = useQueryClient();
    return useMutation<string[], Error, { userId: string; roleIds: string[] }>({
        mutationFn: ({ userId, roleIds }) => rbacApi.setUserRoleGrants(userId, roleIds),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: rbacKeys.users.all() });
            queryClient.invalidateQueries({ queryKey: rbacKeys.roles.lists() });
            queryClient.invalidateQueries({ queryKey: rbacKeys.componentAccess.all() });
        },
    });
}

export function useSystemUsers() {
    return useQuery({
        queryKey: rbacKeys.systemUsers.list(),
        queryFn: async () => (await rbacApi.listSystemUsers()).data,
    });
}

const COMPONENT_ACCESS_PAGE_SIZE = 20;

export function useUsersWithComponentAccess(target: ComponentAccessTarget | null, params?: { search?: string }) {
    const search = params?.search?.trim() || undefined;
    const query = useInfiniteQuery({
        queryKey: rbacKeys.componentAccess.list(target, { search }),
        queryFn: ({ pageParam }) => componentAccessApi.listUsers(target as ComponentAccessTarget, { search, pageNumber: pageParam, pageSize: COMPONENT_ACCESS_PAGE_SIZE }),
        initialPageParam: 1,
        getNextPageParam: last => last.meta.pagination.nextPage ?? undefined,
        enabled: target !== null,
        placeholderData: keepPreviousData,
    });
    return {
        ...query,
        users: query.data?.pages.flatMap(page => page.data) ?? [],
        total: query.data?.pages[0]?.meta.pagination.count ?? 0,
    };
}

export function useClaimMappings(params: ListClaimMappingsParams) {
    return useQuery({
        queryKey: rbacKeys.claimMappings.list(params),
        queryFn: () => rbacApi.listClaimMappings(params),
        placeholderData: keepPreviousData,
    });
}

function invalidateClaimMappingsAndUsers(queryClient: ReturnType<typeof useQueryClient>) {
    queryClient.invalidateQueries({ queryKey: rbacKeys.claimMappings.all() });
    invalidateRBACDerived(queryClient);
}

export function useCreateClaimMappingMutation() {
    const queryClient = useQueryClient();
    return useMutation<ClaimMapping, Error, CreateClaimMappingRequest>({
        mutationFn: rbacApi.createClaimMapping,
        onSuccess: () => invalidateClaimMappingsAndUsers(queryClient),
    });
}

export function useUpdateClaimMappingMutation() {
    const queryClient = useQueryClient();
    return useMutation<ClaimMapping, Error, { id: string; changes: UpdateClaimMappingRequest }>({
        mutationFn: ({ id, changes }) => rbacApi.updateClaimMapping(id, changes),
        onSuccess: () => invalidateClaimMappingsAndUsers(queryClient),
    });
}

export function useDeleteClaimMappingMutation() {
    const queryClient = useQueryClient();
    return useMutation<void, Error, string>({
        mutationFn: rbacApi.deleteClaimMapping,
        onSuccess: () => invalidateClaimMappingsAndUsers(queryClient),
    });
}
