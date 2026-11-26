// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, Info, MoreHorizontal, Pencil, Play, XCircle } from "lucide-react";
import { useLoaderData, useLocation, useNavigate } from "react-router-dom";

import { evaluationsApi, useDeleteExperiment, useDeleteRunGroup, useEvalDatasets, useEvalEvaluators, useUpdateExperiment } from "@/api/evaluations";
import { useModelConfigs } from "@/api/models/hooks";

import { useUserContext } from "@/providers/contexts";

import { PageContentWrapper, PageFooter, PageInlineRow, PageLabel } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { formatDuration } from "@/utils/format";

import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/ui/accordion";
import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { Input } from "@/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import { Textarea } from "@/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { EvalEvaluator, EvalExperiment, EvalRun, ExperimentModelConfig } from "@/types/eval";

import { type AgentOption, findAgentOption, useAgentOptions } from "@/features/entrypoints/hooks";

import { ConfirmationDialog, EmptyState, Header, MessageBanner, PageLayout } from "@/common";
import { useIsWorkflowEvalsEnabled } from "@/hooks";

import {
    AgentAutocomplete,
    AgentNotDeployedDialog,
    AiGenerationDialog,
    calculateRunGroupProgress,
    ConnectToolsPanel,
    CreateDatasetDialog,
    CreateEvaluatorDialog,
    currentRefusal,
    DatasetField,
    DatasetImportDialog,
    DatasetSidePanel,
    EvaluatorPicker,
    ExampleProblemsAlert,
    exampleProblemsForError,
    type ExampleRefusal,
    isAgentNotDeployed,
    LatestReportSection,
    ModelSelect,
    ResizableSidePanel,
    RunExperimentDialog,
    runGroupAggregateStatusBadge,
    ScoreProgressChart,
    TargetTypeIcon,
    WorkflowEarlyAccessBadge,
} from "./components";
import { EVAL_ROUTES } from "./constants";
import { useAgentActiveModel, useEvalRuns, useResizableSidePanel, useWorkflowOptions } from "./hooks";
import {
    addEvaluatorRespectingCap,
    DEFAULT_MAX_WORKERS,
    DUPLICATE_NAME_ERROR,
    experimentTargetName,
    hasActiveRun,
    isDatasetChangedSinceLastRun,
    isRunGroupOutdated,
    isWorkflowExperiment,
    isWorkflowNotDeployed,
    MAX_EVALUATORS_PER_EXPERIMENT,
    MAX_MAX_WORKERS,
    MAX_MODELS_PER_EXPERIMENT,
    MIN_MAX_WORKERS,
    MODEL_COLORS,
    sortModelsByExperimentOrder,
    validateMaxWorkers,
    validateRunsPerExample,
    WORKFLOW_DEPLOY_STATE_UNKNOWN,
    WORKFLOW_EVALS_OFF,
    workflowRunCheck,
} from "./utils";

export interface RunGroupRow {
    runGroupId: string;
    runName?: string;
    models: string[];
    status: EvalRun["status"];
    progressCurrent: number;
    progressTotal: number;
    startedAt: string;
    completedAt?: string;
    runs: EvalRun[];
    configOutdated: boolean;
}

function aggregateStatus(statuses: EvalRun["status"][]): EvalRun["status"] {
    const set = new Set(statuses);
    if (set.has("pending")) return "pending";
    if (set.has("running")) return "running";
    if (set.has("failed")) return "failed";
    if (set.has("cancelled")) return "cancelled";
    if (set.has("completed_with_warnings")) return "completed_with_warnings";
    return "completed";
}

const TERMINAL_RUN_STATUSES: ReadonlySet<EvalRun["status"]> = new Set(["completed", "completed_with_warnings", "failed", "cancelled"]);

function isTerminalRunStatus(status: EvalRun["status"]): boolean {
    return TERMINAL_RUN_STATUSES.has(status);
}

function RunsPerExampleHint({ error, warning }: { readonly error: string | null; readonly warning: string | null }) {
    if (error) {
        return (
            <span className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                <XCircle className="size-3.5 shrink-0 text-(--error-wMain)" />
                {error}
            </span>
        );
    }
    if (warning) {
        return (
            <span className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                <AlertTriangle className="size-3.5 shrink-0 text-(--warning-w100)" />
                {warning}
            </span>
        );
    }
    return <span className="text-xs text-(--secondary-text-wMain)">The number of times each example will be evaluated, with the average score being returned as the result.</span>;
}

interface ConfigurationSidebarProps {
    readonly experiment: EvalExperiment;
    readonly agentDisplayName?: string;
    readonly availableEvaluators: EvalEvaluator[];
    readonly modelDetailEnabled: boolean;
    readonly canEdit: boolean;
    readonly canDelete: boolean;
    readonly onEdit: () => void;
    readonly onDeleteRequest: () => void;
    readonly navigate: ReturnType<typeof useNavigate>;
    readonly toolsPanel?: React.ReactNode;
}

