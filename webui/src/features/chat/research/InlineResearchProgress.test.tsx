// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { RAGSearchResult } from "@/types";

import { InlineResearchProgress, type ResearchProgressData } from "./InlineResearchProgress";

describe("InlineResearchProgress — malformed ragData resilience", () => {
    const progress: ResearchProgressData = {
        type: "deep_research_progress",
        phase: "writing",
        status_text: "done",
        progress_percentage: 100,
        current_iteration: 1,
        total_iterations: 1,
        sources_found: 0,
        current_query: "",
        fetching_urls: [],
        elapsed_seconds: 1,
        max_runtime_seconds: 60,
    };
    const goodSource = { citationId: "a", metadata: { fetched: true }, url: "https://x", title: "X", contentPreview: "", relevanceScore: 1 };
    const search = (sources: unknown): RAGSearchResult => ({ query: "q", searchType: "deep_research", timestamp: "t", taskId: "task-1", sources }) as RAGSearchResult;

    const cases: Record<string, RAGSearchResult[]> = {
        "null/undefined source elements": [search([goodSource, null, undefined])],
        "undefined sources array": [search(undefined)],
        "null search entry": [search([goodSource]), null as unknown as RAGSearchResult],
        "multiple entries with holes": [search([goodSource, null]), search(undefined), search([undefined])],
    };

    for (const [name, ragData] of Object.entries(cases)) {
        it(`renders without throwing: ${name}`, () => {
            expect(() => render(<InlineResearchProgress progress={progress} isComplete ragData={ragData} />)).not.toThrow();
        });
    }
});
