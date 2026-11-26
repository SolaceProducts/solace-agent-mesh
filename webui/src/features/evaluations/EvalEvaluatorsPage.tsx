// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { ListChecks } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";

import { evaluationsApi, useDeleteEvaluator, useEvalEvaluators } from "@/api/evaluations";

import { useUserContext } from "@/providers/contexts";

import { PageContentWrapper, PageSidePanelWrapper } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { SearchInput } from "@/ui/search-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import type { EvalEvaluator, EvalExperiment } from "@/types/eval";

import { EmptyState, Header, InfoTipCard, MessageBanner, MorePopoverButton, PageLayout } from "@/common";
import { useIsDecisionEvaluatorEnabled } from "@/hooks";

import samExperimentsEvaluatorMiniIllustration from "./assets/samExperimentsEvaluatorMiniIllustration.png";
import { EditJudgeModelDialog, EvaluatorSidePanel, TypedDeleteConfirmationDialog } from "./components";
import { EVAL_DOCS, EVAL_ROUTES, EVAL_SORT } from "./constants";
import { getTypeLabel } from "./utils";

const SORT_OPTIONS = [
    { value: EVAL_SORT.NAME_ASC, label: "Alphabetical Ascending" },
    { value: EVAL_SORT.NAME_DESC, label: "Alphabetical Descending" },
    { value: EVAL_SORT.EXPERIMENT_USAGE_DESC, label: "Experiment Usage" },
];

const INFO_BANNER_KEY = "eval-evaluators-info-dismissed";

interface EvaluatorCardProps {
    readonly evaluator: EvalEvaluator;
    readonly selected?: boolean;
    readonly onClick: () => void;
    readonly onOpen?: () => void;
    readonly onCreateExperiment?: () => void;
    readonly onEdit?: () => void;
    readonly onEditModel?: () => void;
    readonly onDelete?: () => void;
}

function EvaluatorCard({ evaluator, selected, onClick, onOpen, onCreateExperiment, onEdit, onEditModel, onDelete }: EvaluatorCardProps) {
    const experimentCount = evaluator.experimentCount ?? 0;

    return (
        <div className={cn("relative flex flex-col gap-2 border-b px-4 py-3 transition-colors last:border-b-0 hover:bg-(--secondary-w10)/50 sm:flex-row sm:items-start sm:justify-between sm:gap-0", selected && "bg-(--secondary-w10)/70")}>
            <button type="button" onClick={onClick} aria-label={`Open ${evaluator.name}`} className="absolute inset-0 cursor-pointer rounded focus-visible:ring-2 focus-visible:ring-(--brand-wMain) focus-visible:outline-none" />
            <div className="pointer-events-none relative flex min-w-0 items-start gap-3">
                <ListChecks className="mt-0.5 size-4 shrink-0 text-(--secondary-text-wMain)" />
                <div className="flex min-w-0 flex-col gap-0.5">
                    <span className="truncate text-sm font-semibold">{evaluator.name}</span>
                    {evaluator.description && <span className="truncate text-sm text-(--secondary-text-wMain)">{evaluator.description}</span>}
                </div>
            </div>
            <div className="pointer-events-none relative flex shrink-0 items-start gap-4 pl-7 sm:pl-4">
                <div className="flex flex-1 flex-col items-start gap-0.5 text-sm sm:flex-none sm:items-end">
                    <span>{getTypeLabel(evaluator)}</span>
                    <span className="text-(--secondary-text-wMain)">
                        Used in <strong>{experimentCount}</strong> experiment{experimentCount !== 1 ? "s" : ""}
                    </span>
                </div>
                {(onOpen || onCreateExperiment || onEdit || onEditModel || onDelete) && (
                    <div className="pointer-events-auto">
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>{MorePopoverButton}</DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                                {onOpen && <DropdownMenuItem onClick={onOpen}>Open Evaluator</DropdownMenuItem>}
                                {onCreateExperiment && <DropdownMenuItem onClick={onCreateExperiment}>Create Experiment</DropdownMenuItem>}
                                {onEdit && <DropdownMenuItem onClick={onEdit}>Edit</DropdownMenuItem>}
                                {onEditModel && <DropdownMenuItem onClick={onEditModel}>Edit Model</DropdownMenuItem>}
                                {onDelete && <DropdownMenuItem onClick={onDelete}>Delete</DropdownMenuItem>}
                            </DropdownMenuContent>
                        </DropdownMenu>
                    </div>
                )}
            </div>
        </div>
    );
}

interface EvalEvaluatorsPageProps {
    readonly embedded?: boolean;
}

