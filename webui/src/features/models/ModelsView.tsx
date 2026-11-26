// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef, useState } from "react";
import { AlertTriangle, Ellipsis } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";

import { useCapabilityBadge } from "@/api/capabilities";
import { isDecisionModel, useDeleteModel, useModelConfigs } from "@/api/models";
import type { ModelConfig } from "@/api/models/types";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { FreeCreditsBanner, FreeModelName, ModelConfigIcon, useFreeModel } from "@/features/activation";

import { EmptyState, OnboardingBanner, OnboardingView, PaginationControls } from "@/common";
import {
    Badge,
    Button,
    LifecycleBadge,
    Menu,
    type MenuAction,
    Popover,
    PopoverContent,
    PopoverTrigger,
    SortableTableHead,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
    Tooltip,
    TooltipContent,
    TooltipTrigger,
    useSortableTable,
} from "@/ui";
import { cn, DOC_LINKS } from "@/utils";

import { DEFAULT_MODEL_ALIASES, getDisplayAliasName, getDisplayModelName, getProviderDisplayName, isModelConfigured, modelConfigurationIssues } from "./common";
import { ModelDeleteDialog } from "./ModelDeleteDialog";
import { PRICE_CURRENCY, REQUIRED_RATES, tokenQuantityLabel } from "./modelPricing";
import { ModelPricingCells } from "./ModelPricingCells";
import { DECISION_MODELS_CAPABILITY } from "./modelProviderUtils";

const MODELS_STORAGE_KEY = "sam-models-onboarding-dismissed";
const MODELS_HEADER = "Your Models Are Now Accessible to Your Team.";
const MODELS_DESCRIPTION =
    "Existing models from your local backend configuration have been imported here and are now accessible for anyone in your organization. Going forward, changes you make in this UI will take precedence over your shared config file.";
const MODELS_LEARN_MORE_TEXT = "Learn about managing models";
const MODELS_URL = DOC_LINKS.configureLlmProvider;

function formatUpdatedAt(iso: string | undefined): string {
    if (!iso) return "—";
    const when = new Date(iso);
    if (Number.isNaN(when.getTime())) return "—";
    return when.toLocaleDateString("en-US", { year: "numeric", month: "short", day: "numeric" });
}

const EMPTY_STATE_TITLE = "Match AI Models to Your Team's Workflows";
const EMPTY_STATE_DESCRIPTION =
    "Different models specialize in different use cases. Organize your models by use case and assign to agents based on what the agent needs. Start with our suggested names or create your own. The 'General' and 'Planning' models are defaults required by the system but you can customize everything else to fit your workflow.";

