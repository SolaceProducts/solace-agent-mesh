// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQuery } from "@tanstack/react-query";

import { sessionKeys } from "./keys";
import { getSession } from "./service";

export function useSessionName(sessionId: string | undefined): string | undefined {
    const { data } = useQuery({
        queryKey: sessionKeys.detail(sessionId ?? ""),
        queryFn: () => getSession(sessionId as string),
        enabled: Boolean(sessionId),
        retry: false,
        staleTime: 60_000,
    });
    return data?.name?.trim() || undefined;
}
