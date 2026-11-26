// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RAGSearchResult, RAGSource } from "@/types/rag";

export { getCleanDomain } from "./url";

export const CITATION_PATTERN = /\[?\[cite:(s\d+r\d+|idx\d+r\d+|research\d+)\]\]?/g;

export const MULTI_CITATION_PATTERN = /\[?\[cite:((?:s\d+r\d+|idx\d+r\d+|research\d+)(?:\s*,\s*(?:cite:)?(?:s\d+r\d+|idx\d+r\d+|research\d+))+)\]\]?/g;

export const INDIVIDUAL_CITATION_PATTERN = /(?:cite:)?(s\d+r\d+|idx\d+r\d+|research\d+)/g;

export const CLEANUP_REGEX = /\[?\[cite:[^\]]+\]\]?/g;

export interface Citation {
    marker: string;
    type: "search" | "research" | "document";
    sourceId: number;
    position: number;
    source?: RAGSource;
    citationId: string;
}

export type { RAGSearchResult as RAGMetadata, RAGSource };

const SEARCH_CITATION_ID_PATTERN = /^s(\d+)r(\d+)$/;
const INDEX_SEARCH_CITATION_ID_PATTERN = /^idx(\d+)r(\d+)$/;
const RESEARCH_CITATION_ID_PATTERN = /^research(\d+)$/;

export function parseCitationId(citationId: string): { type: "search" | "research" | "document"; sourceId: number } | null {
    const searchMatch = citationId.match(SEARCH_CITATION_ID_PATTERN);
    if (searchMatch) {
        return {
            type: "search",
            sourceId: parseInt(searchMatch[2], 10),
        };
    }

    const indexMatch = citationId.match(INDEX_SEARCH_CITATION_ID_PATTERN);
    if (indexMatch) {
        return {
            type: "document",
            sourceId: parseInt(indexMatch[2], 10),
        };
    }

    const researchMatch = citationId.match(RESEARCH_CITATION_ID_PATTERN);
    if (researchMatch) {
        return {
            type: "research",
            sourceId: parseInt(researchMatch[1], 10),
        };
    }

    return null;
}

function parseMultiCitationContent(content: string, position: number, fullMatch: string, ragMetadata?: RAGSearchResult): Citation[] {
    const citations: Citation[] = [];
    let individualMatch;

    INDIVIDUAL_CITATION_PATTERN.lastIndex = 0;

    while ((individualMatch = INDIVIDUAL_CITATION_PATTERN.exec(content)) !== null) {
        const citationId = individualMatch[1];
        const parsed = parseCitationId(citationId);

        if (parsed) {
            const citation: Citation = {
                marker: fullMatch,
                type: parsed.type,
                sourceId: parsed.sourceId,
                position: position,
                citationId: citationId,
            };

            if (ragMetadata?.sources) {
                citation.source = ragMetadata.sources.find((s: RAGSource) => s.citationId === citationId);
            }

            citations.push(citation);
        }
    }

    return citations;
}

