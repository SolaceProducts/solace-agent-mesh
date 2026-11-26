// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface ContextUsage {
    sessionId: string;
    currentContextTokens: number;
    promptTokens: number;
    completionTokens: number;
    cachedTokens: number;
    maxInputTokens: number | null;
    usagePercentage: number;
    model: string;
    totalEvents: number;
    totalMessages: number;
    totalTasks: number;
    hasCompaction: boolean;
}

export interface CompactSessionRequest {
    model?: string;
    compactionPercentage?: number;
}

export interface CompactSessionResponse {
    eventsCompacted: number;
    summary: string;
    remainingEvents: number;
    remainingTokens: number;
}
