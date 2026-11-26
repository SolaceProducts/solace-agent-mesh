// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagValue } from "@openfeature/react-sdk";

const FALLBACK_BEFORE_CATALOG_LOADS = false;

export function useIsBuilderServerApplyEnabled(): boolean {
    return useBooleanFlagValue("builder_server_apply", FALLBACK_BEFORE_CATALOG_LOADS);
}
