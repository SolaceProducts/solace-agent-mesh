// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps, useEffect, useMemo, useRef, useState } from "react";
import { useWhenProviderReady } from "@openfeature/react-sdk";
import { AlertTriangle, Info, XCircle } from "lucide-react";
import { Navigate, useNavigate, useSearchParams } from "react-router-dom";

import { evaluationsApi, useCreateExperiment, useEvalDatasets, useEvalEvaluators, useEvalExperiments } from "@/api/evaluations";

import { useUserContext } from "@/providers/contexts";

import { type RadioOption, RadioOptions } from "@/common/form";
import { PageContentWrapper, PageFooter, PageInlineRow } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";

import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/ui/accordion";
import { Button } from "@/ui/button";
import { Input } from "@/ui/input";
import { LifecycleBadge } from "@/ui/lifecycleBadge";
import { Textarea } from "@/ui/textarea";
import type { EvalExperiment, EvalTargetType, ExperimentModelConfig } from "@/types/eval";

import { type AgentOption, findAgentOption, useAgentOptions } from "@/features/entrypoints/hooks";

import { Header, PageLayout } from "@/common";
import { useIsWorkflowEvalsEnabled } from "@/hooks";

import {
    AgentAutocomplete,
    AiGenerationDialog,
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
    ModelSelect,
    ResizableSidePanel,
    WorkflowAutocomplete,
} from "./components";
import { EVAL_ROUTES } from "./constants";
import { useAgentActiveModel, useResizableSidePanel, useWorkflowOptions, type WorkflowOption } from "./hooks";
import {
    addEvaluatorRespectingCap,
    DEFAULT_MAX_WORKERS,
    DUPLICATE_NAME_ERROR,
    isWorkflowExperiment,
    isWorkflowNotDeployed,
    MAX_EVALUATORS_PER_EXPERIMENT,
    MAX_MAX_WORKERS,
    MAX_MODELS_PER_EXPERIMENT,
    MIN_MAX_WORKERS,
    validateMaxWorkers,
    validateRunsPerExample,
} from "./utils";

const TARGET_TYPE_OPTIONS: readonly RadioOption[] = [
    { value: "agent", label: "Agent" },
    { value: "workflow", label: "Workflow", badge: <LifecycleBadge>EARLY ACCESS</LifecycleBadge> },
];

const CLONED_WORKFLOW_FLAG_OFF = "The experiment you copied evaluates a workflow, but workflow evaluations are turned off for this deployment. Select an agent to evaluate instead.";

function clonedTarget(source: EvalExperiment, workflowEvalsEnabled: boolean): { targetType: EvalTargetType; targetAgent: string; targetWorkflow: string; notice?: string } {
    if (!isWorkflowExperiment(source)) return { targetType: "agent", targetAgent: source.targetAgent ?? "", targetWorkflow: "" };
    if (!workflowEvalsEnabled) return { targetType: "agent", targetAgent: "", targetWorkflow: "", notice: CLONED_WORKFLOW_FLAG_OFF };
    return { targetType: "workflow", targetAgent: "", targetWorkflow: source.targetWorkflow ?? "" };
}

function saveErrorMessage(err: unknown): string {
    return (err as { status?: number }).status === 409 ? DUPLICATE_NAME_ERROR : getErrorMessage(err);
}

interface TargetFieldProps {
    readonly isWorkflowTarget: boolean;
    readonly agentOptions: ComponentProps<typeof AgentAutocomplete>["options"];
    readonly targetAgent: string;
    readonly onAgentChange: (name: string) => void;
    readonly workflowOptions: ComponentProps<typeof WorkflowAutocomplete>["options"];
    readonly targetWorkflow: string;
    readonly onWorkflowChange: (name: string) => void;
    readonly workflowsFailed: boolean;
    readonly notDeployed: boolean;
    readonly disabled: boolean;
}

