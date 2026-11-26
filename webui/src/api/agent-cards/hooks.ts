// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQuery } from "@tanstack/react-query";

import { agentCardKeys } from "./keys";
import { getAgentCards } from "./service";

export function useAgentCards() {
    return useQuery({
        queryKey: agentCardKeys.list(),
        queryFn: getAgentCards,
        refetchOnMount: "always",
        retry: 0,
    });
}
