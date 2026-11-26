// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ComponentAccessTarget, GetUserParams, ListClaimMappingsParams, ListUsersParams } from "./service";

export const rbacKeys = {
    all: ["rbac"] as const,
    componentAccess: {
        all: () => [...rbacKeys.all, "componentAccess"] as const,
        list: (target: ComponentAccessTarget | null, params?: { search?: string }) => [...rbacKeys.componentAccess.all(), target ?? {}, params ?? {}] as const,
    },
    roles: {
        all: () => [...rbacKeys.all, "roles"] as const,
        lists: () => [...rbacKeys.roles.all(), "list"] as const,
        list: () => [...rbacKeys.roles.lists()] as const,
        details: () => [...rbacKeys.roles.all(), "detail"] as const,
        detail: (id: string) => [...rbacKeys.roles.details(), id] as const,
    },
    defaultRoles: {
        all: () => [...rbacKeys.all, "defaultRoles"] as const,
        get: () => [...rbacKeys.defaultRoles.all()] as const,
    },
    users: {
        all: () => [...rbacKeys.all, "users"] as const,
        lists: () => [...rbacKeys.users.all(), "list"] as const,
        list: (params?: Partial<ListUsersParams>) => [...rbacKeys.users.lists(), params ?? {}] as const,
        detail: (userId: string, params: GetUserParams = {}) => [...rbacKeys.users.all(), "detail", userId, params] as const,
        roleGrants: (userId: string) => [...rbacKeys.users.all(), "roleGrants", userId] as const,
    },
    claimMappings: {
        all: () => [...rbacKeys.all, "claimMappings"] as const,
        lists: () => [...rbacKeys.claimMappings.all(), "list"] as const,
        list: (params?: Partial<ListClaimMappingsParams>) => [...rbacKeys.claimMappings.lists(), params ?? {}] as const,
    },
    systemUsers: {
        all: () => [...rbacKeys.all, "serviceAccounts"] as const,
        list: () => [...rbacKeys.systemUsers.all(), "list"] as const,
    },
} as const;
