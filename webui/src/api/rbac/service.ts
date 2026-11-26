// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface ListUsersParams {
    pageNumber: number;
    pageSize: number;
    search?: string;
    roleId?: string;
    budgetId?: string;
    sort?: string;
    expandBudget?: boolean;
}

export interface GetUserParams {
    expandBudget?: boolean;
}

export type ComponentAccessTarget = { agentId: string } | { workflowId: string };

export interface ComponentAccessUser {
    id: string;
    displayName: string;
    userName: string;
    email: string;
    isSystemUser: boolean;
}

export interface ComponentAccessPageParams {
    search?: string;
    pageNumber: number;
    pageSize: number;
}

export interface ListClaimMappingsParams {
    pageNumber: number;
    pageSize: number;
    search?: string;
    roleId?: string;
    sort?: string;
}
