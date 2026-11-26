// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { Pencil } from "lucide-react";
import { Navigate, useLoaderData, useLocation, useNavigate } from "react-router-dom";

import { evaluationsApi, useCreateEvaluator, useDeleteEvaluator, useEvalEvaluators, useUpdateEvaluator } from "@/api/evaluations";
import { useModelConfigs } from "@/api/models/hooks";

import { useUserContext } from "@/providers/contexts";

import { PageContentWrapper, PageFooter, PageSection } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { Tabs, TabsContent, TabsList } from "@/ui/tabs";
import type { EvalEvaluator, EvalExperiment } from "@/types/eval";

import { Header, MorePopoverButton } from "@/common";
import { useIsDecisionEvaluatorEnabled } from "@/hooks";

import {
    buildCreateEvaluatorRequest,
    buildUpdateEvaluatorRequest,
    choiceScoresFromRecord,
    ChoiceScoresList,
    DecisionEvaluatorSummary,
    decisionSettingsFromEvaluator,
    DEFAULT_JUDGE_MODEL_LABEL,
    DetailPageLoading,
    DetailPageNotFound,
    EditJudgeModelDialog,
    emptyEvaluatorFormValue,
    EvalTabsTrigger,
    type EvaluatorFieldErrors,
    EvaluatorFormBody,
    evaluatorFormFromEvaluator,
    type EvaluatorFormValue,
    ExperimentLink,
    ExperimentUsageBanner,
    findModelByRef,
    HighlightedPromptText,
    isEvaluatorFormComplete,
    preSaveErrorState,
    saveErrorState,
    TypedDeleteConfirmationDialog,
} from "./components";
import { EVAL_ROUTES } from "./constants";
import { useDefaultModelSelection } from "./hooks";
import { getTypeLabel } from "./utils";

interface CloneInitialValues {
    name: string;
    description?: string;
    promptTemplate?: string;
    choiceScores?: Record<string, number>;
}

function getPageTitle(isNew: boolean, editing: boolean, evaluator: EvalEvaluator | null): string {
    if (isNew) return "Create Evaluator";
    if (editing) return "Edit Evaluator";
    return evaluator?.name ?? "Evaluator";
}

function getSaveButtonLabel(saving: boolean, isNew: boolean): string {
    if (saving) return "Saving...";
    if (isNew) return "Create";
    return "Save";
}

function experimentWordFor(count: number): string {
    return count === 1 ? "experiment" : "experiments";
}