function TargetField({ isWorkflowTarget, agentOptions, targetAgent, onAgentChange, workflowOptions, targetWorkflow, onWorkflowChange, workflowsFailed, notDeployed, disabled }: TargetFieldProps) {
    const kind = isWorkflowTarget ? "workflow" : "agent";
    return (
        <div className="flex flex-col gap-1">
            {isWorkflowTarget ? (
                <WorkflowAutocomplete options={workflowOptions} value={targetWorkflow} onChange={onWorkflowChange} disabled={disabled} />
            ) : (
                <AgentAutocomplete options={agentOptions} value={targetAgent} onChange={onAgentChange} disabled={disabled} />
            )}
            {isWorkflowTarget && workflowsFailed && (
                <span data-testid="workflowsLoadFailed" role="alert" className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                    <XCircle className="size-3.5 shrink-0 text-(--error-wMain)" />
                    The list of workflows could not be loaded. Reload the page to try again.
                </span>
            )}
            {notDeployed && (
                <span className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                    <Info className="size-3.5 shrink-0 text-(--secondary-wMain)" />
                    This {kind} is not deployed. Deploy the {kind} before running this experiment.
                </span>
            )}
            {isWorkflowTarget && <span className="text-xs text-(--secondary-text-wMain)">The input data in each example must match the fields that this workflow accepts. Saving the experiment checks every example.</span>}
        </div>
    );
}

function useSelectedTarget(targetType: EvalTargetType, targetAgent: string, targetWorkflow: string, agentOptions: AgentOption[], workflowOptions: WorkflowOption[]) {
    const isWorkflowTarget = targetType === "workflow";
    const selectedAgentOption = useMemo(() => (isWorkflowTarget ? undefined : findAgentOption(agentOptions, targetAgent)), [agentOptions, targetAgent, isWorkflowTarget]);
    const selectedWorkflowOption = useMemo(() => (isWorkflowTarget ? workflowOptions.find(o => o.name === targetWorkflow) : undefined), [workflowOptions, targetWorkflow, isWorkflowTarget]);
    const targetName = isWorkflowTarget ? targetWorkflow : targetAgent;
    return {
        isWorkflowTarget,
        selectedWorkflowOption,
        targetNotDeployed: isWorkflowTarget ? isWorkflowNotDeployed(selectedWorkflowOption) : isAgentNotDeployed(selectedAgentOption),
        hasTarget: !!targetName,
        targetKey: `${targetType}:${targetName}`,
    };
}