function ConfigurationSidebar({ experiment, agentDisplayName, availableEvaluators, modelDetailEnabled, canEdit, canDelete, onEdit, onDeleteRequest, navigate, toolsPanel }: ConfigurationSidebarProps) {
    const { data: modelConfigs = [] } = useModelConfigs(modelDetailEnabled);

    const resolveModelConfigId = (m: ExperimentModelConfig) => m.modelConfigId || modelConfigs.find(c => c.alias === m.alias)?.id;

    const renderAgent = () => {
        if (isWorkflowExperiment(experiment)) {
            return <span className="truncate text-sm font-bold text-(--primary-wMain)">{experimentTargetName(experiment) || "—"}</span>;
        }
        if (!experiment.targetAgent) {
            return <span className="text-sm font-bold text-(--primary-wMain)">—</span>;
        }
        return (
            <Button variant="link" dense truncate className="text-sm font-bold" onClick={() => navigate(`/agent-mesh?selected=${encodeURIComponent(experiment.targetAgent)}`)}>
                {agentDisplayName ?? experiment.targetAgent}
            </Button>
        );
    };

    const renderDataset = () => {
        if (!experiment.datasetId) {
            return <span className="text-sm font-bold text-(--primary-wMain)">—</span>;
        }
        return (
            <Button variant="link" dense truncate className="text-sm font-bold" onClick={() => navigate(EVAL_ROUTES.labDatasetsSelected(experiment.datasetId!))}>
                {experiment.datasetName ?? experiment.datasetId}
            </Button>
        );
    };

    const renderModels = () => {
        if (experiment.models.length === 0) {
            return <span className="text-sm text-(--secondary-text-wMain)">—</span>;
        }
        return (
            <div className="flex flex-col gap-2">
                {experiment.models.map((m, idx) => {
                    const modelConfigId = resolveModelConfigId(m);
                    return (
                        <div key={m.modelConfigId || m.alias} className="flex items-start gap-2">
                            <span className="mt-1.5 size-2 shrink-0 rounded-full" style={{ backgroundColor: MODEL_COLORS[idx % MODEL_COLORS.length] }} />
                            <div className="flex min-w-0 flex-col leading-tight">
                                {modelDetailEnabled && modelConfigId ? (
                                    <Button variant="link" dense truncate className="text-sm font-bold" onClick={() => navigate(`/models/${modelConfigId}`)}>
                                        {m.alias}
                                    </Button>
                                ) : (
                                    <span className="truncate text-sm font-bold text-(--primary-wMain)">{m.alias}</span>
                                )}
                                <span className="truncate text-sm text-(--primary-text-wMain)">{m.model}</span>
                            </div>
                        </div>
                    );
                })}
            </div>
        );
    };

    const renderEvaluators = () => {
        if (experiment.evaluatorIds.length === 0) {
            return <span className="text-sm text-(--secondary-text-wMain)">—</span>;
        }
        return (
            <div className="flex flex-col gap-1">
                {experiment.evaluatorIds.map(id => {
                    const ev = availableEvaluators.find(e => e.id === id);
                    return (
                        <Button key={id} variant="link" dense truncate className="text-sm font-bold" onClick={() => navigate(EVAL_ROUTES.labEvaluatorsSelected(id))}>
                            {ev?.name ?? id}
                        </Button>
                    );
                })}
            </div>
        );
    };

    return (
        <aside className="w-[340px] shrink-0 overflow-y-auto border-r px-6 py-6">
            <div className="mb-5 flex items-center justify-between">
                <h2 className="text-base font-medium text-(--primary-text-wMain)">Configuration</h2>
                <div className="flex items-center gap-1">
                    {canEdit && (
                        <Button variant="ghost" size="sm" onClick={onEdit} className="gap-1.5 px-2 text-(--primary-wMain)">
                            <Pencil className="size-4" />
                            Edit
                        </Button>
                    )}
                    {canDelete && (
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                                <Button variant="ghost" size="icon" aria-label="Experiment actions">
                                    <MoreHorizontal className="size-4" />
                                </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                                <DropdownMenuItem onClick={onDeleteRequest}>Delete Experiment</DropdownMenuItem>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    )}
                </div>
            </div>
            <div className="flex flex-col gap-5">
                <div className="flex flex-col gap-1">
                    <PageLabel>Description</PageLabel>
                    <p className="text-sm whitespace-pre-wrap">{experiment.description || "—"}</p>
                </div>
                <div className="flex flex-col gap-1">
                    <PageLabel>{isWorkflowExperiment(experiment) ? "Workflow" : "Agent"}</PageLabel>
                    {renderAgent()}
                </div>
                {(!isWorkflowExperiment(experiment) || experiment.models.length > 0) && (
                    <div className="flex flex-col gap-2">
                        <PageLabel>{isWorkflowExperiment(experiment) ? "Models for AI agent steps" : "Agent Models"}</PageLabel>
                        {renderModels()}
                    </div>
                )}
                <div className="flex flex-col gap-1">
                    <PageLabel>Dataset</PageLabel>
                    {renderDataset()}
                </div>
                <div className="flex flex-col gap-1">
                    <PageLabel>Evaluators</PageLabel>
                    {renderEvaluators()}
                </div>
                <div className="flex flex-col gap-1">
                    <PageLabel>Runs per Example</PageLabel>
                    <span className="text-sm">{experiment.runsPerExample ?? 1}</span>
                </div>
                <div className="flex flex-col gap-1">
                    <PageLabel>Max Workers</PageLabel>
                    <span className="text-sm">{experiment.maxWorkers ?? DEFAULT_MAX_WORKERS}</span>
                </div>
                {toolsPanel}
            </div>
        </aside>
    );
}

function earliestStartedTime(list: EvalRun[]): string {
    let earliest = list[0].startedAt;
    for (const r of list) {
        if (r.startedAt < earliest) earliest = r.startedAt;
    }
    return earliest;
}

