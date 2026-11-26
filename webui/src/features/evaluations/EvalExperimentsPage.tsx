// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";

import { evaluationsApi, useDeleteExperiment, useEvalEvaluators, useEvalExperiments } from "@/api/evaluations";
import { useModelConfigs } from "@/api/models/hooks";
import type { ModelConfig } from "@/api/models/types";

import { useUserContext } from "@/providers/contexts";

import { useLocalStorage } from "@/hooks/useLocalStorage";

import { PageContentWrapper } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { cn } from "@/utils/cnTailwind";
import { formatTimestamp } from "@/utils/format";

import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { SearchInput } from "@/ui/search-input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { EvalExperiment, EvalTargetType, ExperimentModelConfig } from "@/types/eval";

import { ModelConfigIcon } from "@/features/activation";
import { findAgentOption, useAgentOptions } from "@/features/entrypoints/hooks";

import { ConfirmationDialog, EmptyState, MessageBanner, MorePopoverButton, PaginationControls } from "@/common";
import { useIsWorkflowEvalsEnabled } from "@/hooks";

import { AgentNotDeployedDialog, ExperimentQuickStartBanner, isAgentNotDeployed, RunExperimentDialog } from "./components";
import { EVAL_ROUTES } from "./constants";
import { useWorkflowOptions } from "./hooks";
import { experimentTargetName, isDatasetChangedSinceLastRun, isWorkflowExperiment, WORKFLOW_DEPLOY_STATE_UNKNOWN, WORKFLOW_EVALS_OFF, workflowRunCheck } from "./utils";

interface ModelsCellProps {
    readonly models: ExperimentModelConfig[];
    readonly modelLookup: (modelConfigId: string) => ModelConfig | undefined;
}

function ModelLabel({ alias, model }: { readonly alias?: string; readonly model: string }) {
    if (alias && alias !== model) {
        return (
            <span className="text-sm">
                <span className="font-semibold">{alias}</span>
                <span className="ml-1 text-(--secondary-text-wMain)">{model}</span>
            </span>
        );
    }
    return <span className="text-sm font-semibold">{model}</span>;
}

function ModelsCell({ models, modelLookup }: ModelsCellProps) {
    if (models.length === 0) return <span className="text-(--secondary-text-wMain)">—</span>;
    if (models.length === 1) {
        return (
            <div className="flex flex-wrap items-center gap-2">
                <ModelConfigIcon model={modelLookup(models[0].modelConfigId ?? "")} size="xs" />
                <ModelLabel alias={models[0].alias} model={models[0].model} />
            </div>
        );
    }
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span tabIndex={0} className="cursor-default underline decoration-dotted underline-offset-2" onClick={e => e.stopPropagation()}>
                    {models.length} models
                </span>
            </TooltipTrigger>
            <TooltipContent className="p-2">
                <div className="flex flex-col gap-1">
                    {models.map(m => (
                        <div key={m.modelConfigId ?? m.alias} className="flex items-center gap-2">
                            <ModelConfigIcon model={modelLookup(m.modelConfigId ?? "")} size="xs" />
                            <ModelLabel alias={m.alias} model={m.model} />
                        </div>
                    ))}
                </div>
            </TooltipContent>
        </Tooltip>
    );
}

interface EvaluatorsCellProps {
    readonly evaluatorIds: string[];
    readonly evaluatorLookup: (id: string) => string;
}

function EvaluatorsCell({ evaluatorIds, evaluatorLookup }: EvaluatorsCellProps) {
    if (evaluatorIds.length === 0) return <span className="text-(--secondary-text-wMain)">None</span>;
    if (evaluatorIds.length === 1) {
        return <span className="text-sm">{evaluatorLookup(evaluatorIds[0])}</span>;
    }
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span tabIndex={0} className="cursor-default underline decoration-dotted underline-offset-2" onClick={e => e.stopPropagation()}>
                    {evaluatorIds.length} evaluators
                </span>
            </TooltipTrigger>
            <TooltipContent className="p-2">
                <div className="flex flex-col gap-1">
                    {evaluatorIds.map(id => (
                        <span key={id} className="text-sm">
                            {evaluatorLookup(id)}
                        </span>
                    ))}
                </div>
            </TooltipContent>
        </Tooltip>
    );
}

