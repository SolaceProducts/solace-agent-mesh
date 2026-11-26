// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type Dispatch, type SetStateAction, useCallback, useEffect, useId, useRef, useState } from "react";
import { Controller, useFormContext, type UseFormReturn } from "react-hook-form";

import { isSameSupportedModelsQuery, type ModelConfig, type SupportedModelsQueryParams, useSupportedModels } from "@/api/models";
import type { ModelType } from "@/api/models/types";

import { useConfigContext } from "@/providers/contexts";

import { PasswordInput } from "@/common/form";
import { FormFieldLayoutItem, PageLabel, PageLabelWithValue, PageValue } from "@/common/layout";
import { ErrorLabel } from "@/common/status";

import { HTTP_URL_FORMAT_HINT, isHttpUrl } from "@/utils/url";

import { HelpPopover } from "@/common";
import { ComboBox, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea } from "@/ui";

import { AzureWorkloadIdentityInfo } from "./AzureWorkloadIdentityInfo";
import { EMPTY_PRICING_FORM } from "./modelPricing";
import {
    AUTH_FIELDS,
    AUTH_TYPE_DESCRIPTIONS,
    AUTH_TYPE_LABELS,
    type AuthType,
    buildModelPayload,
    CONNECTION_FIELD_NAMES,
    customModelPrefixError,
    DEFAULT_CUSTOM_MODEL_PREFIX,
    getProviderConfig,
    hasModelPrefixField,
    type ModelFormData,
    modelNameFieldLabel,
    modelNameNoun,
    modelPrefixFieldValue,
    type ModelProvider,
    type ProviderField,
    resolveApiBaseHelpText,
    resolveChatRequestUrl,
} from "./modelProviderUtils";
import { ProviderSelect } from "./ProviderSelect";

const OPTIONAL_AUTH_CREDENTIAL_FIELDS = new Set(["apiKey", "clientSecret", "awsSecretAccessKey", "awsSessionToken", "vertexCredentials"]);

const EMPTY_CONNECTION_FIELDS: Readonly<Record<string, string>> = Object.fromEntries(CONNECTION_FIELD_NAMES.map(name => [name, ""]));

interface ModelFormFieldProps {
    field: ProviderField;
    storedCredentialFields: Set<string>;
}

interface FieldRenderProps {
    field: ProviderField;
    isRequiredField: boolean;
    error: { message?: string } | undefined;
    storedCredentialFields: Set<string>;
}

function requiredRule(field: ProviderField, isRequiredField: boolean) {
    return isRequiredField ? `${field.label} is required` : false;
}

function PasswordFormField({ field, isRequiredField, error, storedCredentialFields }: Readonly<FieldRenderProps>) {
    const { control } = useFormContext<ModelFormData>();
    return (
        <FormFieldLayoutItem key={field.name} helpText={field.helpText} label={field.label} required={field.required} error={error}>
            <PasswordInput name={field.name} control={control} hasStoredValue={storedCredentialFields.has(field.name)} placeholder={field.placeholder} rules={{ required: requiredRule(field, isRequiredField) }} />
        </FormFieldLayoutItem>
    );
}

function TextareaFormField({ field, isRequiredField, error }: Readonly<FieldRenderProps>) {
    const { register } = useFormContext<ModelFormData>();
    return (
        <FormFieldLayoutItem key={field.name} label={field.label} required={field.required} error={error} helpText={field.helpText}>
            <Textarea rows={6} placeholder={field.placeholder} {...register(field.name, { required: requiredRule(field, isRequiredField) })} aria-invalid={!!error} />
        </FormFieldLayoutItem>
    );
}

