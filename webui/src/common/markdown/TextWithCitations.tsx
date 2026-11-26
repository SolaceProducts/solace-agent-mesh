// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { type ReactNode, useCallback, useMemo, useRef, useState } from "react";
import DOMPurify from "dompurify";
import parse, { type DOMNode, Element, type HTMLReactParserOptions, Text as DomText } from "html-react-parser";
import { marked } from "marked";

import type { Citation as CitationType } from "@/utils/citations";
import { getCitationTooltip, INDIVIDUAL_CITATION_PATTERN, parseCitationId } from "@/utils/citations";

const DEFAULT_CITATION_MAX_LENGTH = 60;
import { ExternalLink } from "lucide-react";

import { getSourceUrl } from "@/utils/sourceUrlHelpers";
import { getThemeHtmlStyles } from "@/utils/themeHtmlStyles";

import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import { MarkdownHTMLConverter } from "./MarkdownHTMLConverter";

interface CitationProps {
    citation: CitationType;
    onClick?: (citation: CitationType) => void;
    maxLength?: number;
}

function truncateText(text: string, maxLength: number): string {
    if (text.length <= maxLength) {
        return text;
    }
    return text.substring(0, maxLength - 3) + "...";
}

function extractFilename(filename: string): string {
    const hasSessionPrefix = filename.includes("web-session-") || filename.startsWith("sam_dev_user_");

    if (!hasSessionPrefix) {
        return filename;
    }

    let cleaned = filename.replace(/\.pdf$/, "");

    cleaned = cleaned.replace(/_v\d+$/, "");

    const sessionPattern = /^.*web-session-[a-f0-9-]+_/;
    cleaned = cleaned.replace(sessionPattern, "");

    return cleaned + ".pdf";
}

function getCitationDisplayText(citation: CitationType, maxLength: number = 30): string {
    const isWebSearch = citation.source?.metadata?.type === "web_search" || citation.type === "search";

    if (isWebSearch && citation.source?.sourceUrl) {
        try {
            const url = new URL(citation.source.sourceUrl);
            const domain = url.hostname.replace(/^www\./, "");
            return truncateText(domain, maxLength);
        } catch {}
    }

    if (citation.source?.metadata?.link) {
        try {
            const url = new URL(citation.source.metadata.link);
            const domain = url.hostname.replace(/^www\./, "");
            return truncateText(domain, maxLength);
        } catch {}
    }

    if (!citation.source && citation.type === "search") {
        return `Web Source ${citation.sourceId + 1}`;
    }

    if (!citation.source) {
        return `Source ${citation.sourceId + 1}`;
    }

    if (citation.source.filename) {
        const hasSessionPrefix = citation.source.filename.includes("web-session-") || citation.source.filename.startsWith("sam_dev_user_");

        const displayName = hasSessionPrefix ? extractFilename(citation.source.filename) : citation.source.filename;

        if (citation.type === "document") {
            const position = citation.source.metadata?.location_range || citation.source.metadata?.primary_location;
            if (position) {
                const separator = ", ";
                const reservedSpace = separator.length + position.length;
                const availableForName = Math.max(10, maxLength - reservedSpace);

                const truncatedName = truncateText(displayName, availableForName);
                return `${truncatedName}${separator}${position}`;
            }
        }

        return truncateText(displayName, maxLength);
    }

    if (citation.source.sourceUrl) {
        try {
            const url = new URL(citation.source.sourceUrl);
            const domain = url.hostname.replace(/^www\./, "");
            return truncateText(domain, maxLength);
        } catch {
            const filename = citation.source.sourceUrl.split("/").pop() || citation.source.sourceUrl;
            return truncateText(filename, maxLength);
        }
    }

    return `Source ${citation.sourceId + 1}`;
}

