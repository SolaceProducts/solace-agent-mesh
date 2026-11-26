// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";

import { connectorKeys } from "@/api/connectors";
import { connectorsApi } from "@/api/connectors/versioned";

import type { Connector } from "@/types";

interface UseConnectorsPaginatedArgs {
    enabled: boolean;
    pageSize: number;
}

interface UseConnectorsPaginatedReturn {
    connectors: Connector[];
    isLoading: boolean;
    error: string | null;
    pageNumber: number;
    setPageNumber: (n: number) => void;
    totalPages: number;
    totalCount: number;
    searchTerm: string;
    setSearchTerm: (s: string) => void;
    refetch: () => void;
}

const SEARCH_DEBOUNCE_MS = 300;

export const useConnectorsPaginated = ({ enabled, pageSize }: UseConnectorsPaginatedArgs): UseConnectorsPaginatedReturn => {
    const [pageNumber, setPageNumber] = useState<number>(1);
    const [searchTerm, setSearchTermState] = useState<string>("");
    const [debouncedSearch, setDebouncedSearch] = useState<string>("");

    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    const setSearchTerm = useCallback((value: string) => {
        setSearchTermState(value);
        if (timerRef.current) clearTimeout(timerRef.current);
        timerRef.current = setTimeout(() => {
            setDebouncedSearch(value);
            setPageNumber(1);
        }, SEARCH_DEBOUNCE_MS);
    }, []);

    useEffect(
        () => () => {
            if (timerRef.current) clearTimeout(timerRef.current);
        },
        []
    );

    useEffect(() => {
        setPageNumber(1);
    }, [pageSize]);

    const params = { pageNumber, pageSize, name: debouncedSearch.trim() || undefined };

    const query = useQuery({
        queryKey: connectorKeys.paginated(params),
        queryFn: () => connectorsApi.getPage(params),
        enabled,
        placeholderData: keepPreviousData,
        retry: 0,
    });

    const connectors = query.data?.data ?? [];
    const totalPages = query.data?.meta?.pagination?.totalPages ?? 1;
    const totalCount = query.data?.meta?.pagination?.count ?? 0;

    useEffect(() => {
        if (query.isSuccess) {
            setPageNumber(p => Math.min(p, Math.max(totalPages, 1)));
        }
    }, [query.isSuccess, totalPages]);

    let error: string | null = null;
    if (query.isError) {
        error = query.error instanceof Error ? query.error.message : "Could not load connector information.";
    }

    return {
        connectors,
        isLoading: query.isLoading,
        error,
        pageNumber,
        setPageNumber,
        totalPages,
        totalCount,
        searchTerm,
        setSearchTerm,
        refetch: query.refetch,
    };
};
