// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";
import { ChevronDown, Database, ExternalLink, Plus, Sparkles, Trash2, Upload } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";

import { evaluationsApi, useDeleteDataset, useEvalExperiments } from "@/api/evaluations";

import { useUserContext } from "@/providers/contexts";

import { PageContentWrapper } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { SearchInput } from "@/ui/search-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import type { EvalDataset } from "@/types/eval";

import { EmptyState, Header, InfoTipCard, MessageBanner, MorePopoverButton, PaginationControls } from "@/common";

import samExperimentDatasetMiniIllustration from "./assets/samExperimentDatasetMiniIllustration.png";
import { AiGenerationDialog, DatasetImportDialog, DatasetSidePanel, ResizableSidePanel, TypedDeleteConfirmationDialog } from "./components";
import { EVAL_DOCS } from "./constants";
import { useEvalDatasetsPaginated, useResizableSidePanel } from "./hooks";
import { createUntitledDataset } from "./utils";

interface DatasetCardProps {
    readonly dataset: EvalDataset;
    readonly selected?: boolean;
    readonly onClick: () => void;
    readonly onDelete?: () => void;
}

function DatasetCard({ dataset, selected, onClick, onDelete }: DatasetCardProps) {
    return (
        <div className={cn("relative flex flex-col gap-2 border-b px-4 py-3 transition-colors last:border-b-0 hover:bg-(--secondary-w10)/50 sm:flex-row sm:items-center sm:justify-between sm:gap-0", selected && "bg-(--secondary-w10)/70")}>
            <button type="button" onClick={onClick} aria-label={`Open ${dataset.name}`} className="absolute inset-0 cursor-pointer rounded focus-visible:ring-2 focus-visible:ring-(--brand-wMain) focus-visible:outline-none" />
            <div className="pointer-events-none relative flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="truncate text-sm font-semibold">{dataset.name}</span>
                {dataset.description && <span className="truncate text-sm text-(--secondary-text-wMain)">{dataset.description}</span>}
            </div>
            <div className="pointer-events-none relative flex shrink-0 items-center gap-4">
                <div className="flex flex-1 flex-col items-start gap-0.5 text-sm sm:flex-none sm:items-end">
                    <span>
                        Contains{" "}
                        <strong>
                            {dataset.exampleCount} example{dataset.exampleCount !== 1 ? "s" : ""}
                        </strong>
                    </span>
                    {dataset.experimentCount > 0 && (
                        <span>
                            Used in{" "}
                            <strong>
                                {dataset.experimentCount} experiment{dataset.experimentCount !== 1 ? "s" : ""}
                            </strong>
                        </span>
                    )}
                </div>
                {onDelete && (
                    <span className="pointer-events-auto">
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>{MorePopoverButton}</DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                                <DropdownMenuItem onClick={onDelete}>
                                    <Trash2 className="mr-2 size-4" /> Delete
                                </DropdownMenuItem>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    </span>
                )}
            </div>
        </div>
    );
}

function DatasetsEmptyState({ canCreate, onCreateNew, onImport, onAiGenerate }: { readonly canCreate: boolean; readonly onCreateNew: () => void; readonly onImport: () => void; readonly onAiGenerate: () => void }) {
    return (
        <div className="flex flex-col items-center gap-6 p-12">
            <div className="flex size-24 items-center justify-center rounded-full bg-(--secondary-w10)">
                <Database className="size-12 text-(--secondary-text-wMain)" />
            </div>
            <div className="flex flex-col items-center gap-2 text-center">
                <h3 className="text-lg font-semibold">Create Your First Dataset</h3>
                <p className="max-w-lg text-sm text-(--secondary-text-wMain)">
                    A dataset gives your agents a benchmark to use when running an experiment. It contains a set of prompts optionally paired with expected responses to put the agent to the test. Add examples manually or upload a file to get started,
                    and you&apos;ll be ready to run experiments and see how well your agent holds up.
                </p>
            </div>
            {canCreate && (
                <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                        <Button>
                            Create New Dataset <ChevronDown className="ml-1 size-4" />
                        </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="center">
                        <DropdownMenuItem onClick={onAiGenerate}>
                            <Sparkles className="mr-2 size-4" /> Generate with AI
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={onImport}>
                            <Upload className="mr-2 size-4" /> Import From File
                        </DropdownMenuItem>
                        <DropdownMenuItem onClick={onCreateNew}>
                            <Plus className="mr-2 size-4" /> Create New
                        </DropdownMenuItem>
                    </DropdownMenuContent>
                </DropdownMenu>
            )}
            <a href={EVAL_DOCS.DATASETS} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-sm text-(--primary-wMain) hover:underline">
                Learn about datasets and see examples
                <ExternalLink className="size-3.5" />
            </a>
        </div>
    );
}

