// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { isWailsRuntime } from "@/utils/openExternalLink";

export function useIsDesktop(): boolean {
    return isWailsRuntime();
}
