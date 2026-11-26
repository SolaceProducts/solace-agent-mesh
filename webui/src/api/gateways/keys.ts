// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const gatewayKeys = {
    all: ["gateways"] as const,
    lists: () => [...gatewayKeys.all, "list"] as const,
    list: () => [...gatewayKeys.lists()] as const,
    networkingDetails: (gatewayId: string) => [...gatewayKeys.all, "networkingDetails", gatewayId] as const,
};