function Citation({ citation, onClick, maxLength = DEFAULT_CITATION_MAX_LENGTH }: CitationProps) {
    const displayText = getCitationDisplayText(citation, maxLength);

    const isDocumentCitation = citation.type === "document";
    const tooltip = isDocumentCitation ? "" : getCitationTooltip(citation);

    const { url: sourceUrl, sourceType } = getSourceUrl(citation.source);
    const isWebSearch = sourceType === "web_search" || citation.type === "search";
    const isDeepResearch = sourceType === "deep_research" || citation.type === "research";
    const hasClickableUrl = (isWebSearch || isDeepResearch) && sourceUrl;

    const documentPosition = isDocumentCitation ? citation.source?.metadata?.location_range || citation.source?.metadata?.primary_location : null;

    const handleClick = (e: React.MouseEvent) => {
        e.preventDefault();
        e.stopPropagation();

        if (hasClickableUrl) {
            window.open(sourceUrl, "_blank", "noopener,noreferrer");
            return;
        }

        if (onClick) {
            onClick(citation);
        }
    };

    return (
        <button
            onClick={handleClick}
            className="citation-badge mx-0.5 inline-flex cursor-pointer items-center gap-0.5 rounded-sm bg-(--secondary-w10) px-1.5 py-0 align-baseline text-[11px] font-normal whitespace-nowrap text-(--secondary-text-wMain) transition-colors duration-150 hover:bg-(--secondary-w20)"
            title={tooltip}
            aria-label={`Citation: ${tooltip}`}
            type="button"
        >
            {isDocumentCitation && documentPosition ? (
                <>
                    <span className="max-w-[120px] truncate">{citation.source?.filename ? extractFilename(citation.source.filename) : displayText}</span>
                    <span className="flex-shrink-0">, {documentPosition}</span>
                </>
            ) : (
                <span className="max-w-[200px] truncate">{displayText}</span>
            )}
            {hasClickableUrl && <ExternalLink className="h-2.5 w-2.5 flex-shrink-0" />}
        </button>
    );
}

interface BundledCitationsProps {
    citations: CitationType[];
    onCitationClick?: (citation: CitationType) => void;
}

