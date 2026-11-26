// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ClipboardEvent, type FC, type KeyboardEvent, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { X } from "lucide-react";

import { Popover, PopoverAnchor, PopoverContent, Spinner } from "@/ui";

import { usePeopleSearchTypeahead } from "./usePeopleSearchTypeahead";

interface UserMultiTypeaheadProps {
    id: string;
    selectedEmails: string[];
    onAdd: (email: string) => void;
    onRemove: (email: string) => void;
    excludeEmails: string[];
    error?: boolean;
    placeholder?: string;
    autoFocus?: boolean;
}

const looksLikeEmail = (s: string) => {
    if (s.length === 0 || s.length > 254) return false;
    if (s.includes(" ") || s.includes("\t") || s.includes("\n")) return false;
    const at = s.indexOf("@");
    if (at <= 0 || at !== s.lastIndexOf("@")) return false;
    const dot = s.lastIndexOf(".");
    return dot > at + 1 && dot < s.length - 1;
};

const PASTE_DELIMITERS = /[,;\n\r\t]+/;

const splitPastedEmails = (raw: string): string[] => {
    if (!PASTE_DELIMITERS.test(raw)) return [];
    return raw
        .split(PASTE_DELIMITERS)
        .map(s => s.trim())
        .filter(s => s.length > 0);
};

const CHIP_GAP_PX = 4;
const CHIP_PADDING_PX = 16;
const CHIP_X_BUTTON_PX = 14;
const CHIP_INNER_GAP_PX = 4;
const INPUT_MIN_WIDTH_PX = 120;
const PILL_RESERVE_PX = 90;
const CONTAINER_PADDING_X_PX = 16;
const CHIP_FONT = "14px ui-sans-serif, system-ui, sans-serif";

export function computeVisibleChipCount(chipWidths: number[], containerWidth: number): number {
    if (chipWidths.length === 0 || containerWidth <= 0) return chipWidths.length;
    let used = CONTAINER_PADDING_X_PX;
    let count = 0;
    for (let i = 0; i < chipWidths.length; i++) {
        const w = chipWidths[i];
        const remaining = chipWidths.length - i - 1;
        const reserved = remaining > 0 ? PILL_RESERVE_PX + CHIP_GAP_PX + INPUT_MIN_WIDTH_PX : INPUT_MIN_WIDTH_PX;
        if (used + w + CHIP_GAP_PX + reserved > containerWidth) break;
        used += w + CHIP_GAP_PX;
        count++;
    }
    return Math.max(count, 1);
}

function estimateChipWidth(email: string, ctx: CanvasRenderingContext2D): number {
    const textWidth = ctx.measureText(email).width;
    return textWidth + CHIP_PADDING_PX + CHIP_X_BUTTON_PX + CHIP_INNER_GAP_PX;
}

