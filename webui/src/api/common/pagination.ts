// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface ListPagination {
    pageNumber: number;
    count: number;
    pageSize: number;
    nextPage: number | null;
    totalPages: number;
}

export interface PaginatedResponse<T> {
    data: T[];
    meta: {
        pagination: ListPagination;
    };
}

const DEFAULT_PAGE_SIZE = 100;
const MAX_PAGES = 1000;

interface FetchAllPagesParams {
    pageNumber: number;
    pageSize: number;
}

export async function fetchAllPages<T>(fetcher: (params: FetchAllPagesParams) => Promise<PaginatedResponse<T>>): Promise<T[]> {
    const all: T[] = [];
    let pageNumber = 1;

    for (let i = 0; i < MAX_PAGES; i++) {
        const response = await fetcher({ pageNumber, pageSize: DEFAULT_PAGE_SIZE });
        all.push(...(response.data ?? []));

        const nextPage = response.meta?.pagination?.nextPage;
        if (nextPage == null || nextPage === pageNumber) {
            return all;
        }
        pageNumber = nextPage;
    }

    return all;
}
