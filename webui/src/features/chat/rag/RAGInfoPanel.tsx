// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Brain, ChevronDown, ChevronUp, ExternalLink, FileText, Globe, Link2, Search, TrendingUp } from "lucide-react";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/ui/tabs";

import type { RAGSearchResult } from "@/types";

interface TimelineEvent {
    type: "thinking" | "search" | "read";
    timestamp: string;
    content: string;
    url?: string;
    favicon?: string;
    title?: string;
    sourceType?: string;
}

interface RAGInfoPanelProps {
    ragData: RAGSearchResult[] | null;
    enabled: boolean;
}

const extractFilename = (filename: string | undefined): string => {
    if (!filename) return "Unknown";

    let cleaned = filename.replace(/\.pdf$/, "");

    cleaned = cleaned.replace(/_v\d+$/, "");

    const sessionPattern = /^.*web-session-[a-f0-9-]+_/;
    cleaned = cleaned.replace(sessionPattern, "");

    return cleaned + ".pdf";
};

const SourceCard: React.FC<{
    source: RAGSearchResult["sources"][0];
}> = ({ source }) => {
    const [isExpanded, setIsExpanded] = React.useState(false);
    const contentPreview = source.contentPreview;
    const sourceType = source.sourceType || "web";

    let sourceUrl: string;
    let displayTitle: string;

    if (sourceType === "image") {
        sourceUrl = source.sourceUrl || source.metadata?.link || "";
        displayTitle = source.metadata?.title || source.filename || "Image source";
    } else {
        sourceUrl = source.sourceUrl || source.url || "";
        displayTitle = source.title || source.filename || extractFilename(source.fileId);
    }

    const hasRealContent = contentPreview && contentPreview !== "Reading...";
    const shouldTruncate = hasRealContent && contentPreview.length > 200;
    const displayContent = shouldTruncate && !isExpanded ? contentPreview.substring(0, 200) + "..." : contentPreview;

    const showScore = source.relevanceScore !== 1.0;

    return (
        <div className="flex flex-col rounded border bg-(--secondary-w10) p-3">
            <div className="mb-2 flex flex-shrink-0 items-center justify-between">
                <div className="flex min-w-0 flex-1 items-center gap-2">
                    <FileText className="h-3 w-3 flex-shrink-0 text-(--secondary-text-wMain)" />
                    {sourceUrl ? (
                        <a href={sourceUrl} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1 truncate text-xs font-medium text-(--primary-wMain) hover:text-(--primary-w60) hover:underline" title={displayTitle}>
                            <span className="truncate">{displayTitle}</span>
                            <ExternalLink className="h-2.5 w-2.5 flex-shrink-0" />
                        </a>
                    ) : (
                        <span className="truncate text-xs font-medium" title={displayTitle}>
                            {displayTitle}
                        </span>
                    )}
                </div>
                {showScore && (
                    <div className="ml-2 flex flex-shrink-0 items-center gap-1 text-xs font-medium">
                        <TrendingUp className="h-3 w-3" />
                        <span>Score: {source.relevanceScore.toFixed(2)}</span>
                    </div>
                )}
            </div>

            {hasRealContent && <div className={`overflow-hidden text-xs leading-relaxed break-words whitespace-pre-wrap text-(--secondary-text-wMain) ${isExpanded ? "" : "h-[72px]"}`}>{displayContent}</div>}

            {shouldTruncate && (
                <button onClick={() => setIsExpanded(!isExpanded)} className="mt-2 flex flex-shrink-0 items-center gap-1 text-xs text-(--primary-wMain) hover:underline">
                    {isExpanded ? (
                        <>
                            <ChevronUp className="h-3 w-3" />
                            Show less
                        </>
                    ) : (
                        <>
                            <ChevronDown className="h-3 w-3" />
                            Show more
                        </>
                    )}
                </button>
            )}

            {source.metadata && Object.keys(source.metadata).length > 0 && (
                <div className="mt-2 flex-shrink-0 border-t pt-2">
                    <details className="text-xs">
                        <summary className="cursor-pointer text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)">Metadata</summary>
                        <div className="mt-1 space-y-1 pl-2">
                            {Object.entries(source.metadata).map(([key, value]) => (
                                <div key={key} className="flex gap-2">
                                    <span className="font-medium">{key}:</span>
                                    <span className="text-(--secondary-text-wMain)">{typeof value === "object" ? JSON.stringify(value) : String(value)}</span>
                                </div>
                            ))}
                        </div>
                    </details>
                </div>
            )}
        </div>
    );
};

