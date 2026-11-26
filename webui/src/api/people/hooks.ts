// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQuery } from "@tanstack/react-query";

import { peopleKeys } from "./keys";
import * as peopleService from "./service";

export function usePeopleSearch(query: string, options?: { enabled?: boolean }) {
    return useQuery({
        queryKey: peopleKeys.search(query),
        queryFn: () => peopleService.searchPeople(query),
        enabled: options?.enabled ?? query.length > 0,
        staleTime: 30000,
    });
}

export function useDisplayName(email: string | undefined): string {
    const { data } = usePeopleSearch(email ?? "", { enabled: !!email });
    if (!email) return "";
    const match = data?.data?.find(p => p.workEmail.toLowerCase() === email.toLowerCase());
    return match?.displayName?.trim() || email;
}
