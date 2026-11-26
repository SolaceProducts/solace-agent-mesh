// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import type { FeedbackHistoryParams, FeedbackHistoryResponse, FeedbackSummariesParams, FeedbackSummariesResponse } from "./types";

function buildQuery(params: FeedbackHistoryParams): string {
    const query = new URLSearchParams();
    query.set("pageNumber", String(params.pageNumber));
    query.set("pageSize", String(params.pageSize));
    if (params.userIds?.length) query.set("userIds", params.userIds.join(","));
    if (params.agentIds?.length) query.set("agentIds", params.agentIds.join(","));
    if (params.workflowIds?.length) query.set("workflowIds", params.workflowIds.join(","));
    if (params.rating) query.set("rating", params.rating);
    if (params.startDate) query.set("startDate", params.startDate);
    if (params.endDate) query.set("endDate", params.endDate);
    return query.toString();
}

export async function fetchFeedbackHistory(params: FeedbackHistoryParams): Promise<FeedbackHistoryResponse> {
    return api.webui.get<FeedbackHistoryResponse>(`/api/v1/feedback/all?${buildQuery(params)}`);
}

function buildSummariesQuery(params: FeedbackSummariesParams): string {
    const query = new URLSearchParams();
    query.set("groupBy", params.groupBy);
    query.set("trendGranularity", params.trendGranularity);
    if (params.subjectType) query.set("subjectType", params.subjectType);
    if (params.agentIds?.length) query.set("agentIds", params.agentIds.join(","));
    if (params.workflowIds?.length) query.set("workflowIds", params.workflowIds.join(","));
    if (params.startDate) query.set("startDate", params.startDate);
    if (params.endDate) query.set("endDate", params.endDate);
    if (params.sortBy) query.set("sort", `${params.sortBy}:${params.sortDir ?? "desc"}`);
    if (params.pageNumber) query.set("pageNumber", String(params.pageNumber));
    if (params.pageSize) query.set("pageSize", String(params.pageSize));
    return query.toString();
}

export async function fetchFeedbackSummaries(params: FeedbackSummariesParams): Promise<FeedbackSummariesResponse> {
    return api.webui.get<FeedbackSummariesResponse>(`/api/v1/feedback/summaries?${buildSummariesQuery(params)}`);
}
