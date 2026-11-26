// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { PaginatedResponse } from "@/api/common";

export interface CrossUserFeedback {
    id: string;
    sessionId: string;
    taskId: string;
    userId: string;
    rating: "up" | "down";
    comment: string | null;
    shareTaskContent: boolean;
    subjectId: string;
    subjectType: "agent" | "workflow" | "unknown";
    subjectName: string;
    entrypointType: string;
    createdAt: string;
}

export interface FeedbackHistoryParams {
    pageNumber: number;
    pageSize: number;
    userIds?: string[];
    agentIds?: string[];
    workflowIds?: string[];
    rating?: "up" | "down";
    startDate?: string;
    endDate?: string;
}

export type FeedbackHistoryResponse = PaginatedResponse<CrossUserFeedback>;

export type FeedbackSubjectType = "agent" | "workflow";

export interface FeedbackSubjectRef {
    id: string;
    name: string;
    type: FeedbackSubjectType;
}

export interface FeedbackTrendPoint {
    date: string;
    totalActivities: number;
    positiveFeedback: number;
    negativeFeedback: number;
    negativeRatio: number;
}

export interface FeedbackSummaryRow {
    subject?: FeedbackSubjectRef;
    totalActivities: number;
    positiveFeedback: number;
    negativeFeedback: number;
    negativeRatio: number;
    trend: FeedbackTrendPoint[];
}

export type FeedbackSummarySort = "name" | "type" | "totalActivities" | "positiveFeedback" | "negativeFeedback" | "negativeRatio";

export type TrendBucketGranularity = "hour" | "day" | "week";

export interface FeedbackSummariesParams {
    groupBy: "subject" | "tenant";
    trendGranularity: TrendBucketGranularity | "none";
    subjectType?: FeedbackSubjectType;
    agentIds?: string[];
    workflowIds?: string[];
    startDate?: string;
    endDate?: string;
    sortBy?: FeedbackSummarySort;
    sortDir?: "asc" | "desc";
    pageNumber?: number;
    pageSize?: number;
}

export type FeedbackSummariesResponse = PaginatedResponse<FeedbackSummaryRow>;
