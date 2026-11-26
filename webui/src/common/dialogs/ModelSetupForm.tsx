// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { FormProvider, useForm } from "react-hook-form";

import { useCreateModel, useModelConfigs, useTestModelConnection, useUpdateModel } from "@/api/models";
import type { ModelConfig } from "@/api/models/types";

import { MessageBanner } from "@/common/banners/MessageBanner";

import { getErrorMessage } from "@/utils/api";

import { isModelConfigured } from "@/features/models/common";
import { ModelConnectionFields, type ModelNameFieldSpec, useModelConnectionForm } from "@/features/models/ModelConnectionFields";
import { trackModelFormErrors } from "@/features/models/modelFormAnalytics";
import { buildModelPayload, buildTestPayload, CHAT_PROVIDERS, type ModelFormData } from "@/features/models/modelProviderUtils";

import { Button, DialogFooter, Spinner } from "@/ui";

const DIALOG_FORM_ID = "default-model-setup-form";

const MODEL_NAME_FIELDS: ModelNameFieldSpec[] = [{ name: "modelName", label: "Default Model" }];

function buildDefaultModelPayload(alias: "general" | "planning", data: ModelFormData) {
    return buildModelPayload({
        ...data,
        alias,
        description: "",
    });
}

interface ModelSetupFormProps {
    onSuccess?: () => void;
    onClose: () => void;
    hideCancel?: boolean;
    submitLabel?: string;
    replaces?: (model: ModelConfig) => boolean;
}

export function ModelSetupForm({ onSuccess, onClose, hideCancel = false, submitLabel = "Set Up Default Models", replaces }: Readonly<ModelSetupFormProps>) {
    const [submitError, setSubmitError] = useState<string | null>(null);
    const { data: existingModels, isLoading: isLoadingExistingModels, isError: existingModelsError } = useModelConfigs();
    const createModel = useCreateModel();
    const updateModel = useUpdateModel();
    const testConnection = useTestModelConnection();

    const methods = useForm<ModelFormData>({
        mode: "onSubmit",
        reValidateMode: "onChange",
        defaultValues: {
            provider: "",
            authType: "apikey",
            customParams: [],
            cache_strategy: "5m",
        },
    });
    const connection = useModelConnectionForm({ methods, isNew: true });

    const upsertDefault = async (alias: "general" | "planning", data: ModelFormData): Promise<boolean> => {
        const existing = existingModels?.find(model => model.alias?.toLowerCase() === alias);
        if (existing && isModelConfigured(existing) && !replaces?.(existing)) return false;
        const payload = buildDefaultModelPayload(alias, data);
        if (existing) {
            await updateModel.mutateAsync({ id: existing.id, data: payload });
        } else {
            await createModel.mutateAsync(payload);
        }
        return true;
    };

    const submit = async (data: ModelFormData) => {
        setSubmitError(null);

        try {
            const result = await testConnection.mutateAsync(buildTestPayload(buildModelPayload({ ...data, alias: "general", description: "" })));
            if (!result.success) {
                setSubmitError(result.message || "Connection test failed. Verify your connection details and try again.");
                return;
            }
        } catch (error) {
            setSubmitError(getErrorMessage(error, "Connection test failed. Verify your connection details and try again."));
            return;
        }

        let generalSaved: boolean;
        try {
            generalSaved = await upsertDefault("general", data);
        } catch (error) {
            setSubmitError(getErrorMessage(error, "Failed to save the default general model."));
            return;
        }

        try {
            await upsertDefault("planning", data);
        } catch (error) {
            const recovery = generalSaved ? " The general model was saved — finish configuring the planning model from the Models page." : "";
            setSubmitError(getErrorMessage(error, `Failed to save the default planning model.${recovery}`));
            return;
        }

        onSuccess?.();
        onClose();
    };

    const isSubmitting = methods.formState.isSubmitting;
    const listError = existingModelsError ? "Couldn't load existing models. Refresh and try again." : null;

    return (
        <FormProvider {...methods}>
            {(submitError || listError) && (
                <div className="flex-shrink-0 pb-2">
                    <MessageBanner variant="error" message={submitError ?? listError ?? ""} />
                </div>
            )}
            <form id={DIALOG_FORM_ID} className="min-h-0 flex-1 space-y-4 overflow-y-auto" onSubmit={methods.handleSubmit(submit, trackModelFormErrors)}>
                <ModelConnectionFields connection={connection} availableProviders={CHAT_PROVIDERS} modelNameFields={MODEL_NAME_FIELDS} />
            </form>
            <DialogFooter className="flex-shrink-0">
                {!hideCancel && (
                    <Button variant="ghost" type="button" onClick={onClose} disabled={isSubmitting}>
                        Cancel
                    </Button>
                )}
                <Button type="submit" form={DIALOG_FORM_ID} disabled={isSubmitting || isLoadingExistingModels || existingModelsError}>
                    {isSubmitting ? <Spinner /> : null}
                    {submitLabel}
                </Button>
            </DialogFooter>
        </FormProvider>
    );
}

export default ModelSetupForm;
