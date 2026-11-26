// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { usageKeys } from "@/api/usage";

export function useListsFollowSummary(dataAsOf: string | null | undefined) {
    const queryClient = useQueryClient();
    const followed = useRef(dataAsOf);
    useEffect(() => {
        if (dataAsOf === undefined) return;
        const previous = followed.current;
        followed.current = dataAsOf;
        if (previous === undefined || previous === dataAsOf) return;
        queryClient.invalidateQueries({ queryKey: usageKeys.allLists }).catch(() => undefined);
    }, [dataAsOf, queryClient]);
}
