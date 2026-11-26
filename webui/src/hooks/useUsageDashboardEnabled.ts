// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useUserContext } from "@/providers/contexts";

import { canReadSpend } from "@/utils/scopes";

export function useUsageDashboardEnabled(): boolean {
    const { scopes } = useUserContext();
    return canReadSpend(scopes);
}
