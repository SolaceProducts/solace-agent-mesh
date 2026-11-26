// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { FeedbackHistoryParams, FeedbackSummariesParams } from "./types";

export const feedbackKeys = {
    all: ["feedback"] as const,
    histories: () => [...feedbackKeys.all, "history"] as const,
    history: (params: FeedbackHistoryParams) => [...feedbackKeys.histories(), params] as const,
    item: (id: string) => [...feedbackKeys.all, "item", id] as const,
};

export const feedbackSummaryKeys = {
    all: ["feedback", "summaries"] as const,
    summary: (params: FeedbackSummariesParams) => [...feedbackSummaryKeys.all, params] as const,
};
