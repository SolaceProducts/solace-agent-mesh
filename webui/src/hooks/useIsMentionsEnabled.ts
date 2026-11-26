// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagValue } from "@openfeature/react-sdk";

import { useConfigContext } from "@/providers/contexts";

export function useIsMentionsEnabled(): boolean {
    const { identityServiceType } = useConfigContext();
    return useBooleanFlagValue("mentions", false) && identityServiceType !== null;
}
