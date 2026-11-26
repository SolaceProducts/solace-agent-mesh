// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { Download } from "lucide-react";
import { type NavigateFunction, useLoaderData, useNavigate } from "react-router-dom";

import { evaluationsApi, useDeleteRunGroup } from "@/api/evaluations";

import { PageContentWrapper } from "@/common/layout";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import type { EvalExperiment, EvalRunGroup, EvaluatorSnapshotEntry } from "@/types/eval";

import { ConfirmationDialog, EmptyState, Header, PageLayout } from "@/common";

import {
    aggregateEvaluatorStatsForModels,
    AuthRequiredBanner,
    EvalExampleInspector,
    EvalStatusBadge,
    EvaluatorTile,
    type ExampleRow,
    ExamplesTable,
    ExperimentReportBanner,
    type InspectorEvaluator,
    knownEvaluatorType,
    ReportFilterBar,
    ResizableSidePanel,
    RunDetailsAccordion,
    summarizeFailures,
    TargetTypeIcon,
    WorkflowEarlyAccessBadge,
} from "./components";
import { EVAL_ROUTES } from "./constants";
import { useEvalModelAxis, useEvalRunGroup, useEvalRunGroupResults, useEvalRuns, useExperimentReportActions, useResizableSidePanel } from "./hooks";
import { hasActiveRun, isWorkflowExperiment, trialConsistency } from "./utils";

type EvaluatorInfo = InspectorEvaluator & { description?: string };

export function buildEvaluatorInfo(group: { runs: { evaluatorSnapshot?: EvaluatorSnapshotEntry[] }[] } | null): EvaluatorInfo[] {
    if (!group) return [];
    const seen = new Map<string, EvaluatorInfo>();
    for (const run of group.runs) {
        for (const snap of run.evaluatorSnapshot ?? []) {
            if (!snap.id || !snap.name || seen.has(snap.id)) continue;
            seen.set(snap.id, {
                id: snap.id,
                name: snap.name,
                description: snap.description,
                type: knownEvaluatorType(snap.type),
                heuristicType: snap.heuristic_type,
                scoring: snap.decision_config?.scoring,
            });
        }
    }
    return Array.from(seen.values());
}

export function buildScoresByEvaluatorByModel(
    results: { resultsByModel: Record<string, { scores?: { evaluatorId: string; score?: number | null }[] }[]> } | null | undefined,
    evaluatorInfo: EvaluatorInfo[],
    allModels: string[]
): Map<string, Record<string, number[]>> {
    const out = new Map<string, Record<string, number[]>>();
    if (!results) return out;
    for (const ev of evaluatorInfo) {
        const byModel: Record<string, number[]> = {};
        for (const model of allModels) {
            const rs = results.resultsByModel[model] ?? [];
            const scores: number[] = [];
            for (const r of rs) {
                const s = r.scores?.find(s => s.evaluatorId === ev.id);
                if (s?.score != null) scores.push(s.score);
            }
            byModel[model] = scores;
        }
        out.set(ev.id, byModel);
    }
    return out;
}

export function buildReportTitle(group: EvalRunGroup | null, experiment: EvalExperiment | null): string {
    if (group?.runName?.trim()) {
        return `Experiment Report: ${group.runName}`;
    }
    if (experiment) {
        return `Experiment Report: ${experiment.name}`;
    }
    return "Experiment Report";
}

export function buildBreadcrumbs(experiment: EvalExperiment | null, navigate: NavigateFunction): { label: string; onClick?: () => void }[] {
    const crumbs: { label: string; onClick?: () => void }[] = [{ label: "Experiments", onClick: () => navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB) }];
    if (experiment) {
        crumbs.push({ label: experiment.name, onClick: () => navigate(EVAL_ROUTES.experiment(experiment.id)) });
    }
    crumbs.push({ label: "Experiment Report" });
    return crumbs;
}

export function buildModelScoreScope(byModel: Record<string, number[]>, visibleModels: string[]): Record<string, number[]> {
    const scoped: Record<string, number[]> = {};
    for (const m of visibleModels) scoped[m] = byModel[m] ?? [];
    return scoped;
}

type HeaderActionsProps = {
    status: EvalRunGroup["status"];
    canDelete: boolean;
    onExport: (format: "csv" | "json") => void;
    onDelete: () => void;
};

function ReportHeaderActions({ status, canDelete, onExport, onDelete }: Readonly<HeaderActionsProps>) {
    return (
        <>
            <EvalStatusBadge status={status} />
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="sm">
                        <Download className="mr-1 size-4" /> Export
                    </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                    <DropdownMenuItem onClick={() => onExport("csv")}>Export as CSV</DropdownMenuItem>
                    <DropdownMenuItem onClick={() => onExport("json")}>Export as JSON</DropdownMenuItem>
                </DropdownMenuContent>
            </DropdownMenu>
            {canDelete && (
                <Button variant="ghost" size="sm" onClick={onDelete}>
                    Delete Report
                </Button>
            )}
        </>
    );
}