export const RAGInfoPanel: React.FC<RAGInfoPanelProps> = ({ ragData, enabled }) => {
    if (!enabled) {
        return (
            <div className="flex h-full items-center justify-center p-4">
                <div className="text-center text-(--secondary-text-wMain)">
                    <Link2 className="mx-auto mb-4 h-12 w-12 opacity-50" />
                    <div className="text-lg font-medium">RAG Sources</div>
                    <div className="mt-2 text-sm">RAG source visibility is disabled in settings</div>
                </div>
            </div>
        );
    }

    if (!ragData || ragData.length === 0) {
        return (
            <div className="flex h-full items-center justify-center p-4">
                <div className="text-center text-(--secondary-text-wMain)">
                    <Search className="mx-auto mb-4 h-12 w-12 opacity-50" />
                    <div className="text-lg font-medium">Sources</div>
                    <div className="mt-2 text-sm">No sources available yet</div>
                    <div className="mt-1 text-xs">Sources from web research will appear here after completion</div>
                </div>
            </div>
        );
    }

    const isAllDeepResearch = ragData.every(search => search.searchType === "deep_research" || search.searchType === "web_search");

    const totalSources = ragData.reduce((sum, search) => {
        const validSources = search.sources.filter(s => {
            const sourceType = s.sourceType || "web";
            if (sourceType === "image") {
                return s.sourceUrl || s.metadata?.link;
            }
            return true;
        });
        return sum + validSources.length;
    }, 0);

    const SimpleSourceItem: React.FC<{ source: RAGSearchResult["sources"][0] }> = ({ source }) => {
        const sourceType = source.sourceType || "web";

        let url: string;
        let title: string;

        if (sourceType === "image") {
            url = source.sourceUrl || source.metadata?.link || "";
            title = source.metadata?.title || source.filename || "Image source";
        } else {
            url = source.url || source.sourceUrl || "";
            title = source.title || source.filename || "Unknown";
        }

        const favicon = source.metadata?.favicon || (url ? `https://www.google.com/s2/favicons?domain=${url}&sz=32` : "");

        return (
            <div className="-mx-2 flex items-center gap-2 rounded px-2 py-1.5 hover:bg-(--secondary-w10)">
                {favicon && (
                    <img
                        src={favicon}
                        alt=""
                        className="h-4 w-4 flex-shrink-0 rounded"
                        onError={e => {
                            (e.target as HTMLImageElement).style.display = "none";
                        }}
                    />
                )}
                {url ? (
                    <a href={url} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1 truncate text-sm text-(--primary-wMain) hover:text-(--primary-w60) hover:underline" title={title}>
                        <span className="truncate">{title}</span>
                        <ExternalLink className="h-3 w-3 flex-shrink-0" />
                    </a>
                ) : (
                    <span className="truncate text-sm" title={title}>
                        {title}
                    </span>
                )}
            </div>
        );
    };

    const isSourceFullyFetched = (source: RAGSearchResult["sources"][0]): boolean => {
        return source.metadata?.fetched === true || source.metadata?.fetch_status === "success" || (source.contentPreview ? source.contentPreview.includes("[Full Content Fetched]") : false);
    };

    const { fullyReadSources, snippetSources, allUniqueSources } = (() => {
        if (!isAllDeepResearch) return { fullyReadSources: [], snippetSources: [], allUniqueSources: [] };

        const fullyReadMap = new Map<string, RAGSearchResult["sources"][0]>();
        const snippetMap = new Map<string, RAGSearchResult["sources"][0]>();

        const isWebSearch = ragData.some(search => search.searchType === "web_search");
        const isDeepResearch = ragData.some(search => search.searchType === "deep_research");

        ragData.forEach(search => {
            search.sources.forEach(source => {
                const sourceType = source.sourceType || "web";

                if (sourceType === "image") {
                    const sourceLink = source.sourceUrl || source.metadata?.link;
                    if (!sourceLink) {
                        return;
                    }
                    if (!fullyReadMap.has(sourceLink)) {
                        fullyReadMap.set(sourceLink, source);
                    }
                    return;
                }

                const key = source.url || source.sourceUrl || source.title || "";
                if (!key) return;

                if (isWebSearch && !isDeepResearch) {
                    if (!fullyReadMap.has(key)) {
                        fullyReadMap.set(key, source);
                    }
                    return;
                }

                const wasFetched = isSourceFullyFetched(source);
                if (wasFetched) {
                    if (!fullyReadMap.has(key)) {
                        fullyReadMap.set(key, source);
                    }
                    snippetMap.delete(key);
                } else {
                    if (!fullyReadMap.has(key) && !snippetMap.has(key)) {
                        snippetMap.set(key, source);
                    }
                }
            });
        });

        const fullyRead = Array.from(fullyReadMap.values());
        const snippets = Array.from(snippetMap.values());
        const all = [...fullyRead, ...snippets];

        return { fullyReadSources: fullyRead, snippetSources: snippets, allUniqueSources: all };
    })();

    const isDeepResearch = ragData.some(search => search.searchType === "deep_research");
    const showGroupedSources = isDeepResearch && (fullyReadSources.length > 0 || snippetSources.length > 0);

    const panelTitle = ragData && ragData.length > 0 ? ragData[0].title || ragData[0].query : "";

    const hasAnyFetchedSources = isDeepResearch && ragData.some(search => search.sources.some(s => s.metadata?.fetched === true || s.metadata?.fetch_status === "success"));

    const researchComplete = isDeepResearch && (ragData.some(search => search.isComplete) || hasAnyFetchedSources);

    return (
        <div className="flex h-full flex-col overflow-hidden">
            {isAllDeepResearch ? (
                <div className="flex flex-1 flex-col overflow-hidden">
                    <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
                        {panelTitle && (
                            <div className="mb-4 border-b pb-3">
                                <h2 className="text-base leading-tight font-semibold text-(--primary-text-wMain)">{panelTitle}</h2>
                            </div>
                        )}

                        {showGroupedSources && hasAnyFetchedSources ? (
                            <>
                                {fullyReadSources.length > 0 && (
                                    <div className="mb-4">
                                        <div className="mb-2">
                                            <h3 className="text-sm font-semibold text-(--secondary-text-wMain)">
                                                {fullyReadSources.length} Fully Read Source{fullyReadSources.length !== 1 ? "s" : ""}
                                            </h3>
                                        </div>
                                        <div className="space-y-1">
                                            {fullyReadSources.map((source, idx) => (
                                                <SimpleSourceItem key={`fully-read-${idx}`} source={source} />
                                            ))}
                                        </div>
                                    </div>
                                )}

                                {snippetSources.length > 0 && (
                                    <div>
                                        <div className="mb-2">
                                            <h3 className="text-sm font-semibold text-(--secondary-text-wMain)">
                                                {snippetSources.length} Partially Read Source{snippetSources.length !== 1 ? "s" : ""}
                                            </h3>
                                            <p className="mt-0.5 text-xs text-(--secondary-text-wMain)">Search result snippets</p>
                                        </div>
                                        <div className="space-y-1">
                                            {snippetSources.map((source, idx) => (
                                                <SimpleSourceItem key={`partially-read-${idx}`} source={source} />
                                            ))}
                                        </div>
                                    </div>
                                )}
                            </>
                        ) : (
                            <>
                                <div className="mb-3">
                                    <h3 className="text-sm font-semibold text-(--secondary-text-wMain)">{isDeepResearch && !researchComplete ? "Sources Explored So Far" : `${allUniqueSources.length} Sources`}</h3>
                                    {isDeepResearch && !researchComplete && <p className="mt-0.5 text-xs text-(--secondary-text-wMain)">Research in progress...</p>}
                                </div>
                                <div className="space-y-1">
                                    {allUniqueSources.map((source, idx) => (
                                        <SimpleSourceItem key={`source-${idx}`} source={source} />
                                    ))}
                                </div>
                            </>
                        )}
                    </div>
                </div>
            ) : (
                <Tabs defaultValue="activity" className="flex flex-1 flex-col overflow-hidden">
                    <div className="flex-shrink-0 px-4 pt-4 pb-2">
                        <TabsList className="grid w-full grid-cols-2">
                            <TabsTrigger value="activity">Activity</TabsTrigger>
                            <TabsTrigger value="sources">{totalSources} Sources</TabsTrigger>
                        </TabsList>
                    </div>

                    <TabsContent value="activity" className="mt-0 min-h-0 flex-1 overflow-y-auto px-4 pb-4">
                        <div className="mb-3">
                            <h3 className="text-sm font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">Timeline of Research Activity</h3>
                            <p className="mt-1 text-xs text-(--secondary-text-wMain)">
                                {ragData.length} search{ragData.length !== 1 ? "es" : ""} performed
                            </p>
                        </div>

                        <div className="space-y-2">
                            {ragData.map((search, searchIdx) => {
                                const events: TimelineEvent[] = [];

                                events.push({
                                    type: "search",
                                    timestamp: search.timestamp,
                                    content: search.query,
                                });

                                search.sources.forEach(source => {
                                    if (source.url || source.title) {
                                        const sourceType = source.metadata?.sourceType || "web";
                                        events.push({
                                            type: "read",
                                            timestamp: source.retrievedAt || search.timestamp,
                                            content: source.title || source.url || "Unknown",
                                            url: source.url,
                                            favicon: source.metadata?.favicon || (source.url ? `https://www.google.com/s2/favicons?domain=${source.url}&sz=32` : ""),
                                            title: source.title,
                                            sourceType: sourceType,
                                        });
                                    }
                                });

                                return (
                                    <React.Fragment key={searchIdx}>
                                        {events.map((event, eventIdx) => (
                                            <div key={`${searchIdx}-${eventIdx}`} className="flex items-start gap-3 py-2">
                                                <div className="mt-0.5 flex-shrink-0">
                                                    {event.type === "thinking" && <Brain className="h-4 w-4 text-(--secondary-text-wMain)" />}
                                                    {event.type === "search" && <Search className="h-4 w-4 text-(--secondary-text-wMain)" />}
                                                    {event.type === "read" &&
                                                        (() => {
                                                            if (event.favicon && event.favicon.trim() !== "") {
                                                                return (
                                                                    <img
                                                                        src={event.favicon}
                                                                        alt=""
                                                                        className="h-4 w-4 rounded"
                                                                        onError={e => {
                                                                            (e.target as HTMLImageElement).style.display = "none";
                                                                        }}
                                                                    />
                                                                );
                                                            } else {
                                                                return <Globe className="h-4 w-4 text-(--secondary-text-wMain)" />;
                                                            }
                                                        })()}
                                                </div>

                                                <div className="min-w-0 flex-1">
                                                    {event.type === "search" && (
                                                        <div className="text-sm">
                                                            <span className="text-(--secondary-text-wMain)">Searched for </span>
                                                            <span className="font-medium">{event.content}</span>
                                                        </div>
                                                    )}
                                                    {event.type === "read" && (
                                                        <div className="text-sm">
                                                            <span className="text-(--secondary-text-wMain)">Read </span>
                                                            {event.url ? (
                                                                <a href={event.url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-medium text-(--primary-wMain) hover:text-(--primary-w60) hover:underline">
                                                                    <span>{event.title || new URL(event.url).hostname}</span>
                                                                    <ExternalLink className="h-3 w-3 flex-shrink-0" />
                                                                </a>
                                                            ) : (
                                                                <span className="font-medium">{event.content}</span>
                                                            )}
                                                        </div>
                                                    )}
                                                    {event.type === "thinking" && <div className="text-sm text-(--secondary-text-wMain)">{event.content}</div>}
                                                </div>
                                            </div>
                                        ))}
                                    </React.Fragment>
                                );
                            })}
                        </div>
                    </TabsContent>

                    <TabsContent value="sources" className="mt-0 min-h-0 flex-1 overflow-y-auto px-4 pb-4">
                        <div className="mb-3">
                            <h3 className="text-sm font-semibold text-(--secondary-text-wMain)">All Sources</h3>
                            <p className="mt-1 text-xs text-(--secondary-text-wMain)">
                                {totalSources} source{totalSources !== 1 ? "s" : ""} found across {ragData.length} search{ragData.length !== 1 ? "es" : ""}
                            </p>
                        </div>

                        <div className="space-y-2">
                            {ragData.map((search, searchIdx) =>
                                search.sources
                                    .filter(source => {
                                        const sourceType = source.sourceType || "web";
                                        if (sourceType === "image") {
                                            return source.sourceUrl || source.metadata?.link;
                                        }
                                        return true;
                                    })
                                    .map((source, sourceIdx) => <SourceCard key={`${searchIdx}-${sourceIdx}`} source={source} />)
                            )}
                        </div>
                    </TabsContent>
                </Tabs>
            )}
        </div>
    );
};
