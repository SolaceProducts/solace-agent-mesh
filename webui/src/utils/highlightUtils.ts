// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RAGSource } from "@/types";

const MIN_CITATION_LENGTH = 20;

export function extractCitationTexts(citations: RAGSource[]): string[] {
    return citations.map(c => c.contentPreview).filter((t): t is string => !!t && t.length > MIN_CITATION_LENGTH);
}

function escapeRegex(str: string): string {
    return str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function normalizeText(text: string): string {
    return text.replace(/\s+/g, " ").trim();
}

export function highlightCitationsInText(content: string, citations: RAGSource[]): string {
    if (!content || citations.length === 0) {
        return content;
    }

    const citationTexts = citations
        .map(c => c.contentPreview)
        .filter((text): text is string => !!text && text.length > 20)
        .map(text => normalizeText(text));

    const uniqueCitationTexts = [...new Set(citationTexts)];

    if (uniqueCitationTexts.length === 0) {
        return content;
    }

    let highlightedContent = content;

    for (const citationText of uniqueCitationTexts) {
        const pattern = escapeRegex(citationText).replace(/\s+/g, "\\s+");

        try {
            const regex = new RegExp(`(${pattern})`, "gi");
            highlightedContent = highlightedContent.replace(regex, '<mark class="citation-highlight">$1</mark>');
        } catch {
            const exactIndex = highlightedContent.toLowerCase().indexOf(citationText.toLowerCase());
            if (exactIndex !== -1) {
                const originalText = highlightedContent.substring(exactIndex, exactIndex + citationText.length);
                highlightedContent = highlightedContent.substring(0, exactIndex) + `<mark class="citation-highlight">${originalText}</mark>` + highlightedContent.substring(exactIndex + citationText.length);
            }
        }
    }

    return highlightedContent;
}