export function EvalCreateExperimentPage() {
    const navigate = useNavigate();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreate = userHasCapability(CAPABILITIES.createEvaluations);
    const canRun = userHasCapability(CAPABILITIES.invokeEvaluations);
    const [searchParams, setSearchParams] = useSearchParams();
    const { data: datasets = [], refetch: fetchDatasets } = useEvalDatasets();
    const { data: evaluators = [], isLoading: evaluatorsLoading, refetch: fetchEvaluators } = useEvalEvaluators();
    const { data: experiments = [] } = useEvalExperiments();
    const createExperimentMutation = useCreateExperiment();
    const agentOptions = useAgentOptions();
    const workflowEvalsEnabled = useIsWorkflowEvalsEnabled();
    const { options: workflowOptions, readable: workflowsReadable, isLoading: workflowsLoading, isError: workflowsFailed } = useWorkflowOptions(workflowEvalsEnabled);
    const [isClone] = useState(() => !!searchParams.get("cloneFromId"));
    const cloneAppliedRef = useRef(false);

    const [name, setName] = useState("");
    const [description, setDescription] = useState("");
    const [targetType, setTargetType] = useState<EvalTargetType>("agent");
    const [targetAgent, setTargetAgent] = useState("");
    const [targetWorkflow, setTargetWorkflow] = useState("");
    const [datasetId, setDatasetId] = useState("");
    const [models, setModels] = useState<ExperimentModelConfig[]>([]);
    const [evaluatorIds, setEvaluatorIds] = useState<string[]>([]);
    const [primaryEvaluatorId, setPrimaryEvaluatorId] = useState<string | undefined>();
    const [runsPerExample, setRunsPerExample] = useState(2);
    const [maxWorkers, setMaxWorkers] = useState(DEFAULT_MAX_WORKERS);

    const [saving, setSaving] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [exampleRefusal, setExampleRefusal] = useState<ExampleRefusal | null>(null);
    const [showPreview, setShowPreview] = useState(false);
    const [importDialogOpen, setImportDialogOpen] = useState(false);
    const [aiGenerateOpen, setAiGenerateOpen] = useState(false);
    const [createDatasetOpen, setCreateDatasetOpen] = useState(false);
    const [reviewDatasetId, setReviewDatasetId] = useState<string | undefined>();
    const [createEvaluatorOpen, setCreateEvaluatorOpen] = useState(false);

    const { containerRef, width: sidePanelWidth, handleDragStart } = useResizableSidePanel(showPreview);

    const { activeModelId, activeModel } = useAgentActiveModel(targetAgent || undefined);

    useEffect(() => {
        const paramDatasetId = searchParams.get("datasetId");
        if (paramDatasetId && datasets.length > 0 && !datasetId) {
            const exists = datasets.some(d => d.id === paramDatasetId);
            if (exists) {
                setDatasetId(paramDatasetId);
                openPreviewPanel();
                setSearchParams(
                    prev => {
                        const next = new URLSearchParams(prev);
                        next.delete("datasetId");
                        return next;
                    },
                    { replace: true }
                );
            }
        }
    }, [datasets]); // eslint-disable-line react-hooks/exhaustive-deps

    useEffect(() => {
        const paramEvaluatorId = searchParams.get("evaluatorId");
        if (paramEvaluatorId && evaluators.length > 0 && evaluatorIds.length === 0) {
            const exists = evaluators.some(e => e.id === paramEvaluatorId);
            if (exists) {
                setEvaluatorIds([paramEvaluatorId]);
                setPrimaryEvaluatorId(paramEvaluatorId);
                setSearchParams(
                    prev => {
                        const next = new URLSearchParams(prev);
                        next.delete("evaluatorId");
                        return next;
                    },
                    { replace: true }
                );
            }
        }
    }, [evaluators]); // eslint-disable-line react-hooks/exhaustive-deps

    useEffect(() => {
        if (cloneAppliedRef.current) return;
        setModels(activeModel && targetType !== "workflow" ? [activeModel] : []);
    }, [activeModelId, targetType]); // eslint-disable-line react-hooks/exhaustive-deps

    const flagsReady = useWhenProviderReady({ suspendUntilReady: false });
    useEffect(() => {
        const cloneFromId = searchParams.get("cloneFromId");
        if (!cloneFromId || cloneAppliedRef.current || !flagsReady) return;
        let cancelled = false;
        (async () => {
            try {
                const source = await evaluationsApi.getExperiment(cloneFromId);
                if (cancelled) return;
                cloneAppliedRef.current = true;
                setName(`${source.name} (copy)`);
                setDescription(source.description ?? "");
                const target = clonedTarget(source, workflowEvalsEnabled);
                setTargetType(target.targetType);
                setTargetAgent(target.targetAgent);
                setTargetWorkflow(target.targetWorkflow);
                if (target.notice) setError(target.notice);
                setDatasetId(source.datasetId ?? "");
                setModels(source.models ?? []);
                setEvaluatorIds(source.evaluatorIds ?? []);
                setPrimaryEvaluatorId(source.primaryEvaluatorId);
                setRunsPerExample(source.runsPerExample);
                setMaxWorkers(source.maxWorkers ?? DEFAULT_MAX_WORKERS);
                if (source.datasetId) setShowPreview(true);
                setSearchParams(
                    prev => {
                        const next = new URLSearchParams(prev);
                        next.delete("cloneFromId");
                        return next;
                    },
                    { replace: true }
                );
            } catch (err) {
                if (!cancelled) setError(getErrorMessage(err));
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [searchParams, setSearchParams, flagsReady]); // eslint-disable-line react-hooks/exhaustive-deps

    const selectedDataset = useMemo(() => datasets.find(d => d.id === datasetId) ?? null, [datasets, datasetId]);

    const { isWorkflowTarget, selectedWorkflowOption, targetNotDeployed, hasTarget, targetKey } = useSelectedTarget(targetType, targetAgent, targetWorkflow, agentOptions, workflowOptions);
    const deployStateUnknown = isWorkflowTarget && workflowsReadable && (workflowsLoading || workflowsFailed);

    const openPreviewPanel = () => setShowPreview(true);
    const closePreviewPanel = () => setShowPreview(false);

    const pageTitle = isClone ? "Clone Experiment" : "Create Experiment";
    const breadcrumbs = [{ label: "Experiments", onClick: () => navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB) }, { label: pageTitle }];

    const { error: runsError, warning: runsWarning } = validateRunsPerExample(runsPerExample);
    const maxWorkersError = validateMaxWorkers(maxWorkers);

    const trimmedName = name.trim();
    const trimmedNameLower = trimmedName.toLowerCase();
    const nameTaken = !saving && trimmedName.length > 0 && experiments.some(e => e.name.trim().toLowerCase() === trimmedNameLower);

    const isValid = !!(trimmedName && !nameTaken && description.trim() && hasTarget && datasetId && evaluatorIds.length > 0 && runsPerExample >= 1 && !maxWorkersError);

    const buildExperimentPayload = () => {
        const trimmedDescription = description.trim();
        const hasPrimaryEvaluator = primaryEvaluatorId && primaryEvaluatorId.length > 0;
        const singleEvaluatorFallback = evaluatorIds.length === 1 ? evaluatorIds[0] : undefined;

        return {
            name: name.trim(),
            description: trimmedDescription.length > 0 ? trimmedDescription : undefined,
            ...(isWorkflowTarget ? { targetWorkflow } : { targetAgent }),
            datasetId,
            models,
            runsPerExample,
            maxWorkers,
            evaluatorIds,
            primaryEvaluatorId: hasPrimaryEvaluator ? primaryEvaluatorId : singleEvaluatorFallback,
        };
    };

    const openCreatedExperiment = async (id: string, andRun: boolean) => {
        const detailPath = `/evaluations/experiments/${id}`;
        if (!andRun) {
            navigate(detailPath, { replace: true });
            return;
        }
        if (targetNotDeployed) {
            navigate(detailPath, { replace: true, state: { agentNotDeployed: true } });
            return;
        }
        try {
            await evaluationsApi.triggerRun(id, {});
            navigate(`${detailPath}?tab=runs`, { replace: true });
        } catch (err) {
            navigate(detailPath, { replace: true, state: { runNotStarted: getErrorMessage(err) } });
        }
    };

    const handleCreate = async (andRun: boolean) => {
        if (!isValid) return;

        setSaving(true);
        setError(null);
        setExampleRefusal(null);
        try {
            const created = await createExperimentMutation.mutateAsync(buildExperimentPayload());
            await openCreatedExperiment(created.id, andRun);
        } catch (err) {
            const problems = await exampleProblemsForError(err, datasetId);
            setExampleRefusal(problems.length > 0 ? { problems, workflowName: selectedWorkflowOption?.displayName ?? targetWorkflow, target: targetKey, datasetId } : null);
            setError(problems.length > 0 ? null : saveErrorMessage(err));
        } finally {
            setSaving(false);
        }
    };

    const handleTargetTypeChange = (value: string) => {
        setTargetType(value as EvalTargetType);
        if (value === "workflow") setModels([]);
    };

    const handleDatasetChange = (id: string) => {
        setDatasetId(id);
        if (id) openPreviewPanel();
        else closePreviewPanel();
    };

    const openReviewDialog = (id: string) => {
        setReviewDatasetId(id);
        setCreateDatasetOpen(true);
    };

    if (!canCreate) return <Navigate to={EVAL_ROUTES.REPORTS} replace />;

    return (
        <PageLayout>
            <Header title={pageTitle} breadcrumbs={breadcrumbs} />
            <div ref={containerRef} className="relative flex-1 overflow-hidden">
                <div className="flex h-full flex-col">
                    <PageContentWrapper className="p-6" innerClassName="gap-6">
                        <div className="flex max-w-225 flex-col gap-5">
                            <PageInlineRow label="Name" required>
                                <div className="flex flex-col gap-1">
                                    <Input value={name} onChange={e => setName(e.target.value)} disabled={saving} />
                                    {nameTaken && (
                                        <span className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                                            <XCircle className="size-3.5 shrink-0 text-(--error-wMain)" />
                                            An experiment with this name already exists.
                                        </span>
                                    )}
                                </div>
                            </PageInlineRow>
                            <PageInlineRow label="Description" required>
                                <Textarea value={description} onChange={e => setDescription(e.target.value)} rows={2} disabled={saving} />
                            </PageInlineRow>

                            <div className="pt-2">
                                <div className="text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">Configuration</div>
                            </div>

                            {workflowEvalsEnabled && (
                                <PageInlineRow label="Target" required>
                                    <div data-testid="experimentTargetType">
                                        <RadioOptions options={TARGET_TYPE_OPTIONS} value={targetType} onChange={handleTargetTypeChange} disabled={saving} ariaLabel="Target" />
                                    </div>
                                </PageInlineRow>
                            )}

                            <PageInlineRow label={isWorkflowTarget ? "Workflow" : "Agent"} required>
                                <TargetField
                                    isWorkflowTarget={isWorkflowTarget}
                                    agentOptions={agentOptions}
                                    targetAgent={targetAgent}
                                    onAgentChange={setTargetAgent}
                                    workflowOptions={workflowOptions}
                                    targetWorkflow={targetWorkflow}
                                    onWorkflowChange={setTargetWorkflow}
                                    workflowsFailed={workflowsFailed}
                                    notDeployed={targetNotDeployed}
                                    disabled={saving}
                                />
                            </PageInlineRow>

                            {!isWorkflowTarget && (
                                <PageInlineRow
                                    label={
                                        <>
                                            Agent Model{" "}
                                            <span className="text-sm font-normal text-(--secondary-text-wMain)">
                                                ({models.length}/{MAX_MODELS_PER_EXPERIMENT})
                                            </span>
                                        </>
                                    }
                                    required
                                >
                                    <ModelSelect hideLabel multiple value={models} onChange={setModels} disabled={saving} max={MAX_MODELS_PER_EXPERIMENT} activeModelId={activeModelId} />
                                </PageInlineRow>
                            )}

                            <PageInlineRow label="Dataset" required>
                                <DatasetField
                                    datasets={datasets}
                                    value={datasetId}
                                    onChange={handleDatasetChange}
                                    onImportClick={() => setImportDialogOpen(true)}
                                    onCreateClick={() => setCreateDatasetOpen(true)}
                                    onAiGenerateClick={() => setAiGenerateOpen(true)}
                                    disabled={saving}
                                />
                                <ExampleProblemsAlert refusal={currentRefusal(exampleRefusal, targetKey, datasetId)} />
                            </PageInlineRow>

                            <PageInlineRow
                                label={
                                    <>
                                        Evaluator{" "}
                                        <span className="text-sm font-normal text-(--secondary-text-wMain)">
                                            ({evaluatorIds.length}/{MAX_EVALUATORS_PER_EXPERIMENT})
                                        </span>
                                    </>
                                }
                                required
                            >
                                <div className="flex flex-col gap-1">
                                    <EvaluatorPicker
                                        hideLabel
                                        evaluators={evaluators}
                                        loading={evaluatorsLoading}
                                        selectedIds={evaluatorIds}
                                        onChange={setEvaluatorIds}
                                        onCreateClick={() => setCreateEvaluatorOpen(true)}
                                        primaryId={primaryEvaluatorId}
                                        onPrimaryChange={setPrimaryEvaluatorId}
                                        disabled={saving}
                                    />
                                    {evaluatorIds.length > 1 && <span className="text-xs text-(--secondary-text-wMain)">The starred evaluator will be used as the primary.</span>}
                                </div>
                            </PageInlineRow>

                            <Accordion type="single" collapsible>
                                <AccordionItem value="advanced-options" className="border-none">
                                    <AccordionTrigger className="flex-row-reverse justify-end gap-2 py-3 text-sm font-semibold hover:no-underline [&>svg]:-rotate-90 [&[data-state=open]>svg]:rotate-0">Advanced Options</AccordionTrigger>
                                    <AccordionContent className="flex flex-col gap-6 pt-2">
                                        <PageInlineRow label="Runs per Example">
                                            <div className="flex flex-col gap-1">
                                                <Input type="number" min={1} max={5} value={runsPerExample} onChange={e => setRunsPerExample(Number(e.target.value))} className="w-24" disabled={saving} />
                                                {runsError ? (
                                                    <span className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                                                        <XCircle className="size-3.5 shrink-0 text-(--error-wMain)" />
                                                        {runsError}
                                                    </span>
                                                ) : runsWarning ? (
                                                    <span className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                                                        <AlertTriangle className="size-3.5 shrink-0 text-(--warning-w100)" />
                                                        {runsWarning}
                                                    </span>
                                                ) : (
                                                    <span className="text-xs text-(--secondary-text-wMain)">The number of times each example will be evaluated, with the average score being returned as the result.</span>
                                                )}
                                            </div>
                                        </PageInlineRow>
                                        <PageInlineRow label="Max Workers">
                                            <div className="flex flex-col gap-1">
                                                <Input type="number" aria-label="Max Workers" min={MIN_MAX_WORKERS} max={MAX_MAX_WORKERS} value={maxWorkers} onChange={e => setMaxWorkers(Number(e.target.value))} className="w-24" disabled={saving} />
                                                {maxWorkersError ? (
                                                    <span className="inline-flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                                                        <XCircle className="size-3.5 shrink-0 text-(--error-wMain)" />
                                                        {maxWorkersError}
                                                    </span>
                                                ) : (
                                                    <span className="text-xs text-(--secondary-text-wMain)">
                                                        The most trials each model's run sends to the agent at once. Models run in parallel, so lower it if the model provider rate-limits the agent.
                                                    </span>
                                                )}
                                            </div>
                                        </PageInlineRow>
                                    </AccordionContent>
                                </AccordionItem>
                            </Accordion>

                            {error && <span className="text-xs text-(--error-wMain)">{error}</span>}
                        </div>
                    </PageContentWrapper>
                </div>

                <ResizableSidePanel open={showPreview && !!selectedDataset} width={sidePanelWidth} onDragStart={handleDragStart}>
                    {showPreview && selectedDataset && <DatasetSidePanel dataset={selectedDataset} onClose={closePreviewPanel} previewMode />}
                </ResizableSidePanel>
            </div>

            <PageFooter>
                <Button variant="ghost" onClick={() => navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB)} disabled={saving}>
                    Cancel
                </Button>
                <Button data-testid="experimentWizardCreate" variant="outline" onClick={() => void handleCreate(false)} disabled={saving ? true : !isValid}>
                    {saving ? "Creating..." : "Create"}
                </Button>
                {canCreate && canRun && (
                    <Button data-testid="experimentWizardCreateAndRun" onClick={() => void handleCreate(true)} disabled={saving || !isValid || deployStateUnknown}>
                        {saving ? "Creating..." : "Create & Run"}
                    </Button>
                )}
            </PageFooter>

            <DatasetImportDialog open={importDialogOpen} onClose={() => setImportDialogOpen(false)} onImported={openReviewDialog} />

            <AiGenerationDialog mode="dataset" open={aiGenerateOpen} onClose={() => setAiGenerateOpen(false)} onGenerated={dataset => openReviewDialog(dataset.id)} defaultTargetAgent={targetAgent || undefined} />

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
                    handleDatasetChange(id);
                }}
            />

            <CreateEvaluatorDialog
                open={createEvaluatorOpen}
                onOpenChange={setCreateEvaluatorOpen}
                onCreated={id => {
                    void fetchEvaluators();
                    setEvaluatorIds(prev => addEvaluatorRespectingCap(prev, id));
                    if (evaluatorIds.length === 0) setPrimaryEvaluatorId(id);
                }}
            />
        </PageLayout>
    );
}
