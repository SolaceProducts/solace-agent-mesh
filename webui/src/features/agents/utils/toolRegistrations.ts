// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ToolRegistration } from "@/types";

export const runtimeToolNamesForConnector = (registrations: ToolRegistration[] | undefined, connectorId: string | undefined): string[] | undefined => {
    if (!registrations?.length || !connectorId) return undefined;
    const match = registrations.find(reg => reg.connectorId === connectorId && reg.status === "ready");
    return match?.toolNames?.length ? match.toolNames : undefined;
};
