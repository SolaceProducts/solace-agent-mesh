// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { RAGSearchResult, RAGSource } from "@/types/rag";

import { parseCitations } from "./citations";
import { aggregateRagSources } from "./sourceUrlHelpers";

const src = (citationId: string, url: string): RAGSource => ({
    citationId,
    title: url,
    url,
    sourceUrl: url,
    contentPreview: "",
    relevanceScore: 1,
    metadata: { link: url, type: "web_search" },
});

const entry = (over: Partial<RAGSearchResult>): RAGSearchResult => ({ query: "q", searchType: "deep_research", timestamp: "t", sources: [], ...over }) as RAGSearchResult;

describe("parseCitations — deep research research{N} markers", () => {
    it("resolves each research{N} marker to its source by citationId", () => {
        const meta = entry({ sources: [src("research0", "https://a.com"), src("research1", "https://b.com")] });
        const citations = parseCitations("Fact one.[[cite:research0]] Fact two.[[cite:research1]]", meta);

        expect(citations).toHaveLength(2);
        expect(citations[0].type).toBe("research");
        expect(citations[0].source?.url).toBe("https://a.com");
        expect(citations[1].source?.url).toBe("https://b.com");
    });

    it("leaves source undefined when the marker's citationId is absent (drives the 'Web Source N' fallback)", () => {
        const meta = entry({ sources: [src("research0", "https://a.com")] });
        const citations = parseCitations("Claim.[[cite:research3]]", meta);

        expect(citations).toHaveLength(1);
        expect(citations[0].sourceId).toBe(3);
        expect(citations[0].source).toBeUndefined();
    });

    it("resolves markers spread across multiple RAG entries once they are aggregated", () => {
        const first = entry({ query: "q1", sources: [src("research0", "https://a.com"), src("research1", "https://b.com")] });
        const last = entry({ query: "q2", sources: [src("research2", "https://c.com")] });

        const lastOnly = parseCitations("A.[[cite:research0]] C.[[cite:research2]]", last);
        expect(lastOnly[0].source).toBeUndefined();
        expect(lastOnly[1].source?.url).toBe("https://c.com");

        const aggregated = aggregateRagSources([first, last]);
        const both = parseCitations("A.[[cite:research0]] C.[[cite:research2]]", aggregated);
        expect(both[0].source?.url).toBe("https://a.com");
        expect(both[1].source?.url).toBe("https://c.com");
    });
});
