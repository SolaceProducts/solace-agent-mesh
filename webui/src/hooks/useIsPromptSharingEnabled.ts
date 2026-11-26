// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagValue } from "@openfeature/react-sdk";

import { useConfigContext } from "@/providers/contexts";

import { useIsDesktop } from "@/hooks/useIsDesktop";

export function useIsPromptSharingEnabled(): boolean {
    const { identityServiceType } = useConfigContext();
    const flagEnabled = useBooleanFlagValue("prompt_sharing", false);
    const isDesktop = useIsDesktop();
    return !isDesktop && flagEnabled && identityServiceType !== null;
}