interface ExperimentRowProps {
    readonly experiment: EvalExperiment;
    readonly modelLookup: (modelConfigId: string) => ModelConfig | undefined;
    readonly evaluatorLookup: (id: string) => string;
    readonly agentLookup: (brokerName: string) => string;
    readonly canRun: boolean;
    readonly canEdit: boolean;
    readonly canClone: boolean;
    readonly canDelete: boolean;
    readonly onDelete: () => void;
    readonly onClick: () => void;
    readonly onRun: () => void;
    readonly onEdit: () => void;
    readonly onClone: () => void;
}

function experimentTargetLabel(experiment: EvalExperiment, agentLookup: (name: string) => string): string {
    if (isWorkflowExperiment(experiment)) return experimentTargetName(experiment) || "—";
    return experiment.targetAgent ? agentLookup(experiment.targetAgent) : "—";
}

function TruncatedCell({ value, className }: { readonly value: string; readonly className?: string }) {
    if (!value || value === "—") return <span className="text-sm text-(--secondary-text-wMain)">—</span>;
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span className={cn("block truncate text-sm text-(--secondary-text-wMain)", className)}>{value}</span>
            </TooltipTrigger>
            <TooltipContent>{value}</TooltipContent>
        </Tooltip>
    );
}

function ExperimentRow({ experiment, modelLookup, evaluatorLookup, agentLookup, canRun, canEdit, canClone, canDelete, onDelete, onClick, onRun, onEdit, onClone }: ExperimentRowProps) {
    const hasRowActions = canRun || canEdit || canClone || canDelete;
    return (
        <TableRow className="h-10 cursor-pointer hover:bg-(--secondary-w10)/50" onClick={onClick}>
            <TableCell className="max-w-[220px] pl-4">
                <Tooltip>
                    <TooltipTrigger asChild>
                        <button
                            type="button"
                            onClick={e => {
                                e.stopPropagation();
                                onClick();
                            }}
                            className="block w-full cursor-pointer truncate text-left font-semibold text-(--primary-wMain) hover:underline"
                        >
                            {experiment.name}
                        </button>
                    </TooltipTrigger>
                    <TooltipContent>{experiment.name}</TooltipContent>
                </Tooltip>
            </TableCell>
            <TableCell className="max-w-[180px]">
                <TruncatedCell value={experimentTargetLabel(experiment, agentLookup)} />
            </TableCell>
            <TableCell>
                <ModelsCell models={experiment.models} modelLookup={modelLookup} />
            </TableCell>
            <TableCell className="max-w-[220px]">
                <TruncatedCell value={experiment.datasetName ?? "—"} />
            </TableCell>
            <TableCell>
                <EvaluatorsCell evaluatorIds={experiment.evaluatorIds} evaluatorLookup={evaluatorLookup} />
            </TableCell>
            <TableCell className="text-sm text-(--secondary-text-wMain)">{experiment.latestRunTime ? formatTimestamp(experiment.latestRunTime) : "Never"}</TableCell>
            <TableCell className="w-12" onClick={e => e.stopPropagation()}>
                {hasRowActions && (
                    <DropdownMenu>
                        <DropdownMenuTrigger asChild>{MorePopoverButton}</DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                            {canRun && <DropdownMenuItem onClick={onRun}>Run Experiment</DropdownMenuItem>}
                            {canEdit && <DropdownMenuItem onClick={onEdit}>Edit</DropdownMenuItem>}
                            {canClone && <DropdownMenuItem onClick={onClone}>Clone</DropdownMenuItem>}
                            {canDelete && (
                                <DropdownMenuItem onClick={onDelete} className="text-(--error-wMain)">
                                    Delete
                                </DropdownMenuItem>
                            )}
                        </DropdownMenuContent>
                    </DropdownMenu>
                )}
            </TableCell>
        </TableRow>
    );
}

const ITEMS_PER_PAGE = 10;
const QUICK_START_DISMISSED_KEY = "eval.experimentsQuickStartDismissed";

