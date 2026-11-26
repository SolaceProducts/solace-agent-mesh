// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useRef, useState } from "react";
import { Clock, Search, User } from "lucide-react";

import { api } from "@/api/client";

import { getRecentMentions } from "@/utils/recentMentions";

import { Spinner } from "@/ui/spinner";

import type { PeopleSearchResponse, Person } from "@/types";

interface MentionsCommandProps {
    isOpen: boolean;
    onClose: () => void;
    textAreaRef: React.RefObject<HTMLDivElement | HTMLTextAreaElement | null>;
    onPersonSelect: (person: Person) => void;
    searchQuery: string;
}

export const MentionsCommand: React.FC<MentionsCommandProps> = ({ isOpen, onClose, textAreaRef, onPersonSelect, searchQuery }) => {
    const [activeIndex, setActiveIndex] = useState(0);
    const [people, setPeople] = useState<Person[]>([]);
    const [recentMentions, setRecentMentions] = useState<Person[]>([]);
    const [isLoading, setIsLoading] = useState(false);
    const [isKeyboardMode, setIsKeyboardMode] = useState(false);
    const [showingRecent, setShowingRecent] = useState(false);
    const [popupPosition, setPopupPosition] = useState<{ top: number; left: number } | null>(null);

    const popoverRef = useRef<HTMLDivElement>(null);
    const backdropRef = useRef<HTMLDivElement>(null);
    const prevPeopleCountRef = useRef<number>(0);

    useEffect(() => {
        if (isOpen && textAreaRef.current) {
            const selection = window.getSelection();
            if (selection && selection.rangeCount > 0) {
                const range = selection.getRangeAt(0);
                const rect = range.getBoundingClientRect();

                const popupWidth = 400;
                const popupHeight = 300;

                const viewportWidth = window.innerWidth;
                const viewportHeight = window.innerHeight;

                const spaceBelow = viewportHeight - rect.bottom;
                const spaceAbove = rect.top;
                const spaceRight = viewportWidth - rect.left;

                let top: number;
                if (spaceBelow >= popupHeight || spaceBelow > spaceAbove) {
                    top = rect.bottom + 8;
                } else {
                    top = rect.top - popupHeight - 8;
                }

                let left: number;
                if (spaceRight >= popupWidth) {
                    left = rect.left;
                } else {
                    left = viewportWidth - popupWidth - 16;
                }

                left = Math.max(16, left);

                top = Math.max(16, top);

                setPopupPosition({ top, left });
            }
        }
    }, [isOpen, textAreaRef]);

    useEffect(() => {
        if (isOpen) {
            const recent = getRecentMentions();
            setRecentMentions(recent);
        }
    }, [isOpen]);

    useEffect(() => {
        if (!isOpen) return;

        if (searchQuery.length === 0) {
            setPeople([]);
            setShowingRecent(true);
            setIsLoading(false);
            return;
        }

        setShowingRecent(false);

        const fetchPeople = async () => {
            setIsLoading(true);
            try {
                const data: PeopleSearchResponse = await api.webui.get(`/api/v1/people/search?q=${encodeURIComponent(searchQuery.trim())}&pageSize=10`);
                setPeople(data.data || []);
            } catch (error) {
                console.error("Failed to fetch people:", error);
                setPeople([]);
            } finally {
                setIsLoading(false);
            }
        };

        const timeoutId = setTimeout(fetchPeople, 200);
        return () => clearTimeout(timeoutId);
    }, [isOpen, searchQuery]);

    const handlePersonSelect = useCallback(
        (person: Person) => {
            onPersonSelect(person);
            onClose();
        },
        [onPersonSelect, onClose]
    );

    useEffect(() => {
        if (!isOpen) return;

        const handleKeyDown = (e: KeyboardEvent) => {
            const currentList = showingRecent ? recentMentions : people;

            if (e.key === "Escape") {
                e.preventDefault();
                e.stopPropagation();
                onClose();
                textAreaRef.current?.focus();
            } else if (e.key === "ArrowDown") {
                e.preventDefault();
                setIsKeyboardMode(true);
                setActiveIndex(prev => Math.min(prev + 1, currentList.length - 1));
            } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setIsKeyboardMode(true);
                setActiveIndex(prev => Math.max(prev - 1, 0));
            } else if (e.key === "Enter" || e.key === "Tab") {
                if (currentList.length > 0 && currentList[activeIndex]) {
                    e.preventDefault();
                    handlePersonSelect(currentList[activeIndex]);
                }
            }
        };

        window.addEventListener("keydown", handleKeyDown);
        return () => window.removeEventListener("keydown", handleKeyDown);
    }, [isOpen, people, recentMentions, showingRecent, activeIndex, handlePersonSelect, onClose, textAreaRef]);

    useEffect(() => {
        setActiveIndex(0);
    }, [isOpen, showingRecent]);

    useEffect(() => {
        if (!showingRecent && people.length > 0 && people.length !== prevPeopleCountRef.current) {
            setActiveIndex(0);
            prevPeopleCountRef.current = people.length;
        }
    }, [people, showingRecent]);

    useEffect(() => {
        const activeElement = document.getElementById(`person-item-${activeIndex}`);
        if (activeElement) {
            activeElement.scrollIntoView({ behavior: "smooth", block: "nearest" });
        }
    }, [activeIndex]);

    if (!isOpen || !popupPosition) return null;

    return (
        <>
            <div ref={backdropRef} className="fixed inset-0 z-40 bg-transparent" role="presentation" onClick={onClose} />

            <div
                className="fixed z-50 w-[400px]"
                style={{
                    top: `${popupPosition.top}px`,
                    left: `${popupPosition.left}px`,
                }}
            >
                <div ref={popoverRef} className="flex flex-col rounded-lg border bg-(--background-w10) shadow-lg" style={{ maxHeight: "300px" }}>
                    <div className="flex items-center gap-2 border-b p-3">
                        {showingRecent ? (
                            <>
                                <Clock className="size-4 text-(--secondary-text-wMain)" />
                                <div className="flex-1 text-sm text-(--secondary-text-wMain)">
                                    <span>Recent mentions</span>
                                </div>
                            </>
                        ) : (
                            <>
                                <Search className="size-4 text-(--secondary-text-wMain)" />
                                <div className="flex-1 text-sm text-(--secondary-text-wMain)">
                                    <span>
                                        Searching for: <strong>{searchQuery}</strong>
                                    </span>
                                </div>
                            </>
                        )}
                    </div>

                    <div className="min-h-0 flex-1 overflow-y-auto">
                        {isLoading ? (
                            <div className="flex items-center justify-center p-8">
                                <Spinner className="size-6" variant="secondary" />
                            </div>
                        ) : showingRecent && recentMentions.length === 0 ? (
                            <div className="flex flex-col items-center justify-center gap-4 p-8 text-center">
                                <p className="text-sm text-(--secondary-text-wMain)">No recent mentions. Start typing to search...</p>
                            </div>
                        ) : showingRecent ? (
                            <div className="flex flex-col p-2">
                                {recentMentions.map((person, index) => {
                                    const itemClass = index === activeIndex ? "bg-(--secondary-w40)" : !isKeyboardMode ? "hover:bg-(--secondary-w40)" : "";
                                    return (
                                        <button
                                            key={person.id}
                                            id={`person-item-${index}`}
                                            onClick={() => handlePersonSelect(person)}
                                            onMouseEnter={() => {
                                                setIsKeyboardMode(false);
                                                setActiveIndex(index);
                                            }}
                                            className={`w-full rounded-md p-3 text-left transition-colors ${itemClass}`}
                                        >
                                            <div className="flex items-start gap-3">
                                                <Clock className="mt-0.5 size-4 flex-shrink-0 text-(--secondary-text-wMain)" />
                                                <div className="min-w-0 flex-1">
                                                    <div className="flex flex-wrap items-center gap-2">
                                                        <span className="text-sm font-medium">{person.displayName}</span>
                                                        {person.jobTitle && <span className="rounded bg-(--secondary-w10) px-1.5 py-0.5 text-xs text-(--secondary-text-wMain)">{person.jobTitle}</span>}
                                                    </div>
                                                    <p className="mt-1 text-xs text-(--secondary-text-wMain)">{person.workEmail}</p>
                                                </div>
                                            </div>
                                        </button>
                                    );
                                })}
                            </div>
                        ) : people.length === 0 ? (
                            <div className="flex flex-col items-center justify-center gap-4 p-8 text-center">
                                <p className="text-sm text-(--secondary-text-wMain)">No people found matching "{searchQuery}"</p>
                            </div>
                        ) : (
                            <div className="flex flex-col p-2">
                                {people.map((person, index) => {
                                    const itemClass = index === activeIndex ? "bg-(--secondary-w40)" : !isKeyboardMode ? "hover:bg-(--secondary-w40)" : "";
                                    return (
                                        <button
                                            key={person.id}
                                            id={`person-item-${index}`}
                                            onClick={() => handlePersonSelect(person)}
                                            onMouseEnter={() => {
                                                setIsKeyboardMode(false);
                                                setActiveIndex(index);
                                            }}
                                            className={`w-full rounded-md p-3 text-left transition-colors ${itemClass}`}
                                        >
                                            <div className="flex items-start gap-3">
                                                <User className="mt-0.5 size-4 flex-shrink-0 text-(--secondary-text-wMain)" />
                                                <div className="min-w-0 flex-1">
                                                    <div className="flex flex-wrap items-center gap-2">
                                                        <span className="text-sm font-medium">{person.displayName}</span>
                                                        {person.jobTitle && <span className="rounded bg-(--secondary-w10) px-1.5 py-0.5 text-xs text-(--secondary-text-wMain)">{person.jobTitle}</span>}
                                                    </div>
                                                    <p className="mt-1 text-xs text-(--secondary-text-wMain)">{person.workEmail}</p>
                                                </div>
                                            </div>
                                        </button>
                                    );
                                })}
                            </div>
                        )}
                    </div>
                </div>
            </div>
        </>
    );
};
