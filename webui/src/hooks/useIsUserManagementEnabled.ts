// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagValue } from "@openfeature/react-sdk";

import { useConfigContext, useUserContext } from "@/providers/contexts";

import { useIsDesktop } from "@/hooks/useIsDesktop";

export function useIsUserManagementEnabled(): boolean {
    const { configUseAuthorization } = useConfigContext();
    const { isSuperAdmin } = useUserContext();
    const flagEnabled = useBooleanFlagValue("rbac", false);
    const isDesktop = useIsDesktop();
    return !isDesktop && configUseAuthorization && flagEnabled && isSuperAdmin;
}
