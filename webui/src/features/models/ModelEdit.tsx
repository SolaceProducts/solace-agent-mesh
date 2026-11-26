// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Plus } from "lucide-react";
import { Controller, FormProvider, useForm, type UseFormSetValue } from "react-hook-form";

import { type ModelConfig, modelsApi, REDACTED_SENTINEL } from "@/api/models";
import type { ModelType } from "@/api/models/types";

import { KeyValuePairList } from "@/common/form";
import { FormFieldLayoutItem, PageLabel, PageSection } from "@/common/layout";

import { HelpPopover } from "@/common";
import { useDebounce } from "@/hooks";
import { AccordionCard, Button, Input, Textarea } from "@/ui";

import { DEFAULT_MODEL_ALIASES } from "./common";
import { ModelConnectionFields, ModelFormField, useModelConnectionForm } from "./ModelConnectionFields";
import { trackModelFormErrors } from "./modelFormAnalytics";
import { EMPTY_PRICING_FORM, pricingFormValues } from "./modelPricing";
import { ModelPricingFields } from "./ModelPricingFields";
import {
    AUTH_CONFIG_TO_FORM_FIELD_MAP,
    COMMON_MODEL_PARAMS,
    type CustomParamValue,
    defaultProviderForType,
    formatCustomParamValue,
    getProviderConfig,
    type ModelFormData,
    modelNameFormValues,
    type ModelProvider,
    type ProviderField,
    providersForModelType,
} from "./modelProviderUtils";
import { ModelTypeField } from "./ModelTypeField";
import { TestConnectionSection } from "./TestConnectionSection";

const DEFAULT_CACHE_STRATEGY = "5m";

interface ModelEditProps {
    isNew: boolean;
    modelToEdit?: ModelConfig | null;
    onSave: (data: ModelFormData, dirtyFields: Partial<Record<string, boolean>>) => Promise<void>;
    onDirtyStateChange?: (isDirty: boolean) => void;
    modelsByProvider?: Record<string, Array<{ id: string; label: string }>>;
    availableProviders?: ModelProvider[];
    onProviderChange?: (provider: string) => Promise<void>;
    decisionModelsEnabled?: boolean;
}

function toFormString(value: unknown): string {
    if (typeof value === "string") return value;
    if (typeof value === "number" || typeof value === "boolean") return String(value);
    return "";
}

function hydrateStoredCredentials(authConfig: Record<string, unknown>, setValue: UseFormSetValue<ModelFormData>, setStoredCredentialFields: (fields: Set<string>) => void) {
    const stored = new Set<string>();
    Object.entries(AUTH_CONFIG_TO_FORM_FIELD_MAP).forEach(([configKey, fieldName]) => {
        const value = authConfig[configKey];
        if (value && value !== REDACTED_SENTINEL) {
            setValue(fieldName, toFormString(value));
        } else {
            stored.add(fieldName);
        }
    });
    setStoredCredentialFields(stored);
}

function hydrateModelParams(modelParams: Record<string, unknown>, provider: string | null | undefined, setValue: UseFormSetValue<ModelFormData>) {
    const knownParamNames = new Set<string>();

    getProviderConfig(provider).fields.forEach((field: ProviderField) => {
        knownParamNames.add(field.name);
        if (field.name in modelParams) {
            setValue(field.name, modelParams[field.name]);
        }
    });

    COMMON_MODEL_PARAMS.forEach((field: ProviderField) => {
        knownParamNames.add(field.name);
        if (field.name in modelParams) {
            setValue(field.name, toFormString(modelParams[field.name]));
        }
    });

    knownParamNames.add("extra_headers");
    const extraHeaders = modelParams.extra_headers;
    if (extraHeaders && typeof extraHeaders === "object" && !Array.isArray(extraHeaders)) {
        const headersArray = Object.entries(extraHeaders as Record<string, unknown>).map(([key, value]) => ({ key, value: toFormString(value) }));
        if (headersArray.length > 0) {
            setValue("customHeaders", headersArray);
        }
    }

    const customParamsArray = Object.entries(modelParams)
        .filter(([key]) => !knownParamNames.has(key))
        .map(([key, value]) => ({ key, value: formatCustomParamValue(value as CustomParamValue) }));
    if (customParamsArray.length > 0) {
        setValue("customParams", customParamsArray);
    }
}

