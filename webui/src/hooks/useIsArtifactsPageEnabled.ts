// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useBooleanFlagValue } from "@openfeature/react-sdk";

export function useIsArtifactsPageEnabled(): boolean {
    return useBooleanFlagValue("artifacts_page", false);
}
