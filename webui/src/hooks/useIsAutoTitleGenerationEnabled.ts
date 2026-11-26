// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagValue } from "@openfeature/react-sdk";

import { useConfigContext } from "@/providers/contexts";

export function useIsAutoTitleGenerationEnabled(): boolean {
    const { persistenceEnabled } = useConfigContext();
    const flagEnabled = useBooleanFlagValue("auto_title_generation", false);
    return flagEnabled && (persistenceEnabled ?? false);
}