export const UserMultiTypeahead: FC<UserMultiTypeaheadProps> = ({ id, selectedEmails, onAdd, onRemove, excludeEmails, error = false, placeholder = "Search by email...", autoFocus = false }) => {
    const [isOpen, setIsOpen] = useState(false);
    const [isFocused, setIsFocused] = useState(false);
    const inputRef = useRef<HTMLInputElement>(null);
    const containerRef = useRef<HTMLLabelElement>(null);
    const canvasRef = useRef<HTMLCanvasElement | null>(null);

    const [containerWidth, setContainerWidth] = useState(0);
    const [visibleCount, setVisibleCount] = useState(selectedEmails.length);

    const fullExcludes = useMemo(() => [...excludeEmails, ...selectedEmails], [excludeEmails, selectedEmails]);
    const { searchQuery, setSearchQuery, activeIndex, setActiveIndex, filteredPeople, pendingFetch } = usePeopleSearchTypeahead({
        excludeEmails: fullExcludes,
    });

    const handlePick = useCallback(
        (email: string) => {
            onAdd(email);
            setSearchQuery("");
            inputRef.current?.focus();
        },
        [onAdd, setSearchQuery]
    );

    const handlePaste = useCallback(
        (e: ClipboardEvent<HTMLInputElement>) => {
            const pasted = e.clipboardData.getData("text");
            const chunks = splitPastedEmails(pasted);
            if (chunks.length === 0) return;
            e.preventDefault();
            const seen = new Set(selectedEmails);
            for (const chunk of chunks) {
                if (looksLikeEmail(chunk) && !seen.has(chunk)) {
                    onAdd(chunk);
                    seen.add(chunk);
                }
            }
            setSearchQuery("");
        },
        [onAdd, selectedEmails, setSearchQuery]
    );

    const handleKeyDown = useCallback(
        (e: KeyboardEvent<HTMLInputElement>) => {
            if (e.key === "Backspace" && searchQuery === "" && selectedEmails.length > 0) {
                e.preventDefault();
                const last = selectedEmails.at(-1);
                if (last) onRemove(last);
                return;
            }
            if (e.key === "ArrowDown") {
                e.preventDefault();
                setActiveIndex(prev => Math.min(prev + 1, filteredPeople.length - 1));
            } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setActiveIndex(prev => Math.max(prev - 1, 0));
            } else if (e.key === "Enter") {
                e.preventDefault();
                const active = filteredPeople[activeIndex];
                const typed = searchQuery.trim();
                if (active) {
                    handlePick(active.workEmail);
                } else if (looksLikeEmail(typed)) {
                    handlePick(typed);
                }
            } else if (e.key === "Escape") {
                e.preventDefault();
                setIsOpen(false);
            }
        },
        [searchQuery, selectedEmails, onRemove, filteredPeople, activeIndex, handlePick, setActiveIndex]
    );

    useEffect(() => {
        const el = containerRef.current;
        if (!el || typeof ResizeObserver === "undefined") return;
        const update = () => setContainerWidth(el.clientWidth);
        update();
        const ro = new ResizeObserver(update);
        ro.observe(el);
        return () => ro.disconnect();
    }, []);

    useLayoutEffect(() => {
        if (isFocused || selectedEmails.length === 0 || containerWidth <= 0) {
            setVisibleCount(selectedEmails.length);
            return;
        }
        const canvas = canvasRef.current ?? document.createElement("canvas");
        canvasRef.current = canvas;
        const ctx = canvas.getContext("2d");
        if (!ctx) {
            setVisibleCount(selectedEmails.length);
            return;
        }
        ctx.font = CHIP_FONT;
        const widths = selectedEmails.map(email => estimateChipWidth(email, ctx));
        setVisibleCount(computeVisibleChipCount(widths, containerWidth));
    }, [selectedEmails, isFocused, containerWidth]);

    const showResults = isOpen && searchQuery.length > 0;
    const listboxId = `user-multi-typeahead-${id}-listbox`;
    const inputId = `user-multi-typeahead-${id}-input`;

    const visibleEmails = selectedEmails.slice(0, visibleCount);
    const hiddenCount = selectedEmails.length - visibleEmails.length;
    const wrapClass = isFocused ? "flex-wrap" : "flex-nowrap";

    const renderResults = () => {
        if (pendingFetch) {
            return (
                <div className="flex items-center justify-center px-3 py-3">
                    <Spinner size="small" variant="muted" />
                </div>
            );
        }
        if (filteredPeople.length === 0) {
            const typed = searchQuery.trim();
            if (looksLikeEmail(typed)) {
                return (
                    <button type="button" onMouseDown={e => e.preventDefault()} onClick={() => handlePick(typed)} className="w-full px-3 py-2 text-left text-sm transition-colors hover:bg-(--primary-w10)">
                        Add &ldquo;{typed}&rdquo;
                    </button>
                );
            }
            return <div className="px-3 py-2 text-sm text-(--secondary-text-wMain)">No users found</div>;
        }
        return (
            <div className="flex flex-col">
                {filteredPeople.map((person, index) => (
                    <button
                        key={person.id}
                        type="button"
                        id={`user-multi-typeahead-${id}-item-${index}`}
                        onMouseEnter={() => setActiveIndex(index)}
                        onMouseDown={e => e.preventDefault()}
                        onClick={() => handlePick(person.workEmail)}
                        className={`w-full px-3 py-2 text-left text-sm transition-colors ${index === activeIndex ? "bg-(--primary-w10)" : "hover:bg-(--primary-w10)"}`}
                    >
                        {person.workEmail}
                    </button>
                ))}
            </div>
        );
    };

    return (
        <Popover open={showResults} onOpenChange={setIsOpen}>
            <PopoverAnchor asChild>
                <label
                    ref={containerRef}
                    htmlFor={inputId}
                    className={`flex min-h-9 w-full min-w-0 items-center gap-1 overflow-hidden rounded-md border bg-(--background-w10) px-2 py-1 ${wrapClass} ${error ? "border-(--error-wMain)" : "border-(--secondary-w40)"}`}
                >
                    {visibleEmails.map(email => (
                        <span key={email} className="inline-flex max-w-full items-center gap-1 overflow-hidden rounded bg-(--primary-w10) px-2 py-0.5 text-sm">
                            <span className="truncate" title={email}>
                                {email}
                            </span>
                            <button
                                type="button"
                                onMouseDown={e => e.preventDefault()}
                                onClick={e => {
                                    e.preventDefault();
                                    e.stopPropagation();
                                    onRemove(email);
                                }}
                                aria-label={`Remove ${email}`}
                                className="flex-shrink-0 cursor-pointer text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)"
                            >
                                <X className="h-3.5 w-3.5" />
                            </button>
                        </span>
                    ))}
                    <input
                        ref={inputRef}
                        id={inputId}
                        type="text"
                        role="combobox"
                        aria-expanded={showResults}
                        aria-controls={listboxId}
                        aria-autocomplete="list"
                        autoFocus={autoFocus}
                        placeholder={selectedEmails.length === 0 ? placeholder : ""}
                        value={searchQuery}
                        onChange={e => {
                            setSearchQuery(e.target.value);
                            setIsOpen(true);
                        }}
                        onKeyDown={handleKeyDown}
                        onPaste={handlePaste}
                        onFocus={() => {
                            setIsFocused(true);
                            if (searchQuery.length > 0) setIsOpen(true);
                        }}
                        onBlur={() => setIsFocused(false)}
                        className="min-w-[120px] flex-1 bg-transparent text-sm outline-none"
                    />
                    {hiddenCount > 0 && !isFocused && (
                        <button
                            type="button"
                            onClick={e => {
                                e.preventDefault();
                                inputRef.current?.focus();
                            }}
                            className="cursor-pointer rounded px-1 text-sm text-(--secondary-text-wMain) hover:bg-(--primary-w10) hover:text-(--primary-text-wMain)"
                            aria-label={`Show ${hiddenCount} more selected ${hiddenCount === 1 ? "recipient" : "recipients"}`}
                        >
                            + {hiddenCount} more
                        </button>
                    )}
                </label>
            </PopoverAnchor>

            <PopoverContent align="start" className="w-(--radix-popover-trigger-width) min-w-[300px] p-0" onOpenAutoFocus={e => e.preventDefault()}>
                <div id={listboxId} className="max-h-[250px] overflow-y-auto">
                    {renderResults()}
                </div>
            </PopoverContent>
        </Popover>
    );
};
