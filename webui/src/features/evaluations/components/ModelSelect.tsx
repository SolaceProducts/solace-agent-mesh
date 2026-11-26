// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useId, useMemo, useRef, useState } from "react";
import { ChevronDown, Plus, XIcon } from "lucide-react";

import { useChatModelConfigs } from "@/api/models/hooks";
import { type ModelConfig } from "@/api/models/types";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { useClickOutside } from "@/ui/hooks/useClickOutside";
import type { ExperimentModelConfig } from "@/types/eval";

import { ModelConfigIcon } from "@/features/activation";

import { CreateModelDialog } from "./CreateModelDialog";
import { findModelByRef } from "./evaluatorForm";

const TRIGGER_OPEN_KEYS = new Set(["Enter", " ", "ArrowDown"]);

interface MultiProps {
    readonly multiple: true;
    readonly value: ExperimentModelConfig[];
    readonly onChange: (value: ExperimentModelConfig[]) => void;
    readonly disabled?: boolean;
    readonly max?: number;
    readonly activeModelId?: string;
    readonly hideLabel?: boolean;
}

interface SingleProps {
    readonly multiple?: false;
    readonly value: string;
    readonly onChange: (id: string) => void;
    readonly disabled?: boolean;
    readonly hideLabel?: boolean;
    readonly clearable?: boolean;
    readonly placeholder?: string;
}

type ModelSelectProps = MultiProps | SingleProps;

function toExperimentModel(m: ModelConfig): ExperimentModelConfig {
    return { modelConfigId: m.id, model: m.modelName ?? "", alias: m.alias };
}

