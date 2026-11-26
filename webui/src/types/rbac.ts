// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AppliedBudget, AppliedTopUp } from "@/api/budgets/types";

export type RoleOrigin = "custom" | "yaml" | "builtin";

export type ClaimMappingOrigin = Exclude<RoleOrigin, "builtin">;

export type UserRoleSource = "direct" | "claim" | "yaml" | "default";

export interface UserRoleAssignment {
    roleId: string;
    source: UserRoleSource;
}

export interface RBACRole {
    id: string;
    name: string;
    description: string;
    origin: RoleOrigin;
    scopes: string[];
    inherits?: string[];
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export interface DefaultRolesResponse {
    roleIds: string[];
    origin: DefaultRolesOrigin;
}

export type DefaultRolesOrigin = Exclude<RoleOrigin, "builtin">;

export interface User {
    id: string;
    displayName?: string;
    userName?: string;
    email?: string;
    issuer?: string;
    sub?: string;
    oidcProvider?: string;
    provisionedVia?: string;
    active?: boolean;
    groups?: string[];
    roles: UserRoleAssignment[];
    lastLoginAt?: string;
    createdBy?: string;
    updatedBy?: string;
    createdAt?: string;
    updatedAt?: string;
    budget?: AppliedBudget | null;
    budgetTopUp?: AppliedTopUp | null;
    effectiveBudgetAmount?: string | null;
}

export interface CreateRoleRequest {
    name: string;
    description: string;
    scopes?: string[];
}

export interface UpdateRoleRequest {
    name?: string;
    description?: string;
    scopes?: string[];
    inherits?: string[];
}

export interface SystemUser {
    id: string;
    sub: string;
    name: string;
    displayName?: string;
    roleIds: string[];
}

export interface ClaimMapping {
    id: string;
    name: string;
    oidcProvider: string;
    claimKey: string;
    claimValue: string;
    roleIds: string[];
    origin: ClaimMappingOrigin;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export interface CreateClaimMappingRequest {
    name: string;
    oidcProvider?: string;
    claimKey: string;
    claimValue: string;
    roleIds: string[];
}

export interface UpdateClaimMappingRequest {
    name: string;
    oidcProvider: string;
    claimKey: string;
    claimValue: string;
    roleIds: string[];
}
