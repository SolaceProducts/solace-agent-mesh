// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQuery } from "@tanstack/react-query";

import { versionKeys } from "./keys";
import { versionApi } from "./versioned";

export function useVersionInfo() {
    return useQuery({
        queryKey: versionKeys.detail(),
        queryFn: versionApi.get,
        retry: 0,
    });
}