function SelectFormField({ field, isRequiredField, error }: Readonly<FieldRenderProps>) {
    const { control } = useFormContext<ModelFormData>();
    return (
        <FormFieldLayoutItem key={field.name} label={field.label} required={field.required} error={error} helpText={field.helpText}>
            <Controller
                name={field.name}
                control={control}
                rules={{ required: requiredRule(field, isRequiredField) }}
                render={({ field: fieldProps }) => (
                    <Select value={String(fieldProps.value ?? "")} onValueChange={fieldProps.onChange}>
                        <SelectTrigger aria-invalid={!!error}>
                            <SelectValue placeholder={`Select ${field.label.toLowerCase()}`} />
                        </SelectTrigger>
                        <SelectContent>
                            {(field.options ?? []).map(option => (
                                <SelectItem key={option.value} value={option.value}>
                                    {option.label}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                )}
            />
        </FormFieldLayoutItem>
    );
}

function buildTextNumberRules(field: ProviderField, isRequiredField: boolean): Record<string, unknown> {
    const rules: Record<string, unknown> = { required: requiredRule(field, isRequiredField) };
    if (field.pattern) rules.pattern = field.pattern;
    if (field.type === "number") {
        if (field.min !== undefined) rules.min = { value: field.min, message: `Minimum value is ${field.min}` };
        if (field.max !== undefined) rules.max = { value: field.max, message: `Maximum value is ${field.max}` };
    }
    return rules;
}

function TextNumberFormField({ field, isRequiredField, error }: Readonly<FieldRenderProps>) {
    const { register } = useFormContext<ModelFormData>();
    const isNumber = field.type === "number";
    return (
        <FormFieldLayoutItem key={field.name} label={field.label} required={field.required} error={error} helpText={field.helpText}>
            <Input
                {...register(field.name, buildTextNumberRules(field, isRequiredField))}
                type={isNumber ? "number" : "text"}
                inputMode={isNumber ? "decimal" : undefined}
                step={isNumber ? field.step : undefined}
                min={isNumber ? field.min : undefined}
                max={isNumber ? field.max : undefined}
                placeholder={field.placeholder}
                aria-invalid={!!error}
            />
        </FormFieldLayoutItem>
    );
}

export function ModelFormField({ field, storedCredentialFields }: Readonly<ModelFormFieldProps>) {
    const {
        formState: { errors },
    } = useFormContext<ModelFormData>();

    const isStoredCredential = field.storageTarget === "auth" && OPTIONAL_AUTH_CREDENTIAL_FIELDS.has(field.name) && storedCredentialFields.has(field.name);
    const isRequiredField = field.required && !isStoredCredential;
    const error = errors[field.name] as { message?: string } | undefined;
    const renderProps: FieldRenderProps = { field, isRequiredField, error, storedCredentialFields };

    if (field.type === "password") return <PasswordFormField {...renderProps} />;
    if (field.type === "textarea") return <TextareaFormField {...renderProps} />;
    if (field.type === "select") return <SelectFormField {...renderProps} />;
    return <TextNumberFormField {...renderProps} />;
}

export interface UseModelConnectionFormOptions {
    methods: UseFormReturn<ModelFormData>;
    isNew: boolean;
    modelToEdit?: ModelConfig | null;
    modelsByProvider?: Record<string, Array<{ id: string; label: string }>>;
    onProviderChange?: (provider: string) => Promise<void>;
    onProviderReset?: () => void;
}

export interface ModelConnectionState {
    providerConfig: ReturnType<typeof getProviderConfig> | null;
    storedCredentialFields: Set<string>;
    setStoredCredentialFields: Dispatch<SetStateAction<Set<string>>>;
    hasInitializedFromModelRef: { current: boolean };
    selectedProvider: string;
    selectedAuthType: string;
    modelType: ModelType | undefined;
    isAuthCredentialsConfigured: boolean;
    isModelDropdownEnabled: boolean;
    dynamicModels: Array<{ id: string; label: string }>;
    isLoadingModels: boolean;
    hasFetchError: boolean;
    fetchError: Error | null;
    handleAuthTypeChange: (newAuthType: string, formOnChange: (value: string) => void) => void;
    handleModelDropdownOpen: () => void;
}

export function useModelConnectionForm({ methods, isNew, modelToEdit, modelsByProvider = {}, onProviderChange, onProviderReset }: UseModelConnectionFormOptions): ModelConnectionState {
    const { watch, setValue, reset, getValues, formState } = methods;
    const isAuthTypeDirty = !!formState.dirtyFields.authType;

    const [providerConfig, setProviderConfig] = useState<ReturnType<typeof getProviderConfig> | null>(null);
    const [storedCredentialFields, setStoredCredentialFields] = useState<Set<string>>(new Set());
    const [queryParams, setQueryParams] = useState<SupportedModelsQueryParams | null>(null);
    const { data: dynamicModels = [], isLoading: isLoadingModels, isError: hasFetchError, error: fetchError, isSuccess: hasFetchSucceeded, refetch: refetchModels } = useSupportedModels(queryParams);
    const hasInitializedFromModelRef = useRef(false);
    const credentialsDirtyRef = useRef(false);

    const selectedProvider = watch("provider");
    const selectedAuthType = watch("authType");
    const modelType = watch("modelType") as ModelType | undefined;
    const apiBase = watch("apiBase");
    const connectionFieldValues = watch(CONNECTION_FIELD_NAMES) as ReadonlyArray<string | undefined>;
    const connectionSignature = connectionFieldValues.map(v => v ?? "").join("\x01");
    const isAnyConnectionFieldDirty = CONNECTION_FIELD_NAMES.some(name => !!formState.dirtyFields[name]);

    const isProviderConfigured = !!selectedProvider && !!selectedAuthType;

    const isAuthCredentialsConfigured = (() => {
        if (!selectedAuthType) return false;
        if (selectedAuthType === "none") return true;

        const isConfigured = (fieldName: string) => !!getValues(fieldName) || storedCredentialFields.has(fieldName);

        if (selectedAuthType === "apikey") return isConfigured("apiKey");
        if (selectedAuthType === "oauth2") {
            return isConfigured("clientId") && isConfigured("clientSecret") && !!getValues("tokenUrl");
        }
        if (selectedAuthType === "aws_iam") {
            return isConfigured("awsAccessKeyId") && isConfigured("awsSecretAccessKey");
        }
        if (selectedAuthType === "gcp_service_account") {
            return isConfigured("vertexCredentials");
        }
        if (selectedAuthType === "azure_workload_identity") {
            return !!getValues("clientId") && !!getValues("tenantId");
        }
        return false;
    })();

    const isApiBaseReady = !providerConfig?.apiBaseRequired || !!apiBase;
    const isModelDropdownEnabled = isProviderConfigured && isApiBaseReady && (!isNew || isAuthCredentialsConfigured);

    useEffect(() => {
        if (selectedProvider) {
            const config = getProviderConfig(selectedProvider, modelType);
            setProviderConfig(config);
            setQueryParams(null);
            credentialsDirtyRef.current = false;
            onProviderReset?.();

            if (hasInitializedFromModelRef.current && !isNew && modelToEdit && modelToEdit.provider === selectedProvider) {
                hasInitializedFromModelRef.current = false;
                onProviderChange?.(selectedProvider);
                return;
            }

            setStoredCredentialFields(new Set());
            onProviderChange?.(selectedProvider);

            const valuesEveryProviderShares = getValues();
            const clearedValuesOfThePreviousProvider = {
                modelName: "",
                modelPrefix: hasModelPrefixField(selectedProvider) ? modelPrefixFieldValue(DEFAULT_CUSTOM_MODEL_PREFIX) : undefined,
                ...EMPTY_CONNECTION_FIELDS,
                ...(getValues("pricing") ? { pricing: EMPTY_PRICING_FORM } : {}),
            };
            reset({
                ...valuesEveryProviderShares,
                provider: selectedProvider,
                authType: config.allowedAuthTypes.length === 1 ? config.allowedAuthTypes[0] : "",
                ...clearedValuesOfThePreviousProvider,
            });
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [selectedProvider, isNew, modelToEdit]);

    useEffect(() => {
        if (selectedProvider) {
            setQueryParams(null);
        }
    }, [selectedProvider, selectedAuthType, connectionSignature]);

    useEffect(() => {
        if (isAnyConnectionFieldDirty || isAuthTypeDirty) {
            credentialsDirtyRef.current = true;
        }
    }, [isAnyConnectionFieldDirty, isAuthTypeDirty]);

    useEffect(() => {
        if (queryParams !== null && hasFetchSucceeded) {
            credentialsDirtyRef.current = false;
        }
    }, [queryParams, hasFetchSucceeded]);

    const handleAuthTypeChange = useCallback(
        (newAuthType: string, formOnChange: (value: string) => void) => {
            formOnChange(newAuthType);
            for (const fields of Object.values(AUTH_FIELDS)) {
                for (const field of fields) {
                    setValue(field.name, "");
                }
            }
            setStoredCredentialFields(new Set());
            setQueryParams(null);
        },
        [setValue]
    );

    const handleModelDropdownOpen = useCallback(() => {
        if (!selectedProvider) return;

        if (!credentialsDirtyRef.current && queryParams === null && (modelsByProvider[selectedProvider]?.length ?? 0) > 0) {
            return;
        }

        const currentProviderConfig = providerConfig || getProviderConfig(selectedProvider, modelType);
        const modelParams: Record<string, unknown> = {};
        for (const field of currentProviderConfig.fields) {
            const val = getValues(field.name);
            if (val != null && val !== "") {
                modelParams[field.name] = val;
            }
        }

        const formData = getValues() as ModelFormData;
        const payload = buildModelPayload({
            ...formData,
            alias: formData.alias || "",
            modelName: formData.modelName || "",
        });

        const nextParams: SupportedModelsQueryParams = {
            provider: selectedProvider,
            modelId: isNew ? undefined : modelToEdit?.id,
            apiBase: payload.apiBase || undefined,
            authConfig: payload.authConfig,
            modelParams: Object.keys(modelParams).length > 0 ? modelParams : undefined,
            modelType: currentProviderConfig.modelType,
        };

        if (isSameSupportedModelsQuery(queryParams, nextParams)) {
            refetchModels();
            return;
        }
        setQueryParams(nextParams);
    }, [selectedProvider, modelType, isNew, apiBase, modelToEdit, providerConfig, getValues, queryParams, modelsByProvider, refetchModels]);

    return {
        providerConfig,
        storedCredentialFields,
        setStoredCredentialFields,
        hasInitializedFromModelRef,
        selectedProvider,
        selectedAuthType,
        modelType,
        isAuthCredentialsConfigured,
        isModelDropdownEnabled,
        dynamicModels,
        isLoadingModels,
        hasFetchError,
        fetchError: fetchError as Error | null,
        handleAuthTypeChange,
        handleModelDropdownOpen,
    };
}

export interface ModelNameFieldSpec {
    name: string;
    label: string;
}

function ModelPrefixLabel() {
    return (
        <span className="inline-flex items-center gap-1.5">
            Model Prefix
            <HelpPopover label="Model Prefix help" side="right" size="16" content="The API Agent Mesh uses to call the model, such as openai/ or anthropic/." />
        </span>
    );
}

function ModelPrefixField({ invalid, disabled, messagesId }: Readonly<{ invalid: boolean; disabled: boolean; messagesId: string }>) {
    const { control } = useFormContext<ModelFormData>();
    return (
        <Controller
            name="modelPrefix"
            control={control}
            rules={{ validate: value => customModelPrefixError(value as string | undefined) ?? true }}
            render={({ field }) => {
                const typed = (field.value as string | undefined) ?? "";
                return (
                    <FormFieldLayoutItem label={<ModelPrefixLabel />}>
                        <Input
                            ref={field.ref}
                            name={field.name}
                            value={typed}
                            onChange={field.onChange}
                            onBlur={() => {
                                field.onChange(modelPrefixFieldValue(typed));
                                field.onBlur();
                            }}
                            placeholder={modelPrefixFieldValue(DEFAULT_CUSTOM_MODEL_PREFIX)}
                            aria-label="Model prefix"
                            aria-invalid={invalid}
                            aria-describedby={invalid ? messagesId : undefined}
                            disabled={disabled}
                            data-testid="modelPrefixInput"
                        />
                    </FormFieldLayoutItem>
                );
            }}
        />
    );
}

function ModelNameMessages({ id, errors, helpText }: Readonly<{ id: string; errors: Array<string | undefined>; helpText: string | undefined }>) {
    const shownErrors = errors.filter(Boolean);
    if (shownErrors.length === 0 && !helpText) return null;
    return (
        <div id={id} data-testid="modelNameMessages">
            {shownErrors.map(message => (
                <ErrorLabel key={message} message={message} />
            ))}
            {helpText && <div className="text-xs text-(--secondary-text-wMain)">{helpText}</div>}
        </div>
    );
}

const NO_MODELS_FOUND_TEXT = "No models found";
const DEFAULT_MODEL_NAME_FIELDS: ModelNameFieldSpec[] = [{ name: "modelName", label: "Model Name" }];

export function supportedModelsFetchErrorMessage(noun: string, error: Error | null): string {
    const hint = "Verify that your model connection details above are correct.";
    const errorId = (error as { errorId?: unknown } | null)?.errorId;
    const detail = error?.message?.trim();
    if (typeof errorId !== "string" || !errorId || !detail) return `Unable to find ${noun}s. ${hint}`;
    let trimmed = detail;
    while (trimmed.endsWith(".") || trimmed.endsWith("!") || trimmed.endsWith("?")) {
        trimmed = trimmed.slice(0, -1);
    }
    return `${trimmed}. ${hint}`;
}

interface ModelConnectionFieldsProps {
    connection: ModelConnectionState;
    availableProviders: ModelProvider[];
    modelsByProvider?: Record<string, Array<{ id: string; label: string }>>;
    modelNameFields?: ModelNameFieldSpec[];
    lockProvider?: boolean;
}

export function ModelConnectionFields({ connection, availableProviders, modelsByProvider = {}, modelNameFields = DEFAULT_MODEL_NAME_FIELDS, lockProvider = false }: Readonly<ModelConnectionFieldsProps>) {
    const {
        control,
        register,
        watch,
        trigger,
        clearErrors,
        getFieldState,
        formState: { errors, touchedFields },
    } = useFormContext<ModelFormData>();
    const apiBase = watch("apiBase");
    const { providerConfig, storedCredentialFields, selectedProvider, selectedAuthType, modelType, isModelDropdownEnabled, dynamicModels, isLoadingModels, hasFetchError, fetchError, handleAuthTypeChange, handleModelDropdownOpen } = connection;
    const apiBaseRegister = register("apiBase", {
        required: providerConfig?.apiBaseRequired ? "API Base URL is required" : false,
        validate: value => isHttpUrl(value) || HTTP_URL_FORMAT_HINT,
    });
    const [staleApiBaseLatched, setStaleApiBaseLatched] = useState(false);
    useEffect(() => {
        if (!staleApiBaseLatched && !providerConfig?.showApiBase && apiBase && !isHttpUrl(apiBase)) {
            setStaleApiBaseLatched(true);
            trigger("apiBase");
        }
    }, [apiBase, staleApiBaseLatched, trigger, providerConfig?.showApiBase]);
    const { azureWorkloadIdentityEnabled } = useConfigContext();
    const allowedAuthTypes: string[] = providerConfig?.allowedAuthTypes ?? [];
    const storedOnlyAuthType = selectedAuthType && !allowedAuthTypes.includes(selectedAuthType) ? (selectedAuthType as AuthType) : undefined;
    const authTypeOptions = [
        ...(providerConfig?.allowedAuthTypes.filter(authType => authType !== "azure_workload_identity" || azureWorkloadIdentityEnabled || authType === selectedAuthType) ?? []),
        ...(storedOnlyAuthType ? [storedOnlyAuthType] : []),
    ];
    const modelPrefix = watch("modelPrefix");
    const touchedModelPrefixError = touchedFields.modelPrefix ? customModelPrefixError(modelPrefix) : undefined;
    const modelPrefixError = hasModelPrefixField(selectedProvider) ? (errors.modelPrefix?.message ?? touchedModelPrefixError) : undefined;
    const chatRequestUrl = modelType === "decision" ? undefined : resolveChatRequestUrl(selectedProvider, watch("apiBase"), modelPrefix, watch("modelName"));
    const apiBaseHelpText = resolveApiBaseHelpText(providerConfig?.apiBaseHelpText, selectedProvider, modelPrefix, watch("modelName"));
    const modelNameMessagesId = useId();

    return (
        <>
            <FormFieldLayoutItem label="Model Provider" required error={errors.provider as { message?: string }}>
                <Controller
                    name="provider"
                    control={control}
                    rules={{ required: "Provider is required" }}
                    render={({ field }) => <ProviderSelect value={field.value} onValueChange={field.onChange} providers={availableProviders} modelType={modelType} disabled={lockProvider} invalid={!!errors.provider} />}
                />
            </FormFieldLayoutItem>

            {selectedProvider && providerConfig && (
                <>
                    {providerConfig.allowedAuthTypes?.length > 1 && (
                        <Controller
                            name="authType"
                            control={control}
                            rules={{ required: "Authentication type is required" }}
                            render={({ field }) => (
                                <PageLabelWithValue>
                                    <PageLabel required>Authentication Type</PageLabel>
                                    <Select value={field.value ?? ""} onValueChange={value => handleAuthTypeChange(value, field.onChange)}>
                                        <SelectTrigger aria-invalid={!!errors.authType}>
                                            <SelectValue placeholder="Select an authentication type" />
                                        </SelectTrigger>
                                        <SelectContent>
                                            {authTypeOptions.map(authType => (
                                                <SelectItem key={authType} value={authType} description={AUTH_TYPE_DESCRIPTIONS[authType]}>
                                                    {AUTH_TYPE_LABELS[authType]}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                    {(errors.authType as { message?: string } | undefined)?.message && <ErrorLabel message={(errors.authType as { message?: string }).message as string} />}
                                </PageLabelWithValue>
                            )}
                        />
                    )}

                    {providerConfig.allowedAuthTypes?.length === 1 && (
                        <PageLabelWithValue>
                            <PageLabel required>Authentication Type</PageLabel>
                            <PageValue>{AUTH_TYPE_LABELS[storedOnlyAuthType ?? providerConfig.allowedAuthTypes[0]]}</PageValue>
                        </PageLabelWithValue>
                    )}

                    {selectedAuthType && AUTH_FIELDS[selectedAuthType as AuthType] && (
                        <>
                            {AUTH_FIELDS[selectedAuthType as AuthType].map(field => (
                                <ModelFormField key={field.name} field={field} storedCredentialFields={storedCredentialFields} />
                            ))}
                        </>
                    )}

                    {selectedAuthType === "azure_workload_identity" && azureWorkloadIdentityEnabled && <AzureWorkloadIdentityInfo />}

                    {(providerConfig.showApiBase || staleApiBaseLatched) && (
                        <FormFieldLayoutItem label="API Base URL" required={providerConfig.apiBaseRequired} error={errors.apiBase as { message?: string }} helpText={apiBaseHelpText}>
                            <Input
                                {...apiBaseRegister}
                                onChange={async e => {
                                    await apiBaseRegister.onChange(e);
                                    clearErrors("apiBase");
                                }}
                                onBlur={async e => {
                                    await apiBaseRegister.onBlur(e);
                                    if (getFieldState("apiBase").isDirty) {
                                        await trigger("apiBase");
                                    }
                                }}
                                placeholder={providerConfig.apiBasePlaceholder}
                                aria-invalid={!!errors.apiBase}
                                data-testid="apiBase-input"
                            />
                            {chatRequestUrl && !errors.apiBase && (
                                <div className="text-xs break-all text-(--secondary-text-wMain)" data-testid="chatRequestUrl">
                                    Requests go to: {chatRequestUrl}
                                </div>
                            )}
                        </FormFieldLayoutItem>
                    )}

                    {providerConfig.fields.length > 0 && (
                        <>
                            {providerConfig.fields.map(field => (
                                <ModelFormField key={field.name} field={field} storedCredentialFields={storedCredentialFields} />
                            ))}
                        </>
                    )}

                    {modelNameFields.map(spec => {
                        const fieldError = errors[spec.name] as { message?: string } | undefined;
                        const fieldLabel = modelNameFieldLabel(selectedProvider, spec.label);
                        const noun = modelNameNoun(selectedProvider);
                        const typedValue = watch(spec.name) as string | undefined;
                        const hasTypedValue = typeof typedValue === "string" && typedValue.trim() !== "";
                        let modelFieldHelpText: string | undefined;
                        if (isLoadingModels) {
                            modelFieldHelpText = `Finding your available ${noun}s...`;
                        } else if (isModelDropdownEnabled) {
                            modelFieldHelpText = undefined;
                        } else {
                            modelFieldHelpText = `Configure provider and authentication to select a ${noun}`;
                        }
                        const nameError = hasFetchError && !hasTypedValue ? { message: supportedModelsFetchErrorMessage(noun, fetchError) } : fieldError;
                        const nameControl = (
                            <Controller
                                name={spec.name}
                                control={control}
                                rules={{ required: `${fieldLabel} is required`, validate: value => (typeof value === "string" && value.trim() !== "") || `${fieldLabel} is required` }}
                                render={({ field }) => {
                                    const selectedValue = (field.value as string | undefined) ?? "";
                                    const cachedModels = modelsByProvider[selectedProvider] || [];
                                    const modelItems = dynamicModels.length > 0 ? dynamicModels : cachedModels;

                                    let displayItems: Array<{ id: string; label: string }> = modelItems.map(model => ({ id: model.id, label: model.id }));
                                    if (selectedValue && !displayItems.some(item => item.id === selectedValue)) {
                                        displayItems = [{ id: selectedValue, label: selectedValue }, ...displayItems];
                                    }

                                    const testid = spec.name === "modelName" ? "modelNameCombobox" : `modelNameCombobox-${spec.name}`;
                                    return (
                                        <div data-testid={testid}>
                                            <ComboBox
                                                key={selectedAuthType}
                                                value={selectedValue}
                                                onValueChange={field.onChange}
                                                items={displayItems}
                                                placeholder={`Type or select a ${noun}...`}
                                                disabled={!isModelDropdownEnabled}
                                                invalid={!!fieldError}
                                                isLoading={isLoadingModels}
                                                onOpen={handleModelDropdownOpen}
                                                noItemsFoundText={NO_MODELS_FOUND_TEXT}
                                                allowCustomValue
                                            />
                                        </div>
                                    );
                                }}
                            />
                        );
                        if (spec.name !== "modelName") {
                            return (
                                <FormFieldLayoutItem key={spec.name} label={fieldLabel} required error={nameError} helpText={modelFieldHelpText}>
                                    {nameControl}
                                </FormFieldLayoutItem>
                            );
                        }
                        return (
                            <div key={spec.name} className="flex flex-col gap-2">
                                <div className="flex items-start gap-4">
                                    {hasModelPrefixField(selectedProvider) && (
                                        <div className="w-56 shrink-0">
                                            <ModelPrefixField invalid={!!modelPrefixError} disabled={!isModelDropdownEnabled} messagesId={modelNameMessagesId} />
                                        </div>
                                    )}
                                    <div className="min-w-0 flex-1">
                                        <FormFieldLayoutItem label={fieldLabel} required>
                                            {nameControl}
                                        </FormFieldLayoutItem>
                                    </div>
                                </div>
                                <ModelNameMessages id={modelNameMessagesId} errors={[modelPrefixError, nameError?.message]} helpText={modelFieldHelpText} />
                            </div>
                        );
                    })}
                </>
            )}
        </>
    );
}
