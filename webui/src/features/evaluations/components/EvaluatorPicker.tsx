// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useRef, useState } from "react";
import { ChevronDown, Plus, Star, XIcon } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { useClickOutside } from "@/ui/hooks/useClickOutside";
import type { EvalEvaluator } from "@/types/eval";

import { getTypeLabel, MAX_EVALUATORS_PER_EXPERIMENT as MAX_EVALUATORS } from "../utils";

interface EvaluatorPickerProps {
    readonly evaluators: EvalEvaluator[];
    readonly loading?: boolean;
    readonly selectedIds: string[];
    readonly onChange: (ids: string[]) => void;
    readonly onCreateClick: () => void;
    readonly primaryId?: string;
    readonly onPrimaryChange?: (id: string) => void;
    readonly disabled?: boolean;
    readonly hideLabel?: boolean;
}

export function EvaluatorPicker({ evaluators, loading, selectedIds, onChange, onCreateClick, primaryId, onPrimaryChange, disabled, hideLabel }: EvaluatorPickerProps) {
    const [isOpen, setIsOpen] = useState(false);
    const dropdownRef = useRef<HTMLDivElement>(null);

    useClickOutside({ ref: dropdownRef, onClickOutside: () => setIsOpen(false), enabled: isOpen });

    const sortedEvaluators = useMemo(() => [...evaluators].sort((a, b) => a.name.localeCompare(b.name)), [evaluators]);

    const selectedEvaluators = useMemo(() => selectedIds.map(id => evaluators.find(e => e.id === id)).filter(Boolean) as EvalEvaluator[], [selectedIds, evaluators]);

    const isAtMax = selectedIds.length >= MAX_EVALUATORS;

    const reconcilePrimary = (next: string[]) => {
        if (!onPrimaryChange) return;
        if (next.length === 1) {
            onPrimaryChange(next[0]);
            return;
        }
        if (primaryId && !next.includes(primaryId)) {
            onPrimaryChange(next[0] ?? "");
        }
    };

    const toggleOption = (id: string) => {
        if (disabled) return;
        const isSelected = selectedIds.includes(id);
        if (!isSelected && isAtMax) return;
        const next = isSelected ? selectedIds.filter(eid => eid !== id) : [...selectedIds, id];
        onChange(next);
        reconcilePrimary(next);
    };

    const removeOption = (id: string, e: React.MouseEvent) => {
        e.stopPropagation();
        const next = selectedIds.filter(eid => eid !== id);
        onChange(next);
        reconcilePrimary(next);
    };

    const renderItem = (ev: EvalEvaluator) => {
        const isSelected = selectedIds.includes(ev.id);
        const isItemDisabled = isAtMax && !isSelected;
        return (
            <button
                key={ev.id}
                type="button"
                onClick={() => toggleOption(ev.id)}
                disabled={isItemDisabled}
                className={cn(
                    "relative flex w-full cursor-default items-center justify-between gap-2 py-1.5 pr-8 pl-2 text-left text-sm select-none",
                    isSelected && "bg-(--success-w10)",
                    isItemDisabled ? "cursor-not-allowed opacity-40" : "hover:bg-(--primary-w10) hover:text-(--primary-text-w100)"
                )}
            >
                <span className="flex-1 font-medium">{ev.name}</span>
                <span className="shrink-0 text-xs text-(--secondary-text-wMain)">{getTypeLabel(ev)}</span>
            </button>
        );
    };

    return (
        <div className="flex flex-col gap-2">
            {!hideLabel && (
                <div className="flex items-center justify-between">
                    <span className="text-sm font-medium text-(--secondary-text-wMain)">
                        Evaluator
                        <span className="ml-1 text-(--brand-wMain)">*</span>
                        <span className="ml-1 text-xs font-normal text-(--secondary-text-wMain)">
                            ({selectedIds.length}/{MAX_EVALUATORS})
                        </span>
                    </span>
                    <Button variant="ghost" size="sm" onClick={onCreateClick} disabled={isAtMax} tooltip={isAtMax ? `You can pick at most ${MAX_EVALUATORS} evaluators. Remove one to create another.` : undefined}>
                        + Create Evaluator
                    </Button>
                </div>
            )}

            <div className={cn("flex gap-3", hideLabel ? "items-center" : "flex-col items-start")}>
                <div className="relative min-w-0 flex-1" ref={dropdownRef}>
                    <div
                        onClick={() => !disabled && !loading && setIsOpen(!isOpen)}
                        className={cn("flex min-h-[38px] w-full cursor-pointer items-center justify-between rounded-xs border px-3 py-2 text-sm shadow-xs", (disabled || loading) && "cursor-not-allowed opacity-50")}
                    >
                        <div className="flex flex-1 flex-wrap items-center gap-1 text-left">
                            {selectedEvaluators.length > 0 ? (
                                selectedEvaluators.map(ev => (
                                    <div key={ev.id} className="inline-flex h-6 items-center gap-2 rounded-sm bg-(--secondary-w10) px-2 py-0.5 text-sm">
                                        {selectedIds.length > 1 && onPrimaryChange && (
                                            <Button
                                                type="button"
                                                variant="ghost"
                                                size="icon"
                                                className="size-5 shrink-0"
                                                tooltip="Set as Primary Evaluator"
                                                onClick={e => {
                                                    e.stopPropagation();
                                                    onPrimaryChange(ev.id);
                                                }}
                                            >
                                                <Star className={cn("size-3", primaryId === ev.id ? "fill-current text-(--brand-w100)" : "text-(--secondary-text-wMain)")} />
                                            </Button>
                                        )}
                                        {ev.name}
                                        {!disabled && (
                                            <Button variant="ghost" size="icon" className="ml-0.5 h-2 min-h-0 w-2 min-w-0 p-2" title="Remove" onClick={e => removeOption(ev.id, e)}>
                                                <XIcon />
                                            </Button>
                                        )}
                                    </div>
                                ))
                            ) : (
                                <span className="text-(--secondary-text-wMain)">{loading ? "Loading..." : "Select evaluators..."}</span>
                            )}
                        </div>
                        <ChevronDown className={cn("ml-2 size-4 shrink-0 opacity-50 transition-transform", isOpen && "rotate-180")} />
                    </div>

                    {isOpen && !disabled && (
                        <div className="absolute z-50 mt-1 max-h-72 w-full overflow-auto rounded-md border bg-(--background-w10) p-1 shadow-lg">
                            {sortedEvaluators.length === 0 ? <div className="px-2 py-3 text-center text-xs text-(--secondary-text-wMain)">No evaluators found</div> : sortedEvaluators.map(renderItem)}
                            {isAtMax && <div className="px-2 py-2 text-center text-xs text-(--secondary-text-wMain)">You can pick at most {MAX_EVALUATORS} evaluators. Remove one to add another.</div>}
                            <div className="sticky bottom-0 mt-1 border-t bg-(--background-w10) px-2 py-2 text-sm text-(--secondary-text-wMain)">
                                Don&apos;t see the evaluator you need?{" "}
                                <Button
                                    variant="ghost"
                                    disabled={isAtMax}
                                    tooltip={isAtMax ? `You can pick at most ${MAX_EVALUATORS} evaluators. Remove one to create another.` : undefined}
                                    onClick={e => {
                                        e.stopPropagation();
                                        setIsOpen(false);
                                        onCreateClick();
                                    }}
                                >
                                    Create Your Own
                                </Button>
                            </div>
                        </div>
                    )}
                </div>
                {hideLabel && (
                    <div className="w-36 shrink-0">
                        <Button variant="ghost" size="sm" onClick={onCreateClick} disabled={isAtMax} tooltip={isAtMax ? `You can pick at most ${MAX_EVALUATORS} evaluators. Remove one to create another.` : undefined}>
                            <Plus className="mr-0.5 size-3" />
                            Create Evaluator
                        </Button>
                    </div>
                )}
            </div>
        </div>
    );
}
