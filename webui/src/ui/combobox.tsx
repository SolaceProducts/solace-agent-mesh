// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown, Loader2 } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/utils";

import { Input } from "./input";
import { LifecycleBadge } from "./lifecycleBadge";
import { Popover, PopoverAnchor, PopoverContent } from "./popover";

const WHEEL_LINE_HEIGHT_PX = 16;

const NON_PASSIVE_CAPTURE_OPTIONS: AddEventListenerOptions = { capture: true, passive: false };

const wheelDeltaToPixels = (e: WheelEvent, el: HTMLElement): number => {
    if (e.deltaMode === WheelEvent.DOM_DELTA_LINE) return e.deltaY * WHEEL_LINE_HEIGHT_PX;
    if (e.deltaMode === WheelEvent.DOM_DELTA_PAGE) return e.deltaY * el.clientHeight;
    return e.deltaY;
};

export interface ComboBoxItem {
    id: string;
    label: string;
    icon?: ReactNode;
    subtext?: string;
    badge?: string;
    section?: "default" | "advanced";
    disabled?: boolean;
}

interface ComboBoxProps {
    value?: string | undefined;
    onValueChange: (value: string) => void;
    items: ComboBoxItem[];
    placeholder?: string;
    disabled?: boolean;
    invalid?: boolean;
    isLoading?: boolean;
    renderItem?: (item: ComboBoxItem) => ReactNode;
    onOpen?: () => void;
    allowCustomValue?: boolean;
    noItemsFoundText?: string;
    loadingText?: string;
    dropdownMaxHeight?: number;
    defaultOpen?: boolean;
    searchText?: string;
    onSearchTextChange?: (text: string) => void;
    "aria-label"?: string;
    "data-testid"?: string;
}

