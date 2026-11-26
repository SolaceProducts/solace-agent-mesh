// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { ExternalLink } from "lucide-react";
import { type FieldErrors, FormProvider, useForm, useWatch } from "react-hook-form";

import { gatewaysApi } from "@/api/gateways";

import { useSecretAwareSubmit } from "@/hooks/useSecretAwareSubmit";
import { StoredSecretsContext, useStoredSecrets, useTrackStoredSecrets } from "@/hooks/useStoredSecrets";

import { PAGE_COMMON_CLASSES, PageLabel, PageSection } from "@/common/layout";
import { trackFormErrors } from "@/common/product-analytics/trackFormErrors";

import { getErrorMessage } from "@/utils/api";
import { matchesConditionalDisplay } from "@/utils/conditionalDisplay";
import { processBackendValuesForForm } from "@/utils/formDefaults";

import { MessageBanner } from "@/common";
import type { Gateway, GatewayConnectionTestRequest, GatewayFieldSchema, GatewaySchema } from "@/types";

import { supportsCustomEndpoint } from "./customEndpoint";
import { GatewayFormField } from "./GatewayFormField";
import { createFullGatewaySchemaValidator } from "./hooks/useGatewaySchemas";
import { connectionTestDestinationChanged, isConnectionTestCredential } from "./utils/connectionTestDestination";
import { type FieldEntry, groupFieldsIntoSections } from "./utils/fieldSections";
import { sortFieldsByOrder } from "./utils/sortFieldsByOrder";
import { WHATSAPP_PREREQUISITES_DOCS_URL } from "./utils/whatsapp";
import { WhatsAppTemplateDialog } from "./WhatsAppTemplateDialog";

export { useStoredSecrets };

const groupFieldsIntoRows = (fields: FieldEntry[]): FieldEntry[][] => {
    const rows: FieldEntry[][] = [];
    for (const entry of fields) {
        const previousRow = rows.at(-1);
        const rowGroup = entry[1].rowGroup;
        if (rowGroup && previousRow?.[0][1].rowGroup === rowGroup) {
            previousRow.push(entry);
        } else {
            rows.push([entry]);
        }
    }
    return rows;
};

const PREREQUISITE_DOCS_LINKS: Partial<Record<string, { label: string; href: string }>> = {
    whatsapp: { label: "Learn more about WhatsApp prerequisites", href: WHATSAPP_PREREQUISITES_DOCS_URL },
};

export interface GatewayEditProps {
    schemaInfo: GatewaySchema;
    initialGatewayToEdit?: Gateway;
    isNew?: boolean;
    onSubmit: (data: Gateway) => Promise<void> | void;
    onDirtyStateChange?: (isDirty: boolean) => void;
    footer?: React.ReactNode;
}

function buildNewGatewayValues(schemaInfo: GatewaySchema): Record<string, any> {
    const values: Record<string, any> = {};
    for (const [fieldName, fieldConfig] of Object.entries(schemaInfo.schema)) {
        if (fieldConfig.default !== undefined) {
            values[fieldName] = fieldConfig.default;
        }
        if ((fieldConfig.type === "string" || fieldConfig.type === "password") && values[fieldName] === undefined) {
            values[fieldName] = "";
        }
    }
    return values;
}

function buildGatewayDefaultValues(isNew: boolean | undefined, schemaInfo: GatewaySchema | null, initialGatewayToEdit: Gateway | undefined): any {
    if (isNew && schemaInfo) {
        const base: Record<string, any> = { name: "", description: "", values: buildNewGatewayValues(schemaInfo) };
        if (supportsCustomEndpoint(schemaInfo.type)) base.slug = "";
        return base;
    }
    if (initialGatewayToEdit?.values && schemaInfo?.schema) {
        return { ...initialGatewayToEdit, values: processBackendValuesForForm(initialGatewayToEdit.values, schemaInfo.schema) };
    }
    return initialGatewayToEdit;
}

