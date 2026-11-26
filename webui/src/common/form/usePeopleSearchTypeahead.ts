// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";

import { usePeopleSearch } from "@/api/people";

import { useDebounce } from "@/hooks/useDebounce";

import type { Person } from "@/types";

interface UsePeopleSearchTypeaheadOptions {
    excludeEmails: string[];
    debounceMs?: number;
}

interface UsePeopleSearchTypeaheadResult {
    searchQuery: string;
    setSearchQuery: (q: string) => void;
    activeIndex: number;
    setActiveIndex: (next: number | ((prev: number) => number)) => void;
    filteredPeople: Person[];
    isLoading: boolean;
    pendingFetch: boolean;
}

export function usePeopleSearchTypeahead({ excludeEmails, debounceMs = 200 }: UsePeopleSearchTypeaheadOptions): UsePeopleSearchTypeaheadResult {
    const [searchQuery, setSearchQuery] = useState("");
    const [activeIndex, setActiveIndex] = useState(0);

    const debouncedQuery = useDebounce(searchQuery, debounceMs);
    const { data: searchResults, isLoading } = usePeopleSearch(debouncedQuery, { enabled: debouncedQuery.length > 0 });

    const excludedSet = useMemo(() => new Set(excludeEmails), [excludeEmails]);
    const filteredPeople = useMemo(() => (searchResults?.data ?? []).filter(p => !excludedSet.has(p.workEmail)), [searchResults?.data, excludedSet]);

    useEffect(() => {
        setActiveIndex(0);
    }, [filteredPeople.length]);

    const pendingFetch = (searchQuery.trim().length > 0 && debouncedQuery !== searchQuery) || isLoading;

    return { searchQuery, setSearchQuery, activeIndex, setActiveIndex, filteredPeople, isLoading, pendingFetch };
}
