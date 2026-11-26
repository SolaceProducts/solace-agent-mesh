// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RAGSearchResult } from "@/types";

export const documentSearchRagData: RAGSearchResult[] = [
    {
        query: "quarterly revenue performance",
        searchType: "document_search",
        turnNumber: 1,
        timestamp: new Date().toISOString(),
        taskId: "task-123",
        sources: [
            {
                citationId: "idx0r0",
                filename: "quarterly_report_q4_2024.pdf",
                contentPreview: "Revenue increased by 15% in Q4 compared to the previous quarter. The growth was primarily driven by strong performance in the enterprise segment...",
                relevanceScore: 0.95,
                metadata: {
                    location_range: "Pages 3-5",
                    primary_location: "Page 3",
                },
            },
            {
                citationId: "idx0r1",
                filename: "quarterly_report_q4_2024.pdf",
                contentPreview: "Operating expenses decreased by 8% due to cost optimization initiatives implemented in Q3...",
                relevanceScore: 0.91,
                metadata: {
                    location_range: "Page 5",
                    primary_location: "Page 5",
                },
            },
            {
                citationId: "idx0r2",
                filename: "quarterly_report_q4_2024.pdf",
                contentPreview: "Net profit margin improved to 18.5%, up from 15.2% in the same quarter last year...",
                relevanceScore: 0.89,
                metadata: {
                    location_range: "Page 3",
                    primary_location: "Page 3",
                },
            },
            {
                citationId: "idx0r3",
                filename: "annual_summary_2024.pdf",
                contentPreview: "Key highlights from the fiscal year include record revenue of $4.2B and successful expansion into three new markets...",
                relevanceScore: 0.87,
                metadata: {
                    location_range: "Pages 10-12",
                    primary_location: "Page 10",
                },
            },
            {
                citationId: "idx0r4",
                filename: "annual_summary_2024.pdf",
                contentPreview: "Customer acquisition cost decreased by 22% while customer lifetime value increased by 35%...",
                relevanceScore: 0.85,
                metadata: {
                    location_range: "Page 15",
                    primary_location: "Page 15",
                },
            },
            {
                citationId: "idx0r5",
                filename: "market_analysis_report.txt",
                contentPreview: "Market trends indicate strong demand for AI-powered solutions in the healthcare sector...",
                relevanceScore: 0.82,
                metadata: {
                    location_range: "Page 7",
                    primary_location: "Page 7",
                },
            },
        ],
    },
];

export const singleDocumentSinglePage: RAGSearchResult[] = [
    {
        query: "product specifications",
        searchType: "document_search",
        turnNumber: 1,
        timestamp: new Date().toISOString(),
        taskId: "task-456",
        sources: [
            {
                citationId: "idx1r0",
                filename: "product_specs.pdf",
                contentPreview: "The product specifications include detailed measurements and material requirements...",
                relevanceScore: 0.93,
                metadata: {
                    location_range: "Page 1",
                    primary_location: "Page 1",
                },
            },
        ],
    },
];

export const documentWithNoPageMetadata: RAGSearchResult[] = [
    {
        query: "unknown document location",
        searchType: "document_search",
        turnNumber: 1,
        timestamp: new Date().toISOString(),
        taskId: "task-789",
        sources: [
            {
                citationId: "idx2r0",
                filename: "legacy_document.pdf",
                contentPreview: "This document contains important historical data...",
                relevanceScore: 0.88,
                metadata: {},
            },
            {
                citationId: "idx2r1",
                filename: "legacy_document.pdf",
                contentPreview: "Additional context from the same document...",
                relevanceScore: 0.84,
                metadata: {},
            },
        ],
    },
];

export const multipleDocumentsOverlappingPages: RAGSearchResult[] = [
    {
        query: "financial projections",
        searchType: "document_search",
        turnNumber: 1,
        timestamp: new Date().toISOString(),
        taskId: "task-101",
        sources: [
            {
                citationId: "idx3r0",
                filename: "budget_2025.pdf",
                contentPreview: "Budget projections for Q1 2025...",
                relevanceScore: 0.94,
                metadata: {
                    location_range: "Page 3",
                    primary_location: "Page 3",
                },
            },
            {
                citationId: "idx3r1",
                filename: "forecast_report.pdf",
                contentPreview: "Revenue forecast indicates 20% growth...",
                relevanceScore: 0.92,
                metadata: {
                    location_range: "Page 3",
                    primary_location: "Page 3",
                },
            },
            {
                citationId: "idx3r2",
                filename: "budget_2025.pdf",
                contentPreview: "Operating expense projections...",
                relevanceScore: 0.9,
                metadata: {
                    location_range: "Page 3",
                    primary_location: "Page 3",
                },
            },
            {
                citationId: "idx3r3",
                filename: "forecast_report.pdf",
                contentPreview: "Market expansion strategy details...",
                relevanceScore: 0.88,
                metadata: {
                    location_range: "Page 5",
                    primary_location: "Page 5",
                },
            },
        ],
    },
];
