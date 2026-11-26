// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useCallback, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { useAgentCards } from "@/api/agent-cards";
import { useCreateModel, useModelById, useSupportedModels, useTestModelConnection, useUpdateModel } from "@/api/models";
import type { ModelConfig, ModelType } from "@/api/models/types";

import { getErrorMessage } from "@/utils/api";

import { FreeModelManagedNotice, useFreeModel } from "@/features/activation";

import { ConfirmationDialog, EmptyState, Footer, Header, MessageBanner, PageContentWrapper, PageLayout } from "@/common";
import { useIsDecisionEvaluatorEnabled, useIsDecisionNodeEnabled } from "@/hooks";
import { Button } from "@/ui";

import { ModelEdit } from "./ModelEdit";
import type { ModelFormData } from "./modelProviderUtils";
import { buildModelPayload, buildTestPayload, formModelType, providersForModelType } from "./modelProviderUtils";

interface EditModelLoadStateProps {
    modelLoading: boolean;
    isFetchingModels: boolean;
    fetchError: string | null;
    modelFound: boolean;
    onGoToModels: () => void;
}

function editModelLoadState({ modelLoading, isFetchingModels, fetchError, modelFound, onGoToModels }: EditModelLoadStateProps): ReactNode {
    if (modelLoading) {
        return <EmptyState variant="loading" title="Loading Model..." />;
    }
    if (isFetchingModels) {
        return <EmptyState variant="loading" title="Loading Models..." />;
    }
    const goToModels = [{ text: "Go To Models", variant: "default" as const, onClick: onGoToModels }];
    if (fetchError) {
        return <EmptyState variant="error" title={`Error loading model: ${fetchError}`} buttons={goToModels} />;
    }
    if (!modelFound) {
        return <EmptyState variant="error" title="Model Not Found" buttons={goToModels} />;
    }
    return null;
}

function storedCredentialProbe(model: ModelConfig | undefined, lockedFreeModel: boolean) {
    return model?.provider && !lockedFreeModel ? { provider: model.provider, modelId: model.id } : null;
}

