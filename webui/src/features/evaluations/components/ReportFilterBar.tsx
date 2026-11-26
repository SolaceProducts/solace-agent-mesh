// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { MultiSelect } from "@/common/MultiSelect";

import type { ExperimentModelConfig } from "@/types/eval";

import { createExperimentModelLookup, MODEL_COLORS } from "../utils";

interface ReportFilterBarProps {
    readonly allModels: string[];
    readonly selectedModels: string[];
    readonly onModelsChange: (models: string[]) => void;
    readonly allEvaluators: string[];
    readonly selectedEvaluators: string[];
    readonly onEvaluatorsChange: (evaluators: string[]) => void;
    readonly experimentModels?: ExperimentModelConfig[];
}

export function ReportFilterBar({ allModels, selectedModels, onModelsChange, allEvaluators, selectedEvaluators, onEvaluatorsChange, experimentModels }: ReportFilterBarProps) {
    if (allModels.length <= 1 && allEvaluators.length <= 1) return null;

    const { aliasFor, rawModelFor } = createExperimentModelLookup(experimentModels);

    return (
        <div className="flex flex-wrap items-center gap-3">
            <span className="text-sm text-(--secondary-text-wMain)">Filter by:</span>
            {allModels.length > 1 && (
                <MultiSelect
                    summaryMode
                    emptyMeansAll
                    placeholder="All Models"
                    className="min-w-45"
                    options={allModels}
                    value={selectedModels}
                    onChange={onModelsChange}
                    resolveLabel={aliasFor}
                    renderOption={opt => {
                        const idx = allModels.indexOf(opt);
                        const alias = aliasFor(opt);
                        const rawModel = rawModelFor(opt);
                        return (
                            <span className="flex min-w-0 items-center gap-2">
                                <span className="inline-block size-2 shrink-0 rounded-full" style={{ backgroundColor: MODEL_COLORS[idx % MODEL_COLORS.length] }} />
                                <span className="text-sm">{alias}</span>
                                {rawModel && rawModel !== alias && <span className="truncate text-xs text-(--secondary-text-wMain)">{rawModel}</span>}
                            </span>
                        );
                    }}
                />
            )}
            {allEvaluators.length > 1 && <MultiSelect summaryMode emptyMeansAll placeholder="All Evaluators" className="min-w-45" options={allEvaluators} value={selectedEvaluators} onChange={onEvaluatorsChange} />}
        </div>
    );
}