export function parseCitations(text: string, ragMetadata?: RAGSearchResult): Citation[] {
    const citations: Citation[] = [];
    const processedPositions = new Set<number>();
    let match;

    MULTI_CITATION_PATTERN.lastIndex = 0;
    while ((match = MULTI_CITATION_PATTERN.exec(text)) !== null) {
        const [fullMatch, content] = match;
        const multiCitations = parseMultiCitationContent(content, match.index, fullMatch, ragMetadata);
        citations.push(...multiCitations);
        processedPositions.add(match.index);
    }

    CITATION_PATTERN.lastIndex = 0;

    while ((match = CITATION_PATTERN.exec(text)) !== null) {
        if (processedPositions.has(match.index)) {
            continue;
        }

        const afterMatch = text.substring(match.index + match[0].length);
        const beforeMatch = text.substring(0, match.index);

        if (afterMatch.match(/^\s*,\s*(?:s\d+r\d+|research\d+)/)) {
            continue;
        }

        if (beforeMatch.match(/\[?\[cite:[^\]]*,\s*$/)) {
            continue;
        }

        const [fullMatch, citationId] = match;
        const parsed = parseCitationId(citationId);

        if (parsed) {
            const citation: Citation = {
                marker: fullMatch,
                type: parsed.type,
                sourceId: parsed.sourceId,
                position: match.index,
                citationId: citationId,
            };

            if (ragMetadata?.sources) {
                citation.source = ragMetadata.sources.find((s: RAGSource) => s.citationId === citationId);

                if (!citation.source && ragMetadata.sources.length > 0) {
                    console.log(
                        `Citation ${citationId} not found in sources:`,
                        ragMetadata.sources.map(s => s.citationId)
                    );
                }
            }

            citations.push(citation);
        }
    }

    citations.sort((a, b) => a.position - b.position);

    return citations;
}

export function removeCitationMarkers(text: string): string {
    return text.replace(CITATION_PATTERN, "");
}

export function splitTextWithCitations(text: string, citations: Citation[]): Array<{ text: string; citation?: Citation }> {
    if (citations.length === 0) {
        return [{ text }];
    }

    const segments: Array<{ text: string; citation?: Citation }> = [];
    let lastIndex = 0;

    const sortedCitations = [...citations].sort((a, b) => a.position - b.position);

    for (const citation of sortedCitations) {
        if (citation.position > lastIndex) {
            segments.push({
                text: text.substring(lastIndex, citation.position),
            });
        }

        segments.push({
            text: citation.marker,
            citation,
        });

        lastIndex = citation.position + citation.marker.length;
    }

    if (lastIndex < text.length) {
        segments.push({
            text: text.substring(lastIndex),
        });
    }

    return segments;
}

export function groupCitationsByParagraph(text: string, citations: Citation[]): Array<{ text: string; citations: Citation[] }> {
    if (citations.length === 0) {
        return [{ text, citations: [] }];
    }

    const paragraphs = text.split(/\n\n|\n/);
    const result: Array<{ text: string; citations: Citation[] }> = [];

    let currentPosition = 0;

    for (const paragraph of paragraphs) {
        const paragraphStart = currentPosition;
        const paragraphEnd = paragraphStart + paragraph.length;

        const paragraphCitations = citations.filter(citation => citation.position >= paragraphStart && citation.position < paragraphEnd);

        let cleanText = paragraph;
        for (const citation of paragraphCitations.sort((a, b) => b.position - a.position)) {
            const relativePos = citation.position - paragraphStart;
            cleanText = cleanText.substring(0, relativePos) + cleanText.substring(relativePos + citation.marker.length);
        }

        result.push({
            text: cleanText,
            citations: paragraphCitations,
        });

        currentPosition = paragraphEnd + (text[paragraphEnd] === "\n" && text[paragraphEnd + 1] === "\n" ? 2 : 1);
    }

    return result;
}

export function getCitationNumber(citation: Citation): number {
    return citation.sourceId + 1;
}

export function getCitationTooltip(citation: Citation): string {
    const isWebSearch = citation.source?.metadata?.type === "web_search" || citation.type === "search";
    const isDeepResearch = citation.source?.metadata?.type === "deep_research" || citation.type === "research";
    const sourceUrl = citation.source?.sourceUrl || citation.source?.url || citation.source?.metadata?.link;

    if ((isWebSearch || isDeepResearch) && sourceUrl) {
        const title = citation.source?.metadata?.title || citation.source?.filename;
        if (title && title !== sourceUrl) {
            return `${title}\n${sourceUrl}`;
        }
        return sourceUrl;
    }

    if (!citation.source) {
        return `Source ${getCitationNumber(citation)}`;
    }

    if (citation.type === "document" && citation.source.metadata?.location_range) {
        const score = (citation.source.relevanceScore * 100).toFixed(1);
        return `${citation.source.filename}\n${citation.source.metadata.location_range}\n${score}% relevance`;
    }

    const score = (citation.source.relevanceScore * 100).toFixed(1);
    return `${citation.source.filename} (${score}% relevance)`;
}

export function getCitationLink(citation: Citation): string | undefined {
    return citation.source?.sourceUrl;
}