export const ModelsView: React.FC = () => {
    const navigate = useNavigate();
    const location = useLocation();
    const { addNotification } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const decisionModelsBadge = useCapabilityBadge(DECISION_MODELS_CAPABILITY);
    const { isFreeModel, freeModelLabel } = useFreeModel();
    const canCreateModelConfig = userHasCapability(CAPABILITIES.createModelConfig);
    const { data: modelConfigs = [], isLoading: modelConfigsLoading, error: modelConfigsErrorObj } = useModelConfigs();
    const deleteModel = useDeleteModel();
    const [currentPage, setCurrentPage] = useState<number>(1);
    const [modelToDelete, setModelToDelete] = useState<ModelConfig | null>(null);
    const [highlightedModelId, setHighlightedModelId] = useState<string | null>(null);
    const highlightedRowRef = useRef<HTMLTableRowElement>(null);
    const { sortedData: sortedModels, sortKey, sortDir, handleSort } = useSortableTable(modelConfigs, ["alias", "modelName", "provider"]);

    useEffect(() => {
        setCurrentPage(1);
    }, [sortKey, sortDir]);

    const locationState = location.state as { highlightModelId?: string } | null;
    useEffect(() => {
        if (locationState?.highlightModelId) {
            setHighlightedModelId(locationState.highlightModelId);
            addNotification("Model Added", "success");

            navigate(location.pathname + location.search, { replace: true });

            const highlightTimer = setTimeout(() => {
                setHighlightedModelId(null);
            }, 4000);

            return () => {
                clearTimeout(highlightTimer);
            };
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [locationState?.highlightModelId]);

    useEffect(() => {
        if (highlightedRowRef.current) {
            highlightedRowRef.current.scrollIntoView({ behavior: "smooth", block: "center" });
        }
    }, [highlightedModelId]);

    const modelConfigsError = modelConfigsErrorObj ? (modelConfigsErrorObj instanceof Error ? modelConfigsErrorObj.message : "Failed to load models") : null;

    const hasModels = modelConfigs && modelConfigs.length > 0;

    const itemsPerPage = 20;
    const totalPages = Math.ceil(sortedModels.length / itemsPerPage);
    const effectiveCurrentPage = Math.min(currentPage, Math.max(totalPages, 1));
    const startIndex = (effectiveCurrentPage - 1) * itemsPerPage;
    const currentModels = sortedModels.slice(startIndex, startIndex + itemsPerPage);

    const handleSelectModel = (model: ModelConfig) => {
        navigate(`/models/${model.id}`);
    };

    const handleCreateModel = () => {
        navigate("/models/new/edit");
    };

    const handleEditModel = (model: ModelConfig) => {
        navigate(`/models/${model.id}/edit`);
    };

    const getRowMenuActions = (model: ModelConfig): MenuAction[] => {
        const actions: MenuAction[] = [
            {
                id: "open-details",
                label: "Open Details",
                onClick: () => handleSelectModel(model),
            },
        ];
        if (isFreeModel(model)) return actions;
        if (userHasCapability(CAPABILITIES.updateModelConfig)) {
            actions.push({
                id: "edit",
                label: "Edit",
                onClick: () => handleEditModel(model),
            });
        }
        if (userHasCapability(CAPABILITIES.deleteModelConfig)) {
            actions.push({
                id: "delete",
                label: "Delete",
                onClick: () => setModelToDelete(model),
                divider: true,
            });
        }
        return actions;
    };

    if (modelConfigsLoading) {
        return <EmptyState variant="loading" title="Loading Models..." />;
    }

    if (modelConfigsError) {
        return <EmptyState variant="error" title={`Error loading models: ${modelConfigsError}`} />;
    }

    return (
        <div className="flex h-full w-full flex-col overflow-hidden">
            <div className="flex h-full flex-col overflow-hidden">
                {hasModels && <OnboardingBanner storageKey={MODELS_STORAGE_KEY} header={MODELS_HEADER} description={MODELS_DESCRIPTION} learnMoreText={MODELS_LEARN_MORE_TEXT} learnMoreUrl={MODELS_URL} className="mx-6 mt-6" />}
                <FreeCreditsBanner className="mx-6 mt-6" />

                <div className="flex min-h-0 flex-1 flex-col overflow-hidden px-6 py-6">
                    {currentModels.length > 0 && (
                        <p className="mb-2 text-right text-xs text-(--secondary-text-wMain)">
                            Prices in {PRICE_CURRENCY} / {tokenQuantityLabel()} tokens
                        </p>
                    )}
                    {currentModels.length > 0 ? (
                        <div className="min-h-0 flex-1 overflow-y-auto rounded-xs border">
                            <Table containerClassName="overflow-x-visible">
                                <TableHeader sticky>
                                    <TableRow>
                                        <SortableTableHead column="alias" currentSortKey={sortKey} sortDir={sortDir} onSort={handleSort} className="pl-4">
                                            Name
                                        </SortableTableHead>
                                        <SortableTableHead column="modelName" currentSortKey={sortKey} sortDir={sortDir} onSort={handleSort}>
                                            Model
                                        </SortableTableHead>
                                        <SortableTableHead column="provider" currentSortKey={sortKey} sortDir={sortDir} onSort={handleSort}>
                                            Model Provider
                                        </SortableTableHead>
                                        {REQUIRED_RATES.map(rate => (
                                            <TableHead key={rate.key} className="w-28 text-right font-semibold">
                                                {rate.label}
                                            </TableHead>
                                        ))}
                                        <TableHead className="w-40 pl-8 font-semibold">Last Updated</TableHead>
                                        <TableHead className="w-12"></TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody borderLastRow>
                                    {currentModels.map(model => (
                                        <TableRow
                                            key={model.id}
                                            ref={highlightedModelId === model.id ? highlightedRowRef : null}
                                            className={cn("transition-colors duration-500", highlightedModelId === model.id ? "bg-(--success-w10)" : "hover:bg-(--primary-w10)")}
                                            data-testid={`modelRow-${model.alias}`}
                                        >
                                            <TableCell className="flex items-center gap-2 pl-4 font-semibold">
                                                <ModelConfigIcon model={model} size="sm" />
                                                <Button title={model.alias} variant="link" className="p-0" onClick={() => handleSelectModel(model)}>
                                                    {getDisplayAliasName(model.alias, model.createdBy)}
                                                </Button>
                                                {isDecisionModel(model) && (
                                                    <Badge tooltip="A decision model, called only by workflow decision nodes and decision evaluators. Agents cannot use it." tooltipSide="right" data-testid="decisionModelBadge">
                                                        Decision
                                                    </Badge>
                                                )}
                                                {isDecisionModel(model) && decisionModelsBadge && <LifecycleBadge>{decisionModelsBadge}</LifecycleBadge>}
                                                {DEFAULT_MODEL_ALIASES.includes(model.alias) && (
                                                    <>
                                                        <Badge
                                                            tooltip={
                                                                model.alias === "general"
                                                                    ? "Used by all built-in AI features. This cannot be deleted but can be modified."
                                                                    : "Used for the Orchestrator Agent. This cannot be deleted but can be modified."
                                                            }
                                                            tooltipSide="right"
                                                        >
                                                            Default
                                                        </Badge>
                                                        {!isModelConfigured(model) && (
                                                            <Tooltip>
                                                                <TooltipTrigger asChild>
                                                                    <AlertTriangle className="h-4 w-4 shrink-0 cursor-default text-(--warning-wMain)" />
                                                                </TooltipTrigger>
                                                                <TooltipContent side="right">
                                                                    <p>This model needs connection details configured before it can be used.</p>
                                                                </TooltipContent>
                                                            </Tooltip>
                                                        )}
                                                    </>
                                                )}
                                                {(() => {
                                                    const issues = modelConfigurationIssues(model);
                                                    if (issues.length === 0) return null;
                                                    return (
                                                        <Tooltip>
                                                            <TooltipTrigger asChild>
                                                                <AlertTriangle data-testid={`modelConfigIssue-${model.alias}`} className="h-4 w-4 shrink-0 cursor-default text-(--warning-wMain)" />
                                                            </TooltipTrigger>
                                                            <TooltipContent side="right">
                                                                {issues.map(issue => (
                                                                    <p key={issue}>{issue}</p>
                                                                ))}
                                                            </TooltipContent>
                                                        </Tooltip>
                                                    );
                                                })()}
                                            </TableCell>
                                            <TableCell>
                                                {(() => {
                                                    const label = freeModelLabel(model);
                                                    return label ? <FreeModelName label={label} /> : getDisplayModelName(model) || "Not configured";
                                                })()}
                                            </TableCell>
                                            <TableCell>{freeModelLabel(model)?.providerName ?? getProviderDisplayName(model.provider) ?? "Not configured"}</TableCell>
                                            <ModelPricingCells pricing={model.pricing} offersCacheRates={!isDecisionModel(model)} />
                                            <TableCell className="pl-8">{formatUpdatedAt(model.updatedAt)}</TableCell>
                                            <TableCell className="pr-4 text-right">
                                                <Popover>
                                                    <PopoverTrigger asChild>
                                                        <Button variant="ghost" size="sm" title="Actions">
                                                            <Ellipsis className="h-5 w-5" />
                                                        </Button>
                                                    </PopoverTrigger>
                                                    <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0}>
                                                        <Menu actions={getRowMenuActions(model)} />
                                                    </PopoverContent>
                                                </Popover>
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                    ) : (
                        <OnboardingView
                            title={EMPTY_STATE_TITLE}
                            description={EMPTY_STATE_DESCRIPTION}
                            learnMoreText={MODELS_LEARN_MORE_TEXT}
                            learnMoreHref={MODELS_URL}
                            actionButton={canCreateModelConfig ? { text: "Add Model", onClick: handleCreateModel } : undefined}
                        />
                    )}
                </div>

                <PaginationControls totalPages={totalPages} currentPage={effectiveCurrentPage} onPageChange={setCurrentPage} />
            </div>

            {modelToDelete && (
                <ModelDeleteDialog
                    open={!!modelToDelete}
                    onOpenChange={open => {
                        if (!open) setModelToDelete(null);
                    }}
                    onConfirm={async () => {
                        await deleteModel.mutateAsync(modelToDelete.id);
                        setModelToDelete(null);
                    }}
                    isLoading={deleteModel.isPending}
                    modelId={modelToDelete.id}
                    modelAlias={modelToDelete.alias}
                />
            )}
        </div>
    );
};
