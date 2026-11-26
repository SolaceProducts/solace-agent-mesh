// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";
import { Ellipsis, Pencil, Trash2 } from "lucide-react";
import { useNavigate, useParams } from "react-router-dom";

import { useCapabilityBadge } from "@/api/capabilities";
import { isDecisionModel, useDeleteModel, useModelConfigs } from "@/api/models";
import type { ModelConfig, ModelPricing } from "@/api/models/types";

import { useUserContext } from "@/providers/contexts";

import { FreeCreditsBanner, FreeModelName, ModelConfigIcon, useFreeModel } from "@/features/activation";

import { EmptyState, Footer, Header, Metadata, PageContentWrapper, PageLabel, PageLabelWithValue, PageLayout, PageSection, PageValue } from "@/common";
import { Button, LifecycleBadge, Menu, type MenuAction, Popover, PopoverContent, PopoverTrigger } from "@/ui";

import { AUTH_TYPE_LABELS, getDisplayModelName, getProviderDisplayName } from "./common";
import { InheritedRate } from "./InheritedRate";
import { ModelDeleteDialog } from "./ModelDeleteDialog";
import { type ALL_RATES, CACHE_RATES, INPUT_RATE, OUTPUT_RATE, PRICE_CURRENCY, rateDisplay, tokenQuantityLabel } from "./modelPricing";
import { customModelPrefix, DECISION_MODELS_CAPABILITY, MODEL_TYPE_LABELS, modelNameFieldLabel } from "./modelProviderUtils";

function RateValue({ label, display }: Readonly<{ label: string; display: ReturnType<typeof rateDisplay> }>) {
    if (!display) return <span className="text-(--secondary-text-wMain)">Not set</span>;
    if (display.resolved) return <InheritedRate label={label} amount={display.amount} />;
    return <>{display.amount}</>;
}

function RateItem({ rate, pricing }: Readonly<{ rate: (typeof ALL_RATES)[number]; pricing: ModelPricing | null | undefined }>) {
    return (
        <PageLabelWithValue>
            <PageLabel>{rate.label} Price</PageLabel>
            <PageValue>
                <RateValue label={rate.label} display={rateDisplay(pricing, rate.key)} />
            </PageValue>
        </PageLabelWithValue>
    );
}

const ModelPrefixDetail = ({ model }: { model: ModelConfig }) => {
    const prefix = model.modelName ? customModelPrefix(model.provider, model.modelName) : undefined;
    if (!prefix) return null;
    return (
        <PageLabelWithValue>
            <PageLabel>Model Prefix</PageLabel>
            <PageValue>{prefix}/</PageValue>
        </PageLabelWithValue>
    );
};

