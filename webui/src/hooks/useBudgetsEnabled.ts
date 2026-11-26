// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useUserContext } from "@/providers/contexts";

export function useBudgetsEnabled(): boolean {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    return userHasCapability(CAPABILITIES.readBudgets);
}