export function EvalExperimentsPage() {
    const navigate = useNavigate();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreate = userHasCapability(CAPABILITIES.createEvaluations);
    const canEdit = userHasCapability(CAPABILITIES.updateEvaluations);
    const canDelete = userHasCapability(CAPABILITIES.deleteEvaluations);
    const canRun = userHasCapability(CAPABILITIES.invokeEvaluations);
    const { data: experiments = [], isLoading: loading } = useEvalExperiments();
    const { data: evaluators = [] } = useEvalEvaluators();
    const deleteExperimentMutation = useDeleteExperiment();
    const { data: modelConfigs = [] } = useModelConfigs();
    const agentOptions = useAgentOptions();
    const [deleteTarget, setDeleteTarget] = useState<EvalExperiment | null>(null);
    const [runTarget, setRunTarget] = useState<EvalExperiment | null>(null);
    const [notDeployed, setNotDeployed] = useState<{ name: string; agentId?: string; targetType: EvalTargetType } | null>(null);
    const workflowEvalsEnabled = useIsWorkflowEvalsEnabled();
    const workflows = useWorkflowOptions(workflowEvalsEnabled && experiments.some(isWorkflowExperiment));
    const [running, setRunning] = useState(false);
    const [runError, setRunError] = useState<string | null>(null);
    const [currentPage, setCurrentPage] = useState(1);
    const [nameFilter, setNameFilter] = useState("");
    const [quickStartDismissed, setQuickStartDismissed] = useLocalStorage(QUICK_START_DISMISSED_KEY, false);

    const modelLookup = useMemo(() => {
        const map = new Map(modelConfigs.map(m => [m.id, m]));
        return (modelConfigId: string) => map.get(modelConfigId);
    }, [modelConfigs]);

    const evaluatorLookup = useMemo(() => {
        const map = new Map(evaluators.map(e => [e.id, e.name]));
        return (id: string) => map.get(id) ?? id;
    }, [evaluators]);

    const agentLookup = useMemo(() => {
        const map = new Map(agentOptions.map(a => [a.name, a.displayName]));
        return (brokerName: string) => map.get(brokerName) ?? brokerName;
    }, [agentOptions]);

    const filtered = useMemo(() => {
        const q = nameFilter.trim().toLowerCase();
        if (!q) return experiments;
        return experiments.filter(e => e.name.toLowerCase().includes(q));
    }, [experiments, nameFilter]);

    const totalCount = filtered.length;
    const totalPages = Math.max(1, Math.ceil(totalCount / ITEMS_PER_PAGE));
    const safePage = Math.min(currentPage, totalPages);
    const paged = useMemo(() => {
        const start = (safePage - 1) * ITEMS_PER_PAGE;
        return filtered.slice(start, start + ITEMS_PER_PAGE);
    }, [filtered, safePage]);

    const handleDelete = async () => {
        if (!deleteTarget) return;
        await deleteExperimentMutation.mutateAsync(deleteTarget.id);
        setDeleteTarget(null);
    };

    const handleRunConfirm = async (runName: string) => {
        if (!runTarget) return;
        setRunning(true);
        setRunError(null);
        try {
            await evaluationsApi.triggerRun(runTarget.id, { runName: runName || undefined });
            navigate(`${EVAL_ROUTES.experiment(runTarget.id)}?tab=runs`);
        } catch (err) {
            setRunError(getErrorMessage(err));
        } finally {
            setRunning(false);
            setRunTarget(null);
        }
    };

    const handleWorkflowRunClick = (experiment: EvalExperiment) => {
        const check = workflowRunCheck(workflows, experiment.targetWorkflow, workflowEvalsEnabled);
        if (check.state === "off") setRunError(WORKFLOW_EVALS_OFF);
        else if (check.state === "unknown") setRunError(WORKFLOW_DEPLOY_STATE_UNKNOWN);
        else if (check.state === "not_deployed") setNotDeployed({ name: check.displayName, targetType: "workflow" });
        else setRunTarget(experiment);
    };

    const handleRunClick = (experiment: EvalExperiment) => {
        if (isWorkflowExperiment(experiment)) {
            handleWorkflowRunClick(experiment);
            return;
        }
        const agentOption = findAgentOption(agentOptions, experiment.targetAgent);
        if (agentOption && isAgentNotDeployed(agentOption)) {
            setNotDeployed({ name: agentOption.displayName || experiment.targetAgent, agentId: agentOption.agentId, targetType: "agent" });
        } else {
            setRunTarget(experiment);
        }
    };

    const getEmptyMessage = () => {
        if (experiments.length === 0) return "You haven't created any experiments yet.";
        if (filtered.length === 0) return `No experiments match "${nameFilter}".`;
        return null;
    };

    const renderScrollableContent = () => {
        if (loading) return <EmptyState variant="loading" title="Loading experiments..." />;

        const emptyMessage = getEmptyMessage();

        return (
            <>
                {runError && <MessageBanner variant="error" message={runError} dismissible onDismiss={() => setRunError(null)} />}
                {!quickStartDismissed && canCreate && <ExperimentQuickStartBanner onCreateExperiment={() => navigate(EVAL_ROUTES.EXPERIMENT_NEW)} onDismiss={() => setQuickStartDismissed(true)} />}
                <SearchInput
                    className="w-xs"
                    value={nameFilter}
                    onChange={value => {
                        setNameFilter(value);
                        setCurrentPage(1);
                    }}
                    placeholder="Filter by name"
                />
                {emptyMessage ? (
                    <div className="flex flex-col items-center justify-center gap-3 rounded-xs border bg-(--secondary-w10)/40 px-4 py-10">
                        <p className="text-sm text-(--secondary-text-wMain)">{emptyMessage}</p>
                    </div>
                ) : (
                    <div className="rounded-xs border">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead className="pl-4 font-semibold">Name</TableHead>
                                    <TableHead className="font-semibold">Agent</TableHead>
                                    <TableHead className="font-semibold">Agent Models</TableHead>
                                    <TableHead className="font-semibold">Dataset</TableHead>
                                    <TableHead className="font-semibold">Evaluators</TableHead>
                                    <TableHead className="font-semibold">Last Run</TableHead>
                                    <TableHead className="w-12" />
                                </TableRow>
                            </TableHeader>
                            <TableBody className="[&_tr:last-child]:border-b!">
                                {paged.map(experiment => (
                                    <ExperimentRow
                                        key={experiment.id}
                                        experiment={experiment}
                                        modelLookup={modelLookup}
                                        evaluatorLookup={evaluatorLookup}
                                        agentLookup={agentLookup}
                                        canRun={canRun}
                                        canEdit={canEdit}
                                        canClone={canCreate && (workflowEvalsEnabled || !isWorkflowExperiment(experiment))}
                                        canDelete={canDelete}
                                        onClick={() => navigate(EVAL_ROUTES.experiment(experiment.id))}
                                        onRun={() => handleRunClick(experiment)}
                                        onEdit={() => navigate(EVAL_ROUTES.experiment(experiment.id))}
                                        onClone={() => navigate(`${EVAL_ROUTES.EXPERIMENT_NEW}?cloneFromId=${experiment.id}`)}
                                        onDelete={() => setDeleteTarget(experiment)}
                                    />
                                ))}
                            </TableBody>
                        </Table>
                        {totalCount > 0 && (
                            <div
                                className={cn(
                                    "grid grid-cols-3 items-center border-t px-6 py-3",
                                    "[&_.bg-\\(--background-w10\\)]:bg-transparent [&_.border-t]:border-t-0 [&_.mt-4]:mt-0 [&_.pb-2]:pb-0 [&_.pt-4]:pt-0",
                                    "[&_a[aria-label]]:gap-0 [&_a[aria-label]]:px-2 [&_a[aria-label]>span]:hidden"
                                )}
                            >
                                <div />
                                <div className="flex justify-center">{totalPages > 1 && <PaginationControls totalPages={totalPages} currentPage={safePage} onPageChange={setCurrentPage} />}</div>
                                <span className="justify-self-end text-sm text-(--secondary-text-wMain)">
                                    Showing {(safePage - 1) * ITEMS_PER_PAGE + 1}–{Math.min(safePage * ITEMS_PER_PAGE, totalCount)} of {totalCount} results
                                </span>
                            </div>
                        )}
                    </div>
                )}
            </>
        );
    };

    const deleteDialog = (
        <ConfirmationDialog
            title="Delete Experiment"
            content={
                <span>
                    Delete <strong>{deleteTarget?.name}</strong>? This cannot be undone.
                </span>
            }
            open={!!deleteTarget}
            onOpenChange={open => {
                if (!open) setDeleteTarget(null);
            }}
            onCancel={() => setDeleteTarget(null)}
            onConfirm={() => void handleDelete()}
        />
    );

    return (
        <div className="flex h-full flex-col">
            <PageContentWrapper className="flex-1 overflow-y-auto p-6" innerClassName="gap-6">
                {renderScrollableContent()}
            </PageContentWrapper>
            {deleteDialog}
            <RunExperimentDialog
                open={!!runTarget}
                experimentName={runTarget?.name ?? ""}
                onOpenChange={open => {
                    if (!open) setRunTarget(null);
                }}
                onConfirm={runName => void handleRunConfirm(runName)}
                isLoading={running}
                datasetChanged={isDatasetChangedSinceLastRun(runTarget)}
            />
            <AgentNotDeployedDialog
                open={!!notDeployed}
                onOpenChange={open => {
                    if (!open) setNotDeployed(null);
                }}
                agentName={notDeployed?.name ?? ""}
                agentId={notDeployed?.agentId}
                targetType={notDeployed?.targetType}
                flow="run"
            />
        </div>
    );
}
