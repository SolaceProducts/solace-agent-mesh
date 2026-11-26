// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useMemo, useRef, useState } from "react";
import { Check, ChevronDown, X } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { useClickOutside } from "./hooks/useClickOutside";

interface SearchableComboboxProps<T> {
    readonly items: readonly T[];
    readonly value: string;
    readonly onChange: (id: string) => void;
    readonly getId: (item: T) => string;
    readonly getSearchText: (item: T) => string;
    readonly renderSelected: (item: T) => ReactNode;
    readonly renderItem: (item: T) => ReactNode;
    readonly placeholder?: string;
    readonly searchPlaceholder: string;
    readonly noMatchText: string;
    readonly disabled?: boolean;
    readonly clearable?: boolean;
    readonly "data-testid"?: string;
}

export function SearchableCombobox<T>({ items, value, onChange, getId, getSearchText, renderSelected, renderItem, placeholder, searchPlaceholder, noMatchText, disabled, clearable, "data-testid": dataTestId }: SearchableComboboxProps<T>) {
    const [isOpen, setIsOpen] = useState(false);
    const [search, setSearch] = useState("");
    const dropdownRef = useRef<HTMLDivElement>(null);
    const inputRef = useRef<HTMLInputElement>(null);

    useClickOutside({
        ref: dropdownRef,
        onClickOutside: () => {
            setIsOpen(false);
            setSearch("");
        },
        enabled: isOpen,
    });

    const filtered = useMemo(() => {
        if (!search.trim()) return items;
        const q = search.toLowerCase();
        return items.filter(item => getSearchText(item).toLowerCase().includes(q));
    }, [items, search, getSearchText]);

    const selected = items.find(item => getId(item) === value);

    const handleTriggerClick = () => {
        if (disabled) return;
        const next = !isOpen;
        setIsOpen(next);
        if (next) setTimeout(() => inputRef.current?.focus(), 0);
        else setSearch("");
    };

    return (
        <div className="relative" ref={dropdownRef}>
            <div
                role="combobox"
                aria-expanded={isOpen}
                aria-disabled={disabled}
                tabIndex={disabled ? -1 : 0}
                data-testid={dataTestId}
                onClick={handleTriggerClick}
                onKeyDown={e => {
                    if (disabled) return;
                    if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        handleTriggerClick();
                    } else if (e.key === "Escape" && isOpen) {
                        setIsOpen(false);
                        setSearch("");
                    }
                }}
                className={cn(
                    "flex min-h-[38px] w-full cursor-pointer items-center justify-between rounded-xs border px-3 py-2 text-sm shadow-xs",
                    isOpen && "border-transparent outline outline-(--brand-wMain)",
                    disabled && "cursor-not-allowed opacity-50"
                )}
            >
                <div className="flex flex-1 items-center text-left">{selected ? renderSelected(selected) : <span className="text-(--secondary-text-wMain)">{placeholder}</span>}</div>
                {clearable && selected && !disabled && (
                    <button
                        type="button"
                        aria-label="Clear selection"
                        onClick={e => {
                            e.stopPropagation();
                            onChange("");
                        }}
                        className="ml-2 flex size-4 shrink-0 items-center justify-center rounded text-(--secondary-text-wMain) hover:text-(--primary-text-w100)"
                    >
                        <X className="size-3.5" />
                    </button>
                )}
                <ChevronDown className={cn("ml-2 size-4 shrink-0 opacity-50 transition-transform", isOpen && "rotate-180")} />
            </div>

            {isOpen && !disabled && (
                <div className="absolute z-50 mt-1 max-h-72 w-full overflow-auto rounded-md border bg-(--background-w10) p-1 shadow-lg">
                    <div className="px-2 py-1.5">
                        <input ref={inputRef} value={search} onChange={e => setSearch(e.target.value)} placeholder={searchPlaceholder} className="w-full border-0 bg-transparent p-0 text-sm outline-none placeholder:text-(--secondary-text-wMain)" />
                    </div>
                    <div className="border-t" />
                    {filtered.length === 0 ? (
                        <div className="px-2 py-3 text-center text-xs text-(--secondary-text-wMain)">{noMatchText}</div>
                    ) : (
                        filtered.map(item => {
                            const id = getId(item);
                            const isSelected = id === value;
                            return (
                                <button
                                    key={id}
                                    type="button"
                                    onClick={() => {
                                        onChange(id);
                                        setIsOpen(false);
                                        setSearch("");
                                    }}
                                    className={cn("relative flex w-full cursor-default items-center gap-2 py-1.5 pr-8 pl-2 text-left text-sm select-none", "hover:bg-(--primary-w10) hover:text-(--primary-text-w100)")}
                                >
                                    {renderItem(item)}
                                    {isSelected && (
                                        <span className="absolute right-2 flex size-3.5 items-center justify-center">
                                            <Check className="size-4" />
                                        </span>
                                    )}
                                </button>
                            );
                        })
                    )}
                </div>
            )}
        </div>
    );
}