function groupRunsByGroupId(runs: EvalRun[], experiment: EvalExperiment | null): RunGroupRow[] {
    const byGroup = new Map<string, EvalRun[]>();
    for (const r of runs) {
        const gid = r.runGroupId ?? r.id;
        const list = byGroup.get(gid) ?? [];
        list.push(r);
        byGroup.set(gid, list);
    }
    const groups: RunGroupRow[] = [];
    for (const [runGroupId, list] of byGroup) {
        const first = list[0];
        const allTerminal = list.every(r => isTerminalRunStatus(r.status));
        const progress = calculateRunGroupProgress(list);
        groups.push({
            runGroupId,
            runName: first.runName,
            models: list.map(r => r.model),
            status: aggregateStatus(list.map(r => r.status)),
            progressCurrent: progress.current,
            progressTotal: progress.total,
            startedAt: earliestStartedTime(list),
            completedAt: allTerminal ? list.reduce<string | undefined>((max, r) => (r.completedAt && (!max || r.completedAt > max) ? r.completedAt : max), undefined) : undefined,
            runs: list,
            configOutdated: isRunGroupOutdated(list, experiment),
        });
    }
    return groups.sort((a, b) => new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime());
}

const REPORT_HISTORY_PAGE_SIZE = 20;

interface RunsTableProps {
    readonly groups: RunGroupRow[];
    readonly loading: boolean;
    readonly canDeleteReport: boolean;
    readonly onGroupClick: (groupId: string) => void;
    readonly onDeleteGroup: (group: RunGroupRow) => void;
}

