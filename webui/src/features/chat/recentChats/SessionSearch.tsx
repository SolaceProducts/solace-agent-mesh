// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Search, X } from "lucide-react";

import { api } from "@/api/client";

import { BUILDER_AGENT_NAME } from "@/providers/contexts/BuilderContext";

import { useBuilderEnabled, useDebounce, useListKeyboardNavigation } from "@/hooks";
import type { Session } from "@/types";
import { Button, Input, useClickOutside } from "@/ui";
import { cn, formatRelativeTime } from "@/utils";

import { ProjectBadge } from "../file/ProjectBadge";

import { isSessionInTab, isSessionUserVisible, type Tab } from "./recentChatsPageUtils";

interface SessionSearchProps {
    onSessionSelect: (session: Session) => void;
    projectId?: string | null;
    agentId?: string | null;
    activeTab: Tab;
}

const SEARCH_OVERFETCH_LIMIT = 50;

interface SearchResult {
    data: Session[];
    meta?: {
        pagination?: {
            pageNumber: number;
            pageSize: number;
            totalPages: number;
            count: number;
            nextPage: number | null;
        };
    };
}

export const SessionSearch = ({ onSessionSelect, projectId, agentId, activeTab }: SessionSearchProps) => {
    const [searchQuery, setSearchQuery] = useState("");
    const [searchResults, setSearchResults] = useState<Session[]>([]);
    const [totalMatches, setTotalMatches] = useState(0);
    const [isSearching, setIsSearching] = useState(false);
    const [hasError, setHasError] = useState(false);
    const [showResults, setShowResults] = useState(false);
    const debouncedSearchQuery = useDebounce(searchQuery, 300);
    const builderEnabled = useBuilderEnabled();
    const containerRef = useRef<HTMLDivElement>(null);

    const handleClear = useCallback(() => {
        setSearchQuery("");
        setSearchResults([]);
        setTotalMatches(0);
        setHasError(false);
        setShowResults(false);
    }, []);

    useClickOutside({ ref: containerRef, onClickOutside: () => setShowResults(false), enabled: showResults });

    const handleSessionClick = useCallback(
        (session: Session | undefined) => {
            if (!session) return;
            onSessionSelect(session);
            handleClear();
        },
        [onSessionSelect, handleClear]
    );

    const visibleResults = useMemo(() => searchResults.filter(session => isSessionUserVisible(session, builderEnabled) && isSessionInTab(session, activeTab)), [searchResults, builderEnabled, activeTab]);

    const { activeIndex, isKeyboardMode, handleMouseEnter, handleKeyDown } = useListKeyboardNavigation({
        itemCount: visibleResults.length,
        isOpen: showResults,
        onSelect: index => handleSessionClick(visibleResults[index]),
        onClose: handleClear,
        idPrefix: "session-search-",
        selectOnTab: false,
    });

    const performSearch = useCallback(async (query: string, currentProjectId: string | null | undefined, currentAgentId: string | null | undefined, excludeBuilder: boolean) => {
        if (!query.trim()) {
            setSearchResults([]);
            setShowResults(false);
            return;
        }

        setIsSearching(true);
        try {
            const params = new URLSearchParams({
                query: query.trim(),
                pageNumber: "1",
                pageSize: String(SEARCH_OVERFETCH_LIMIT),
            });

            if (currentProjectId) {
                params.append("projectId", currentProjectId);
            }
            if (currentAgentId) {
                params.append("includeAgentIds", currentAgentId);
            }
            if (excludeBuilder) {
                params.append("excludeAgentIds", BUILDER_AGENT_NAME);
            }

            const data: SearchResult = await api.webui.get(`/api/v1/sessions/search?${params.toString()}`);
            setHasError(false);
            setSearchResults(data.data || []);
            setTotalMatches(data.meta?.pagination?.count ?? 0);
            setShowResults(true);
        } catch (error) {
            console.error("Search error:", error);
            setHasError(true);
            setSearchResults([]);
            setTotalMatches(0);
            setShowResults(true);
        } finally {
            setIsSearching(false);
        }
    }, []);

    useEffect(() => {
        performSearch(debouncedSearchQuery, projectId, agentId, !builderEnabled);
    }, [debouncedSearchQuery, projectId, agentId, builderEnabled, performSearch]);

    const placeholder = "Search chats by title";

    const rowClass = (isActive: boolean) => {
        if (isActive) {
            return "bg-(--primary-w10) text-(--primary-text-w60)";
        }
        if (!isKeyboardMode) {
            return "hover:bg-(--primary-w10) hover:text-(--primary-text-w60)";
        }
        return "";
    };

    const renderResults = () => {
        if (isSearching) {
            return <div className="p-4 text-center text-sm text-(--secondary-text-wMain)">Searching...</div>;
        }
        if (hasError) {
            return <div className="p-4 text-center text-sm text-(--error-wMain)">Search failed. Please try again.</div>;
        }
        if (visibleResults.length === 0) {
            const message = searchResults.length > 0 ? "No results in this view" : "No results found";
            return <div className="p-4 text-center text-sm text-(--secondary-text-wMain)">{message}</div>;
        }
        return (
            <>
                <div className="max-h-[300px] overflow-y-auto">
                    {visibleResults.map((session, index) => (
                        <button
                            key={session.id}
                            id={`session-search-${index}`}
                            onClick={() => handleSessionClick(session)}
                            onMouseEnter={() => handleMouseEnter(index)}
                            className={cn("w-full rounded-sm px-3 py-2 text-left text-sm", rowClass(index === activeIndex))}
                        >
                            <div className="flex items-center justify-between gap-2">
                                <div className="min-w-0 flex-1">
                                    <div className="truncate font-medium">{session.name || "Untitled Session"}</div>
                                    <div className="text-xs text-(--secondary-text-wMain)">Last message {formatRelativeTime(session.updatedAt)}</div>
                                </div>
                                {session.project?.name && <ProjectBadge text={session.project.name} />}
                            </div>
                        </button>
                    ))}
                </div>
                {visibleResults.length < totalMatches && (
                    <div className="border-t px-3 pt-2 pb-1 text-center text-xs text-(--secondary-text-wMain)">
                        Showing {visibleResults.length} of {totalMatches} — refine your search to narrow it down
                    </div>
                )}
            </>
        );
    };

    return (
        <div ref={containerRef} className="relative w-full">
            <div className="relative">
                <Search className="absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-(--secondary-text-wMain)" />
                <Input type="text" placeholder={placeholder} value={searchQuery} onChange={e => setSearchQuery(e.target.value)} onKeyDown={handleKeyDown} className="pr-9 pl-9" />
                {searchQuery && (
                    <Button variant="ghost" size="sm" className="absolute top-1/2 right-1 h-7 w-7 -translate-y-1/2 p-0" onClick={handleClear}>
                        <X className="h-4 w-4" />
                    </Button>
                )}
            </div>

            {showResults && <div className="absolute z-50 mt-2 w-full rounded-md border bg-(--background-w10) p-2 shadow-md">{renderResults()}</div>}
        </div>
    );
};