export function EvalEvaluatorsPage({ embedded }: EvalEvaluatorsPageProps = {}) {
    const navigate = useNavigate();
    const location = useLocation();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreate = userHasCapability(CAPABILITIES.createEvaluations);
    const canUpdate = userHasCapability(CAPABILITIES.updateEvaluations);
    const canDelete = userHasCapability(CAPABILITIES.deleteEvaluations);
    const decisionEnabled = useIsDecisionEvaluatorEnabled();
    const { data: evaluators = [], isLoading: loading } = useEvalEvaluators();
    const deleteEvaluatorMutation = useDeleteEvaluator();
    const [filter, setFilter] = useState("");
    const [sortValue, setSortValue] = useState<string>(EVAL_SORT.NAME_ASC);
    const [deleteTarget, setDeleteTarget] = useState<EvalEvaluator | null>(null);
    const [modelTarget, setModelTarget] = useState<EvalEvaluator | null>(null);
    const [deleteTargetExperiments, setDeleteTargetExperiments] = useState<EvalExperiment[]>([]);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);

    useEffect(() => {
        if (!deleteTarget || (deleteTarget.experimentCount ?? 0) === 0) {
            setDeleteTargetExperiments([]);
            return;
        }
        let ignore = false;
        evaluationsApi
            .getExperiments({ evaluatorId: deleteTarget.id })
            .then(list => {
                if (!ignore) setDeleteTargetExperiments(list);
            })
            .catch(() => {
                if (!ignore) setDeleteTargetExperiments([]);
            });
        return () => {
            ignore = true;
        };
    }, [deleteTarget]);
    const [selectedEvaluator, setSelectedEvaluator] = useState<EvalEvaluator | null>(null);
    const [successMessage, setSuccessMessage] = useState<string | null>(null);

    const isPanelOpen = !!selectedEvaluator;

    useEffect(() => {
        const params = new URLSearchParams(location.search);
        const selectedId = params.get("selected");
        if (selectedId && evaluators.length > 0) {
            const match = evaluators.find(e => e.id === selectedId);
            if (match) {
                setSelectedEvaluator(match);
                const justCreated = (location.state as { justCreated?: boolean } | null)?.justCreated === true;
                if (justCreated) setSuccessMessage(`Evaluator "${match.name}" created.`);
                params.delete("selected");
                const search = params.toString();
                navigate(`${location.pathname}${search ? `?${search}` : ""}`, { replace: true, state: null });
            }
        }
    }, [evaluators, location.pathname, location.search, location.state, navigate]);

    const sortedAndFiltered = useMemo(() => {
        const text = filter.toLowerCase();
        const matched = text ? evaluators.filter(e => e.name.toLowerCase().includes(text)) : [...evaluators];
        const [sortBy, sortOrder] = sortValue.split(":");
        matched.sort((a, b) => {
            let cmp: number;
            if (sortBy === "experiment_count") {
                cmp = (a.experimentCount ?? 0) - (b.experimentCount ?? 0);
            } else {
                cmp = a.name.localeCompare(b.name);
            }
            return sortOrder === "desc" ? -cmp : cmp;
        });
        return matched;
    }, [evaluators, filter, sortValue]);

    const custom = useMemo(() => sortedAndFiltered.filter(e => !e.isDefault), [sortedAndFiltered]);
    const builtIn = useMemo(() => sortedAndFiltered.filter(e => e.isDefault), [sortedAndFiltered]);

    const deleteTargetExperimentWord = deleteTargetExperiments.length === 1 ? "experiment" : "experiments";

    const handleDelete = async () => {
        if (!deleteTarget) return;
        setDeleting(true);
        setDeleteError(null);
        try {
            await deleteEvaluatorMutation.mutateAsync(deleteTarget.id);
            if (selectedEvaluator?.id === deleteTarget.id) {
                setSelectedEvaluator(null);
            }
            setDeleteTarget(null);
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

    const handleCardClick = (ev: EvalEvaluator) => {
        if (selectedEvaluator?.id === ev.id) {
            setSelectedEvaluator(null);
        } else {
            setSelectedEvaluator(ev);
            setSuccessMessage(null);
        }
    };

    const handleCloseSidePanel = useCallback(() => {
        setSelectedEvaluator(null);
    }, []);

    const renderContent = () => {
        if (loading) return <EmptyState variant="loading" title="Loading evaluators..." />;
        if (evaluators.length === 0) {
            return (
                <EmptyState
                    variant="noImage"
                    title="No evaluators yet"
                    subtitle="Create an evaluator to define how agent responses are scored."
                    buttons={canCreate ? [{ text: "Create Evaluator", variant: "default", onClick: () => navigate(EVAL_ROUTES.EVALUATOR_NEW) }] : []}
                />
            );
        }
        return (
            <div className="flex flex-col gap-4">
                {successMessage && <MessageBanner variant="success" message={successMessage} dismissible onDismiss={() => setSuccessMessage(null)} />}

                <InfoTipCard
                    storageKey={INFO_BANNER_KEY}
                    header="Evaluators score how well agent responses meet your quality criteria"
                    description="Each experiment runs one or more evaluators against an agent's response, producing a score that is tied back to the dataset and experiment. Use built-in evaluators or create your own tailored to your business use case."
                    learnMoreText="Learn about evaluators and see examples"
                    learnMoreUrl={EVAL_DOCS.EVALUATORS}
                    illustration={samExperimentsEvaluatorMiniIllustration}
                />

                <div className="flex flex-wrap items-center justify-between gap-3">
                    <SearchInput placeholder="Filter by name" value={filter} onChange={setFilter} />
                    <div className="flex flex-wrap items-center gap-2">
                        <span className="text-sm text-(--secondary-text-wMain)">Sort By</span>
                        <Select value={sortValue} onValueChange={setSortValue}>
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

                <div className="flex flex-col gap-2">
                    <h3 className="text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">My Evaluators</h3>
                    {custom.length > 0 ? (
                        <div className="rounded-xs border">
                            {custom.map(ev => (
                                <EvaluatorCard
                                    key={ev.id}
                                    evaluator={ev}
                                    selected={selectedEvaluator?.id === ev.id}
                                    onClick={() => handleCardClick(ev)}
                                    onOpen={() => navigate(EVAL_ROUTES.evaluator(ev.id))}
                                    onCreateExperiment={canCreate ? () => navigate(EVAL_ROUTES.experimentForEvaluator(ev.id)) : undefined}
                                    onEdit={canUpdate && (ev.type !== "decision" || decisionEnabled) ? () => navigate(EVAL_ROUTES.evaluator(ev.id), { state: { editing: true } }) : undefined}
                                    onDelete={canDelete ? () => setDeleteTarget(ev) : undefined}
                                />
                            ))}
                        </div>
                    ) : (
                        <div className="flex flex-col items-center justify-center gap-3 rounded-xs bg-(--secondary-w10)/40 px-4 py-10">
                            <p className="text-sm text-(--secondary-text-wMain)">You haven't created any evaluators yet.</p>
                            {canCreate && <Button onClick={() => navigate(EVAL_ROUTES.EVALUATOR_NEW)}>+ Create Evaluator</Button>}
                        </div>
                    )}
                </div>

                {builtIn.length > 0 && (
                    <div className="flex flex-col gap-2">
                        <h3 className="text-xs font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">Built-in Evaluators</h3>
                        <div className="rounded-xs border">
                            {builtIn.map(ev => (
                                <EvaluatorCard
                                    key={ev.id}
                                    evaluator={ev}
                                    selected={selectedEvaluator?.id === ev.id}
                                    onClick={() => handleCardClick(ev)}
                                    {...(ev.type === "llm_judge" && {
                                        onOpen: () => navigate(EVAL_ROUTES.evaluator(ev.id)),
                                        onCreateExperiment: canCreate ? () => navigate(EVAL_ROUTES.experimentForEvaluator(ev.id)) : undefined,
                                        onEditModel: canUpdate ? () => setModelTarget(ev) : undefined,
                                    })}
                                />
                            ))}
                        </div>
                    </div>
                )}

                {sortedAndFiltered.length === 0 && <div className="py-8 text-center text-sm text-(--secondary-text-wMain)">No evaluators match the current filter.</div>}
            </div>
        );
    };

    const modelDialog = modelTarget && <EditJudgeModelDialog evaluator={modelTarget} open onOpenChange={open => !open && setModelTarget(null)} onSaved={updated => setSelectedEvaluator(current => (current?.id === updated.id ? updated : current))} />;

    const deleteDialog = (
        <TypedDeleteConfirmationDialog
            title="Delete Evaluator"
            open={deleteTarget !== null}
            onOpenChange={handleDeleteDialogChange}
            deleting={deleting}
            error={deleteError}
            onConfirm={handleDelete}
            primaryMessage={
                <>
                    This will permanently delete <strong>{deleteTarget?.name}</strong>. This action cannot be undone.
                </>
            }
            warningMessage={
                deleteTargetExperiments.length > 0 ? (
                    <div className="flex flex-col gap-1">
                        <span>
                            This evaluator is used in{" "}
                            <strong>
                                {deleteTargetExperiments.length} {deleteTargetExperimentWord}
                            </strong>
                            . The experiment will remain but will lose its evaluator and need to be reconfigured before it can be run again.
                        </span>
                        <ul className="ml-4 list-disc">
                            {deleteTargetExperiments.map(ex => (
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

    const mainContent = (
        <div className="flex h-full flex-col">
            {!embedded && (
                <Header
                    title="Evaluators"
                    buttons={
                        canCreate
                            ? [
                                  <Button key="new" onClick={() => navigate(EVAL_ROUTES.EVALUATOR_NEW)}>
                                      + Create Evaluator
                                  </Button>,
                              ]
                            : []
                    }
                />
            )}
            <div className="flex flex-1 overflow-hidden">
                <div className="flex h-full min-w-0 flex-1 flex-col">
                    <PageContentWrapper className="flex-1 overflow-auto p-6" innerClassName="gap-6">
                        {renderContent()}
                    </PageContentWrapper>
                </div>
                <PageSidePanelWrapper isSidePanelOpen={isPanelOpen} onClose={handleCloseSidePanel} sidepanel={selectedEvaluator ? <EvaluatorSidePanel evaluator={selectedEvaluator} onClose={handleCloseSidePanel} /> : null} />
            </div>
            {deleteDialog}
            {modelDialog}
        </div>
    );

    if (embedded) {
        return mainContent;
    }

    return <PageLayout>{mainContent}</PageLayout>;
}
