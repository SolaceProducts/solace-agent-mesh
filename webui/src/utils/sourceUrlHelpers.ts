// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Citation as CitationType } from "@/utils/citations";

import type { RAGSearchResult, RAGSource } from "@/types/rag";

export function sanitizeRagData(ragData: RAGSearchResult[] | undefined): RAGSearchResult[] {
    return (ragData ?? []).filter(Boolean).map(search => ({
        ...search,
        sources: (search.sources ?? []).filter(Boolean),
    }));
}

export function aggregateRagSources(entries: RAGSearchResult[]): RAGSearchResult | undefined {
    if (entries.length === 0) return undefined;
    if (entries.length === 1) return entries[0];

    const base = entries.at(-1)!;
    const seen = new Set<string>();
    const sources = entries
        .flatMap(entry => entry.sources ?? [])
        .filter(source => {
            const id = source?.citationId;
            if (!id || seen.has(id)) return false;
            seen.add(id);
            return true;
        });

    return { ...base, sources };
}

export type SourceType = "web" | "image" | "file" | "web_search" | "deep_research" | "kb_search" | "file_search";

export interface SourceUrlInfo {
    url: string | null;
    hasUrl: boolean;
    sourceType: SourceType;
}

type SourceLike = RAGSource | CitationType["source"] | null | undefined;

export function getSourceUrl(source: SourceLike): SourceUrlInfo {
    if (!source) {
        return { url: null, hasUrl: false, sourceType: "web" };
    }

    const sourceType = (source.sourceType || source.metadata?.type || "web") as SourceType;

    const url = source.sourceUrl || source.url || source.metadata?.link || null;

    return {
        url,
        hasUrl: !!url,
        sourceType,
    };
}

export function hasSourcesWithUrls(ragData: RAGSearchResult[] | undefined): boolean {
    if (!ragData?.length) return false;

    return ragData.some(search => search.sources?.some(source => getSourceUrl(source).hasUrl) ?? false);
}

export function getSourcesWithUrls(ragData: RAGSearchResult[]): RAGSource[] {
    return ragData.flatMap(search => (search.sources ?? []).filter(source => getSourceUrl(source).hasUrl));
}

export function isWebBasedSource(sourceType: SourceType | string | undefined): boolean {
    return sourceType === "web_search" || sourceType === "deep_research" || sourceType === "web";
}

export function hasClickableExternalUrl(source: SourceLike): boolean {
    const { hasUrl, sourceType } = getSourceUrl(source);
    return hasUrl && isWebBasedSource(sourceType);
}

export function hasDocumentSearchResults(ragData: RAGSearchResult[] | undefined): boolean {
    if (!ragData?.length) return false;
    return ragData.some(search => search.searchType === "document_search");
}
