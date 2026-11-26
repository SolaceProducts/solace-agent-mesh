// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Check, ChevronDown, XIcon } from "lucide-react";
import { createPortal } from "react-dom";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

const WHEEL_LINE_HEIGHT_PX = 16;

const wheelDeltaToPixels = (e: WheelEvent, el: HTMLElement): number => {
    if (e.deltaMode === WheelEvent.DOM_DELTA_LINE) return e.deltaY * WHEEL_LINE_HEIGHT_PX;
    if (e.deltaMode === WheelEvent.DOM_DELTA_PAGE) return e.deltaY * el.clientHeight;
    return e.deltaY;
};

interface MultiSelectProps {
    options: string[];
    value: string[];
    onChange: (value: string[]) => void;
    placeholder?: string;
    disabled?: boolean;
    className?: string;
    renderOption?: (option: string, isSelected: boolean) => React.ReactNode;
    resolveLabel?: (option: string) => string;
    renderChip?: (option: string, remove: (e?: React.MouseEvent) => void) => React.ReactNode;
    summaryMode?: boolean;
    emptyMeansAll?: boolean;
}

export function MultiSelect({ options, value, onChange, placeholder = "Select options...", disabled = false, className = "", renderOption, resolveLabel, renderChip, summaryMode = false, emptyMeansAll = false }: Readonly<MultiSelectProps>) {
    const [isOpen, setIsOpen] = useState(false);
    const triggerRef = useRef<HTMLDivElement>(null);
    const menuRef = useRef<HTMLDivElement>(null);
    const [menuPos, setMenuPos] = useState<{ left: number; width: number; maxHeight: number; top?: number; bottom?: number }>();

    const updateMenuPosition = useCallback(() => {
        const rect = triggerRef.current?.getBoundingClientRect();
        if (!rect) {
            return;
        }
        const gap = 4;
        const spaceBelow = globalThis.innerHeight - rect.bottom - gap;
        const spaceAbove = rect.top - gap;
        const openUp = spaceBelow < 160 && spaceAbove > spaceBelow;
        setMenuPos({
            left: rect.left,
            width: rect.width,
            maxHeight: Math.min(240, openUp ? spaceAbove : spaceBelow),
            ...(openUp ? { bottom: globalThis.innerHeight - rect.top + gap } : { top: rect.bottom + gap }),
        });
    }, []);

    useLayoutEffect(() => {
        if (!isOpen) {
            setMenuPos(undefined);
            return;
        }
        updateMenuPosition();
    }, [isOpen, updateMenuPosition]);

    useEffect(() => {
        if (!isOpen) {
            return;
        }
        const reposition = () => updateMenuPosition();
        globalThis.addEventListener("scroll", reposition, true);
        globalThis.addEventListener("resize", reposition);
        return () => {
            globalThis.removeEventListener("scroll", reposition, true);
            globalThis.removeEventListener("resize", reposition);
        };
    }, [isOpen, updateMenuPosition]);

    useEffect(() => {
        if (!isOpen) return;
        const onWheel = (e: WheelEvent) => {
            const el = menuRef.current;
            if (!el || !(e.target instanceof Node) || !el.contains(e.target)) return;
            const maxScroll = el.scrollHeight - el.clientHeight;
            if (maxScroll <= 0) return;
            const next = Math.max(0, Math.min(maxScroll, el.scrollTop + wheelDeltaToPixels(e, el)));
            if (next === el.scrollTop) return;
            el.scrollTop = next;
            e.preventDefault();
        };
        document.addEventListener("wheel", onWheel, { capture: true, passive: false });
        return () => document.removeEventListener("wheel", onWheel, { capture: true });
    }, [isOpen]);

    useEffect(() => {
        const handleClickOutside = (event: MouseEvent) => {
            const target = event.target as Node;
            if (triggerRef.current?.contains(target) || menuRef.current?.contains(target)) {
                return;
            }
            setIsOpen(false);
        };

        if (isOpen) {
            document.addEventListener("mousedown", handleClickOutside);
            return () => {
                document.removeEventListener("mousedown", handleClickOutside);
            };
        }
    }, [isOpen]);

    const toggleOption = (option: string) => {
        const selectionToToggle = emptyMeansAll && value.length === 0 ? options : value;
        onChange(selectionToToggle.includes(option) ? selectionToToggle.filter(v => v !== option) : [...selectionToToggle, option]);
    };

    const removeOption = (option: string, e?: React.MouseEvent) => {
        e?.stopPropagation();
        onChange(value.filter(v => v !== option));
    };

    const labelOf = (option: string) => resolveLabel?.(option) ?? option;
    const showAll = value.length === 0 || (emptyMeansAll && value.length === options.length);
    const toggleOpen = () => {
        if (!disabled) {
            setIsOpen(open => !open);
        }
    };

    const handleTriggerKeyDown = (e: React.KeyboardEvent) => {
        if (disabled) {
            return;
        }
        if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            setIsOpen(open => !open);
        } else if (e.key === "Escape") {
            setIsOpen(false);
        }
    };

    let summaryText: string;
    if (showAll) {
        summaryText = placeholder;
    } else if (value.length === 1) {
        summaryText = labelOf(value[0]);
    } else {
        summaryText = `${value.length} selected`;
    }

    const renderTriggerContent = () => {
        if (summaryMode) {
            return <span className={cn("truncate text-sm", showAll && "text-(--secondary-text-wMain)")}>{summaryText}</span>;
        }
        if (value.length === 0) {
            return <span className="text-(--secondary-text-wMain)">{placeholder}</span>;
        }
        if (renderChip) {
            return value.map(item => <React.Fragment key={item}>{renderChip(item, e => removeOption(item, e))}</React.Fragment>);
        }
        return value.map(item => (
            <div key={item} className="inline-flex h-6 max-w-50 items-center rounded-sm bg-(--secondary-w10) px-2 py-0.5 text-xs">
                <span className="min-w-0 truncate" title={item}>
                    {item}
                </span>
                {!disabled && (
                    <Button
                        variant="ghost"
                        size="icon"
                        className="ml-1 h-2 min-h-0 w-2 min-w-0 shrink-0 p-2"
                        title="Remove"
                        onClick={e => {
                            e.stopPropagation();
                            removeOption(item, e);
                        }}
                    >
                        <XIcon />
                    </Button>
                )}
            </div>
        ));
    };

    return (
        <div className={`relative ${className}`}>
            <div
                ref={triggerRef}
                role="button"
                tabIndex={disabled ? -1 : 0}
                aria-haspopup="listbox"
                aria-expanded={isOpen}
                aria-disabled={disabled}
                onClick={toggleOpen}
                onKeyDown={handleTriggerKeyDown}
                className={`flex min-h-9.5 w-full cursor-pointer items-center justify-between border px-3 py-2 text-sm shadow-xs ${disabled ? "cursor-not-allowed opacity-50" : ""}`}
            >
                <div className="flex min-h-6 flex-1 flex-wrap items-center gap-1 text-left">{renderTriggerContent()}</div>
                <ChevronDown className={`ml-2 size-4 shrink-0 opacity-50 transition-transform ${isOpen ? "rotate-180" : ""}`} />
            </div>

            {isOpen &&
                !disabled &&
                menuPos &&
                createPortal(
                    <div
                        ref={menuRef}
                        style={{ position: "fixed", left: menuPos.left, minWidth: menuPos.width, maxHeight: menuPos.maxHeight, top: menuPos.top, bottom: menuPos.bottom, pointerEvents: "auto" }}
                        className="z-50 w-max max-w-md overflow-x-hidden overflow-y-auto rounded-sm border bg-(--background-w10) p-1 shadow-lg"
                    >
                        {options.map(option => {
                            const isSelected = emptyMeansAll && value.length === 0 ? true : value.includes(option);
                            return (
                                <button
                                    key={option}
                                    type="button"
                                    onClick={() => toggleOption(option)}
                                    className="relative flex w-full cursor-default items-center py-1.5 pr-8 pl-2 text-left text-sm whitespace-nowrap select-none hover:bg-(--primary-w10) hover:text-(--primary-text-w100)"
                                >
                                    <span className="min-w-0 flex-1 truncate">{renderOption ? renderOption(option, isSelected) : labelOf(option)}</span>
                                    {isSelected && (
                                        <span className="absolute right-2 flex size-3.5 items-center justify-center">
                                            <Check className="size-4" />
                                        </span>
                                    )}
                                </button>
                            );
                        })}
                    </div>,
                    document.body
                )}
        </div>
    );
}
