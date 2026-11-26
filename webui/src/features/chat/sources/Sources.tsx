// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import type { RAGSource, SearchSource } from "@/types/rag";

import { StackedFavicons } from "./StackedFavicons";

interface SourcesProps {
    isDeepResearch?: boolean;
    onDeepResearchClick?: () => void;
}

export function Sources({ ragMetadata, isDeepResearch = false, onDeepResearchClick }: SourcesProps & { ragMetadata?: { sources?: RAGSource[] } }) {
    const webSources = useMemo(() => {
        if (!ragMetadata?.sources) {
            return [];
        }

        const sources: SearchSource[] = [];
        const seenSources = new Set<string>();

        ragMetadata.sources.forEach((s: RAGSource) => {
            const sourceType = s.sourceType || "web";

            if (sourceType === "document" || (!s.sourceUrl && !s.metadata?.link && s.filename)) {
                const source: SearchSource = {
                    filename: s.filename || "Unknown document",
                    title: s.metadata?.title || s.filename || "Unknown document",
                    snippet: s.contentPreview ?? "",
                    attribution: s.filename ?? "",
                    processed: false,
                    sourceType: "document",
                };

                const uniqueKey = `document:${source.filename}`;

                if (seenSources.has(uniqueKey)) {
                    return;
                }
                seenSources.add(uniqueKey);
                sources.push(source);
                return;
            }

            let link: string;
            let title: string;

            if (sourceType === "image") {
                link = s.sourceUrl || s.metadata?.link || "";
                title = s.metadata?.title || s.filename || "Image source";
            } else {
                link = s.sourceUrl || s.metadata?.link || "";
                title = s.metadata?.title || s.filename || "";
            }

            const source: SearchSource = {
                link,
                title,
                snippet: s.contentPreview ?? "",
                attribution: s.filename ?? "",
                processed: false,
                sourceType: sourceType,
            };

            const uniqueKey = `${sourceType}:${source.link}:${source.title}`;

            if (!source.link || seenSources.has(uniqueKey)) {
                return;
            }
            seenSources.add(uniqueKey);

            sources.push(source);
        });

        return sources;
    }, [ragMetadata]);

    if (webSources.length === 0) {
        return null;
    }

    return (
        <div
            className={`flex items-center gap-2 rounded border border-(--secondary-w20) px-2 py-1 ${onDeepResearchClick ? "cursor-pointer transition-colors hover:bg-(--secondary-w10)" : ""}`}
            role={onDeepResearchClick ? "button" : undefined}
            tabIndex={onDeepResearchClick ? 0 : undefined}
            aria-label={isDeepResearch ? "View deep research sources" : "View web search sources"}
            onClick={onDeepResearchClick}
            onKeyDown={
                onDeepResearchClick
                    ? e => {
                          if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              onDeepResearchClick();
                          }
                      }
                    : undefined
            }
        >
            <StackedFavicons sources={webSources} end={3} size={16} />
            <span className="text-sm text-(--secondary-text-wMain)">
                {webSources.length} {webSources.length === 1 ? "source" : "sources"}
            </span>
        </div>
    );
}

export default Sources;