function RunsTable({ groups, loading, canDeleteReport, onGroupClick, onDeleteGroup }: RunsTableProps) {
    const [page, setPage] = useState(1);

    const totalPages = Math.max(1, Math.ceil(groups.length / REPORT_HISTORY_PAGE_SIZE));
    const safePage = Math.min(page, totalPages);
    const pageGroups = groups.slice((safePage - 1) * REPORT_HISTORY_PAGE_SIZE, safePage * REPORT_HISTORY_PAGE_SIZE);
    const startIdx = groups.length === 0 ? 0 : (safePage - 1) * REPORT_HISTORY_PAGE_SIZE + 1;
    const endIdx = Math.min(safePage * REPORT_HISTORY_PAGE_SIZE, groups.length);

    if (loading) return <EmptyState variant="loading" title="Loading runs..." />;
    if (groups.length === 0) {
        return <EmptyState variant="noImage" title="No runs yet" subtitle="Click 'Run Experiment' to start an evaluation." />;
    }

    return (
        <div className="rounded-xs border">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead className="pl-4 font-semibold">Name</TableHead>
                        <TableHead className="w-44 font-semibold">Status</TableHead>
                        <TableHead className="w-32 font-semibold">Duration</TableHead>
                        <TableHead className="w-52 font-semibold">Completed On</TableHead>
                        <TableHead className="w-12" />
                    </TableRow>
                </TableHeader>
                <TableBody borderLastRow>
                    {pageGroups.map(group => {
                        const durationMs = group.completedAt ? new Date(group.completedAt).getTime() - new Date(group.startedAt).getTime() : null;
                        return (
                            <TableRow key={group.runGroupId} className="group h-10">
                                <TableCell className="pl-4">
                                    <div className="flex items-center gap-2">
                                        <button type="button" onClick={() => onGroupClick(group.runGroupId)} className="cursor-pointer text-left text-sm font-medium text-(--primary-wMain) hover:underline">
                                            {group.runName ?? "Untitled run"}
                                        </button>
                                        {group.configOutdated && (
                                            <Tooltip>
                                                <TooltipTrigger asChild>
                                                    <Info className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-label="Older config version" />
                                                </TooltipTrigger>
                                                <TooltipContent>This report references an older version of this experiment.</TooltipContent>
                                            </Tooltip>
                                        )}
                                    </div>
                                </TableCell>
                                <TableCell>{runGroupAggregateStatusBadge(group.status, group.runs, { current: group.progressCurrent, total: group.progressTotal })}</TableCell>
                                <TableCell className="text-sm text-(--primary-text-wMain)">{durationMs === null ? "—" : formatDuration(durationMs)}</TableCell>
                                <TableCell className="text-sm text-(--primary-text-wMain)">{group.completedAt ? new Date(group.completedAt).toLocaleString() : "—"}</TableCell>
                                <TableCell className="w-12">
                                    <DropdownMenu>
                                        <DropdownMenuTrigger asChild>
                                            <button
                                                type="button"
                                                aria-label="Row actions"
                                                className="flex size-8 items-center justify-center rounded-xs opacity-0 transition-opacity group-hover:opacity-100 hover:bg-(--secondary-w10) data-[state=open]:opacity-100"
                                            >
                                                <MoreHorizontal className="size-4" />
                                            </button>
                                        </DropdownMenuTrigger>
                                        <DropdownMenuContent align="end">
                                            <DropdownMenuItem onClick={() => onGroupClick(group.runGroupId)}>Open Report</DropdownMenuItem>
                                            {canDeleteReport && !hasActiveRun(group.runs) && <DropdownMenuItem onClick={() => onDeleteGroup(group)}>Delete Report</DropdownMenuItem>}
                                        </DropdownMenuContent>
                                    </DropdownMenu>
                                </TableCell>
                            </TableRow>
                        );
                    })}
                </TableBody>
            </Table>
            {groups.length > 0 && (
                <div className="flex items-center justify-between border-t px-4 py-3">
                    <span className="text-xs text-(--secondary-text-wMain)">
                        Showing {startIdx}-{endIdx} of {groups.length} results
                    </span>
                    {totalPages > 1 && (
                        <div className="flex items-center gap-2">
                            <Button variant="ghost" size="sm" onClick={() => setPage(p => Math.max(1, p - 1))} disabled={safePage === 1}>
                                Previous
                            </Button>
                            <span className="text-xs text-(--secondary-text-wMain)">
                                Page {safePage} of {totalPages}
                            </span>
                            <Button variant="ghost" size="sm" onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={safePage === totalPages}>
                                Next
                            </Button>
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}

function saveErrorMessage(err: unknown): string {
    return (err as { status?: number } | null)?.status === 409 ? DUPLICATE_NAME_ERROR : getErrorMessage(err);
}

function workflowRunBlockedMessage(check: ReturnType<typeof workflowRunCheck>): string | null {
    if (check.state === "off") return WORKFLOW_EVALS_OFF;
    if (check.state === "unknown") return WORKFLOW_DEPLOY_STATE_UNKNOWN;
    return null;
}

function editTargetMissing(experiment: EvalExperiment | null, editTargetAgent: string): boolean {
    return isWorkflowExperiment(experiment) ? !experiment?.targetWorkflow : !editTargetAgent.trim();
}

function useExperimentTarget(experiment: EvalExperiment | null, agentOptions: AgentOption[]) {
    const isWorkflowTarget = isWorkflowExperiment(experiment);
    const workflowEvalsEnabled = useIsWorkflowEvalsEnabled();
    const workflows = useWorkflowOptions(isWorkflowTarget && workflowEvalsEnabled);
    const agentOption = useMemo(() => (isWorkflowTarget ? undefined : findAgentOption(agentOptions, experiment?.targetAgent)), [isWorkflowTarget, agentOptions, experiment?.targetAgent]);
    const workflowOption = useMemo(() => (isWorkflowTarget ? workflows.options.find(o => o.name === experiment?.targetWorkflow) : undefined), [isWorkflowTarget, workflows.options, experiment?.targetWorkflow]);
    if (isWorkflowTarget) {
        return {
            experimentAgentOption: undefined,
            isWorkflowTarget,
            targetNotDeployed: isWorkflowNotDeployed(workflowOption),
            targetDisplayName: workflowOption?.displayName ?? experiment?.targetWorkflow ?? "",
            workflowRunBlocked: () => workflowRunBlockedMessage(workflowRunCheck(workflows, experiment?.targetWorkflow, workflowEvalsEnabled)),
        };
    }
    return {
        experimentAgentOption: agentOption,
        isWorkflowTarget,
        targetNotDeployed: isAgentNotDeployed(agentOption),
        targetDisplayName: agentOption?.displayName ?? experiment?.targetAgent ?? "",
        workflowRunBlocked: () => null,
    };
}

export function EvalExperimentDetailPage() {
    const loaderData: { experimentId?: string } = useLoaderData();
    const experimentId = loaderData.experimentId;
    const navigate = useNavigate();
    const location = useLocation();

    const [experiment, setExperiment] = useState<EvalExperiment | null>(null);
    const [loading, setLoading] = useState(true);
    const [editMode, setEditMode] = useState(false);
    const [saving, setSaving] = useState(false);
    const [running, setRunning] = useState(false);
    const [actionError, setActionError] = useState<string | null>(null);
    const [editRefusal, setEditRefusal] = useState<ExampleRefusal | null>(null);
    const [runDialogOpen, setRunDialogOpen] = useState(false);

    const [editName, setEditName] = useState("");
    const [editDesc, setEditDesc] = useState("");
    const [editTargetAgent, setEditTargetAgent] = useState("");
    const [editDatasetId, setEditDatasetId] = useState("");
    const [editModels, setEditModels] = useState<ExperimentModelConfig[]>([]);
    const [editRunsPerExample, setEditRunsPerExample] = useState(1);
    const [editMaxWorkers, setEditMaxWorkers] = useState(DEFAULT_MAX_WORKERS);
    const [editEvaluatorIds, setEditEvaluatorIds] = useState<string[]>([]);
    const [editPrimaryEvaluatorId, setEditPrimaryEvaluatorId] = useState<string | undefined>();
    const [importDialogOpen, setImportDialogOpen] = useState(false);
    const [aiGenerateOpen, setAiGenerateOpen] = useState(false);
    const [createDatasetOpen, setCreateDatasetOpen] = useState(false);
    const [reviewDatasetId, setReviewDatasetId] = useState<string | undefined>();
    const [createEvaluatorOpen, setCreateEvaluatorOpen] = useState(false);

    const [previewOpen, setPreviewOpen] = useState(false);

    const agentOptions = useAgentOptions();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const modelDetailEnabled = userHasCapability(CAPABILITIES.readModelConfig);
    const canUpdate = userHasCapability(CAPABILITIES.updateEvaluations);
    const canDelete = userHasCapability(CAPABILITIES.deleteEvaluations);
    const canRun = userHasCapability(CAPABILITIES.invokeEvaluations);
    const { activeModelId } = useAgentActiveModel(editTargetAgent || undefined);
    const { data: availableEvaluators = [], isLoading: evaluatorsLoading, refetch: fetchEvaluators } = useEvalEvaluators();
    const { data: datasets = [], refetch: fetchDatasets } = useEvalDatasets();
    const updateExperimentMutation = useUpdateExperiment();
    const deleteExperimentMutation = useDeleteExperiment();
    const deleteRunGroupMutation = useDeleteRunGroup();
    const { runs, loading: runsLoading, triggerRun, fetchRuns } = useEvalRuns(experimentId);
    const [deleteGroupTarget, setDeleteGroupTarget] = useState<RunGroupRow | null>(null);
    const [deleteExperimentOpen, setDeleteExperimentOpen] = useState(false);
    const [deletingExperiment, setDeletingExperiment] = useState(false);
    const [agentNotDeployedFlow, setAgentNotDeployedFlow] = useState<"create" | "run" | null>(null);

    const uniqueModels = useMemo(
        () =>
            sortModelsByExperimentOrder(
                runs.map(r => r.model),
                experiment?.models
            ),
        [runs, experiment]
    );

    const sortedRuns = useMemo(() => [...runs].sort((a, b) => new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime()), [runs]);

    const groups = useMemo(() => groupRunsByGroupId(runs, experiment), [runs, experiment]);

    const latestReportGroup = useMemo(() => groups.find(g => g.runs.some(r => isTerminalRunStatus(r.status))) ?? null, [groups]);

    const selectedDataset = useMemo(() => datasets.find(d => d.id === editDatasetId) ?? null, [datasets, editDatasetId]);

    const { experimentAgentOption, targetNotDeployed, targetDisplayName, workflowRunBlocked } = useExperimentTarget(experiment, agentOptions);

    const isPanelOpen = editMode && previewOpen && !!selectedDataset;
    const { containerRef, width: sidePanelWidth, handleDragStart } = useResizableSidePanel(isPanelOpen);

    const closePreviewPanel = useCallback(() => setPreviewOpen(false), []);

    const evaluatorNamesFromRuns = useMemo(() => {
        const keys = new Set<string>();
        for (const run of runs) {
            if (run.summaryScores) {
                for (const key of Object.keys(run.summaryScores)) {
                    keys.add(key);
                }
            }
        }
        return Array.from(keys);
    }, [runs]);

    const evaluatorIdToName = useMemo(() => {
        const map = new Map<string, string>();
        for (const ev of availableEvaluators) map.set(ev.id, ev.name);
        return map;
    }, [availableEvaluators]);

    useEffect(() => {
        if (!experimentId) return;
        setLoading(true);
        evaluationsApi
            .getExperiment(experimentId)
            .then(t => {
                setExperiment(t);
                setEditName(t.name);
                setEditDesc(t.description ?? "");
                setEditTargetAgent(t.targetAgent ?? "");
                setEditDatasetId(t.datasetId ?? "");
                setEditModels(t.models);
                setEditRunsPerExample(t.runsPerExample);
                setEditMaxWorkers(t.maxWorkers ?? DEFAULT_MAX_WORKERS);
                setEditEvaluatorIds(t.evaluatorIds ?? []);
                setEditPrimaryEvaluatorId(t.primaryEvaluatorId ?? (t.evaluatorIds?.length === 1 ? t.evaluatorIds[0] : undefined));
            })
            .catch(() => setExperiment(null))
            .finally(() => setLoading(false));
    }, [experimentId]);

    useEffect(() => {
        setPreviewOpen(editMode && !!editDatasetId);
    }, [editMode, editDatasetId]);

    useEffect(() => {
        const navState = location.state as { agentNotDeployed?: boolean; runNotStarted?: string } | null;
        if (!navState?.agentNotDeployed && !navState?.runNotStarted) return;
        if (navState.agentNotDeployed) setAgentNotDeployedFlow("create");
        if (navState.runNotStarted) setActionError(`The experiment was saved, but its run did not start: ${navState.runNotStarted}`);
        navigate({ pathname: location.pathname, search: location.search, hash: location.hash }, { replace: true, state: null });
    }, [location.hash, location.pathname, location.search, location.state, navigate]);

    const handleSave = async () => {
        setSaving(true);
        setActionError(null);
        setEditRefusal(null);
        try {
            const payload = {
                name: editName,
                description: editDesc,
                ...(isWorkflowExperiment(experiment) ? {} : { targetAgent: editTargetAgent || "" }),
                ...(editDatasetId === experiment?.datasetId ? {} : { datasetId: editDatasetId }),
                models: editModels,
                runsPerExample: editRunsPerExample,
                maxWorkers: editMaxWorkers,
                evaluatorIds: editEvaluatorIds,
                primaryEvaluatorId: editPrimaryEvaluatorId || (editEvaluatorIds.length === 1 ? editEvaluatorIds[0] : undefined),
            };
            if (experimentId) {
                const updated = await updateExperimentMutation.mutateAsync({ id: experimentId, data: payload });
                setExperiment(updated);
                setEditMode(false);
            }
        } catch (err) {
            const problems = await exampleProblemsForError(err, editDatasetId);
            setEditRefusal(problems.length > 0 ? { problems, workflowName: targetDisplayName, target: experiment?.targetWorkflow ?? "", datasetId: editDatasetId } : null);
            setActionError(problems.length > 0 ? null : saveErrorMessage(err));
        } finally {
            setSaving(false);
        }
    };

    const handleDiscard = () => {
        if (!experiment) return;
        setEditName(experiment.name);
        setEditDesc(experiment.description ?? "");
        setEditTargetAgent(experiment.targetAgent ?? "");
        setEditDatasetId(experiment.datasetId ?? "");
        setEditModels(experiment.models);
        setEditRunsPerExample(experiment.runsPerExample);
        setEditMaxWorkers(experiment.maxWorkers ?? DEFAULT_MAX_WORKERS);
        setEditEvaluatorIds(experiment.evaluatorIds ?? []);
        setEditPrimaryEvaluatorId(experiment.primaryEvaluatorId ?? ((experiment.evaluatorIds ?? []).length === 1 ? (experiment.evaluatorIds ?? [])[0] : undefined));
        setEditMode(false);
    };

    const handleEditDatasetChange = (id: string) => {
        setEditDatasetId(id);
        setPreviewOpen(!!id);
    };

    const openReviewDialog = (id: string) => {
        setReviewDatasetId(id);
        setCreateDatasetOpen(true);
    };

    const handleRunExperiment = async (runName: string) => {
        setRunDialogOpen(false);
        setRunning(true);
        setActionError(null);
        try {
            await triggerRun(runName || undefined);
        } catch (err) {
            setActionError(getErrorMessage(err));
        } finally {
            setRunning(false);
        }
    };

    const handleRunButtonClick = () => {
        if (!experiment || !experimentTargetName(experiment)) {
            return;
        }
        const blocked = workflowRunBlocked();
        if (blocked) {
            setActionError(blocked);
            return;
        }
        if (targetNotDeployed) {
            setAgentNotDeployedFlow("run");
        } else {
            setRunDialogOpen(true);
        }
    };

    const breadcrumbs = [
        {
            label: "Experiment Lab",
            onClick: () => {
                navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB);
            },
        },
        { label: experiment?.name ?? "Experiment" },
    ];

    const { error: editRunsError, warning: editRunsWarning } = validateRunsPerExample(editRunsPerExample);
    const editMaxWorkersError = validateMaxWorkers(editMaxWorkers);

    const renderConfigEditForm = () => (
        <div className="flex max-w-3xl flex-col gap-5">
            <PageInlineRow label="Name" required>
                <Input value={editName} onChange={e => setEditName(e.target.value)} disabled={saving} placeholder="e.g. Agent Accuracy Test" />
            </PageInlineRow>
            <PageInlineRow label="Description" required>
                <Textarea value={editDesc} onChange={e => setEditDesc(e.target.value)} rows={2} disabled={saving} placeholder="Describe the purpose of this experiment" />
            </PageInlineRow>

            <div className="pt-2">
                <div className="text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">Configuration</div>
            </div>

            {isWorkflowExperiment(experiment) ? (
                <PageInlineRow label="Workflow" required>
                    <span className="text-sm">{experiment ? experimentTargetName(experiment) : ""}</span>
                </PageInlineRow>
            ) : (
                <PageInlineRow label="Agent" required>
                    <AgentAutocomplete options={agentOptions} value={editTargetAgent} onChange={setEditTargetAgent} disabled={saving} />
                </PageInlineRow>
            )}

            {!isWorkflowExperiment(experiment) && (
                <PageInlineRow
                    label={
                        <>
                            Agent Model{" "}
                            <span className="text-xs font-normal text-(--secondary-text-wMain)">
                                ({editModels.length}/{MAX_MODELS_PER_EXPERIMENT})
                            </span>
                        </>
                    }
                    required
                >
                    <ModelSelect hideLabel multiple value={editModels} onChange={setEditModels} disabled={saving} max={MAX_MODELS_PER_EXPERIMENT} activeModelId={activeModelId} />
                </PageInlineRow>
            )}

            <PageInlineRow label="Dataset" required>
                <DatasetField
                    datasets={datasets}
                    value={editDatasetId}
                    onChange={handleEditDatasetChange}
                    onImportClick={() => setImportDialogOpen(true)}
                    onCreateClick={() => setCreateDatasetOpen(true)}
                    onAiGenerateClick={() => setAiGenerateOpen(true)}
                    disabled={saving}
                />
                <ExampleProblemsAlert refusal={currentRefusal(editRefusal, experiment?.targetWorkflow ?? "", editDatasetId)} />
            </PageInlineRow>

            <PageInlineRow
                label={
                    <>
                        Evaluator{" "}
                        <span className="text-xs font-normal text-(--secondary-text-wMain)">
                            ({editEvaluatorIds.length}/{MAX_EVALUATORS_PER_EXPERIMENT})
                        </span>
                    </>
                }
                required
            >
                <EvaluatorPicker
                    hideLabel
                    evaluators={availableEvaluators}
                    loading={evaluatorsLoading}
                    selectedIds={editEvaluatorIds}
                    onChange={setEditEvaluatorIds}
                    onCreateClick={() => setCreateEvaluatorOpen(true)}
                    primaryId={editPrimaryEvaluatorId}
                    onPrimaryChange={setEditPrimaryEvaluatorId}
                    disabled={saving}
                />
            </PageInlineRow>

            <Accordion type="single" collapsible>
                <AccordionItem value="advanced-options" className="border-none">
                    <AccordionTrigger className="flex-row-reverse justify-end gap-2 py-3 text-sm font-semibold hover:no-underline [&>svg]:-rotate-90 [&[data-state=open]>svg]:rotate-0">Advanced Options</AccordionTrigger>
                    <AccordionContent className="flex flex-col gap-6 pt-2">
                        <PageInlineRow label="Runs per Example">
                            <div className="flex flex-col gap-1">
                                <Input type="number" min={1} max={5} value={editRunsPerExample} onChange={e => setEditRunsPerExample(Number(e.target.value))} className="w-24" disabled={saving} />
                                <RunsPerExampleHint error={editRunsError} warning={editRunsWarning} />
                            </div>
                        </PageInlineRow>
                        <PageInlineRow label="Max Workers">
                            <div className="flex flex-col gap-1">
                                <Input type="number" aria-label="Max Workers" min={MIN_MAX_WORKERS} max={MAX_MAX_WORKERS} value={editMaxWorkers} onChange={e => setEditMaxWorkers(Number(e.target.value))} className="w-24" disabled={saving} />
                                {editMaxWorkersError ? (
                                    <span className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                                        <XCircle className="size-3.5 shrink-0 text-(--error-wMain)" />
                                        {editMaxWorkersError}
                                    </span>
                                ) : (
                                    <span className="text-xs text-(--secondary-text-wMain)">The most trials each model's run sends to the agent at once. Models run in parallel, so lower it if the model provider rate-limits the agent.</span>
                                )}
                            </div>
                        </PageInlineRow>
                    </AccordionContent>
                </AccordionItem>
            </Accordion>

            <DatasetImportDialog open={importDialogOpen} onClose={() => setImportDialogOpen(false)} onImported={openReviewDialog} />

            <AiGenerationDialog mode="dataset" open={aiGenerateOpen} onClose={() => setAiGenerateOpen(false)} onGenerated={dataset => openReviewDialog(dataset.id)} defaultTargetAgent={editTargetAgent || undefined} />

            <CreateDatasetDialog
                open={createDatasetOpen}
                onOpenChange={next => {
                    setCreateDatasetOpen(next);
                    if (!next) {
                        setReviewDatasetId(undefined);
                        void fetchDatasets();
                    }
                }}
                existingDatasetId={reviewDatasetId}
                onCreated={id => {
                    void fetchDatasets();
                    handleEditDatasetChange(id);
                }}
            />

            <CreateEvaluatorDialog
                open={createEvaluatorOpen}
                onOpenChange={setCreateEvaluatorOpen}
                onCreated={id => {
                    void fetchEvaluators();
                    setEditEvaluatorIds(prev => addEvaluatorRespectingCap(prev, id));
                    if (editEvaluatorIds.length === 0) setEditPrimaryEvaluatorId(id);
                }}
            />
        </div>
    );

    if (loading) {
        return (
            <PageLayout>
                <Header title="Experiment" breadcrumbs={breadcrumbs} />
                <EmptyState variant="loading" title="Loading..." />
            </PageLayout>
        );
    }

    if (!experiment) {
        return (
            <PageLayout>
                <Header title="Experiment" breadcrumbs={breadcrumbs} />
                <EmptyState
                    variant="error"
                    title="Experiment not found"
                    buttons={[
                        {
                            text: "Back to Experiments",
                            variant: "default",
                            onClick: () => {
                                navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB);
                            },
                        },
                    ]}
                />
            </PageLayout>
        );
    }

    const hasRuns = runs.length > 0;

    return (
        <PageLayout>
            <div ref={containerRef} className="relative flex flex-1 overflow-hidden">
                <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
                    <div className="flex items-center gap-2 px-6 pt-4 text-sm">
                        <button type="button" className="cursor-pointer font-semibold text-(--primary-wMain) hover:underline" onClick={() => navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB)}>
                            Experiment Lab
                        </button>
                        <span className="text-(--secondary-text-wMain)">&gt;</span>
                        <span className="text-(--secondary-text-wMain)">{experiment?.name ?? "Experiment"}</span>
                    </div>
                    <div className="flex items-center justify-between border-b px-6 py-3">
                        <h1 className="flex items-center gap-2 text-xl font-medium">
                            {experiment && <TargetTypeIcon targetType={experiment.targetType} />}
                            {experiment?.name ?? "Experiment"}
                            <WorkflowEarlyAccessBadge targetType={experiment?.targetType} />
                        </h1>
                        {!editMode && canRun && (
                            <Button variant="outline" onClick={handleRunButtonClick} disabled={running}>
                                <Play className="size-4 fill-current" />
                                {running ? "Running..." : "Run Experiment"}
                            </Button>
                        )}
                    </div>

                    {actionError && <MessageBanner variant="error" message={actionError} />}
                    {editMode ? (
                        <PageContentWrapper className="flex-1 overflow-auto p-6" innerClassName="gap-6">
                            {renderConfigEditForm()}
                        </PageContentWrapper>
                    ) : (
                        <div className="flex flex-1 overflow-hidden">
                            <ConfigurationSidebar
                                experiment={experiment}
                                agentDisplayName={experimentAgentOption?.displayName}
                                availableEvaluators={availableEvaluators}
                                modelDetailEnabled={modelDetailEnabled}
                                canEdit={canUpdate}
                                canDelete={canDelete}
                                onEdit={() => setEditMode(true)}
                                onDeleteRequest={() => setDeleteExperimentOpen(true)}
                                navigate={navigate}
                                toolsPanel={<ConnectToolsPanel experimentId={experiment.id} runAs={experiment.runAs} targetIsWorkflow={isWorkflowExperiment(experiment)} />}
                            />

                            <main className="flex-1 overflow-y-auto px-6 py-6">
                                <div className="flex flex-col gap-8">
                                    {latestReportGroup && (
                                        <LatestReportSection
                                            groupId={latestReportGroup.runGroupId}
                                            runName={latestReportGroup.runName}
                                            runs={latestReportGroup.runs}
                                            status={latestReportGroup.status}
                                            completedAt={latestReportGroup.completedAt}
                                            startedAt={latestReportGroup.startedAt}
                                            models={experiment.models}
                                            evaluatorIds={evaluatorNamesFromRuns}
                                            evaluatorNameMap={evaluatorIdToName}
                                            onViewFullReport={() => navigate(EVAL_ROUTES.runGroup(latestReportGroup.runGroupId))}
                                        />
                                    )}

                                    <section>
                                        <div className="mb-3 flex items-baseline gap-2">
                                            <h2 className="text-sm font-bold">Performance</h2>
                                            <span className="text-sm text-(--secondary-text-wMain)">(Last 10 runs)</span>
                                        </div>
                                        {hasRuns && evaluatorNamesFromRuns.length > 0 ? (
                                            <div className="grid gap-4" style={{ gridTemplateColumns: `repeat(${Math.min(evaluatorNamesFromRuns.length, 3)}, minmax(0, 1fr))` }}>
                                                {evaluatorNamesFromRuns.map(evalId => (
                                                    <div key={evalId} className="flex flex-col gap-2">
                                                        <span className="text-sm text-(--secondary-text-wMain)">{evaluatorIdToName.get(evalId) ?? evalId}</span>
                                                        <ScoreProgressChart runs={sortedRuns} evaluatorNames={[evalId]} allModels={uniqueModels} hideHeader />
                                                    </div>
                                                ))}
                                            </div>
                                        ) : (
                                            <div className="flex min-h-[151px] flex-col items-center justify-center gap-1 rounded-xs bg-(--secondary-w10)/50 p-10">
                                                <div className="text-sm font-medium text-(--secondary-text-wMain)">No Data Found</div>
                                                <div className="text-sm text-(--secondary-text-wMain)">Run experiments to see performance of your experiment over time</div>
                                            </div>
                                        )}
                                    </section>

                                    <section>
                                        <h2 className="mb-3 text-sm font-bold">Report History</h2>
                                        {hasRuns ? (
                                            <RunsTable groups={groups} loading={runsLoading} canDeleteReport={canDelete} onGroupClick={groupId => navigate(EVAL_ROUTES.runGroup(groupId))} onDeleteGroup={group => setDeleteGroupTarget(group)} />
                                        ) : (
                                            <div className="flex min-h-[500px] flex-col items-center justify-center gap-4 rounded-xs bg-(--secondary-w10)/50 p-10">
                                                <h3 className="text-xl font-medium text-(--primary-text-wMain)">Evaluate and Coach Your Agents</h3>
                                                <p className="max-w-[576px] text-center text-sm text-(--primary-text-wMain)">
                                                    Experiments validate that your agents respond consistently and safely allowing you to coach your agent in the correct behaviour before you ship and catch regressions whenever a prompt, model, or
                                                    tool changes.
                                                </p>
                                                {canRun && (
                                                    <Button onClick={handleRunButtonClick} disabled={running}>
                                                        {running ? "Running..." : "Run Experiment"}
                                                    </Button>
                                                )}
                                            </div>
                                        )}
                                    </section>
                                </div>
                            </main>
                        </div>
                    )}
                </div>
                <ResizableSidePanel open={isPanelOpen && !!selectedDataset} width={sidePanelWidth} onDragStart={handleDragStart}>
                    {isPanelOpen && selectedDataset && <DatasetSidePanel dataset={selectedDataset} onClose={closePreviewPanel} previewMode />}
                </ResizableSidePanel>
            </div>

            {editMode && (
                <PageFooter>
                    <Button variant="ghost" onClick={handleDiscard} disabled={saving}>
                        Discard Changes
                    </Button>
                    <Button
                        onClick={() => void handleSave()}
                        disabled={saving || !canUpdate || !editName.trim() || !editDesc.trim() || !editDatasetId || editTargetMissing(experiment, editTargetAgent) || editEvaluatorIds.length === 0 || !!editMaxWorkersError}
                    >
                        {saving ? "Saving..." : "Save"}
                    </Button>
                </PageFooter>
            )}

            <ConfirmationDialog
                title="Delete Report"
                content={<span>Delete this report? All per-model runs and results will be permanently removed.</span>}
                open={!!deleteGroupTarget}
                onOpenChange={open => {
                    if (!open) setDeleteGroupTarget(null);
                }}
                onCancel={() => setDeleteGroupTarget(null)}
                onConfirm={async () => {
                    if (!deleteGroupTarget) return;
                    await deleteRunGroupMutation.mutateAsync(deleteGroupTarget.runGroupId);
                    setDeleteGroupTarget(null);
                    await fetchRuns();
                }}
            />

            <RunExperimentDialog
                open={runDialogOpen}
                experimentName={experiment?.name ?? ""}
                onOpenChange={setRunDialogOpen}
                onConfirm={runName => void handleRunExperiment(runName)}
                isLoading={running}
                datasetChanged={isDatasetChangedSinceLastRun(experiment)}
            />

            <ConfirmationDialog
                title="Delete Experiment"
                content={
                    <span>
                        Delete <strong>{experiment?.name}</strong>? All runs, reports, and configuration will be permanently removed.
                    </span>
                }
                open={deleteExperimentOpen}
                onOpenChange={open => {
                    if (!open) setDeleteExperimentOpen(false);
                }}
                onCancel={() => setDeleteExperimentOpen(false)}
                onConfirm={async () => {
                    if (!experimentId || deletingExperiment) return;
                    setDeletingExperiment(true);
                    try {
                        await deleteExperimentMutation.mutateAsync(experimentId);
                        setDeleteExperimentOpen(false);
                        navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB);
                    } finally {
                        setDeletingExperiment(false);
                    }
                }}
            />

            <AgentNotDeployedDialog
                open={agentNotDeployedFlow !== null}
                onOpenChange={open => {
                    if (!open) setAgentNotDeployedFlow(null);
                }}
                agentName={targetDisplayName}
                agentId={experimentAgentOption?.agentId}
                targetType={experiment?.targetType}
                flow={agentNotDeployedFlow ?? "run"}
            />
        </PageLayout>
    );
}
