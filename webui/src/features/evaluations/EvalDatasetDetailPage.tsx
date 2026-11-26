// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";
import { Pencil } from "lucide-react";
import { useLoaderData, useNavigate, useSearchParams } from "react-router-dom";

import { evaluationsApi, useDeleteDataset, useUpdateDataset } from "@/api/evaluations";

import { PageContentWrapper, PageFooter, PageSection } from "@/common/layout";

import { Button } from "@/ui/button";
import { Table, TableBody, TableCell, TableRow } from "@/ui/table";
import type { EvalDataset, EvalExperiment } from "@/types/eval";

import { EmptyState, Header } from "@/common";

import { DatasetFormBody, DetailPageLoading, DetailPageNotFound, ExampleArtifactField, ExamplesPaginationFooter, ExamplesTableHeader, ExpectedFactsCell, ExpectedPathCell, ExperimentUsageBanner, InlineEditableCell, InputDataCell } from "./components";
import { EVAL_ROUTES, MAX_EXAMPLES_PER_DATASET } from "./constants";
import { useEvalExamplesPaginated } from "./hooks";
import { downloadExampleArtifact, examplesDescription, isUntitledPlaceholder } from "./utils";

const EXAMPLES_PAGE_SIZE = 10;

function datasetHeaderTitle(isNew: boolean, isEditMode: boolean, displayName: string): string {
    if (isNew) return "Create Dataset";
    if (isEditMode) return "Edit Dataset";
    return displayName || "Dataset";
}

