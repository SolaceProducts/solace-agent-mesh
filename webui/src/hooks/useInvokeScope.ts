// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagDetails } from "@openfeature/react-sdk";

import { instanceScope } from "@/utils/scopes";

export function useInvokeScope(resourceType: "agent" | "workflow", id?: string): string | undefined {
    const { value: rbacEnabled } = useBooleanFlagDetails("rbac", false);
    return rbacEnabled && id ? instanceScope(resourceType, id, "invoke") : undefined;
}
