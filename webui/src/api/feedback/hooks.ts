// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { keepPreviousData, useQuery, type UseQueryResult } from "@tanstack/react-query";

import { fetchFeedbackItem } from "./item";
import { feedbackKeys, feedbackSummaryKeys } from "./keys";
import { fetchFeedbackHistory, fetchFeedbackSummaries } from "./service";
import type { CrossUserFeedback, FeedbackHistoryParams, FeedbackSummariesParams } from "./types";

export function useFeedbackHistory(params: FeedbackHistoryParams) {
    return useQuery({
        queryKey: feedbackKeys.history(params),
        queryFn: () => fetchFeedbackHistory(params),
        retry: 0,
        placeholderData: keepPreviousData,
    });
}

export function useFeedbackSummaries(params: FeedbackSummariesParams) {
    return useQuery({
        queryKey: feedbackSummaryKeys.summary(params),
        queryFn: () => fetchFeedbackSummaries(params),
        retry: 0,
        placeholderData: keepPreviousData,
    });
}

export function useFeedbackItem(id: string | null | undefined): UseQueryResult<CrossUserFeedback> {
    return useQuery({
        queryKey: feedbackKeys.item(id ?? ""),
        queryFn: () => fetchFeedbackItem(id as string),
        enabled: Boolean(id),
        retry: false,
    });
}
