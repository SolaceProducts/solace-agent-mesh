// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagValue } from "@openfeature/react-sdk";

import { useConfigContext } from "@/providers/contexts";

import { useIsDesktop } from "@/hooks/useIsDesktop";

export function useIsChatSharingEnabled(): boolean {
    const { identityServiceType, persistenceEnabled } = useConfigContext();
    const flagEnabled = useBooleanFlagValue("chat_sharing", false);
    const isDesktop = useIsDesktop();
    return !isDesktop && flagEnabled && identityServiceType !== null && Boolean(persistenceEnabled);
}