const SORT_OPTIONS = [
    { value: "name:asc", label: "Alphabetical Ascending" },
    { value: "name:desc", label: "Alphabetical Descending" },
    { value: "experiment_count:desc", label: "Experiment Usage" },
];

const INFO_BANNER_KEY = "eval-datasets-info-dismissed";

interface EvalDatasetsPageProps {
    readonly embedded?: boolean;
}

export function EvalDatasetsPage({ embedded }: EvalDatasetsPageProps = {}) {
    const navigate = useNavigate();
    const location = useLocation();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreate = userHasCapability(CAPABILITIES.createEvaluations);
    const canDelete = userHasCapability(CAPABILITIES.deleteEvaluations);
    const { datasets, loading, pageNumber, setPageNumber, totalPages, nameFilter, setNameFilter, sortBy, setSortBy, sortOrder, setSortOrder, totalCount, currentPageSize, fetchDatasets } = useEvalDatasetsPaginated();

    const [deleteTarget, setDeleteTarget] = useState<EvalDataset | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);
    const [createError, setCreateError] = useState<string | null>(null);
    const [importOpen, setImportOpen] = useState(false);
    const [aiGenerateOpen, setAiGenerateOpen] = useState(false);
    const [selectedDataset, setSelectedDataset] = useState<EvalDataset | null>(null);

    useEffect(() => {
        const params = new URLSearchParams(location.search);
        const selectedId = params.get("selected");
        if (selectedId && datasets.length > 0) {
            const match = datasets.find(d => d.id === selectedId);
            if (match) {
                setSelectedDataset(match);
                params.delete("selected");
                const search = params.toString();
                navigate(`${location.pathname}${search ? `?${search}` : ""}`, { replace: true });
            }
        }
    }, [datasets, location.pathname, location.search, navigate]);

    const experimentCount = deleteTarget?.experimentCount ?? 0;
    const { data: affectedExperiments = [] } = useEvalExperiments({ datasetId: deleteTarget?.id }, { enabled: !!deleteTarget && experimentCount > 0 });
    const deleteDatasetMutation = useDeleteDataset();

    const isPanelOpen = !!selectedDataset;
    const { containerRef, width: sidePanelWidth, handleDragStart } = useResizableSidePanel(isPanelOpen);

    const sortValue = `${sortBy}:${sortOrder}`;

    const handleSortChange = (value: string) => {
        const [newSortBy, newSortOrder] = value.split(":");
        setSortBy(newSortBy);
        setSortOrder(newSortOrder);
    };

    const handleCreateNew = async () => {
        setCreateError(null);
        try {
            const created = await createUntitledDataset(evaluationsApi);
            navigate(`/evaluations/datasets/${created.id}?edit=true`);
        } catch (err) {
            setCreateError(getErrorMessage(err));
        }
    };

    const handleDelete = async () => {
        if (!deleteTarget) return;
        setDeleting(true);
        setDeleteError(null);
        try {
            await deleteDatasetMutation.mutateAsync(deleteTarget.id);
            if (selectedDataset?.id === deleteTarget.id) {
                setSelectedDataset(null);
            }
            setDeleteTarget(null);
            await fetchDatasets();
        } catch (err) {
            setDeleteError(getErrorMessage(err));
            throw err;
        } finally {
            setDeleting(false);
        }
    };

    const handleDeleteDialogChange = (open: boolean) => {
        if (!open) {
            setDeleteTarget(null);
            setDeleteError(null);
        }
    };

    const handleImported = (datasetId: string) => {
        navigate(`/evaluations/datasets/${datasetId}?edit=true&source=import`);
    };

    const handleCardClick = (ds: EvalDataset) => {
        if (selectedDataset?.id === ds.id) {
            setSelectedDataset(null);
        } else {
            setSelectedDataset(ds);
        }
    };

    const handleCloseSidePanel = useCallback(() => {
        setSelectedDataset(null);
    }, []);

    const createButton = (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <Button variant="ghost">+ Create New Dataset</Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => setAiGenerateOpen(true)}>
                    <Sparkles className="mr-2 size-4" /> Generate with AI
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => setImportOpen(true)}>
                    <Upload className="mr-2 size-4" /> Import From File
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => void handleCreateNew()}>
                    <Plus className="mr-2 size-4" /> Create New
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    );

    const renderContent = () => {
        if (loading && datasets.length === 0) return <EmptyState variant="loading" title="Loading datasets..." />;
        if (!loading && datasets.length === 0 && !nameFilter) {
            return (
                <div className="flex h-full items-center justify-center">
                    <DatasetsEmptyState canCreate={canCreate} onCreateNew={() => void handleCreateNew()} onImport={() => setImportOpen(true)} onAiGenerate={() => setAiGenerateOpen(true)} />
                </div>
            );
        }
        return (
            <div className="flex flex-col gap-4">
                <InfoTipCard
                    storageKey={INFO_BANNER_KEY}
                    header="A dataset gives your agents a benchmark to use when running an experiment"
                    description="It contains a set of prompts optionally paired with expected responses to put the agent to the test. Add examples manually or upload a file to get started, and you'll be ready to run experiments and see how well your agent holds up."
                    learnMoreText="Learn about datasets and see examples"
                    learnMoreUrl={EVAL_DOCS.DATASETS}
                    illustration={samExperimentDatasetMiniIllustration}
                />

                <div className="flex flex-wrap items-center justify-between gap-3">
                    <SearchInput placeholder="Filter by name" value={nameFilter} onChange={setNameFilter} />
                    <div className="flex flex-wrap items-center gap-2">
                        <span className="text-sm text-(--secondary-text-wMain)">Sort By</span>
                        <Select value={sortValue} onValueChange={handleSortChange}>
                            <SelectTrigger className="w-full min-w-0 sm:w-52">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {SORT_OPTIONS.map(opt => (
                                    <SelectItem key={opt.value} value={opt.value}>
                                        {opt.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                </div>

                {datasets.length === 0 ? (
                    <div className="py-8 text-center text-sm text-(--secondary-text-wMain)">No datasets match the current filter.</div>
                ) : (
                    <div className="rounded-xs border">
                        {datasets.map(ds => (
                            <DatasetCard key={ds.id} dataset={ds} selected={selectedDataset?.id === ds.id} onClick={() => handleCardClick(ds)} onDelete={canDelete ? () => setDeleteTarget(ds) : undefined} />
                        ))}
                    </div>
                )}
            </div>
        );
    };

    return (
        <div className="flex h-full flex-col">
            {!embedded && <Header title="Datasets" buttons={[<span key="new">{createButton}</span>]} />}
            <div ref={containerRef} className="relative flex-1 overflow-hidden">
                <div className="flex h-full flex-col">
                    <PageContentWrapper className="flex-1 overflow-auto p-6" innerClassName="gap-6">
                        {createError && <MessageBanner variant="error" message={createError} />}
                        {renderContent()}
                    </PageContentWrapper>
                    {datasets.length > 0 && !isPanelOpen && (
                        <div className="flex items-center justify-between border-t px-6 py-3">
                            <div />
                            {totalPages > 1 && <PaginationControls totalPages={totalPages} currentPage={pageNumber} onPageChange={setPageNumber} />}
                            <span className="text-sm text-(--secondary-text-wMain)">
                                Showing {(pageNumber - 1) * currentPageSize + 1}-{Math.min(pageNumber * currentPageSize, totalCount)} of {totalCount} results
                            </span>
                        </div>
                    )}
                </div>

                <ResizableSidePanel open={isPanelOpen && !!selectedDataset} width={sidePanelWidth} onDragStart={handleDragStart}>
                    {isPanelOpen && selectedDataset && <DatasetSidePanel dataset={selectedDataset} onClose={handleCloseSidePanel} />}
                </ResizableSidePanel>
            </div>

            <DatasetImportDialog open={importOpen} onClose={() => setImportOpen(false)} onImported={handleImported} />

            <AiGenerationDialog mode="dataset" open={aiGenerateOpen} onClose={() => setAiGenerateOpen(false)} onGenerated={dataset => navigate(`/evaluations/datasets/${dataset.id}?edit=true&source=ai`)} />

            <TypedDeleteConfirmationDialog
                title="Delete Dataset"
                open={deleteTarget !== null}
                onOpenChange={handleDeleteDialogChange}
                deleting={deleting}
                error={deleteError}
                onConfirm={handleDelete}
                primaryMessage={
                    <>
                        This will permanently delete <strong>{deleteTarget?.name}</strong> and all {deleteTarget?.exampleCount ?? 0} example{(deleteTarget?.exampleCount ?? 0) !== 1 ? "s" : ""}. This action cannot be undone.
                    </>
                }
                warningMessage={
                    experimentCount > 0 ? (
                        <div className="flex flex-col gap-2">
                            <span>
                                This dataset is used in <strong>{experimentCount}</strong> {experimentCount === 1 ? "experiment. The experiment" : "experiments. The experiments"} will remain but will lose {experimentCount === 1 ? "its" : "their"}{" "}
                                dataset and need to be reconfigured before {experimentCount === 1 ? "it can be" : "they can be"} run again.
                            </span>
                            {affectedExperiments.length > 0 && (
                                <ul className="list-disc pl-5 text-sm">
                                    {affectedExperiments.map(e => (
                                        <li key={e.id}>{e.name}</li>
                                    ))}
                                </ul>
                            )}
                        </div>
                    ) : undefined
                }
                confirmLabel={
                    <>
                        Type <strong>DELETE</strong> to confirm.
                    </>
                }
            />
        </div>
    );
}
