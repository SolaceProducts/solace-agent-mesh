// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const workflowKeys = {
    all: ["workflows"] as const,
    lists: () => [...workflowKeys.all, "list"] as const,
    list: () => [...workflowKeys.lists()] as const,
    details: () => [...workflowKeys.all, "detail"] as const,
    detail: (id: string) => [...workflowKeys.details(), id] as const,
    deployments: () => [...workflowKeys.all, "deployments"] as const,
};