export const GatewayEdit = ({ schemaInfo, initialGatewayToEdit, isNew, onSubmit, onDirtyStateChange, footer }: GatewayEditProps) => {
    const fieldsWithStoredSecrets = useTrackStoredSecrets(initialGatewayToEdit?.values);

    const [submitErrorMessage, setSubmitErrorMessage] = useState<string | null>(null);

    const [isTestingConnection, setIsTestingConnection] = useState(false);
    const [isTemplateLookupOpen, setIsTemplateLookupOpen] = useState(false);
    const [connectionTestResult, setConnectionTestResult] = useState<{
        success: boolean;
        message: string;
    } | null>(null);

    const defaultValues = useMemo(() => buildGatewayDefaultValues(isNew, schemaInfo, initialGatewayToEdit), [isNew, schemaInfo, initialGatewayToEdit]);

    const schemaValidator = useMemo(() => {
        if (!schemaInfo) return undefined;
        return createFullGatewaySchemaValidator(schemaInfo, !isNew);
    }, [schemaInfo, isNew]);

    const methods = useForm<Gateway>({
        resolver: schemaValidator ? (zodResolver(schemaValidator) as any) : undefined,
        defaultValues: defaultValues as any,
        mode: "onSubmit",
    });

    const {
        register,
        control,
        formState: { errors, isDirty, dirtyFields, touchedFields },
        handleSubmit,
        reset,
        watch,
        setValue,
    } = methods;

    const formValues = watch("values");

    useEffect(() => {
        reset(defaultValues, { keepDirtyValues: isNew, keepTouched: isNew });
    }, [reset, defaultValues, isNew]);

    useEffect(() => {
        onDirtyStateChange?.(isDirty);
    }, [isDirty, onDirtyStateChange]);

    const onSubmitWithDirtyTracking = useSecretAwareSubmit({
        onSubmit,
        isNew,
        formState: { dirtyFields, touchedFields },
        fieldsWithStoredSecrets,
        getFieldSchema: (fieldName: string) => schemaInfo.schema?.[fieldName],
    });

    const handleInvalidSubmit = useCallback(
        (formErrors: FieldErrors<Gateway>) => {
            trackFormErrors(formErrors, "entrypoint");
            const hiddenLabels: string[] = [];
            const valueErrors = formErrors.values as Record<string, unknown> | undefined;
            if (valueErrors) {
                const currentValues = methods.getValues("values");
                for (const fieldName of Object.keys(valueErrors)) {
                    const fieldConfig = schemaInfo.schema?.[fieldName];
                    if (fieldConfig && !matchesConditionalDisplay(fieldConfig.conditionalDisplay, currentValues)) {
                        hiddenLabels.push(fieldConfig.label ?? fieldName);
                    }
                }
            }
            setSubmitErrorMessage(hiddenLabels.length > 0 ? `Cannot save. Please correct: ${hiddenLabels.join(", ")}.` : null);
        },
        [schemaInfo, methods]
    );

    const formRef = useRef<HTMLFormElement>(null);
    useEffect(() => {
        const form = formRef.current;
        if (!form) return;
        const blockEnterSubmit = (e: KeyboardEvent) => {
            if (e.key === "Enter" && (e.target as HTMLElement).tagName === "INPUT") {
                e.preventDefault();
            }
        };
        form.addEventListener("keydown", blockEnterSubmit);
        return () => form.removeEventListener("keydown", blockEnterSubmit);
    }, []);

    const autoFillPairs = useMemo(() => {
        if (!schemaInfo?.schema) return [] as Array<{ target: string; source: string }>;
        const pairs: Array<{ target: string; source: string }> = [];
        for (const [fieldName, fieldConfig] of Object.entries(schemaInfo.schema)) {
            if (fieldConfig.defaultFromField) {
                pairs.push({ target: fieldName, source: fieldConfig.defaultFromField });
            }
        }
        return pairs;
    }, [schemaInfo]);

    const autoFillSourcePaths = useMemo(() => autoFillPairs.map(p => `values.${p.source}` as const), [autoFillPairs]);

    const autoFillSourceValues = useWatch({
        control,
        name: autoFillSourcePaths,
    }) as Array<unknown>;

    useEffect(() => {
        autoFillPairs.forEach((pair, idx) => {
            const sourceValue = autoFillSourceValues[idx];
            if (typeof sourceValue !== "string" || sourceValue === "") return;
            const userTouchedTarget = !!(touchedFields as any)?.values?.[pair.target];
            if (userTouchedTarget) return;
            const targetPath = `values.${pair.target}` as const;
            if (methods.getValues(targetPath as any) !== sourceValue) {
                setValue(targetPath as any, sourceValue, { shouldDirty: false });
            }
        });
    }, [autoFillPairs, autoFillSourceValues, setValue, touchedFields, methods]);

    const formValuesJson = JSON.stringify(formValues);

    const clearWhenHiddenFields = useMemo(() => {
        if (!schemaInfo?.schema) return [] as Array<[string, GatewayFieldSchema]>;
        return Object.entries(schemaInfo.schema).filter(([, fieldConfig]) => fieldConfig.clearWhenHidden);
    }, [schemaInfo]);

    useEffect(() => {
        const currentValues = methods.getValues("values") as Record<string, unknown>;
        for (const [fieldName, fieldConfig] of clearWhenHiddenFields) {
            if (matchesConditionalDisplay(fieldConfig.conditionalDisplay, currentValues)) continue;
            const fieldPath = `values.${fieldName}` as const;
            if (methods.getValues(fieldPath as any)) {
                setValue(fieldPath as any, "", { shouldDirty: true });
            }
        }
    }, [clearWhenHiddenFields, formValuesJson, methods, setValue]);

    const showCustomEndpoint = supportsCustomEndpoint(schemaInfo?.type, formValues);

    useEffect(() => {
        if (!showCustomEndpoint && methods.getValues("slug")) {
            setValue("slug", "", { shouldDirty: true });
        }
    }, [showCustomEndpoint, methods, setValue]);

    useEffect(() => {
        setConnectionTestResult(null);
        setSubmitErrorMessage(null);
    }, [formValuesJson]);

    const handleTestConnection = useCallback(async () => {
        if (isTestingConnection) return;

        setIsTestingConnection(true);
        setConnectionTestResult(null);

        try {
            const testRequest: GatewayConnectionTestRequest = {
                type: schemaInfo.type,
                values: formValues,
            };
            if (!isNew && initialGatewayToEdit?.id) {
                testRequest.gatewayId = initialGatewayToEdit.id;
            }

            const result = await gatewaysApi.testConnection(testRequest);
            setConnectionTestResult(result);
        } catch (error) {
            const errorMessage = getErrorMessage(error, "Connection test failed");
            setConnectionTestResult({
                success: false,
                message: errorMessage,
            });
        } finally {
            setIsTestingConnection(false);
        }
    }, [schemaInfo.type, formValues, isTestingConnection, isNew, initialGatewayToEdit?.id]);

    const submitWithConnectionGate = useCallback(
        async (data: Gateway) => {
            if (schemaInfo.type === "event_mesh") {
                setIsTestingConnection(true);
                setConnectionTestResult(null);
                let result: { success: boolean; message: string };
                try {
                    const testRequest: GatewayConnectionTestRequest = { type: schemaInfo.type, values: data.values };
                    if (!isNew && initialGatewayToEdit?.id) testRequest.gatewayId = initialGatewayToEdit.id;
                    result = await gatewaysApi.testConnection(testRequest);
                } catch (error) {
                    result = { success: false, message: getErrorMessage(error, "Connection test failed") };
                } finally {
                    setIsTestingConnection(false);
                }
                setConnectionTestResult(result);
                if (!result.success) return;
            }
            onSubmitWithDirtyTracking(data);
        },
        [schemaInfo.type, isNew, initialGatewayToEdit?.id, onSubmitWithDirtyTracking]
    );

    const shouldShowTestConnection = useMemo(() => {
        return schemaInfo.type === "event_mesh" || schemaInfo.type === "email";
    }, [schemaInfo.type]);

    const connectionTestDestinationIsChanged = !isNew && connectionTestDestinationChanged(schemaInfo.type, initialGatewayToEdit?.values, formValues);

    const isTestConnectionEnabled = useMemo(() => {
        const secretSatisfied = (field: string, typedValue: unknown) => !!typedValue || (fieldsWithStoredSecrets.has(`values.${field}`) && !connectionTestDestinationIsChanged);
        if (schemaInfo.type === "email") {
            const mailbox = formValues?.mailbox;
            const imapHost = formValues?.imap_host;
            const authMode = formValues?.auth_mode;
            if (!mailbox || !imapHost) return false;
            if (authMode === "oauth2") {
                return !!formValues?.oauth2_tenant_id && !!formValues?.oauth2_client_id && secretSatisfied("oauth2_client_secret", formValues?.oauth2_client_secret);
            }
            return secretSatisfied("imap_password", formValues?.imap_password);
        }
        const brokerUrl = formValues?.broker_url;
        const brokerVpn = formValues?.broker_vpn;
        const brokerUsername = formValues?.broker_username;

        return !!brokerUrl && !!brokerVpn && !!brokerUsername && secretSatisfied("broker_password", formValues?.broker_password);
    }, [
        schemaInfo.type,
        fieldsWithStoredSecrets,
        connectionTestDestinationIsChanged,
        formValues?.broker_url,
        formValues?.broker_vpn,
        formValues?.broker_username,
        formValues?.broker_password,
        formValues?.mailbox,
        formValues?.imap_host,
        formValues?.auth_mode,
        formValues?.imap_password,
        formValues?.oauth2_tenant_id,
        formValues?.oauth2_client_id,
        formValues?.oauth2_client_secret,
    ]);

    const shouldDisplayField = (fieldConfig: GatewayFieldSchema): boolean => matchesConditionalDisplay(fieldConfig.conditionalDisplay, formValues);

    const isConditionalFieldActive = (fieldConfig: GatewayFieldSchema): boolean => {
        return !!fieldConfig.required;
    };

    const renderValuesField = ([fieldName, fieldConfig]: [string, GatewayFieldSchema], sideBySide: boolean) => {
        const fieldPath = `values.${fieldName}`;
        const fieldError = errors.values && typeof errors.values === "object" ? (errors.values as Record<string, any>)[fieldName] : undefined;

        const fieldOptsIntoTestConnection = fieldName === "broker_password" || fieldConfig.showTestConnection === true;
        const showTestConnectionForField = fieldOptsIntoTestConnection && shouldShowTestConnection;

        const showTemplateLookupForField = fieldConfig.showTemplateLookup === true;

        return (
            <GatewayFormField
                key={fieldPath}
                name={fieldPath}
                label={fieldConfig.label}
                type={fieldConfig.type as any}
                required={isConditionalFieldActive(fieldConfig)}
                hint={fieldConfig.hint}
                options={fieldConfig.options}
                itemKey={fieldConfig.itemKey}
                itemLabel={fieldConfig.itemLabel}
                placeholder={fieldConfig.placeholder}
                fillWidth={sideBySide}
                register={register as any}
                control={control as any}
                error={fieldError}
                showTestConnection={showTestConnectionForField}
                onTestConnection={fieldOptsIntoTestConnection ? handleTestConnection : undefined}
                isTestingConnection={isTestingConnection}
                connectionTestResult={connectionTestResult}
                onDismissConnectionTest={() => setConnectionTestResult(null)}
                isTestConnectionEnabled={isTestConnectionEnabled}
                storedSecretNeedsReentry={connectionTestDestinationIsChanged && isConnectionTestCredential(schemaInfo.type, fieldName)}
                showTemplateLookup={showTemplateLookupForField}
                onTemplateLookup={showTemplateLookupForField ? () => setIsTemplateLookupOpen(true) : undefined}
            />
        );
    };

    const prerequisitesDocsLink = PREREQUISITE_DOCS_LINKS[schemaInfo.type];

    const renderInlinePrerequisites = () => {
        if (!prerequisitesDocsLink || !schemaInfo.warning) return null;
        return (
            <p className="mb-2 text-sm" data-testid="entrypointPrerequisitesWarning">
                {schemaInfo.warning}{" "}
                <a href={prerequisitesDocsLink.href} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-semibold text-(--primary-wMain) hover:underline">
                    {prerequisitesDocsLink.label} <ExternalLink className="size-4" />
                </a>
            </p>
        );
    };

    const renderFieldRow = (row: FieldEntry[]) => {
        if (row.length === 1) return renderValuesField(row[0], false);
        return (
            <div key={row[0][0]} className="grid grid-cols-1 gap-4 md:grid-cols-2" data-testid={`fieldRow-${row[0][1].rowGroup}`}>
                {row.map(field => renderValuesField(field, true))}
            </div>
        );
    };

    const renderValuesFields = () => {
        const configFields = schemaInfo.schema;
        const visibleFields = sortFieldsByOrder(configFields).filter(([_, fieldConfig]) => shouldDisplayField(fieldConfig));

        return groupFieldsIntoSections(visibleFields).map((section, index) => (
            <PageSection key={`${index}-${section.title}`} className={index === 0 ? "mt-8" : "mt-14"}>
                <div className="pb-2.5 text-lg font-semibold">{section.title}</div>
                {index === 0 && renderInlinePrerequisites()}
                <div className="space-y-6">{groupFieldsIntoRows(section.fields).map(renderFieldRow)}</div>
            </PageSection>
        ));
    };

    return (
        <StoredSecretsContext.Provider value={fieldsWithStoredSecrets}>
            <FormProvider {...methods}>
                <div className={PAGE_COMMON_CLASSES}>
                    <form ref={formRef} id="gateway-form" onSubmit={handleSubmit(submitWithConnectionGate, handleInvalidSubmit)}>
                        {schemaInfo.warning && !prerequisitesDocsLink && <MessageBanner variant="info" className="mb-6" message={schemaInfo.warning} role="note" aria-live="off" data-testid="entrypointPrerequisitesWarning" />}
                        <PageSection className="gap-6">
                            <div>
                                <PageLabel className="mb-1">Type</PageLabel>
                                <div>{schemaInfo.name}</div>
                            </div>
                            <GatewayFormField name="name" label="Name" type="string" required register={register as any} error={errors.name as any} />
                            <GatewayFormField name="description" label="Description" type="text" required register={register as any} error={errors.description as any} maxLength={1000} />
                            {showCustomEndpoint &&
                                (isNew ? (
                                    <GatewayFormField
                                        name="slug"
                                        label="Custom endpoint (optional)"
                                        type="string"
                                        register={register as any}
                                        error={errors.slug as any}
                                        hint="Sets the client-facing URL path /gw/<endpoint>. Lowercase letters, digits, and dashes; 3–63 characters. Leave blank to use the entrypoint's generated id. Cannot be changed after creation."
                                    />
                                ) : (
                                    initialGatewayToEdit?.slug && (
                                        <div>
                                            <PageLabel className="mb-1">Custom endpoint</PageLabel>
                                            <div className="font-mono text-sm">{initialGatewayToEdit.slug}</div>
                                        </div>
                                    )
                                ))}
                        </PageSection>
                        {renderValuesFields()}
                        <WhatsAppTemplateDialog
                            open={isTemplateLookupOpen}
                            onOpenChange={setIsTemplateLookupOpen}
                            values={(formValues ?? {}) as Record<string, unknown>}
                            gatewayId={isNew ? undefined : initialGatewayToEdit?.id}
                            onSelect={(name, language) => {
                                setValue("values.contact_request_template" as any, name, { shouldDirty: true });
                                setValue("values.contact_request_template_language" as any, language, { shouldDirty: true });
                            }}
                        />
                        {submitErrorMessage && <MessageBanner variant="error" message={submitErrorMessage} dismissible onDismiss={() => setSubmitErrorMessage(null)} className="mt-4" />}
                        {footer}
                    </form>
                </div>
            </FormProvider>
        </StoredSecretsContext.Provider>
    );
};
