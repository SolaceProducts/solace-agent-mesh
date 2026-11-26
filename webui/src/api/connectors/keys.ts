// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { GetConnectorsParams } from "./service";

export const connectorKeys = {
    all: ["connectors"] as const,
    lists: () => [...connectorKeys.all, "list"] as const,
    list: () => [...connectorKeys.lists()] as const,
    paginated: (params: GetConnectorsParams) => [...connectorKeys.lists(), "paginated", params] as const,
    builderCredentials: (signature: string) => [...connectorKeys.lists(), "builderCredentials", signature] as const,
    details: () => [...connectorKeys.all, "detail"] as const,
    detail: (id: string) => [...connectorKeys.details(), id] as const,
};
