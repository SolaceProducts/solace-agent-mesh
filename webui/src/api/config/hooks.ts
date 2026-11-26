// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQuery } from "@tanstack/react-query";

import { useCsrfContext } from "@/providers/contexts";

import { configKeys } from "./keys";
import { fetchConfig } from "./service";

const STALE_UNTIL_INVALIDATED = Infinity;

export function useConfigQuery() {
    const { fetchCsrfToken } = useCsrfContext();
    return useQuery({
        queryKey: configKeys.detail(),
        queryFn: () => fetchConfig(fetchCsrfToken),
        staleTime: STALE_UNTIL_INVALIDATED,
        retry: 0,
    });
}
