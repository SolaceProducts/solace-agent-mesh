// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const modelKeys = {
    all: ["models"] as const,
    lists: () => [...modelKeys.all, "list"] as const,
    list: () => [...modelKeys.lists()] as const,
    status: () => [...modelKeys.all, "status"] as const,
    azureWorkloadIdentity: () => [...modelKeys.all, "azureWorkloadIdentity"] as const,
    detail: (id: string) => [...modelKeys.all, "detail", id] as const,
    supportedModels: (params: unknown) => [...modelKeys.all, "supported", params] as const,
};