export function EvalDatasetDetailPage() {
    const loaderData: { datasetId?: string } = useLoaderData();
    const datasetId = loaderData.datasetId;
    const navigate = useNavigate();
    const [searchParams, setSearchParams] = useSearchParams();

    const [dataset, setDataset] = useState<EvalDataset | null>(null);
    const [dsLoading, setDsLoading] = useState(true);
    const [isNew, setIsNew] = useState(false);
    const [isEditMode, setIsEditMode] = useState(false);
    const [editName, setEditName] = useState("");
    const [editDesc, setEditDesc] = useState("");
    const [relatedExperiments, setRelatedExperiments] = useState<EvalExperiment[]>([]);
    const [creationSource, setCreationSource] = useState<"import" | "ai" | "manual">("manual");
    const nameInputRef = useRef<HTMLInputElement>(null);

    const { examples, loading: exLoading, pageNumber: exPage, setPageNumber: setExPage, totalPages: exTotalPages, totalCount: exTotalCount } = useEvalExamplesPaginated(isEditMode ? undefined : datasetId, { pageSize: EXAMPLES_PAGE_SIZE });

    const deleteDatasetMutation = useDeleteDataset();
    const updateDatasetMutation = useUpdateDataset();

    useEffect(() => {
        if (!datasetId) return;
        setDsLoading(true);
        setRelatedExperiments([]);
        evaluationsApi
            .getDataset(datasetId)
            .then(ds => {
                setDataset(ds);
                const newFlag = searchParams.get("edit") === "true";
                const sourceParam = searchParams.get("source");
                const source: "import" | "ai" | "manual" = sourceParam === "import" || sourceParam === "ai" ? sourceParam : "manual";
                setIsNew(newFlag);
                setIsEditMode(newFlag);
                setCreationSource(source);
                setEditName(newFlag && isUntitledPlaceholder(ds.name) ? "" : ds.name);
                setEditDesc(ds.description ?? "");
                if (newFlag) {
                    setSearchParams({}, { replace: true });
                    setTimeout(() => nameInputRef.current?.focus(), 100);
                }
                if (!newFlag && ds.experimentCount > 0) {
                    evaluationsApi
                        .getExperiments({ datasetId })
                        .then(setRelatedExperiments)
                        .catch(() => setRelatedExperiments([]));
                }
            })
            .catch(() => setDataset(null))
            .finally(() => setDsLoading(false));
    }, [datasetId]); // eslint-disable-line react-hooks/exhaustive-deps

    const displayName = dataset?.name && !isUntitledPlaceholder(dataset.name) ? dataset.name : "";

    const breadcrumbs = [
        { label: "Experiments", onClick: () => navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB) },
        { label: "Datasets", onClick: () => navigate(EVAL_ROUTES.LAB_DATASETS_TAB) },
    ];

    const headerButtons: React.ReactElement[] = [];
    if (!isEditMode && !isNew && dataset) {
        headerButtons.push(
            <Button key="edit" variant="outline" onClick={() => setIsEditMode(true)}>
                <Pencil className="mr-1 size-4" /> Edit
            </Button>
        );
    }

    if (dsLoading) {
        return <DetailPageLoading title="Dataset" breadcrumbs={breadcrumbs} />;
    }

    if (!dataset) {
        return <DetailPageNotFound title="Dataset" breadcrumbs={breadcrumbs} notFoundTitle="Dataset not found" backLabel="Back to Datasets" onBack={() => navigate(EVAL_ROUTES.LAB_DATASETS_TAB)} />;
    }

    const hasInputData = (dataset?.inputDataExampleCount ?? 0) > 0;

    const renderReadOnlyView = () => (
        <>
            <PageSection>
                <div className="flex flex-col gap-2">
                    <div className="text-sm font-semibold">Description</div>
                    <div className="max-w-lg text-sm whitespace-pre-wrap">{editDesc || <span className="text-(--secondary-text-wMain)">No description</span>}</div>
                </div>
            </PageSection>

            <PageSection>
                <div className="flex items-start justify-between gap-4">
                    <div className="flex flex-col gap-1">
                        <div className="flex items-center gap-2">
                            <div className="text-sm font-semibold">Examples</div>
                            {exTotalCount > 0 && (
                                <span className="text-xs text-(--secondary-text-wMain)">
                                    {exTotalCount} / {MAX_EXAMPLES_PER_DATASET}
                                </span>
                            )}
                        </div>
                        <div className="text-sm text-(--secondary-text-wMain)">{examplesDescription(hasInputData)}</div>
                    </div>
                </div>

                {exLoading && <EmptyState variant="loading" title="Loading examples..." />}
                {!exLoading && examples.length > 0 && (
                    <div className="rounded-xs border">
                        <Table className="table-fixed">
                            <ExamplesTableHeader withInputData={hasInputData} />
                            <TableBody borderLastRow>
                                {examples.map((ex, idx) => (
                                    <TableRow key={ex.id} className="align-top">
                                        <TableCell className="pl-4 text-xs text-(--secondary-text-wMain)">{(exPage - 1) * EXAMPLES_PAGE_SIZE + idx + 1}</TableCell>
                                        <TableCell className="[overflow-wrap:anywhere]">
                                            {ex.inputData && ex.prompt && <span className="px-2 text-xs text-(--secondary-text-wMain)">Evaluator instructions</span>}
                                            <InlineEditableCell value={ex.prompt} onSave={async () => {}} readOnly />
                                            {ex.inputData && <InputDataCell value={ex.inputData} />}
                                        </TableCell>
                                        <TableCell className="[overflow-wrap:anywhere]">
                                            <InlineEditableCell value={ex.expectedResponse ?? ""} onSave={async () => {}} readOnly />
                                            {ex.expectedPath && <ExpectedPathCell value={ex.expectedPath} />}
                                            {ex.expectedFacts && <ExpectedFactsCell value={ex.expectedFacts} />}
                                        </TableCell>
                                        <TableCell>
                                            <ExampleArtifactField artifact={ex.inputArtifact} onDownload={() => downloadExampleArtifact(evaluationsApi, ex, "input")} />
                                        </TableCell>
                                        <TableCell>
                                            <ExampleArtifactField artifact={ex.expectedArtifact} onDownload={() => downloadExampleArtifact(evaluationsApi, ex, "expected")} subject="expected file" />
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                        <ExamplesPaginationFooter page={exPage} pageSize={EXAMPLES_PAGE_SIZE} totalPages={exTotalPages} totalCount={exTotalCount} onPageChange={setExPage} />
                    </div>
                )}
            </PageSection>
        </>
    );

    const headerTitle = datasetHeaderTitle(isNew, isEditMode, displayName);

    const experimentCount = relatedExperiments.length;
    const showUsageBanner = isEditMode && !isNew && experimentCount > 0;

    return (
        <div className="flex h-full flex-col">
            <Header title={headerTitle} breadcrumbs={breadcrumbs} buttons={headerButtons} />
            <PageContentWrapper className="p-6" innerClassName="gap-8">
                {showUsageBanner && <ExperimentUsageBanner resourceLabel="dataset" experiments={relatedExperiments} />}
                {isEditMode && datasetId ? (
                    <DatasetFormBody
                        ref={nameInputRef}
                        datasetId={datasetId}
                        dataset={dataset}
                        onDatasetChange={setDataset}
                        name={editName}
                        onNameChange={setEditName}
                        description={editDesc}
                        onDescriptionChange={setEditDesc}
                        source={creationSource}
                        autosaveFields={false}
                    />
                ) : (
                    renderReadOnlyView()
                )}
            </PageContentWrapper>

            {isEditMode && (
                <PageFooter>
                    <Button
                        variant="ghost"
                        title={isNew ? "Cancel" : "Discard Changes"}
                        onClick={async () => {
                            if (isNew && datasetId) {
                                try {
                                    await deleteDatasetMutation.mutateAsync(datasetId);
                                } catch {}
                                navigate(EVAL_ROUTES.LAB_DATASETS_TAB);
                                return;
                            }
                            if (dataset) {
                                setEditName(dataset.name);
                                setEditDesc(dataset.description ?? "");
                            }
                            setIsEditMode(false);
                        }}
                    >
                        {isNew ? "Cancel" : "Discard Changes"}
                    </Button>
                    <Button
                        onClick={async () => {
                            if (!dataset) return;
                            const trimmedName = editName.trim();
                            const trimmedDesc = editDesc.trim();
                            const payload: { name?: string; description?: string } = {};
                            if (trimmedName && trimmedName !== dataset.name) payload.name = trimmedName;
                            if (trimmedDesc !== (dataset.description ?? "")) payload.description = trimmedDesc;
                            try {
                                if (Object.keys(payload).length > 0) {
                                    const updated = await updateDatasetMutation.mutateAsync({ id: dataset.id, data: payload });
                                    setDataset(updated);
                                }
                                if (isNew) {
                                    navigate(EVAL_ROUTES.LAB_DATASETS_TAB);
                                } else {
                                    setIsEditMode(false);
                                }
                            } catch {}
                        }}
                        disabled={!editName.trim() || !editDesc.trim()}
                    >
                        {isNew ? "Create" : "Save"}
                    </Button>
                </PageFooter>
            )}
        </div>
    );
}