function ReportTitle({ title, experiment }: { readonly title: string; readonly experiment: EvalExperiment | null }) {
    if (!experiment) return title;
    return (
        <span className="flex items-center gap-2">
            <TargetTypeIcon targetType={experiment.targetType} />
            {title}
            <WorkflowEarlyAccessBadge targetType={experiment.targetType} />
        </span>
    );
}

export function EvalExperimentReportPage() {
    const { groupId } = useLoaderData() as { groupId?: string };
    const navigate = useNavigate();

    const { group, loading, isActive } = useEvalRunGroup(groupId);
    const deleteRunGroupMutation = useDeleteRunGroup();
    const { results, loading: resultsLoading } = useEvalRunGroupResults(groupId, isActive);
    const { cancelRun } = useEvalRuns(group?.experimentId, { enableListQuery: false });

    const [experiment, setExperiment] = useState<EvalExperiment | null>(null);
    const [selectedRow, setSelectedRow] = useState<{ row: ExampleRow; exampleNumber: number } | null>(null);

    const [selectedModels, setSelectedModels] = useState<string[]>([]);
    const [selectedEvaluators, setSelectedEvaluators] = useState<string[]>([]);

    const { deleteOpen, setDeleteOpen, openDelete, closeDelete, confirmDelete, cancelOpen, setCancelOpen, openCancel, closeCancel, confirmCancel, cancelling, cancelError, handleExport } = useExperimentReportActions({
        group,
        experiment,
        deleteRunGroupMutation,
        cancelRun,
        navigate,
    });

    const {
        containerRef,
        width: sidePanelWidth,
        handleDragStart,
    } = useResizableSidePanel(!!selectedRow, {
        minWidth: 640,
        defaultFraction: 0.6,
        maxFraction: 0.8,
    });

    useEffect(() => {
        if (!group) return;
        evaluationsApi
            .getExperiment(group.experimentId)
            .then(setExperiment)
            .catch(() => setExperiment(null));
    }, [group]);

    const { effectiveModels, allModels } = useEvalModelAxis(experiment, group);
    const evaluatorInfo = useMemo(() => buildEvaluatorInfo(group), [group]);
    const evaluatorNameMap = useMemo(() => new Map(evaluatorInfo.map(e => [e.id, e.name])), [evaluatorInfo]);
    const allEvaluatorNames = useMemo(() => evaluatorInfo.map(e => e.name), [evaluatorInfo]);

    const visibleModels = useMemo(() => (selectedModels.length === 0 ? allModels : allModels.filter(m => selectedModels.includes(m))), [selectedModels, allModels]);
    const visibleEvaluators = useMemo(() => (selectedEvaluators.length === 0 ? evaluatorInfo : evaluatorInfo.filter(e => selectedEvaluators.includes(e.name))), [selectedEvaluators, evaluatorInfo]);

    const scoresByEvaluatorByModel = useMemo(() => buildScoresByEvaluatorByModel(results, evaluatorInfo, allModels), [results, evaluatorInfo, allModels]);

    const breadcrumbs = useMemo(() => buildBreadcrumbs(experiment, navigate), [experiment, navigate]);
    const title = buildReportTitle(group, experiment);
    const resultsByModel = useMemo(() => results?.resultsByModel ?? {}, [results]);

    const failures = useMemo(() => summarizeFailures(results), [results]);
    const authRequiredCount = failures.authRequired;
    const hideGenericFailureBanner = group?.status === "failed" && authRequiredCount > 0 && failures.other === 0;

    if (loading) {
        return (
            <PageLayout>
                <Header title="Experiment Report" breadcrumbs={breadcrumbs} />
                <EmptyState variant="loading" title="Loading…" />
            </PageLayout>
        );
    }

    if (!group) {
        return (
            <PageLayout>
                <Header title="Experiment Report" breadcrumbs={breadcrumbs} />
                <EmptyState
                    variant="error"
                    title="Report not found"
                    buttons={[
                        {
                            text: "Back to Experiments",
                            variant: "default",
                            onClick: () => navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB),
                        },
                    ]}
                />
            </PageLayout>
        );
    }

    const inspectorOpen = !!selectedRow;

    const handleOpenFullDetails = () => {
        if (!selectedRow) return;
        navigate(EVAL_ROUTES.runGroupExample(group.id, selectedRow.row.key));
    };

    const handleRunNew = experiment ? () => navigate(EVAL_ROUTES.experiment(experiment.id)) : undefined;

    const headerButtons = [<ReportHeaderActions key="actions" status={group.status} canDelete={!hasActiveRun(group.runs)} onExport={handleExport} onDelete={openDelete} />];

    return (
        <PageLayout>
            <Header title={<ReportTitle title={title} experiment={experiment} />} breadcrumbs={breadcrumbs} buttons={headerButtons} />
            <div ref={containerRef} className="relative flex-1 overflow-hidden">
                <div className="flex h-full flex-col">
                    <PageContentWrapper className="p-6" innerClassName="gap-4">
                        <RunDetailsAccordion group={group} />

                        {!hideGenericFailureBanner && <ExperimentReportBanner status={group.status} progressCurrent={group.progressCurrent} progressTotal={group.progressTotal} onRunNew={handleRunNew} onCancel={openCancel} cancelling={cancelling} />}
                        {cancelError && (
                            <div role="alert" className="rounded-xs border border-(--error-wMain)/30 bg-(--error-wMain)/5 px-4 py-2 text-sm text-(--error-wMain)">
                                {cancelError}
                            </div>
                        )}
                        <AuthRequiredBanner count={authRequiredCount} targetIsWorkflow={isWorkflowExperiment(experiment)} onConnectTools={experiment ? () => navigate(EVAL_ROUTES.experiment(experiment.id)) : undefined} />

                        <ReportFilterBar
                            allModels={allModels}
                            selectedModels={selectedModels}
                            onModelsChange={setSelectedModels}
                            allEvaluators={allEvaluatorNames}
                            selectedEvaluators={selectedEvaluators}
                            onEvaluatorsChange={setSelectedEvaluators}
                            experimentModels={effectiveModels}
                        />

                        <div className="flex flex-col gap-3">
                            {visibleEvaluators.map(ev => (
                                <EvaluatorTile
                                    key={ev.id}
                                    evaluatorName={ev.name}
                                    evaluatorSummary={ev.description}
                                    scoresByModel={buildModelScoreScope(scoresByEvaluatorByModel.get(ev.id) ?? {}, visibleModels)}
                                    allModels={allModels}
                                    experimentModels={effectiveModels}
                                    stats={aggregateEvaluatorStatsForModels(group?.runs ?? [], visibleModels, ev.id)}
                                    consistency={trialConsistency(resultsByModel, visibleModels, ev.id)}
                                />
                            ))}
                        </div>

                        <div className="mt-6">
                            {resultsLoading && !results ? (
                                <EmptyState variant="loading" title="Loading results…" />
                            ) : (
                                <ExamplesTable
                                    resultsByModel={results?.resultsByModel ?? {}}
                                    models={visibleModels}
                                    allModels={allModels}
                                    evaluatorNameMap={evaluatorNameMap}
                                    experimentModels={effectiveModels}
                                    visibleEvaluatorNames={selectedEvaluators.length === 0 ? undefined : selectedEvaluators}
                                    onRowClick={(row, exampleNumber) => {
                                        setSelectedRow({ row, exampleNumber });
                                    }}
                                    selectedRowKey={selectedRow?.row.key ?? null}
                                />
                            )}
                        </div>
                    </PageContentWrapper>
                </div>

                <ResizableSidePanel open={inspectorOpen && selectedRow !== null} width={sidePanelWidth} onDragStart={handleDragStart}>
                    {inspectorOpen && selectedRow && (
                        <EvalExampleInspector
                            row={selectedRow.row}
                            exampleNumber={selectedRow.exampleNumber}
                            models={visibleModels}
                            allModels={allModels}
                            experimentModels={effectiveModels}
                            evaluators={evaluatorInfo}
                            onClose={() => setSelectedRow(null)}
                            onOpenFullDetails={handleOpenFullDetails}
                        />
                    )}
                </ResizableSidePanel>
            </div>

            <ConfirmationDialog
                title="Delete Report"
                content={<span>Delete this report? All per-model runs and results will be permanently removed.</span>}
                open={deleteOpen}
                onOpenChange={setDeleteOpen}
                onCancel={closeDelete}
                onConfirm={confirmDelete}
            />

            <ConfirmationDialog
                title="Cancel Experiment"
                content={<span>This experiment will be cancelled. Completed example runs will remain available. Examples that are currently in progress will be stopped with partial results available.</span>}
                open={cancelOpen}
                onOpenChange={setCancelOpen}
                actionLabels={{ confirm: "Cancel Experiment", cancel: "Keep Running" }}
                onCancel={closeCancel}
                onConfirm={confirmCancel}
            />
        </PageLayout>
    );
}
