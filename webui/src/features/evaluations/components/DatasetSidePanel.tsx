// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { X } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { Button } from "@/ui/button";
import { Input } from "@/ui/input";
import { Separator } from "@/ui/separator";
import type { EvalDataset } from "@/types/eval";

import { EmptyState } from "@/common";

import { useEvalExamplesPaginated } from "../hooks";

import { type DatasetExampleRow, DatasetExamplesTable } from "./DatasetExamplesTable";
import { ExamplesPaginationFooter } from "./ExamplesPaginationFooter";

const SIDE_PANEL_PAGE_SIZE = 5;
const PREVIEW_PAGE_SIZE = 5;

function renderExamplesBody({ loading, examples, contentFilter, withInputData }: { loading: boolean; examples: DatasetExampleRow[]; contentFilter: string; withInputData: boolean }) {
    if (loading) return <EmptyState variant="loading" title="Loading examples..." />;
    if (examples.length === 0) {
        return <div className="py-8 text-center text-sm text-(--secondary-text-wMain)">{contentFilter ? "No examples match the filter." : "No examples in this dataset."}</div>;
    }
    return <DatasetExamplesTable rows={examples} showArtifacts showInputData={withInputData} />;
}

interface DatasetSidePanelProps {
    readonly dataset: EvalDataset;
    readonly onClose: () => void;
    readonly previewMode?: boolean;
}

export function DatasetSidePanel({ dataset, onClose, previewMode }: DatasetSidePanelProps) {
    const navigate = useNavigate();
    const pageSize = previewMode ? PREVIEW_PAGE_SIZE : SIDE_PANEL_PAGE_SIZE;
    const { examples, loading, pageNumber, setPageNumber, totalPages, totalCount, contentFilter, setContentFilter } = useEvalExamplesPaginated(dataset.id, { pageSize });

    const effectiveTotalCount = totalCount > 0 ? totalCount : examples.length;
    const effectiveTotalPages = totalPages > 1 ? totalPages : Math.ceil(effectiveTotalCount / pageSize);
    const displayExamples = examples.length > pageSize ? examples.slice((pageNumber - 1) * pageSize, pageNumber * pageSize) : examples;

    return (
        <div className="flex h-full w-full flex-col overflow-hidden bg-(--background-w10)">
            <div className="flex shrink-0 items-center justify-between gap-3 px-5 py-4">
                {previewMode ? (
                    <span className="text-base font-semibold">Dataset Preview</span>
                ) : (
                    <span className="min-w-0 truncate text-base font-semibold" title={dataset.name}>
                        {dataset.name}
                    </span>
                )}
                <div className="flex shrink-0 items-center gap-2">
                    {!previewMode && (
                        <>
                            <Button size="sm" onClick={() => navigate(`/evaluations/experiments/new?datasetId=${dataset.id}`)}>
                                Create Experiment
                            </Button>
                            <Button variant="outline" size="sm" onClick={() => navigate(`/evaluations/datasets/${dataset.id}`)}>
                                Open Full Page
                            </Button>
                        </>
                    )}
                    <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
                        <X className="size-5" />
                    </Button>
                </div>
            </div>

            <Separator />

            {!previewMode && (
                <div className="shrink-0 px-5 pt-3 pb-1">
                    <Input placeholder="Filter by content" value={contentFilter} onChange={e => setContentFilter(e.target.value)} className="w-full" />
                </div>
            )}

            <div className="min-h-0 flex-1 overflow-y-auto px-5 py-3">{renderExamplesBody({ loading, examples: displayExamples, contentFilter, withInputData: (dataset.inputDataExampleCount ?? 0) > 0 })}</div>

            {displayExamples.length > 0 && (
                <div className="shrink-0 border-t px-5 py-2">
                    <ExamplesPaginationFooter page={pageNumber} pageSize={pageSize} totalPages={effectiveTotalPages} totalCount={effectiveTotalCount} onPageChange={setPageNumber} summariseSinglePage layout="stacked" />
                </div>
            )}
        </div>
    );
}