export const ModelEdit = ({ isNew, modelToEdit, onSave, onDirtyStateChange, modelsByProvider = {}, availableProviders = [], onProviderChange, decisionModelsEnabled = false }: ModelEditProps) => {
    const methods = useForm<ModelFormData>({
        mode: "onSubmit",
        reValidateMode: "onChange",
        defaultValues: {
            customParams: [],
            customHeaders: [],
            cache_strategy: DEFAULT_CACHE_STRATEGY,
            pricing: EMPTY_PRICING_FORM,
        },
    });

    const [supportedParams, setSupportedParams] = useState<string[] | null>(null);
    const [paramsError, setParamsError] = useState(false);

    const connection = useModelConnectionForm({ methods, isNew, modelToEdit, modelsByProvider, onProviderChange, onProviderReset: () => setSupportedParams(null) });
    const { providerConfig, storedCredentialFields, setStoredCredentialFields, hasInitializedFromModelRef, selectedProvider, isAuthCredentialsConfigured } = connection;
    const isDecisionProvider = providerConfig?.modelType === "decision";
    const {
        register,
        control,
        formState: { errors, isDirty, dirtyFields },
        handleSubmit,
        watch,
        setValue,
        getValues,
    } = methods;

    const [newModelType, setNewModelType] = useState<ModelType>("chat");
    const editedModelType: ModelType = modelToEdit?.modelType === "decision" ? "decision" : "chat";
    const offersModelTypeChoice = isNew && decisionModelsEnabled;
    const providers = offersModelTypeChoice ? providersForModelType(newModelType) : availableProviders;

    const handleModelTypeChange = useCallback(
        (next: ModelType) => {
            setNewModelType(next);
            setValue("modelType", next);
            setValue("customParams", []);
            setValue("customHeaders", []);
            setValue("pricing", EMPTY_PRICING_FORM);
            setValue("maxInputTokens", "");
            setValue("max_tokens", "");
            setValue("cache_strategy", DEFAULT_CACHE_STRATEGY);
            setValue("provider", defaultProviderForType(next), { shouldDirty: true });
        },
        [setValue]
    );

    const handleAddCustomParam = useCallback(() => {
        const currentParams = getValues("customParams") ?? [];
        setValue("customParams", [{ key: "", value: "" }, ...currentParams]);
    }, [getValues, setValue]);

    const handleAddHeader = useCallback(() => {
        const currentHeaders = getValues("customHeaders") ?? [];
        setValue("customHeaders", [{ key: "", value: "" }, ...currentHeaders]);
    }, [getValues, setValue]);

    const customParams = watch("customParams") ?? [];
    const hasEmptyCustomParam = customParams.some((p: { key: string; value: string }) => !p.key?.trim() || !p.value?.trim());

    const customHeaders = watch("customHeaders") ?? [];
    const hasEmptyHeader = customHeaders.some((h: { key: string; value: string }) => !h.key?.trim() || !h.value?.trim());

    const [committedCustomParamsJson, setCommittedCustomParamsJson] = useState("[]");
    const handleCustomParamKeyCommit = useCallback(() => {
        setCommittedCustomParamsJson(JSON.stringify(getValues("customParams") ?? []));
    }, [getValues]);

    useEffect(() => {
        if (supportedParams && supportedParams.length > 0) {
            handleCustomParamKeyCommit();
        }
    }, [supportedParams, handleCustomParamKeyCommit]);

    const unsupportedCustomKeys = useMemo(() => {
        if (!supportedParams || supportedParams.length === 0) return [];
        const committed: { key: string }[] = JSON.parse(committedCustomParamsJson);
        return committed.filter(p => p.key && !supportedParams.includes(p.key)).map(p => p.key);
    }, [committedCustomParamsJson, supportedParams]);

    const selectedModelName = watch("modelName");
    const debouncedModelName = useDebounce(selectedModelName, 500);

    useEffect(() => {
        onDirtyStateChange?.(isDirty);
    }, [isDirty, onDirtyStateChange]);

    useEffect(() => {
        if (!selectedProvider || !debouncedModelName || isDecisionProvider) {
            setSupportedParams(null);
            setParamsError(false);
            return;
        }
        let cancelled = false;
        setParamsError(false);
        modelsApi
            .getSupportedParams(selectedProvider, debouncedModelName)
            .then(params => {
                if (!cancelled) setSupportedParams(params);
            })
            .catch(() => {
                if (!cancelled) {
                    setSupportedParams(null);
                    setParamsError(true);
                }
            });
        return () => {
            cancelled = true;
        };
    }, [selectedProvider, debouncedModelName, isDecisionProvider]);

    useEffect(() => {
        if (!isNew && modelToEdit) {
            hasInitializedFromModelRef.current = true;

            const modelType = modelToEdit.modelType === "decision" ? "decision" : "chat";
            setValue("alias", modelToEdit.alias);
            setValue("modelType", modelType);
            setValue("provider", modelToEdit.provider ?? "");

            const { modelName, modelPrefix } = modelNameFormValues(modelToEdit.provider, modelToEdit.modelName ?? "");
            setValue("modelName", modelName);
            setValue("modelPrefix", modelPrefix);
            setValue("apiBase", modelToEdit.apiBase || "");
            setValue("maxInputTokens", modelToEdit.maxInputTokens != null ? String(modelToEdit.maxInputTokens) : "");
            setValue("pricing", pricingFormValues(modelToEdit.pricing));
            setValue("authType", modelToEdit.authType || "apikey");
            setValue("description", modelToEdit.description || "");

            if (modelToEdit.authConfig) {
                hydrateStoredCredentials(modelToEdit.authConfig, setValue, setStoredCredentialFields);
            }

            if (modelToEdit.modelParams) {
                hydrateModelParams(modelToEdit.modelParams, modelToEdit.provider, setValue);
            }
        } else if (isNew) {
            hasInitializedFromModelRef.current = false;
        }
    }, [modelToEdit, isNew, setValue, hasInitializedFromModelRef, setStoredCredentialFields]);

    const onFormSubmit = async (data: ModelFormData) => {
        await onSave(data, dirtyFields);
    };

    const isDefaultModel = !isNew && modelToEdit ? DEFAULT_MODEL_ALIASES.includes(modelToEdit.alias.toLowerCase()) : false;

    return (
        <FormProvider {...methods}>
            <div>
                <form id="model-form" className="w-full max-w-[1200px]" onSubmit={handleSubmit(onFormSubmit, trackModelFormErrors)}>
                    <PageSection className="gap-6">
                        {offersModelTypeChoice && <ModelTypeField modelType={newModelType} onChange={handleModelTypeChange} />}
                        {!isNew && (decisionModelsEnabled || editedModelType === "decision") && <ModelTypeField modelType={editedModelType} />}

                        <FormFieldLayoutItem
                            label="Display Name"
                            required
                            error={errors.alias as { message?: string }}
                            helpText="Provide a short, descriptive name for this model for others in your organization to reference. Example: 'Analysis'."
                            statusIndicator={isDefaultModel ? <div className="text-xs text-(--warning-wMain)">Cannot be changed</div> : undefined}
                        >
                            <Input {...register("alias", { required: "Display name is required" })} aria-invalid={!!errors.alias} disabled={isDefaultModel} title={isDefaultModel ? "This model's name cannot be changed" : ""} />
                        </FormFieldLayoutItem>

                        <FormFieldLayoutItem label="Description" required error={errors.description as { message?: string }} helpText="Describe types of tasks this model is suitable or not suitable for.">
                            <Textarea {...register("description", { required: "Description is required" })} rows={4} maxLength={1001} aria-invalid={!!errors.description} />
                        </FormFieldLayoutItem>

                        <ModelConnectionFields connection={connection} availableProviders={providers} modelsByProvider={modelsByProvider} lockProvider={!isNew && editedModelType === "decision"} />

                        {selectedProvider && providerConfig && !isDecisionProvider && (
                            <>
                                <FormFieldLayoutItem
                                    label={
                                        <span className="inline-flex items-center gap-1.5">
                                            Max Input Tokens
                                            <HelpPopover
                                                label="Max Input Tokens help"
                                                side="right"
                                                size="16"
                                                content={
                                                    <>
                                                        <p>
                                                            Drives the chat context-usage indicator so users can see how much of the window they&apos;ve consumed and trigger compaction before it overflows. This doesn&apos;t change what the provider
                                                            accepts — it only changes what the usage indicator displays.
                                                        </p>
                                                        <p className="mt-2">If left blank, the indicator falls back to LiteLLM&apos;s built-in registry, then to the agent&apos;s configured value, and finally hides if nothing is known.</p>
                                                    </>
                                                }
                                            />
                                        </span>
                                    }
                                    error={errors.maxInputTokens as { message?: string }}
                                    helpText="Check the model provider's documentation for the current context window limit for this model version."
                                >
                                    <Input
                                        type="number"
                                        min={1}
                                        placeholder="e.g. 200000"
                                        {...register("maxInputTokens", {
                                            validate: value => {
                                                if (value == null || value === "") return true;
                                                const n = Number(value);
                                                if (!Number.isFinite(n) || n < 1) return "Must be a positive integer";
                                                return true;
                                            },
                                        })}
                                        aria-invalid={!!errors.maxInputTokens}
                                    />
                                </FormFieldLayoutItem>

                                <ModelPricingFields />

                                <AccordionCard value="advanced-settings" variant="borderless" trigger={<span className="text-sm font-medium text-(--primary-text-wMain)">Advanced Settings</span>} className="mt-4">
                                    <div className="flex flex-col gap-6">
                                        {!providerConfig.hideCommonParams && COMMON_MODEL_PARAMS.length > 0 && (
                                            <>
                                                {COMMON_MODEL_PARAMS.map((field: ProviderField) => (
                                                    <ModelFormField key={field.name} field={field} storedCredentialFields={storedCredentialFields} />
                                                ))}
                                            </>
                                        )}

                                        <div className="mt-2">
                                            <div className="flex items-start justify-between">
                                                <div>
                                                    <PageLabel>Custom Parameters</PageLabel>
                                                    <p className="mt-1 text-sm text-(--secondary-text-wMain)">Add any additional parameters supported by your model provider.</p>
                                                </div>
                                                <Button type="button" variant="ghost" size="sm" onClick={handleAddCustomParam} disabled={hasEmptyCustomParam} tooltip={hasEmptyCustomParam ? "Each row needs a key and value" : undefined}>
                                                    <Plus className="mr-2 size-4" />
                                                    New Pair
                                                </Button>
                                            </div>
                                            <div className="mt-4">
                                                <Controller
                                                    name="customParams"
                                                    control={control}
                                                    rules={{
                                                        validate: params => {
                                                            if (!params || params.length === 0) return true;
                                                            for (const param of params) {
                                                                if (!param.key || !param.key.trim()) {
                                                                    return "Custom parameter keys cannot be empty";
                                                                }
                                                                if (!param.value || !param.value.trim()) {
                                                                    return "Custom parameter values cannot be empty";
                                                                }
                                                            }
                                                            return true;
                                                        },
                                                    }}
                                                    render={() => (
                                                        <>
                                                            <KeyValuePairList name="customParams" error={errors.customParams} minPairs={0} emptyMessage="No custom parameters added yet" onChange={handleCustomParamKeyCommit} />
                                                            {unsupportedCustomKeys.length > 0 && (
                                                                <p className="mt-2 text-xs text-(--warning-wMain)">Some custom parameters may not be supported by the selected model: {unsupportedCustomKeys.join(", ")}</p>
                                                            )}
                                                        </>
                                                    )}
                                                />
                                                {paramsError && <p className="mt-2 text-xs text-(--warning-wMain)">Unable to validate custom parameters for this model.</p>}
                                            </div>
                                        </div>

                                        <div className="mt-2">
                                            <div className="flex items-start justify-between">
                                                <div>
                                                    <PageLabel>Custom HTTP Headers</PageLabel>
                                                    <p className="mt-1 text-sm text-(--secondary-text-wMain)">
                                                        Fixed headers sent verbatim on every request to this model provider (e.g. a required API version or tenant header). Values are constant for all requests.
                                                    </p>
                                                </div>
                                                <Button type="button" variant="ghost" size="sm" onClick={handleAddHeader} disabled={hasEmptyHeader} tooltip={hasEmptyHeader ? "Each header needs a key and value" : undefined}>
                                                    <Plus className="mr-2 size-4" />
                                                    New Header
                                                </Button>
                                            </div>
                                            <div className="mt-4">
                                                <KeyValuePairList name="customHeaders" error={errors.customHeaders} minPairs={0} emptyMessage="No custom headers added yet" />
                                            </div>
                                        </div>
                                    </div>
                                </AccordionCard>
                            </>
                        )}

                        {selectedProvider && isDecisionProvider && <ModelPricingFields offersCacheRates={false} />}

                        {selectedProvider && providerConfig && (
                            <TestConnectionSection
                                getFormData={() => getValues() as ModelFormData}
                                getDirtyFields={() => dirtyFields}
                                isNew={isNew}
                                modelId={modelToEdit?.id}
                                disabled={!selectedProvider || !selectedModelName || (isNew && !isAuthCredentialsConfigured)}
                            />
                        )}
                    </PageSection>
                </form>
            </div>
        </FormProvider>
    );
};