export const ModelDetailsPage = () => {
    const navigate = useNavigate();
    const { id: modelId } = useParams<{ id: string }>();
    const { data: modelConfigs = [], isLoading: modelConfigsLoading } = useModelConfigs();
    const decisionModelsBadge = useCapabilityBadge(DECISION_MODELS_CAPABILITY);
    const deleteModel = useDeleteModel();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
    const { freeModelLabel } = useFreeModel();

    const modelToView = useMemo(() => {
        if (!modelId) return null;
        return modelConfigs.find(m => m.id === modelId) || null;
    }, [modelId, modelConfigs]);

    const handleBack = useCallback(() => {
        navigate(`/models`);
    }, [navigate]);

    const handleEdit = useCallback(() => {
        if (modelToView?.id) {
            navigate(`/models/${modelToView.id}/edit`);
        }
    }, [modelToView?.id, navigate]);

    const menuActions = useMemo(() => {
        const actions: MenuAction[] = [];
        if (userHasCapability(CAPABILITIES.deleteModelConfig)) {
            actions.push({
                id: "delete",
                label: "Delete",
                onClick: () => setDeleteDialogOpen(true),
                icon: <Trash2 />,
                iconPosition: "left",
            });
        }
        return actions;
    }, [userHasCapability, CAPABILITIES]);

    const headerButtons = useMemo(() => {
        const buttons = [];
        if (userHasCapability(CAPABILITIES.updateModelConfig)) {
            buttons.push(
                <Button key="edit" variant="ghost" onClick={handleEdit} title="Edit Model">
                    <Pencil />
                    Edit
                </Button>
            );
        }
        if (menuActions.length > 0) {
            buttons.push(
                <Popover key="more-menu">
                    <PopoverTrigger asChild>
                        <Button variant="ghost" title="More" data-testid="modelMoreButton">
                            <Ellipsis className="h-5 w-5" />
                        </Button>
                    </PopoverTrigger>
                    <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0}>
                        <Menu actions={menuActions} />
                    </PopoverContent>
                </Popover>
            );
        }
        return buttons;
    }, [handleEdit, menuActions, userHasCapability, CAPABILITIES]);

    const title = modelToView ? modelToView.alias : "N/A";
    const freeModel = modelToView ? freeModelLabel(modelToView) : undefined;

    let detailsBody;
    if (modelConfigsLoading) {
        detailsBody = <EmptyState variant="loading" title="Loading Models..." />;
    } else if (!modelToView) {
        detailsBody = <EmptyState variant="error" title="Model Not Found" buttons={[{ text: "Go To Models", variant: "default", onClick: () => navigate("/models") }]} />;
    } else {
        detailsBody = (
            <PageContentWrapper>
                {freeModel && <FreeCreditsBanner />}
                <PageSection className="gap-4">
                    <div className="font-semibold">Model Details</div>
                    {modelToView.description && (
                        <PageLabelWithValue>
                            <PageLabel>Description</PageLabel>
                            <PageValue className="whitespace-pre-wrap">{modelToView.description}</PageValue>
                        </PageLabelWithValue>
                    )}

                    <PageLabelWithValue>
                        <PageLabel>Model Provider</PageLabel>
                        <PageValue className="flex items-center gap-2">
                            <ModelConfigIcon model={modelToView} size="xs" />
                            {freeModel ? freeModel.providerName : (getProviderDisplayName(modelToView.provider) ?? <span className="text-(--secondary-text-wMain)">Not configured</span>)}
                        </PageValue>
                    </PageLabelWithValue>

                    <PageLabelWithValue>
                        <PageLabel>Model Type</PageLabel>
                        <PageValue>
                            {isDecisionModel(modelToView) ? (
                                <span className="inline-flex items-center gap-2">
                                    {MODEL_TYPE_LABELS.decision}
                                    {decisionModelsBadge && <LifecycleBadge>{decisionModelsBadge}</LifecycleBadge>}
                                </span>
                            ) : (
                                MODEL_TYPE_LABELS.chat
                            )}
                        </PageValue>
                    </PageLabelWithValue>
                </PageSection>

                <PageSection className="gap-4">
                    <div className="pt-6 font-semibold">Model Connection Details</div>
                    {!freeModel && <ModelPrefixDetail model={modelToView} />}

                    <PageLabelWithValue>
                        <PageLabel>{modelNameFieldLabel(modelToView.provider, "Model Name")}</PageLabel>
                        <PageValue>{freeModel ? <FreeModelName label={freeModel} /> : getDisplayModelName(modelToView) || <span className="text-(--secondary-text-wMain)">Not configured</span>}</PageValue>
                    </PageLabelWithValue>

                    {modelToView.apiBase && (
                        <PageLabelWithValue>
                            <PageLabel>URL</PageLabel>
                            <PageValue>{modelToView.apiBase}</PageValue>
                        </PageLabelWithValue>
                    )}

                    <PageLabelWithValue>
                        <PageLabel>Authentication</PageLabel>
                        <PageValue>{AUTH_TYPE_LABELS[(modelToView.authType ?? "none") as keyof typeof AUTH_TYPE_LABELS] ?? modelToView.authType}</PageValue>
                    </PageLabelWithValue>

                    {Object.keys(modelToView.modelParams).length > 0 && (
                        <PageLabelWithValue>
                            <PageLabel>Model Parameters</PageLabel>
                            <PageValue className="space-y-1">
                                {Object.entries(modelToView.modelParams).map(([key, value]) => (
                                    <div key={key}>
                                        <span className="font-medium">{key}:</span> {String(value)}
                                    </div>
                                ))}
                            </PageValue>
                        </PageLabelWithValue>
                    )}
                </PageSection>

                <div data-testid="model-pricing-details">
                    <PageSection className="gap-4">
                        <div className="flex flex-col gap-1 pt-6">
                            <div className="font-semibold">Pricing</div>
                            <p className="text-sm text-(--secondary-text-wMain)">
                                What this model costs, in {PRICE_CURRENCY} / {tokenQuantityLabel()} tokens.
                            </p>
                        </div>

                        <RateItem rate={INPUT_RATE} pricing={modelToView.pricing} />
                        {!isDecisionModel(modelToView) && (
                            <div className="flex flex-col gap-4 sm:flex-row sm:gap-8">
                                {CACHE_RATES.map(rate => (
                                    <RateItem key={rate.key} rate={rate} pricing={modelToView.pricing} />
                                ))}
                            </div>
                        )}
                        <RateItem rate={OUTPUT_RATE} pricing={modelToView.pricing} />
                    </PageSection>
                </div>

                <Metadata
                    metadata={{
                        id: modelToView.id,
                        createdBy: modelToView.createdBy,
                        createdAt: modelToView.createdAt,
                        updatedBy: modelToView.updatedBy,
                        updatedAt: modelToView.updatedAt,
                    }}
                />
            </PageContentWrapper>
        );
    }

    return (
        <PageLayout className="min-[896px]:min-w-4xl">
            <Header title={title} breadcrumbs={[{ label: "Models", onClick: () => navigate("/models") }, { label: title }]} buttons={freeModel ? [] : headerButtons} />

            {detailsBody}

            {modelToView && (
                <ModelDeleteDialog
                    open={deleteDialogOpen}
                    onOpenChange={setDeleteDialogOpen}
                    onConfirm={async () => {
                        await deleteModel.mutateAsync(modelToView.id);
                        navigate("/models");
                    }}
                    isLoading={deleteModel.isPending}
                    modelId={modelToView.id}
                    modelAlias={modelToView.alias}
                />
            )}

            <Footer>
                <Button variant="outline" title="Close" onClick={handleBack}>
                    Close
                </Button>
            </Footer>
        </PageLayout>
    );
};
