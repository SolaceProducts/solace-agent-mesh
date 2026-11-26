// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagDetails } from "@openfeature/react-sdk";

import { useUserContext } from "@/providers/contexts";

export function useBuilderEnabled(): boolean {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { value: flagEnabled } = useBooleanFlagDetails("builder", false);
    return flagEnabled && userHasCapability(CAPABILITIES.useBuilder, true);
}