export function EvalCreateEvaluatorPage() {
    const navigate = useNavigate();
    const location = useLocation();
    const loaderData = useLoaderData() as { evaluatorId?: string } | undefined;
    const evaluatorId = loaderData?.evaluatorId;
    const requestedEditing = (location.state as { editing?: boolean } | null)?.editing === true;
    const createEvaluatorMutation = useCreateEvaluator();
    const updateEvaluatorMutation = useUpdateEvaluator();
    const deleteEvaluatorMutation = useDeleteEvaluator();
    const { data: modelConfigs = [] } = useModelConfigs();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreate = userHasCapability(CAPABILITIES.createEvaluations);
    const canUpdate = userHasCapability(CAPABILITIES.updateEvaluations);
    const canDelete = userHasCapability(CAPABILITIES.deleteEvaluations);
    const decisionEnabled = useIsDecisionEvaluatorEnabled();
    const { data: allEvaluators = [] } = useEvalEvaluators();

    const cloneValues = (location.state as { initialValues?: CloneInitialValues } | null)?.initialValues;

    const [evaluator, setEvaluator] = useState<EvalEvaluator | null>(null);
    const [pageLoading, setPageLoading] = useState(!!evaluatorId);
    const [editing, setEditing] = useState(!evaluatorId || requestedEditing);
    const isNew = !evaluatorId;
    const isReadOnly = !editing || (evaluator?.type === "decision" && !decisionEnabled);
    const isBuiltIn = evaluator?.isDefault ?? false;

    const [value, setValue] = useState<EvaluatorFormValue>(() =>
        cloneValues
            ? {
                  ...emptyEvaluatorFormValue(),
                  name: cloneValues.name ?? "",
                  description: cloneValues.description ?? "",
                  promptTemplate: cloneValues.promptTemplate ?? "",
                  choiceScores: choiceScoresFromRecord(cloneValues.choiceScores),
              }
            : emptyEvaluatorFormValue()
    );
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [fieldErrors, setFieldErrors] = useState<EvaluatorFieldErrors>({});
    const [relatedExperiments, setRelatedExperiments] = useState<EvalExperiment[]>([]);

    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
    const [modelDialogOpen, setModelDialogOpen] = useState(false);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);

    useDefaultModelSelection(isNew, value.model, id => setValue(v => ({ ...v, model: id })));

    useEffect(() => {
        if (!evaluatorId) return;
        setPageLoading(true);
        setRelatedExperiments([]);
        let ignore = false;
        Promise.all([evaluationsApi.getEvaluator(evaluatorId), evaluationsApi.getExperiments({ evaluatorId }).catch(() => [] as EvalExperiment[])])
            .then(([ev, experiments]) => {
                if (ignore) return;
                setEvaluator(ev);
                setValue(evaluatorFormFromEvaluator(ev));
                setRelatedExperiments(experiments);
            })
            .catch(() => {
                if (!ignore) setEvaluator(null);
            })
            .finally(() => {
                if (!ignore) setPageLoading(false);
            });
        return () => {
            ignore = true;
        };
    }, [evaluatorId]);

    const showError = (state: { fieldErrors: EvaluatorFieldErrors; message: string }) => {
        setFieldErrors(state.fieldErrors);
        setError(state.message);
    };

    const handleSave = async () => {
        const refused = preSaveErrorState(value);
        if (refused) {
            showError(refused);
            return;
        }

        setSaving(true);
        setError(null);
        setFieldErrors({});
        try {
            if (isNew) {
                const created = await createEvaluatorMutation.mutateAsync(buildCreateEvaluatorRequest(value));
                navigate(EVAL_ROUTES.labEvaluatorsSelected(created.id), { state: { justCreated: true } });
            } else if (evaluatorId) {
                const updated = await updateEvaluatorMutation.mutateAsync({ id: evaluatorId, data: buildUpdateEvaluatorRequest(value) });
                setEvaluator(updated);
                setEditing(false);
            }
        } catch (err) {
            showError(saveErrorState(err, value.type));
        } finally {
            setSaving(false);
        }
    };

    const handleDiscard = () => {
        if (isNew) {
            navigate(-1);
            return;
        }
        if (!evaluator) return;
        setValue(evaluatorFormFromEvaluator(evaluator));
        setEditing(false);
        setError(null);
        setFieldErrors({});
    };

    const breadcrumbs = [
        { label: "Experiments", onClick: () => navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB) },
        { label: "Evaluators", onClick: () => navigate(EVAL_ROUTES.LAB_EVALUATORS_TAB) },
    ];

    const title = getPageTitle(isNew, editing, evaluator);
    const isValid = isEvaluatorFormComplete(value);
    const disabled = saving || isReadOnly;

    const handleClone = () => {
        if (!evaluator) return;
        navigate("/evaluations/evaluators/new", {
            state: {
                initialValues: {
                    name: `${evaluator.name} (Clone)`,
                    description: evaluator.description,
                    promptTemplate: evaluator.promptTemplate,
                    choiceScores: evaluator.choiceScores,
                },
            },
        });
    };

    const saveButtonLabel = getSaveButtonLabel(saving, isNew);
    const experimentWord = experimentWordFor(relatedExperiments.length);

    const handleDelete = async () => {
        if (!evaluator) return;
        setDeleting(true);
        setDeleteError(null);
        try {
            await deleteEvaluatorMutation.mutateAsync(evaluator.id);
            navigate(EVAL_ROUTES.LAB_EVALUATORS_TAB);
        } catch (err) {
            setDeleteError(getErrorMessage(err));
            throw err;
        } finally {
            setDeleting(false);
        }
    };

    const handleDeleteDialogChange = (open: boolean) => {
        setDeleteDialogOpen(open);
        if (!open) {
            setDeleteError(null);
        }
    };

    const buildHeaderButtons = () => {
        if (isNew || !isReadOnly) return [];
        if (isBuiltIn && evaluator?.type === "llm_judge") {
            const buttons = [];
            if (canUpdate) {
                buttons.push(
                    <Button key="editModel" variant="ghost" onClick={() => setModelDialogOpen(true)}>
                        <Pencil className="mr-1 size-4" /> Edit Model
                    </Button>
                );
            }
            if (canCreate) {
                buttons.push(
                    <Button key="clone" variant="ghost" onClick={handleClone}>
                        Clone
                    </Button>
                );
            }
            return buttons;
        }
        if (isBuiltIn) return [];
        const buttons = [];
        const editable = evaluator?.type !== "decision" || decisionEnabled;
        if (canUpdate && editable) {
            buttons.push(
                <Button key="edit" variant="ghost" onClick={() => setEditing(true)}>
                    <Pencil className="mr-1 size-4" /> Edit
                </Button>
            );
        }
        if (canDelete) {
            buttons.push(
                <DropdownMenu key="more">
                    <DropdownMenuTrigger asChild>{MorePopoverButton}</DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => setDeleteDialogOpen(true)}>Delete</DropdownMenuItem>
                    </DropdownMenuContent>
                </DropdownMenu>
            );
        }
        return buttons;
    };

    const [activeTab, setActiveTab] = useState<"config" | "usage">("config");

    const experimentCount = relatedExperiments.length;
    const showUsageBanner = editing && !isNew && experimentCount > 0;

    const editDenied = isNew ? !canCreate : editing && !canUpdate;
    if (editDenied) return <Navigate to={EVAL_ROUTES.LAB_EVALUATORS_TAB} replace />;

    if (pageLoading) {
        return <DetailPageLoading title="Evaluator" breadcrumbs={breadcrumbs} />;
    }

    if (!isNew && !evaluator) {
        return <DetailPageNotFound title="Evaluator" breadcrumbs={breadcrumbs} notFoundTitle="Evaluator not found" backLabel="Back to Evaluators" onBack={() => navigate(EVAL_ROUTES.LAB_EVALUATORS_TAB)} />;
    }

    const resolvedModelConfig = evaluator?.type === "decision" ? findModelByRef(modelConfigs, evaluator.model) : modelConfigs.find(m => !!evaluator?.model && m.id === evaluator.model);
    const modelChip = evaluator?.model ? (
        <span className="w-fit rounded-md bg-(--secondary-w10) px-3 py-1.5 text-sm font-medium">
            {resolvedModelConfig?.alias ?? evaluator.model}
            {resolvedModelConfig && <span className="ml-2 font-mono text-xs text-(--secondary-text-wMain)">{resolvedModelConfig.modelName}</span>}
        </span>
    ) : undefined;

    const renderDecisionSettings = (ev: EvalEvaluator) => {
        const escalateToName = allEvaluators.find(e => e.id === ev.escalateToEvaluatorId)?.name;
        return (
            <PageSection>
                <DecisionEvaluatorSummary settings={decisionSettingsFromEvaluator(ev, escalateToName)} model={modelChip ?? <span className="text-(--secondary-text-wMain)">No model configured</span>} />
            </PageSection>
        );
    };

    const renderReadOnlyView = (ev: EvalEvaluator) => (
        <>
            <PageSection>
                <div className="flex flex-col gap-2">
                    <div className="text-sm font-semibold">Description</div>
                    <div className="max-w-lg text-sm whitespace-pre-wrap">{ev.description || <span className="text-(--secondary-text-wMain)">No description</span>}</div>
                </div>
            </PageSection>

            <PageSection>
                <div className="flex flex-col gap-2">
                    <div className="text-sm font-semibold">Type</div>
                    <div className="text-sm">{getTypeLabel(ev)}</div>
                </div>
            </PageSection>

            {ev.type === "decision" && renderDecisionSettings(ev)}

            {ev.type !== "decision" && (modelChip || (isBuiltIn && ev.type === "llm_judge")) && (
                <PageSection>
                    <div className="flex flex-col gap-2">
                        <div className="text-sm font-semibold">Model</div>
                        {modelChip ?? <span className="text-sm text-(--secondary-text-wMain)">{DEFAULT_JUDGE_MODEL_LABEL}</span>}
                    </div>
                </PageSection>
            )}

            {ev.type !== "decision" && ev.promptTemplate && (
                <PageSection>
                    <div className="flex flex-col gap-2">
                        <div className="text-sm font-semibold">Criteria</div>
                        <div className="max-w-2xl rounded-xs bg-(--secondary-w10)/30 p-3 text-sm whitespace-pre-wrap">
                            <HighlightedPromptText text={ev.promptTemplate} />
                        </div>
                    </div>
                </PageSection>
            )}

            {ev.type !== "decision" && ev.choiceScores && Object.keys(ev.choiceScores).length > 0 && (
                <PageSection>
                    <div className="flex max-w-xl flex-col gap-2">
                        <div className="text-sm font-semibold">Scores</div>
                        <ChoiceScoresList scores={ev.choiceScores} />
                    </div>
                </PageSection>
            )}
        </>
    );

    const renderUsageTab = () => {
        if (relatedExperiments.length === 0) {
            return (
                <PageSection>
                    <div className="text-sm text-(--secondary-text-wMain)">This evaluator is not used in any experiments yet.</div>
                </PageSection>
            );
        }
        return (
            <PageSection>
                <div className="flex flex-col gap-3">
                    {relatedExperiments.map(ex => (
                        <div key={ex.id} className="rounded-sm border px-4 py-3">
                            <ExperimentLink id={ex.id} name={ex.name} className="text-sm" iconClassName="size-3.5" />
                            {ex.description && <div className="mt-1 text-sm text-(--secondary-text-wMain)">{ex.description}</div>}
                        </div>
                    ))}
                </div>
            </PageSection>
        );
    };

    const deleteDialog = (
        <TypedDeleteConfirmationDialog
            title="Delete Evaluator"
            open={deleteDialogOpen}
            onOpenChange={handleDeleteDialogChange}
            deleting={deleting}
            error={deleteError}
            onConfirm={handleDelete}
            primaryMessage={
                <>
                    This will permanently delete <strong>{evaluator?.name}</strong>. This action cannot be undone.
                </>
            }
            warningMessage={
                relatedExperiments.length > 0 ? (
                    <div className="flex flex-col gap-1">
                        <span>
                            This evaluator is used in{" "}
                            <strong>
                                {relatedExperiments.length} {experimentWord}
                            </strong>
                            . The experiment will remain but will lose its evaluator and need to be reconfigured before it can be run again.
                        </span>
                        <ul className="ml-4 list-disc">
                            {relatedExperiments.map(ex => (
                                <li key={ex.id}>{ex.name}</li>
                            ))}
                        </ul>
                    </div>
                ) : undefined
            }
            confirmLabel={
                <>
                    Type <strong>DELETE</strong> to confirm
                    <span className="ml-0.5 text-(--brand-wMain)">*</span>
                </>
            }
        />
    );

    if (isReadOnly) {
        return (
            <div className="flex h-full flex-col">
                <Tabs value={activeTab} onValueChange={v => setActiveTab(v as "config" | "usage")} className="flex min-w-0 flex-1 flex-col overflow-hidden">
                    <div className="scrollbar-hide flex items-center gap-2 overflow-x-auto px-4 pt-4 text-sm whitespace-nowrap sm:px-6">
                        <button type="button" className="shrink-0 cursor-pointer font-semibold text-(--primary-wMain) hover:underline" onClick={() => navigate(EVAL_ROUTES.LAB_EXPERIMENTS_TAB)}>
                            Experiments
                        </button>
                        <span className="shrink-0 text-(--secondary-text-wMain)">&gt;</span>
                        <button type="button" className="shrink-0 cursor-pointer font-semibold text-(--primary-wMain) hover:underline" onClick={() => navigate(EVAL_ROUTES.LAB_EVALUATORS_TAB)}>
                            Evaluators
                        </button>
                    </div>
                    <div className="flex items-center border-b pr-6">
                        <h1 className="shrink-0 pr-6 pl-6 text-lg">{evaluator?.name ?? "Evaluator"}</h1>
                        <TabsList className="h-auto flex-1 justify-start rounded-none bg-transparent p-0">
                            <EvalTabsTrigger value="config" className="min-h-10 py-2">
                                Configuration
                            </EvalTabsTrigger>
                            <EvalTabsTrigger value="usage" className="min-h-10 py-2">
                                Usage
                            </EvalTabsTrigger>
                        </TabsList>
                        <div className="ml-auto flex shrink-0 items-center gap-2">{buildHeaderButtons()}</div>
                    </div>
                    <PageContentWrapper className="flex-1 overflow-auto p-6" innerClassName="gap-8">
                        <TabsContent value="config" className="mt-0">
                            <div className="flex flex-col gap-8">{evaluator && renderReadOnlyView(evaluator)}</div>
                        </TabsContent>
                        <TabsContent value="usage" className="mt-0">
                            {renderUsageTab()}
                        </TabsContent>
                    </PageContentWrapper>
                </Tabs>
                {deleteDialog}
                {modelDialogOpen && evaluator && <EditJudgeModelDialog evaluator={evaluator} open={modelDialogOpen} onOpenChange={setModelDialogOpen} onSaved={setEvaluator} />}
            </div>
        );
    }

    return (
        <div className="flex h-full flex-col">
            <Header title={title} breadcrumbs={breadcrumbs} buttons={buildHeaderButtons()} />
            <PageContentWrapper className="p-6" innerClassName="gap-8">
                {showUsageBanner && <ExperimentUsageBanner resourceLabel="evaluator" experiments={relatedExperiments} />}
                <EvaluatorFormBody value={value} onChange={setValue} disabled={disabled} error={error} variant="page" fieldErrors={fieldErrors} typeEditable={isNew} evaluatorId={evaluatorId} />
            </PageContentWrapper>

            {(isNew || editing) && (
                <PageFooter>
                    <Button variant="ghost" onClick={handleDiscard} disabled={saving}>
                        {isNew ? "Cancel" : "Discard Changes"}
                    </Button>
                    <Button onClick={() => void handleSave()} disabled={saving || !isValid || (isNew ? !canCreate : !canUpdate)}>
                        {saveButtonLabel}
                    </Button>
                </PageFooter>
            )}
        </div>
    );
}
