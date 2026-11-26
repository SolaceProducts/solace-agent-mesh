// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/* eslint-disable @typescript-eslint/no-explicit-any */

export interface SearchSource {
    link?: string;
    title?: string;
    snippet?: string;
    attribution?: string;
    processed?: boolean;
    sourceType?: string;
    filename?: string;
}

export interface RAGSource {
    citationId: string;
    fileId?: string;
    filename?: string;
    title?: string;
    sourceType?: string;
    sourceUrl?: string;
    url?: string;
    contentPreview: string;
    relevanceScore: number;
    retrievedAt?: string;
    metadata: Record<string, any>;
}

export const RAG_SEARCH_TYPES = ["file_search", "kb_search", "deep_research", "web_search", "document_search"] as const;

export type RAGSearchType = (typeof RAG_SEARCH_TYPES)[number];

export function isRAGSearchType(value: unknown): value is RAGSearchType {
    return typeof value === "string" && (RAG_SEARCH_TYPES as readonly string[]).includes(value);
}

export interface RAGSearchResult {
    query: string;
    title?: string;
    searchType: RAGSearchType;
    turnNumber?: number;
    searchTurn?: number;
    timestamp: string;
    sources: RAGSource[];
    taskId?: string;
    isComplete?: boolean;
    metadata?: {
        queries?: Array<{
            query: string;
            timestamp: string;
            sourceCitationIds: string[];
        }>;
        [key: string]: any;
    };
}

export interface RAGSearchResultEvent {
    type: "rag_search_result";
    data: {
        ragMetadata: {
            query: string;
            searchType: string;
            timestamp: string;
            sources: RAGSource[];
        };
    };
}