export const ModelEditPage = () => {
    const navigate = useNavigate();
    const { id: modelId } = useParams<{ id?: string }>();
    const isNew = !modelId;

    const [errorMessage, setErrorMessage] = useState<string | null>(null);
    const [showSaveFailedDialog, setShowSaveFailedDialog] = useState(false);
    const [saveFailMessage, setSaveFailMessage] = useState("");
    const pendingSaveRef = useRef<{ payload: ReturnType<typeof buildModelPayload>; modelType: ModelType } | null>(null);

    const { data: modelToEdit, isLoading: modelLoading, error: fetchErrorObj } = useModelById(isNew ? undefined : modelId);
    const fetchError = fetchErrorObj ? getErrorMessage(fetchErrorObj, "Failed to load model.") : null;
    const { isFreeModel } = useFreeModel();
    const lockedFreeModel = !isNew && modelToEdit !== undefined && isFreeModel(modelToEdit);

    const decisionNodesEnabled = useIsDecisionNodeEnabled();
    const decisionEvaluatorsEnabled = useIsDecisionEvaluatorEnabled();
    const decisionModelsEnabled = decisionNodesEnabled || decisionEvaluatorsEnabled;
    const availableProviders = providersForModelType(isNew ? "chat" : (modelToEdit?.modelType ?? "chat"));

    const { refetch: agentsRefetch } = useAgentCards();
    const createMutation = useCreateModel();
    const updateMutation = useUpdateModel();
    const testConnectionMutation = useTestModelConnection();

    const isSaving = createMutation.isPending || updateMutation.isPending || testConnectionMutation.isPending;

    const { data: initialModels = [], isLoading: isFetchingModels } = useSupportedModels(storedCredentialProbe(isNew ? undefined : modelToEdit, lockedFreeModel));
    const modelsByProvider = modelToEdit?.provider ? { [modelToEdit.provider]: initialModels } : {};

    const performSave = useCallback(
        async (payload: ReturnType<typeof buildModelPayload>, modelType: ModelType) => {
            try {
                if (isNew) {
                    const result = await createMutation.mutateAsync(modelType === "decision" ? { ...payload, modelType } : payload);
                    agentsRefetch();
                    navigate("/models", { state: { highlightModelId: result.id } });
                } else if (modelToEdit) {
                    await updateMutation.mutateAsync({ id: modelToEdit.id, data: payload });
                    agentsRefetch();
                    navigate("/models");
                }
            } catch (error) {
                setErrorMessage(getErrorMessage(error, "An unknown error occurred while saving the model."));
            }
        },
        [isNew, modelToEdit, navigate, createMutation, updateMutation]
    );

    const handleSaveAnyway = useCallback(async () => {
        if (!pendingSaveRef.current) return;
        await performSave(pendingSaveRef.current.payload, pendingSaveRef.current.modelType);
    }, [performSave]);

    const handleSave = async (data: ModelFormData, dirtyFields?: Partial<Record<string, boolean>>) => {
        setErrorMessage(null);

        const payload = buildModelPayload(data, dirtyFields);
        const modelType = formModelType(data);
        pendingSaveRef.current = { payload, modelType };

        let testPassed = false;
        try {
            const result = await testConnectionMutation.mutateAsync(buildTestPayload(payload, !isNew ? modelToEdit?.id : undefined, modelType));
            testPassed = result.success;
            if (!testPassed) setSaveFailMessage(result.message);
        } catch (error) {
            setSaveFailMessage(getErrorMessage(error, "Connection test failed."));
        }

        if (!testPassed) {
            setShowSaveFailedDialog(true);
            return;
        }

        await performSave(payload, modelType);
    };

    const handleCancel = () => {
        navigate("/models");
    };

    const editTitle = modelToEdit ? `Edit ${modelToEdit.alias}` : "Model";
    const title = isNew ? "Add Model" : editTitle;

    const editLoadState = isNew ? null : editModelLoadState({ modelLoading, isFetchingModels, fetchError, modelFound: !!modelToEdit, onGoToModels: () => navigate("/models") });
    if (editLoadState) {
        return editLoadState;
    }
    if (lockedFreeModel) {
        return (
            <PageLayout className="min-[896px]:min-w-4xl">
                <Header title={title} breadcrumbs={[{ label: "Models", onClick: () => navigate("/models") }, { label: title }]} />
                <PageContentWrapper>
                    <FreeModelManagedNotice />
                </PageContentWrapper>
                <Footer>
                    <Button variant="outline" title="Back to Models" onClick={handleCancel}>
                        Back to Models
                    </Button>
                </Footer>
            </PageLayout>
        );
    }

    return (
        <PageLayout className="min-[896px]:min-w-4xl">
            <Header title={title} breadcrumbs={[{ label: "Models", onClick: () => navigate("/models") }, { label: title }]} />

            <PageContentWrapper>
                {errorMessage && <MessageBanner variant="error" message={errorMessage} dismissible onDismiss={() => setErrorMessage(null)} />}
                <ModelEdit isNew={isNew} modelToEdit={modelToEdit} onSave={handleSave} modelsByProvider={modelsByProvider} availableProviders={availableProviders} decisionModelsEnabled={decisionModelsEnabled} />
            </PageContentWrapper>

            <ConfirmationDialog
                open={showSaveFailedDialog}
                onOpenChange={setShowSaveFailedDialog}
                title="Connection Test Failed"
                content={
                    <div className="space-y-3">
                        <MessageBanner variant="error" message={saveFailMessage} />
                        <p className="text-sm text-(--secondary-text-wMain)">Would you like to save anyway or go back to fix the issue?</p>
                    </div>
                }
                actionLabels={{ cancel: "Go Back", confirm: "Save Anyway" }}
                onConfirm={handleSaveAnyway}
                isLoading={isSaving}
            />

            <Footer>
                <Button variant="ghost" title="Cancel" onClick={handleCancel} disabled={isSaving}>
                    Cancel
                </Button>
                <Button type="submit" form="model-form" disabled={isSaving} title={isNew ? "Add Model" : "Save Model"}>
                    {isSaving ? "Saving..." : isNew ? "Add" : "Save"}
                </Button>
            </Footer>
        </PageLayout>
    );
};
