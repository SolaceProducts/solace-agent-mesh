// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { ChevronDown, Plus, Sparkles, Upload } from "lucide-react";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { SearchableCombobox } from "@/ui/searchable-combobox";
import type { EvalDataset } from "@/types/eval";

interface DatasetFieldProps {
    readonly datasets: EvalDataset[];
    readonly value: string;
    readonly onChange: (id: string) => void;
    readonly onImportClick: () => void;
    readonly onCreateClick: () => void;
    readonly onAiGenerateClick: () => void;
    readonly disabled?: boolean;
}

const getDatasetId = (d: EvalDataset) => d.id;
const getDatasetSearchText = (d: EvalDataset) => d.name;
const renderDatasetSelected = (d: EvalDataset) => <span>{d.name}</span>;
const renderDatasetItem = (d: EvalDataset) => (
    <span className="flex flex-1 flex-col">
        <span className="font-medium">{d.name}</span>
        {d.description && <span className="truncate text-xs text-(--secondary-text-wMain)">{d.description}</span>}
    </span>
);

export function DatasetField({ datasets, value, onChange, onImportClick, onCreateClick, onAiGenerateClick, disabled }: DatasetFieldProps) {
    const hasDatasets = datasets.length > 0;
    const sorted = useMemo(() => [...datasets].sort((a, b) => a.name.localeCompare(b.name)), [datasets]);

    const menuItems = (
        <>
            <DropdownMenuItem onClick={onImportClick}>
                <Upload className="mr-2 size-4" /> Import From File
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onCreateClick}>
                <Plus className="mr-2 size-4" /> Create New Dataset
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onAiGenerateClick}>
                <Sparkles className="mr-2 size-4" /> Generate with AI
            </DropdownMenuItem>
        </>
    );

    if (!hasDatasets) {
        return (
            <div className="flex flex-col items-center justify-center gap-3 rounded-xs border border-dashed bg-(--secondary-w10)/40 px-4 py-6 text-center">
                <div className="flex flex-col gap-1">
                    <span className="text-sm font-semibold">No Dataset Found</span>
                    <span className="text-xs text-(--secondary-text-wMain)">Experiments require a dataset. Add one below to get started.</span>
                </div>
                <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                        <Button variant="outline" size="sm" disabled={disabled}>
                            Add Dataset
                            <ChevronDown className="ml-1 size-4" />
                        </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="center">{menuItems}</DropdownMenuContent>
                </DropdownMenu>
            </div>
        );
    }

    return (
        <div className="flex items-center gap-3">
            <div className="flex-1">
                <SearchableCombobox
                    items={sorted}
                    value={value}
                    onChange={onChange}
                    disabled={disabled}
                    getId={getDatasetId}
                    getSearchText={getDatasetSearchText}
                    renderSelected={renderDatasetSelected}
                    renderItem={renderDatasetItem}
                    searchPlaceholder="Search datasets..."
                    noMatchText="No datasets match your search."
                />
            </div>
            <div className="w-36 shrink-0">
                <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="sm" disabled={disabled}>
                            Add Dataset
                            <ChevronDown className="ml-0.5 size-3" />
                        </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">{menuItems}</DropdownMenuContent>
                </DropdownMenu>
            </div>
        </div>
    );
}