function BundledCitations({ citations, onCitationClick }: BundledCitationsProps) {
    const [isOpen, setIsOpen] = useState(false);
    const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const showTimeout = 150;
    const hideTimeout = 150;

    React.useEffect(() => {
        return () => {
            if (timeoutRef.current) {
                clearTimeout(timeoutRef.current);
            }
        };
    }, []);

    const handleMouseEnter = useCallback(() => {
        if (timeoutRef.current) {
            clearTimeout(timeoutRef.current);
        }
        timeoutRef.current = setTimeout(() => {
            setIsOpen(true);
        }, showTimeout);
    }, []);

    const handleMouseLeave = useCallback(() => {
        if (timeoutRef.current) {
            clearTimeout(timeoutRef.current);
        }
        timeoutRef.current = setTimeout(() => {
            setIsOpen(false);
        }, hideTimeout);
    }, []);

    const handleContentMouseEnter = useCallback(() => {
        if (timeoutRef.current) {
            clearTimeout(timeoutRef.current);
        }
    }, []);

    const handleContentMouseLeave = useCallback(() => {
        if (timeoutRef.current) {
            clearTimeout(timeoutRef.current);
        }
        timeoutRef.current = setTimeout(() => {
            setIsOpen(false);
        }, hideTimeout);
    }, []);

    if (citations.length === 0) return null;

    const uniqueCitations = citations.filter((citation, index, self) => index === self.findIndex(c => c.sourceId === citation.sourceId && c.type === citation.type));

    if (uniqueCitations.length === 1) {
        return <Citation citation={uniqueCitations[0]} onClick={onCitationClick} />;
    }

    const firstCitation = uniqueCitations[0];
    const remainingCount = uniqueCitations.length - 1;
    const firstDisplayText = getCitationDisplayText(firstCitation, 50);

    const isDocumentCitation = firstCitation.type === "document";

    const allCitationsTooltip = isDocumentCitation ? "" : getCitationTooltip(firstCitation);

    const { url: sourceUrl, sourceType } = getSourceUrl(firstCitation.source);
    const isWebSearch = sourceType === "web_search" || firstCitation.type === "search";
    const isDeepResearch = sourceType === "deep_research" || firstCitation.type === "research";
    const hasClickableUrl = (isWebSearch || isDeepResearch) && sourceUrl;

    const handleClick = (e: React.MouseEvent) => {
        e.preventDefault();
        e.stopPropagation();

        if (hasClickableUrl && sourceUrl) {
            window.open(sourceUrl, "_blank", "noopener,noreferrer");
            return;
        }

        if (onCitationClick) {
            onCitationClick(firstCitation);
        }
    };

    if (isDocumentCitation) {
        const documentPosition = firstCitation.source?.metadata?.location_range || firstCitation.source?.metadata?.primary_location;
        const filename = firstCitation.source?.filename ? extractFilename(firstCitation.source.filename) : firstDisplayText;

        return (
            <button
                onClick={handleClick}
                className="citation-badge mx-0.5 inline-flex cursor-pointer items-center gap-1 rounded-sm bg-(--secondary-w10) px-1.5 py-0 align-baseline text-[11px] font-normal whitespace-nowrap text-(--secondary-text-wMain) transition-colors duration-150 hover:bg-(--secondary-w20)"
                aria-label={`${uniqueCitations.length} document citations`}
                type="button"
            >
                {documentPosition ? (
                    <>
                        <span className="max-w-[120px] truncate">{filename}</span>
                        <span className="flex-shrink-0">, {documentPosition}</span>
                    </>
                ) : (
                    <span className="max-w-[200px] truncate">{firstDisplayText}</span>
                )}
                <span className="text-[10px] opacity-70">+{remainingCount}</span>
            </button>
        );
    }

    return (
        <Popover open={isOpen} onOpenChange={setIsOpen}>
            <PopoverTrigger asChild>
                <button
                    onClick={handleClick}
                    onMouseEnter={handleMouseEnter}
                    onMouseLeave={handleMouseLeave}
                    className="citation-badge mx-0.5 inline-flex cursor-pointer items-center gap-1 rounded-sm bg-(--secondary-w10) px-1.5 py-0 align-baseline text-[11px] font-normal whitespace-nowrap text-(--secondary-text-wMain) transition-colors duration-150 hover:bg-(--secondary-w20)"
                    title={allCitationsTooltip}
                    aria-label={`Citation: ${allCitationsTooltip}`}
                    type="button"
                >
                    <span className="max-w-[200px] truncate">{firstDisplayText}</span>
                    {hasClickableUrl && <ExternalLink className="h-2.5 w-2.5 flex-shrink-0" />}
                    <span className="text-[10px] opacity-70">+{remainingCount}</span>
                </button>
            </PopoverTrigger>
            <PopoverContent
                sideOffset={8}
                onMouseEnter={handleContentMouseEnter}
                onMouseLeave={handleContentMouseLeave}
                className="z-[999] max-h-[400px] w-[320px] max-w-[calc(100vw-2rem)] cursor-default overflow-y-auto rounded-lg border p-3 shadow-xl"
                style={{
                    backgroundColor: "var(--background-w10)",
                    borderColor: "var(--secondary-w40)",
                    color: "var(--primary-text-wMain)",
                }}
            >
                <div className="cursor-default space-y-2">
                    <div className="mb-3 border-b pb-2" style={{ borderColor: "var(--secondary-w40)" }}>
                        <h3 className="text-sm font-semibold">All Sources · {uniqueCitations.length}</h3>
                    </div>
                    {uniqueCitations.map((citation, index) => {
                        const displayText = getCitationDisplayText(citation, 50);
                        const { url: sourceUrl, sourceType } = getSourceUrl(citation.source);
                        const isWebSearch = sourceType === "web_search" || citation.type === "search";
                        const isDeepResearch = sourceType === "deep_research" || citation.type === "research";
                        const hasClickableUrl = (isWebSearch || isDeepResearch) && sourceUrl;

                        let favicon = null;
                        if ((isWebSearch || isDeepResearch) && sourceUrl) {
                            try {
                                const url = new URL(sourceUrl);
                                const domain = url.hostname;
                                favicon = `https://www.google.com/s2/favicons?domain=${domain}&sz=32`;
                            } catch {}
                        }

                        const handleClick = (e: React.MouseEvent) => {
                            e.preventDefault();
                            e.stopPropagation();

                            if (hasClickableUrl && sourceUrl) {
                                window.open(sourceUrl, "_blank", "noopener,noreferrer");
                            } else if (onCitationClick) {
                                onCitationClick(citation);
                            }
                        };

                        return (
                            <button key={`bundled-citation-${index}`} onClick={handleClick} className="group flex w-full cursor-pointer items-start gap-2 rounded-md p-2 text-left transition-colors hover:bg-(--secondary-w40)" type="button">
                                {favicon && (
                                    <div className="relative mt-0.5 h-4 w-4 flex-shrink-0 overflow-hidden rounded-full bg-(--background-w10)">
                                        <img src={favicon} alt="" className="h-full w-full" />
                                        <div className="absolute inset-0 rounded-full border border-(--secondary-w20)" />
                                    </div>
                                )}
                                <div className="flex-1 overflow-hidden">
                                    <div className="flex items-center gap-1">
                                        <span className="truncate text-sm font-medium text-(--primary-wMain) group-hover:text-(--primary-w60)">{displayText}</span>
                                        {hasClickableUrl && <ExternalLink className="h-3 w-3 flex-shrink-0 text-(--primary-wMain) group-hover:text-(--primary-w60)" />}
                                    </div>
                                    {citation.source?.metadata?.title && <div className="mt-0.5 truncate text-xs text-(--secondary-text-wMain)">{citation.source.metadata.title}</div>}
                                </div>
                            </button>
                        );
                    })}
                </div>
            </PopoverContent>
        </Popover>
    );
}

