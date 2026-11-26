// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQuery } from "@tanstack/react-query";

import { type CrossUserFeedback, feedbackKeys, fetchFeedbackHistory } from "@/api/feedback";

import { subjectFilterFor } from "./dashboardFilters";
import { ANY_RATING, ANY_SUBJECT, type RatingFilter } from "./feedbackHistoryUrlState";

export interface TableFilters {
    subjectKey: string;
    rating: RatingFilter;
    userId?: string | null;
    startDate: string;
    endDate?: string;
}

export function isRowWithinFilters(row: CrossUserFeedback, filters: TableFilters): boolean {
    if (filters.rating !== ANY_RATING && row.rating !== filters.rating) return false;
    if (filters.subjectKey !== ANY_SUBJECT && `${row.subjectType}:${row.subjectId}` !== filters.subjectKey) return false;
    if (filters.userId && row.userId !== filters.userId) return false;
    const at = Date.parse(row.createdAt);
    if (filters.endDate !== undefined && at > Date.parse(filters.endDate)) return false;
    return at >= Date.parse(filters.startDate);
}

export function pageOfRank(rank: number, pageSize: number): number {
    return Math.max(1, Math.ceil(rank / pageSize));
}

export function rankQueryParams(row: CrossUserFeedback | null, filters: TableFilters) {
    return {
        pageNumber: 1,
        pageSize: 1,
        ...subjectFilterFor(filters.subjectKey),
        ...(filters.rating === ANY_RATING ? {} : { rating: filters.rating }),
        ...(filters.userId ? { userIds: [filters.userId] } : {}),
        startDate: row?.createdAt ?? filters.startDate,
        ...(filters.endDate === undefined ? {} : { endDate: filters.endDate }),
    };
}

interface UseRowPageArgs {
    row: CrossUserFeedback | null;
    filters: TableFilters;
    pageSize: number;
    enabled: boolean;
}

export function useFeedbackRowPage({ row, filters, pageSize, enabled }: UseRowPageArgs): number | null {
    const params = rankQueryParams(row, filters);

    const { data } = useQuery({
        queryKey: feedbackKeys.history(params),
        queryFn: () => fetchFeedbackHistory(params),
        enabled: enabled && row !== null,
        retry: 0,
    });

    if (!data) return null;
    return pageOfRank(data.meta.pagination.count, pageSize);
}