export const ComboBox = ({
    value,
    onValueChange,
    items,
    placeholder = "Select an option...",
    disabled,
    invalid,
    isLoading,
    renderItem,
    onOpen,
    allowCustomValue = false,
    noItemsFoundText = "No items found",
    loadingText = "Loading...",
    dropdownMaxHeight = 240,
    defaultOpen = false,
    searchText: controlledSearchText,
    onSearchTextChange,
    "aria-label": ariaLabel,
    "data-testid": dataTestId,
}: ComboBoxProps) => {
    const [isOpen, setIsOpen] = useState(defaultOpen);
    const [localSearchText, setLocalSearchText] = useState("");
    const searchControlled = onSearchTextChange !== undefined;
    const searchText = searchControlled ? (controlledSearchText ?? "") : localSearchText;
    const setSearchText = searchControlled ? onSearchTextChange : setLocalSearchText;
    const [highlightedIndex, setHighlightedIndex] = useState(-1);
    const inputRef = useRef<HTMLInputElement>(null);
    const containerRef = useRef<HTMLDivElement>(null);
    const dropdownRef = useRef<HTMLDivElement>(null);

    const selectedItem = items.find(item => item.id === value);

    const query = searchText.trim();
    const normalizedQuery = query.toLowerCase();
    const filteredItems = query && !searchControlled ? items.filter(item => item.label.toLowerCase().includes(normalizedQuery) || item.id.toLowerCase().includes(normalizedQuery)) : items;

    let itemsToDisplay = filteredItems;
    if (query && filteredItems.length === 0 && allowCustomValue) {
        itemsToDisplay = [{ id: query, label: query }];
    }

    const groupedItems = itemsToDisplay.reduce(
        (acc, item) => {
            const section = item.section || "default";
            if (!acc[section]) {
                acc[section] = [];
            }
            acc[section].push(item);
            return acc;
        },
        { default: [], advanced: [] } as Record<string, ComboBoxItem[]>
    );

    const flatItems = [...(groupedItems.default || []), ...(groupedItems.advanced || [])];

    const nextEnabledIndex = (from: number, step: 1 | -1) => {
        for (let i = from + step; i >= 0 && i < flatItems.length; i += step) {
            if (!flatItems[i].disabled) return i;
        }
        return from;
    };

    const onOpenRef = useRef(onOpen);
    onOpenRef.current = onOpen;

    const isShowingItems = isOpen && items.length > 0;
    const requestOpen = () => {
        setIsOpen(true);
        if (!isShowingItems) onOpenRef.current?.();
    };

    useEffect(() => {
        if (isOpen && inputRef.current && document.activeElement !== inputRef.current) {
            inputRef.current.focus();
        }
    }, [isOpen, items.length]);

    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (!isShowingItems && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
            e.preventDefault();
            requestOpen();
            return;
        }

        switch (e.key) {
            case "ArrowDown":
                e.preventDefault();
                setHighlightedIndex(prev => nextEnabledIndex(prev, 1));
                break;
            case "ArrowUp":
                e.preventDefault();
                setHighlightedIndex(prev => nextEnabledIndex(prev, -1));
                break;
            case "Enter":
                e.preventDefault();
                if (flatItems[highlightedIndex] && !flatItems[highlightedIndex].disabled) {
                    onValueChange(flatItems[highlightedIndex].id);
                    setIsOpen(false);
                    setSearchText("");
                }
                break;
            case "Escape":
                e.preventDefault();
                setIsOpen(false);
                setSearchText("");
                break;
            default:
                break;
        }
    };

    const handleInputChange = (searchValue: string) => {
        requestOpen();
        setSearchText(searchValue);
        setHighlightedIndex(0);
        if (allowCustomValue) {
            onValueChange(searchValue.trim());
        } else if (searchValue === "") {
            onValueChange("");
        }
    };

    const handleItemSelect = (itemId: string) => {
        onValueChange(itemId);
        setIsOpen(false);
        setSearchText("");
    };

    const handleClick = () => {
        requestOpen();
    };

    const renderListBody = () => {
        if (isLoading) {
            return <div className="py-1.5 pr-8 pl-2 text-sm text-(--secondary-text-wMain)">{loadingText}</div>;
        }
        if (flatItems.length === 0) {
            return <div className="py-1.5 pr-8 pl-2 text-sm text-(--secondary-text-wMain)">{noItemsFoundText}</div>;
        }
        const defaultItems = groupedItems.default ?? [];
        const advancedItems = groupedItems.advanced ?? [];
        const hasDivider = defaultItems.length > 0 && advancedItems.length > 0;
        return (
            <>
                {defaultItems.map((item, index) => renderOption(item, index))}
                {hasDivider && <div className="my-1 border-t" aria-hidden="true" />}
                {advancedItems.map((item, index) => renderOption(item, defaultItems.length + index))}
            </>
        );
    };

    const renderOption = (item: ComboBoxItem, index: number) => (
        <button
            type="button"
            key={item.id}
            tabIndex={-1}
            aria-disabled={item.disabled}
            className={cn(
                "relative flex h-auto w-full cursor-pointer items-center justify-start gap-2 py-1.5 pr-8 pl-2 text-left text-sm font-normal text-(--primary-text-wMain) select-none aria-disabled:cursor-not-allowed aria-disabled:opacity-50",
                index === highlightedIndex && "bg-(--primary-w10)"
            )}
            onMouseDown={e => e.preventDefault()}
            onClick={() => !item.disabled && handleItemSelect(item.id)}
            onMouseEnter={() => !item.disabled && setHighlightedIndex(index)}
        >
            {item.icon && <div className="flex flex-shrink-0 items-center justify-center">{item.icon}</div>}
            <div className="min-w-0 flex-1">
                {renderItem ? (
                    renderItem(item)
                ) : (
                    <>
                        <div className="flex items-center gap-2">
                            {item.label}
                            {item.badge && <LifecycleBadge>{item.badge}</LifecycleBadge>}
                        </div>
                        {item.subtext && <div className="text-xs text-(--secondary-text-wMain)">{item.subtext}</div>}
                    </>
                )}
            </div>
            {item.id === value && (
                <div className="absolute right-2 flex h-4 w-4 items-center justify-center">
                    <Check className="h-4 w-4" />
                </div>
            )}
        </button>
    );

    useEffect(() => {
        if (!isOpen) return;
        const onWheel = (e: WheelEvent) => {
            const el = dropdownRef.current;
            if (!el || !(e.target instanceof Node) || !el.contains(e.target)) return;
            const maxScroll = el.scrollHeight - el.clientHeight;
            if (maxScroll <= 0) return;
            const next = Math.max(0, Math.min(maxScroll, el.scrollTop + wheelDeltaToPixels(e, el)));
            if (next === el.scrollTop) return;
            el.scrollTop = next;
            e.preventDefault();
        };
        document.addEventListener("wheel", onWheel, NON_PASSIVE_CAPTURE_OPTIONS);
        return () => document.removeEventListener("wheel", onWheel, { capture: true });
    }, [isOpen]);

    return (
        <Popover open={isOpen} onOpenChange={open => !open && setIsOpen(false)}>
            <PopoverAnchor asChild>
                <div ref={containerRef} className="relative w-full">
                    <Input
                        ref={inputRef}
                        type="text"
                        role="combobox"
                        aria-expanded={isOpen}
                        aria-label={ariaLabel}
                        placeholder={placeholder}
                        value={searchText || selectedItem?.label || ""}
                        onChange={e => handleInputChange(e.target.value)}
                        onKeyDown={handleKeyDown}
                        onClick={handleClick}
                        disabled={disabled}
                        aria-invalid={invalid}
                        className={cn("w-full bg-(--background-w10) pr-10", invalid && "border-(--error-w100)")}
                        autoComplete="off"
                        data-testid={dataTestId}
                    />
                    {isLoading ? (
                        <Loader2 className="pointer-events-none absolute top-1/2 right-3 h-4 w-4 -translate-y-1/2 transform animate-spin text-(--secondary-text-w50)" />
                    ) : (
                        <ChevronDown className="pointer-events-none absolute top-1/2 right-3 h-4 w-4 -translate-y-1/2 transform text-(--secondary-text-w50)" />
                    )}
                </div>
            </PopoverAnchor>

            <PopoverContent
                align="start"
                sideOffset={4}
                className="w-(--radix-popover-trigger-width) p-0"
                onOpenAutoFocus={e => e.preventDefault()}
                onInteractOutside={e => {
                    if (containerRef.current?.contains(e.target as Node)) {
                        e.preventDefault();
                    }
                }}
            >
                <div ref={dropdownRef} className="overflow-y-auto bg-(--background-w10)" style={{ maxHeight: `${dropdownMaxHeight}px` }}>
                    {renderListBody()}
                </div>
            </PopoverContent>
        </Popover>
    );
};