export function ModelSelect(props: ModelSelectProps) {
    const { disabled, hideLabel } = props;
    const isMulti = props.multiple === true;
    const max = isMulti ? (props as MultiProps).max : undefined;
    const activeModelId = isMulti ? (props as MultiProps).activeModelId : undefined;
    const isAtMax = isMulti && max != null && (props as MultiProps).value.length >= max;

    const { data: models = [], isLoading, error, refetch } = useChatModelConfigs();
    const [isOpen, setIsOpen] = useState(false);
    const [search, setSearch] = useState("");
    const [createDialogOpen, setCreateDialogOpen] = useState(false);
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

    const selectedIds = useMemo(() => {
        if (isMulti) return new Set((props as MultiProps).value.map(v => v.modelConfigId));
        const id = (props as SingleProps).value;
        return id ? new Set([id]) : new Set<string>();
    }, [isMulti, props.value]);

    const filteredModels = useMemo(() => {
        const sorted = [...models].sort((a, b) => {
            if (activeModelId) {
                if (a.id === activeModelId && b.id !== activeModelId) return -1;
                if (b.id === activeModelId && a.id !== activeModelId) return 1;
            }
            return a.alias.localeCompare(b.alias);
        });
        if (!search.trim()) return sorted;
        const q = search.toLowerCase();
        return sorted.filter(m => m.alias.toLowerCase().includes(q) || (m.modelName ?? "").toLowerCase().includes(q));
    }, [models, search, activeModelId]);

    const handleItemClick = (m: ModelConfig) => {
        if (disabled) return;
        if (isMulti) {
            const { value, onChange } = props as MultiProps;
            if (selectedIds.has(m.id)) {
                onChange(value.filter(v => v.modelConfigId !== m.id));
            } else if (!isAtMax) {
                onChange([...value, toExperimentModel(m)]);
            }
            setSearch("");
        } else {
            (props as SingleProps).onChange(m.id);
            setIsOpen(false);
            setSearch("");
        }
    };

    const handleRemove = (id: string | undefined, e: React.MouseEvent) => {
        e.stopPropagation();
        if (isMulti) {
            const { value, onChange } = props as MultiProps;
            onChange(value.filter(v => v.modelConfigId !== id));
        } else {
            (props as SingleProps).onChange("");
        }
    };

    const popupId = useId();

    const handleTriggerClick = () => {
        if (disabled || isLoading) return;
        const next = !isOpen;
        setIsOpen(next);
        if (next) {
            setTimeout(() => inputRef.current?.focus(), 0);
        } else {
            setSearch("");
        }
    };

    const handleTriggerKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
        if (e.target !== e.currentTarget || !TRIGGER_OPEN_KEYS.has(e.key)) return;
        e.preventDefault();
        handleTriggerClick();
    };

    const handleCreated = (createdId: string) => {
        void refetch().then(({ data: updated }) => {
            if (isMulti) {
                const { value, onChange } = props as MultiProps;
                if (max != null && value.length >= max) return;
                const created = (updated as ModelConfig[] | undefined)?.find(m => m.id === createdId);
                if (created) onChange([...value, toExperimentModel(created)]);
            } else {
                (props as SingleProps).onChange(createdId);
            }
        });
    };

    if (error) {
        return <span className="text-xs text-(--error-wMain)">Failed to load model configurations.</span>;
    }

    const renderTriggerContent = () => {
        if (isMulti) {
            const multiValue = (props as MultiProps).value;
            if (multiValue.length === 0) {
                return isLoading ? <span className="text-(--secondary-text-wMain)">Loading...</span> : <span className="text-(--secondary-wMain)">Select models</span>;
            }
            return multiValue.map(v => {
                const model = models.find(m => m.id === v.modelConfigId);
                return <SelectedChip key={v.modelConfigId} id={v.modelConfigId} alias={v.alias} modelName={v.model} model={model} disabled={disabled} onRemove={handleRemove} />;
            });
        }
        const { value, placeholder, clearable } = props;
        const selectedModel = findModelByRef(models, value);
        const singleClearable = clearable ?? true;
        if (!selectedModel) {
            if (isLoading) return <span className="text-(--secondary-text-wMain)">Loading...</span>;
            if (!value) return <span className="text-(--secondary-wMain)">{placeholder ?? "Select a model"}</span>;
            return (
                <div className="flex min-w-0 items-center gap-2">
                    <span className="truncate font-medium">{value}</span>
                    {!disabled && singleClearable && (
                        <Button variant="ghost" size="icon" className="ml-1 h-4 min-h-0 w-4 min-w-0 p-2" title="Remove" onClick={e => handleRemove(value, e)}>
                            <XIcon />
                        </Button>
                    )}
                </div>
            );
        }
        return (
            <div className="flex min-w-0 items-center gap-2">
                <ModelConfigIcon model={selectedModel} size="xs" />
                <span className="truncate font-medium">{selectedModel.alias}</span>
                {selectedModel.modelName && <span className="truncate font-mono text-xs text-(--secondary-text-wMain)">{selectedModel.modelName}</span>}
                {!disabled && singleClearable && (
                    <Button variant="ghost" size="icon" className="ml-1 h-4 min-h-0 w-4 min-w-0 p-2" title="Remove" onClick={e => handleRemove(selectedModel.id, e)}>
                        <XIcon />
                    </Button>
                )}
            </div>
        );
    };

    return (
        <div className="flex flex-col gap-2">
            {!hideLabel && (
                <div className="flex items-center justify-between">
                    <span className="text-sm font-medium text-(--secondary-text-wMain)">
                        Model
                        <span className="ml-1 text-(--brand-wMain)">*</span>
                        {isMulti && max != null && (
                            <span className="ml-1 text-xs font-normal text-(--secondary-text-wMain)">
                                ({(props as MultiProps).value.length}/{max})
                            </span>
                        )}
                    </span>
                    <Button variant="ghost" size="sm" onClick={() => setCreateDialogOpen(true)} disabled={disabled}>
                        <Plus className="mr-0.5 size-3" />
                        New Model
                    </Button>
                </div>
            )}

            <CreateModelDialog open={createDialogOpen} onOpenChange={setCreateDialogOpen} onCreated={handleCreated} />

            <div className={cn("flex gap-3", hideLabel ? "items-center" : "flex-col items-stretch")}>
                <div className="relative w-full min-w-0 flex-1" ref={dropdownRef}>
                    <div
                        role="combobox"
                        aria-expanded={isOpen}
                        aria-haspopup="dialog"
                        aria-controls={isOpen ? popupId : undefined}
                        aria-disabled={disabled || isLoading}
                        tabIndex={disabled ? -1 : 0}
                        onClick={handleTriggerClick}
                        onKeyDown={handleTriggerKeyDown}
                        data-testid="modelSelectTrigger"
                        className={cn("flex min-h-[38px] w-full cursor-pointer items-center justify-between rounded-xs border px-3 py-2 text-sm shadow-xs", (disabled || isLoading) && "cursor-not-allowed opacity-50")}
                    >
                        <div className="flex flex-1 flex-wrap items-center gap-1 text-left">{renderTriggerContent()}</div>
                        <ChevronDown className={cn("ml-2 size-4 shrink-0 opacity-50 transition-transform", isOpen && "rotate-180")} />
                    </div>

                    {isOpen && !disabled && (
                        <div id={popupId} role="dialog" aria-label="Select a model" className="absolute z-50 mt-1 max-h-72 w-full overflow-auto rounded-md border bg-(--background-w10) p-1 shadow-lg">
                            <div className="px-2 py-1.5">
                                <input
                                    ref={inputRef}
                                    value={search}
                                    onChange={e => setSearch(e.target.value)}
                                    placeholder="Search models..."
                                    className="w-full border-0 bg-transparent p-0 text-sm outline-none placeholder:text-(--secondary-text-wMain)"
                                />
                            </div>
                            <div className="border-t" />

                            {filteredModels.length === 0 ? (
                                <div className="px-2 py-3 text-center text-xs text-(--secondary-text-wMain)">{models.length === 0 ? "No models configured." : "No models match your search."}</div>
                            ) : (
                                filteredModels.map((m, idx) => {
                                    const isSelected = selectedIds.has(m.id);
                                    const isActive = activeModelId === m.id;
                                    const isDisabled = isAtMax && !isSelected;
                                    const prevIsActive = idx > 0 && activeModelId === filteredModels[idx - 1].id;
                                    return (
                                        <React.Fragment key={m.id}>
                                            {prevIsActive && <div className="my-1 border-t" />}
                                            <button
                                                type="button"
                                                onClick={() => handleItemClick(m)}
                                                disabled={isDisabled}
                                                className={cn(
                                                    "relative flex w-full cursor-default items-center gap-2 px-2 py-1.5 text-left text-sm select-none",
                                                    isSelected && "bg-(--success-w10)",
                                                    !isSelected && isActive && "bg-(--secondary-w10)/50",
                                                    isDisabled ? "cursor-not-allowed opacity-40" : "hover:bg-(--primary-w10) hover:text-(--primary-text-w100)"
                                                )}
                                            >
                                                <ModelConfigIcon model={m} size="xs" />
                                                <span className="flex-1 font-medium">{m.alias}</span>
                                                {isActive && <span className="inline-flex items-center rounded-sm bg-(--brand-w10) px-1.5 py-0.5 text-xs font-medium text-(--brand-w100)">Agent Model</span>}
                                                <span className="shrink-0 font-mono text-xs text-(--secondary-text-wMain)">{m.modelName}</span>
                                            </button>
                                        </React.Fragment>
                                    );
                                })
                            )}
                        </div>
                    )}
                </div>
                {hideLabel && (
                    <div className="w-36 shrink-0">
                        <Button variant="ghost" size="sm" onClick={() => setCreateDialogOpen(true)} disabled={disabled}>
                            <Plus className="mr-0.5 size-3" />
                            New Model
                        </Button>
                    </div>
                )}
            </div>
        </div>
    );
}