interface TextWithCitationsProps {
    text: string;
    citations: CitationType[];
    onCitationClick?: (citation: CitationType) => void;
}

function parseMultiCitationIds(content: string): Array<{ type: "search" | "research" | "document"; sourceId: number; citationId: string }> {
    const results: Array<{ type: "search" | "research" | "document"; sourceId: number; citationId: string }> = [];
    let individualMatch;

    INDIVIDUAL_CITATION_PATTERN.lastIndex = 0;
    while ((individualMatch = INDIVIDUAL_CITATION_PATTERN.exec(content)) !== null) {
        const citationId = individualMatch[1];
        const parsed = parseCitationId(citationId);

        if (parsed) {
            results.push({
                type: parsed.type,
                sourceId: parsed.sourceId,
                citationId: citationId,
            });
        }
    }

    return results;
}

const COMBINED_CITATION_PATTERN = /\[?\[cite:((?:s\d+r\d+|idx\d+r\d+|research\d+)(?:\s*,\s*(?:cite:)?(?:s\d+r\d+|idx\d+r\d+|research\d+))*)\]\]?/g;

function processTextWithCitations(textContent: string, citations: CitationType[], onCitationClick?: (citation: CitationType) => void): ReactNode[] {
    const result: ReactNode[] = [];
    let lastIndex = 0;
    let match;
    let pendingCitations: CitationType[] = [];

    COMBINED_CITATION_PATTERN.lastIndex = 0;

    while ((match = COMBINED_CITATION_PATTERN.exec(textContent)) !== null) {
        if (match.index > lastIndex) {
            if (pendingCitations.length > 0) {
                result.push(<BundledCitations key={`cit-${lastIndex}`} citations={pendingCitations} onCitationClick={onCitationClick} />);
                pendingCitations = [];
            }
            result.push(textContent.substring(lastIndex, match.index));
        }

        const [, content] = match;
        const citationIds = parseMultiCitationIds(content);

        for (const { citationId } of citationIds) {
            const citation = citations.find(c => c.citationId === citationId);

            if (citation) {
                pendingCitations.push(citation);
            }
        }

        lastIndex = match.index + match[0].length;
    }

    if (lastIndex < textContent.length) {
        if (pendingCitations.length > 0) {
            result.push(<BundledCitations key={`cit-${lastIndex}`} citations={pendingCitations} onCitationClick={onCitationClick} />);
        }
        result.push(textContent.substring(lastIndex));
    } else if (pendingCitations.length > 0) {
        result.push(<BundledCitations key={`cit-end`} citations={pendingCitations} onCitationClick={onCitationClick} />);
    }

    return result;
}

export function TextWithCitations({ text, citations, onCitationClick }: TextWithCitationsProps) {
    const parserOptions: HTMLReactParserOptions = useMemo(
        () => ({
            replace: (domNode: DOMNode) => {
                if (domNode.type === "text" && domNode instanceof DomText) {
                    const textContent = domNode.data;

                    COMBINED_CITATION_PATTERN.lastIndex = 0;
                    if (COMBINED_CITATION_PATTERN.test(textContent)) {
                        COMBINED_CITATION_PATTERN.lastIndex = 0;
                        const processed = processTextWithCitations(textContent, citations, onCitationClick);
                        if (processed.length > 0) {
                            return <>{processed}</>;
                        }
                    }
                }

                if (domNode instanceof Element && domNode.name === "a") {
                    domNode.attribs.target = "_blank";
                    domNode.attribs.rel = "noopener noreferrer";
                }

                return undefined;
            },
        }),
        [citations, onCitationClick]
    );

    if (citations.length === 0) {
        return <MarkdownHTMLConverter>{text}</MarkdownHTMLConverter>;
    }

    try {
        const rawHtml = marked.parse(text, { gfm: true }) as string;
        const cleanHtml = DOMPurify.sanitize(rawHtml, { USE_PROFILES: { html: true } });

        const reactElements = parse(cleanHtml, parserOptions);

        return <div className={getThemeHtmlStyles()}>{reactElements}</div>;
    } catch {
        return <MarkdownHTMLConverter>{text}</MarkdownHTMLConverter>;
    }
}
