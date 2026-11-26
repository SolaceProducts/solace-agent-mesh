// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { RAGSearchResult, RAGSource } from "@/types/rag";

import { aggregateRagSources, sanitizeRagData } from "./sourceUrlHelpers";

describe("sanitizeRagData", () => {
    const source = (citationId: string): RAGSource => ({ citationId, metadata: {}, contentPreview: "", relevanceScore: 1 });
    const search = (over: Partial<RAGSearchResult> = {}): RAGSearchResult => ({ query: "q", searchType: "deep_research", timestamp: "t", sources: [source("a")], ...over }) as RAGSearchResult;

    it("returns [] for null/undefined input", () => {
        expect(sanitizeRagData(undefined)).toEqual([]);
        expect(sanitizeRagData(null as unknown as RAGSearchResult[])).toEqual([]);
    });

    it("drops null/undefined source elements within a search", () => {
        const dirty = [search({ sources: [source("a"), undefined, null, source("b")] as unknown as RAGSearchResult["sources"] })];
        const [clean] = sanitizeRagData(dirty);
        expect(clean.sources).toHaveLength(2);
        expect(clean.sources.map(s => s.citationId)).toEqual(["a", "b"]);
    });

    it("drops null/undefined search entries", () => {
        const dirty = [search(), null, undefined, search({ query: "q2" })] as unknown as RAGSearchResult[];
        const clean = sanitizeRagData(dirty);
        expect(clean).toHaveLength(2);
        expect(clean.map(s => s.query)).toEqual(["q", "q2"]);
    });

    it("normalizes a missing sources array to []", () => {
        const dirty = [search({ sources: undefined as unknown as RAGSearchResult["sources"] })];
        const [clean] = sanitizeRagData(dirty);
        expect(clean.sources).toEqual([]);
    });

    it("leaves well-formed data intact (new array, same content)", () => {
        const ok = [search({ sources: [source("a"), source("b")] })];
        const clean = sanitizeRagData(ok);
        expect(clean).toHaveLength(1);
        expect(clean[0].sources).toHaveLength(2);
        expect(clean).not.toBe(ok);
    });
});

describe("aggregateRagSources", () => {
    const source = (citationId: string): RAGSource => ({ citationId, metadata: {}, contentPreview: "", relevanceScore: 1 });
    const search = (over: Partial<RAGSearchResult> = {}): RAGSearchResult => ({ query: "q", searchType: "deep_research", timestamp: "t", sources: [], ...over }) as RAGSearchResult;

    it("returns undefined for an empty list", () => {
        expect(aggregateRagSources([])).toBeUndefined();
    });

    it("returns the sole entry unchanged (same reference) for a single-element list", () => {
        const only = search({ sources: [source("research0")] });
        expect(aggregateRagSources([only])).toBe(only);
    });

    it("unions sources across entries, deduped by citationId, keeping the last entry as base", () => {
        const first = search({ query: "q1", title: "T1", sources: [source("research0"), source("research1")] });
        const second = search({ query: "q2", title: "T2", sources: [source("research1"), source("research2")] });
        const agg = aggregateRagSources([first, second])!;

        expect(agg.query).toBe("q2");
        expect(agg.title).toBe("T2");
        expect(agg.sources.map(s => s.citationId)).toEqual(["research0", "research1", "research2"]);
    });

    it("skips sources without a citationId", () => {
        const entry = search({ sources: [source("research0"), { metadata: {}, contentPreview: "", relevanceScore: 1 } as RAGSource] });
        const agg = aggregateRagSources([entry, search({ sources: [source("research1")] })])!;
        expect(agg.sources.map(s => s.citationId)).toEqual(["research0", "research1"]);
    });
});