function SelectedChip({
    id,
    alias,
    modelName,
    model,
    disabled,
    onRemove,
}: {
    readonly id?: string;
    readonly alias: string;
    readonly modelName: string;
    readonly model: ModelConfig | undefined;
    readonly disabled?: boolean;
    readonly onRemove: (id: string | undefined, e: React.MouseEvent) => void;
}) {
    return (
        <div className="group relative inline-flex">
            <div className="inline-flex h-6 items-center gap-2 rounded-sm bg-(--secondary-w10) px-2 py-0.5 text-sm">
                <ModelConfigIcon model={model} size="xs" />
                {alias}
                {!disabled && (
                    <Button variant="ghost" size="icon" className="ml-0.5 h-2 min-h-0 w-2 min-w-0 p-2" title="Remove" onClick={e => onRemove(id, e)}>
                        <XIcon />
                    </Button>
                )}
            </div>
            <div className="pointer-events-none absolute top-full left-0 z-50 mt-1 hidden rounded-md border bg-(--background-w10) px-2 py-1.5 shadow-md group-hover:block">
                <div className="flex items-center gap-2 whitespace-nowrap">
                    <ModelConfigIcon model={model} size="xs" />
                    <div className="flex flex-col">
                        <span className="text-sm font-medium">{alias}</span>
                        <span className="font-mono text-xs text-(--secondary-text-wMain)">{modelName}</span>
                    </div>
                </div>
            </div>
        </div>
    );
}
