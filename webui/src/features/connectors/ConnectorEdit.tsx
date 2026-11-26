// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type JSX, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { ExternalLink } from "lucide-react";
import { type FieldErrors, FormProvider, useForm } from "react-hook-form";

import { connectorsApi } from "@/api/connectors/versioned";

import { useSecretAwareSubmit } from "@/hooks/useSecretAwareSubmit";
import { StoredSecretsContext, useStoredSecrets, useTrackStoredSecrets } from "@/hooks/useStoredSecrets";

import { LearningBanner, MessageBanner } from "@/common/banners";
import { PAGE_COMMON_CLASSES, PageLabel, PageSection } from "@/common/layout";
import { trackFormErrors } from "@/common/product-analytics/trackFormErrors";

import { getErrorMessage } from "@/utils/api";
import { matchesConditionalDisplay } from "@/utils/conditionalDisplay";
import { DOC_LINKS } from "@/utils/docs";
import { processBackendValuesForForm } from "@/utils/formDefaults";

import { Button } from "@/ui/button";
import { defineStepper } from "@/ui/stepper";

import { ConnectorAllowListField, ConnectorAuthTypeField, ConnectorBaseUrlField, ConnectorHeadersField, ConnectorOpenAPIField, ConnectorParametersField } from "@/features/connectors/fields";
import { isAPIConnectorWizard, isConnectorWizard, isEventMeshConnectorWizard, isMCPConnectorWizard, useAPIConnectorWizard, useEventMeshConnectorWizard, useMCPConnectorWizard } from "@/features/connectors/hooks";
import { baseConnectorSchema, createFullConnectorSchemaValidator, sortFieldsByOrder } from "@/features/connectors/utils";

import type { Connector, ConnectorFieldSchema, ConnectorSchema } from "@/types";

import { APIConnectorReview } from "./APIConnectorReview";
import { ConnectorFormField } from "./ConnectorFormField";
import { ConnectorIcon } from "./ConnectorIcon";
import { ConnectorToolSelection } from "./ConnectorToolSelection";
import { EventMeshConnectorReview } from "./EventMeshConnectorReview";
import { EventMeshConnectorWorkspace } from "./EventMeshConnectorWorkspace";
import { EVENT_MESH_SECTION_TITLE } from "./eventMeshStyles";
import { createEventMeshConnectorSchema, normalizeEventMeshValues } from "./eventMeshUtils";
import { MCPConnectorReview } from "./MCPConnectorReview";

export { useStoredSecrets };

type WizardStep = "config" | "tools" | "review";

const WIZARD_STEP_ORDER: WizardStep[] = ["config", "tools", "review"];

export const CONNECTOR_WIZARD_STEPS = [
    { id: "config" as const, title: "Configure Connector" },
    { id: "tools" as const, title: "Select Tools" },
    { id: "review" as const, title: "Review Summary" },
] as const;

const { Stepper: ConnectorStepper } = defineStepper(...CONNECTOR_WIZARD_STEPS);

export const EVENT_MESH_WIZARD_STEPS = [
    { id: "config" as const, title: "Event Broker Credentials" },
    { id: "tools" as const, title: "Tool Configuration" },
    { id: "review" as const, title: "Summary" },
] as const;

const { Stepper: EventMeshStepper } = defineStepper(...EVENT_MESH_WIZARD_STEPS);

const KV_SEED = [{ key: "", value: "" }];

function normaliseKeyValueField(raw: unknown): { key: string; value: unknown }[] {
    if (!raw) return [...KV_SEED];
    if (Array.isArray(raw)) return raw.length > 0 ? raw : [...KV_SEED];
    if (typeof raw === "object") {
        const pairs = Object.entries(raw as Record<string, unknown>).map(([key, value]) => ({ key, value }));
        return pairs.length > 0 ? pairs : [...KV_SEED];
    }
    return [...KV_SEED];
}

function buildNewConnectorValues(schemaInfo: ConnectorSchema | undefined): Record<string, any> {
    const values: Record<string, any> = {};
    if (!schemaInfo?.fieldSchema) return values;
    for (const [fieldName, fieldConfig] of Object.entries(schemaInfo.fieldSchema)) {
        if (fieldConfig.default !== undefined) {
            values[fieldName] = fieldConfig.default;
        }
        if (fieldConfig.type === "key_value") {
            values[fieldName] = values[fieldName] ?? [...KV_SEED];
        }
        if ((fieldConfig.type === "string" || fieldConfig.type === "password") && values[fieldName] === undefined) {
            values[fieldName] = "";
        }
    }
    return values;
}

function buildEditConnectorValues(initial: Connector | undefined, schemaInfo: ConnectorSchema | undefined): Record<string, any> {
    if (!schemaInfo?.fieldSchema || !initial?.values) return {};
    const values = processBackendValuesForForm(initial.values, schemaInfo.fieldSchema);
    if (Array.isArray(values.manifest)) {
        values.manifest = JSON.stringify(values.manifest);
    }
    for (const [fieldName, fieldConfig] of Object.entries(schemaInfo.fieldSchema)) {
        if (fieldConfig.type === "key_value") {
            values[fieldName] = normaliseKeyValueField(values[fieldName]);
        }
    }
    return values;
}

function buildConnectorDefaultValues(isNew: boolean | undefined, schemaInfo: ConnectorSchema | undefined, initial: Connector | undefined): any {
    const values = isNew ? buildNewConnectorValues(schemaInfo) : buildEditConnectorValues(initial, schemaInfo);
    const resolvedValues = schemaInfo?.type === "event_mesh" ? normalizeEventMeshValues(values) : values;
    if (isNew) {
        return { name: "", description: "", values: resolvedValues };
    }
    return { ...initial, values: resolvedValues };
}

interface ToolSelectionConfig {
    fieldPath: string;
    description: string;
}

const MCP_TOOL_CONFIG: ToolSelectionConfig = {
    fieldPath: "values.selected_tools",
    description: "Choose which MCP tools to enable for this connector.",
};

const API_TOOL_CONFIG: ToolSelectionConfig = {
    fieldPath: "values.allow_list",
    description: "Choose which API operations to enable for this connector.",
};

export interface FieldProps {
    fieldPath: string;
    fieldConfig: ConnectorFieldSchema;
    fieldError: any;
    wizardStep?: WizardStep;
    isAPIWizard?: boolean;
    isNew?: boolean;
}

const FIELD_OVERRIDES: Record<string, (fieldProps: FieldProps) => JSX.Element | null> = {
    specification_url: () => null,
    file_s3_key: ({ fieldPath, isNew }) => <ConnectorOpenAPIField key={fieldPath} isNew={isNew} />,
    base_url: ({ fieldPath, fieldConfig, fieldError }) => <ConnectorBaseUrlField key={fieldPath} fieldPath={fieldPath} fieldConfig={fieldConfig} fieldError={fieldError} />,
    allow_list: ({ fieldPath, wizardStep, isAPIWizard }) => {
        if (isAPIWizard && wizardStep === "config") {
            return null;
        }
        return <ConnectorAllowListField key={fieldPath} fieldPath={fieldPath} />;
    },
    auth_type: ({ fieldPath, fieldConfig, fieldError }) => <ConnectorAuthTypeField key={fieldPath} name={fieldPath} fieldConfig={fieldConfig} error={fieldError} />,
    custom_headers: ({ fieldPath, fieldConfig, fieldError }) => <ConnectorHeadersField key={fieldPath} fieldPath={fieldPath} fieldConfig={fieldConfig} fieldError={fieldError} />,
    parameters: ({ fieldPath, fieldConfig, fieldError }) => <ConnectorParametersField key={fieldPath} fieldPath={fieldPath} fieldConfig={fieldConfig} fieldError={fieldError} />,
} as const;

export interface ConnectorEditProps {
    schemaInfo: ConnectorSchema;
    initialConnectorToEdit?: Connector;
    isNew?: boolean;
    existingConnectors?: Connector[];
    onSubmit: (data: any) => void;
    onWizardStepChange?: (step: WizardStep) => void;
    onExposeBackHandler?: (backFn: () => void) => void;
    validationError?: string | null;
    onDirtyStateChange?: (isDirty: boolean) => void;
    onClearValidationError?: () => void;
}

export const ConnectorEdit = ({ schemaInfo, initialConnectorToEdit, isNew, existingConnectors, onSubmit, onWizardStepChange, onExposeBackHandler, validationError, onDirtyStateChange, onClearValidationError }: ConnectorEditProps) => {
    const fieldsWithStoredSecrets = useTrackStoredSecrets(initialConnectorToEdit?.values);

    const defaultValues = useMemo(() => buildConnectorDefaultValues(isNew, schemaInfo, initialConnectorToEdit), [isNew, schemaInfo, initialConnectorToEdit]);

    const wizardStepRef = useRef<WizardStep>("config");

    const dynamicResolver = useMemo(() => {
        return async (data: any, context: any, options: any) => {
            const currentFormValues = data.values || {};

            const schema = (() => {
                if (schemaInfo?.type === "event_mesh") return createEventMeshConnectorSchema(isNew === false, wizardStepRef.current !== "config");
                if (schemaInfo) return createFullConnectorSchemaValidator(schemaInfo, currentFormValues, !isNew);
                return baseConnectorSchema;
            })();

            return zodResolver(schema)(data, context, options);
        };
    }, [schemaInfo, isNew]);

    const methods = useForm({
        resolver: dynamicResolver,
        defaultValues: defaultValues,
        mode: "onSubmit",
        reValidateMode: "onChange",
    });

    const {
        formState: { errors, isDirty, dirtyFields, touchedFields },
        watch,
        getValues,
        setValue,
        setError,
        clearErrors,
    } = methods;

    useEffect(() => {
        onDirtyStateChange?.(isNew ? true : isDirty);
    }, [isDirty, onDirtyStateChange]);

    const formValues = watch("values");

    const onSubmitWithDirtyTracking = useSecretAwareSubmit({
        onSubmit,
        isNew,
        formState: { dirtyFields, touchedFields },
        fieldsWithStoredSecrets,
        getFieldSchema: (fieldName: string) => schemaInfo.fieldSchema?.[fieldName],
    });

    const mcpWizard = useMCPConnectorWizard({
        schemaInfo,
        initialConnectorToEdit,
        formValues,
        getValues,
        setValue,
        setError,
        clearErrors,
        onSubmit: onSubmitWithDirtyTracking,
        onWizardStepChange,
        onClearValidationError,
    });

    const apiWizard = useAPIConnectorWizard({
        schemaInfo,
        getValues,
        setError,
        clearErrors,
        onSubmit: onSubmitWithDirtyTracking,
        onWizardStepChange,
        onClearValidationError,
        existingConnectors,
        isNew,
        currentConnectorId: initialConnectorToEdit?.id,
    });

    const [isTesting, setIsTesting] = useState(false);
    const [connectionResult, setConnectionResult] = useState<{ success: boolean; message: string } | null>(null);

    const testConnection = async (): Promise<boolean> => {
        setIsTesting(true);
        setConnectionResult(null);
        try {
            const values = getValues("values");
            const response = await connectorsApi.testConnection({
                connectorId: initialConnectorToEdit?.id === "new" ? undefined : initialConnectorToEdit?.id,
                values: {
                    broker_url: values.broker_url,
                    broker_vpn: values.broker_vpn,
                    broker_username: values.broker_username,
                    broker_password: values.broker_password,
                },
            });
            setConnectionResult({ success: response.success, message: response.message || (response.success ? "Connection successful" : "Connection failed") });
            return response.success;
        } catch (error) {
            setConnectionResult({ success: false, message: getErrorMessage(error, "Connection test failed") });
            return false;
        } finally {
            setIsTesting(false);
        }
    };

    const eventMeshWizard = useEventMeshConnectorWizard({
        schemaInfo,
        onSubmit: onSubmitWithDirtyTracking,
        onWizardStepChange,
    });

    const isMCPWizard = isMCPConnectorWizard(schemaInfo);
    const isAPIWizard = isAPIConnectorWizard(schemaInfo);
    const isEventMeshWizard = isEventMeshConnectorWizard(schemaInfo);
    const isWizardActive = isConnectorWizard(schemaInfo);

    const activeWizard = (() => {
        if (isMCPWizard) return mcpWizard;
        if (isAPIWizard) return apiWizard;
        return eventMeshWizard;
    })();
    const { wizardStep, setWizardStep, handleNextOrCreate } = activeWizard;
    wizardStepRef.current = wizardStep;

    const [toolErrorsVisible, setToolErrorsVisible] = useState(false);
    const handleInvalidSubmit = useCallback((formErrors: FieldErrors) => {
        trackFormErrors(formErrors, "connector");
        if (wizardStepRef.current === "tools") setToolErrorsVisible(true);
    }, []);

    const handleFormSubmit = async (data: any) => {
        if (isEventMeshWizard && wizardStep === "config") {
            const connected = await testConnection();
            if (!connected) return;
        }
        handleNextOrCreate(data);
    };

    const isAuthenticating = isMCPWizard && mcpWizard.isAuthenticating;
    const isDiscovering = isMCPWizard && mcpWizard.isDiscovering;
    const oauthError = isMCPWizard ? mcpWizard.oauthError : undefined;

    useEffect(() => {
        if (onExposeBackHandler) {
            const handleBack = () => {
                if (wizardStep === "tools") {
                    setWizardStep("config");
                } else if (wizardStep === "review") {
                    setWizardStep("tools");
                }
            };
            onExposeBackHandler(handleBack);
        }
    }, [onExposeBackHandler, wizardStep, setWizardStep]);

    const renderField = (field: [string, ConnectorFieldSchema]): JSX.Element | null => {
        const [fieldName, fieldConfig] = field;
        const fieldPath = `values.${fieldName}`;
        const fieldError = (errors.values as any)?.[fieldName];

        if (FIELD_OVERRIDES[fieldName]) {
            return FIELD_OVERRIDES[fieldName]({
                fieldPath,
                fieldConfig,
                fieldError,
                wizardStep,
                isAPIWizard,
                isNew,
            });
        }

        return <ConnectorFormField key={fieldPath} name={fieldPath} fieldConfig={fieldConfig} error={fieldError} />;
    };

    const renderValuesFields = (configFields: Array<[string, ConnectorFieldSchema]>) => {
        const shouldDisplayField = (fieldConfig: ConnectorFieldSchema): boolean => !fieldConfig.hidden && matchesConditionalDisplay(fieldConfig.conditionalDisplay, formValues);

        return (
            <PageSection className="mt-10">
                <div className="font-semibold">Connector Configuration</div>
                <div className="space-y-4">{configFields.filter(([_, fieldConfig]) => shouldDisplayField(fieldConfig)).map(renderField)}</div>
            </PageSection>
        );
    };

    const stepperMethodsRef = useRef<{ goTo: (stepId: WizardStep) => void } | null>(null);

    useEffect(() => {
        if (stepperMethodsRef.current && isWizardActive) {
            stepperMethodsRef.current.goTo(wizardStep);
        }
    }, [wizardStep, isWizardActive]);

    const [maxReachedStepIndex, setMaxReachedStepIndex] = useState(0);
    useEffect(() => {
        setMaxReachedStepIndex(prev => Math.max(prev, WIZARD_STEP_ORDER.indexOf(wizardStep)));
    }, [wizardStep]);

    const goToReachedStep = (target: WizardStep) => {
        const targetIndex = WIZARD_STEP_ORDER.indexOf(target);
        if (targetIndex !== WIZARD_STEP_ORDER.indexOf(wizardStep) && targetIndex <= maxReachedStepIndex) {
            setWizardStep(target);
        }
    };

    const configFields = sortFieldsByOrder(schemaInfo.fieldSchema || {});

    const renderConfigStep = () => (
        <>
            {validationError && <MessageBanner variant="error" message={validationError} className="mb-6" />}

            {schemaInfo.warning && <MessageBanner variant="info" className="mb-6" message={schemaInfo.warning} />}

            <PageSection className="gap-4">
                <ConnectorFormField name="name" fieldConfig={{ label: "Connector Name", required: true, type: "text" }} error={errors.name} />
                <div>
                    <PageLabel className="mb-1">Type</PageLabel>
                    <div>{schemaInfo.name}</div>
                </div>
                {configFields.filter(([_, field]) => (field?.order ?? 0) < 0).map(renderField)}
                <ConnectorFormField name="description" fieldConfig={{ label: "Description", hint: "What does this connector do?", type: "textarea", required: true, maxLength: 1000 }} error={errors.description} />
            </PageSection>
            {renderValuesFields(configFields.filter(([_, field]) => (field?.order ?? 0) >= 0))}
        </>
    );

    const renderStepperHeader = () => (
        <div className="sticky top-0 z-10 mb-6 bg-(--background-w10) px-8 pt-8 pb-4">
            <ConnectorStepper.Navigation>
                <ConnectorStepper.Step of="config" onClick={() => goToReachedStep("config")}>
                    <ConnectorStepper.Title>Configure Connector</ConnectorStepper.Title>
                </ConnectorStepper.Step>
                <ConnectorStepper.Step of="tools" onClick={() => goToReachedStep("tools")}>
                    <ConnectorStepper.Title>Select Tools</ConnectorStepper.Title>
                </ConnectorStepper.Step>
                <ConnectorStepper.Step of="review" onClick={() => goToReachedStep("review")}>
                    <ConnectorStepper.Title>Review Summary</ConnectorStepper.Title>
                </ConnectorStepper.Step>
            </ConnectorStepper.Navigation>
        </div>
    );

    const renderWizardForm = (StepperPanel: JSX.Element, fullWidth = false) => (
        <form id="connector-form" className={`${fullWidth ? "w-full leading-[21px]" : PAGE_COMMON_CLASSES} px-8 pb-8`} onSubmit={methods.handleSubmit(handleFormSubmit, handleInvalidSubmit)}>
            {isMCPWizard && isAuthenticating && <MessageBanner variant="info" message="Authorizing... Please complete the authorization in the popup window." className="mb-4" />}
            {isMCPWizard && isDiscovering && <MessageBanner variant="info" message="Discovering tools from MCP server..." className="mb-4" />}
            {isMCPWizard && oauthError && <MessageBanner variant="error" message={oauthError} className="mb-4" />}

            {StepperPanel}
        </form>
    );

    const renderWizard = (toolConfig: ToolSelectionConfig, reviewComponent: JSX.Element) => (
        <ConnectorStepper.Provider variant="horizontal" labelOrientation="horizontal">
            {({ methods: stepperMethods }) => {
                stepperMethodsRef.current = stepperMethods;

                return (
                    <>
                        {renderStepperHeader()}
                        {renderWizardForm(
                            <ConnectorStepper.Panel>
                                {wizardStep === "config" && renderConfigStep()}
                                {wizardStep === "tools" && <ConnectorToolSelection fieldPath={toolConfig.fieldPath} description={toolConfig.description} />}
                                {wizardStep === "review" && reviewComponent}
                            </ConnectorStepper.Panel>
                        )}
                    </>
                );
            }}
        </ConnectorStepper.Provider>
    );

    const renderWizardContent = () => {
        if (isMCPWizard) {
            return renderWizard(MCP_TOOL_CONFIG, <MCPConnectorReview connectorType={schemaInfo.name} schemaInfo={schemaInfo} />);
        }

        if (isAPIWizard) {
            return renderWizard(API_TOOL_CONFIG, <APIConnectorReview connectorType={schemaInfo.name} schemaInfo={schemaInfo} />);
        }

        if (isEventMeshWizard) {
            const renderEventMeshConfigStep = () => (
                <>
                    {validationError && <MessageBanner variant="error" message={validationError} className="mb-6" />}
                    {schemaInfo.warning && <MessageBanner variant="info" className="mb-6" message={schemaInfo.warning} />}
                    <div className="flex gap-8">
                        <div className="w-full max-w-3xl min-w-0 space-y-8">
                            <div className="space-y-6">
                                <ConnectorFormField name="name" fieldConfig={{ label: "Connector Name", required: true, type: "text" }} error={errors.name} />
                                <div>
                                    <PageLabel className="mb-0.5">Type</PageLabel>
                                    <div className="flex items-center gap-2">
                                        <ConnectorIcon icon={schemaInfo.icon} name={schemaInfo.name} size="sm" />
                                        <div className="font-medium">{schemaInfo.name}</div>
                                    </div>
                                </div>
                                <ConnectorFormField name="description" fieldConfig={{ label: "Description", hint: "What does this connector do?", type: "textarea", required: true, maxLength: 1000 }} error={errors.description} />
                            </div>
                            <div>
                                <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Connector Configuration</div>
                                <div className="space-y-6">
                                    {configFields
                                        .filter(([_, field]) => (field?.order ?? 0) < 10)
                                        .filter(([_, fieldConfig]) => matchesConditionalDisplay(fieldConfig.conditionalDisplay, formValues))
                                        .map(renderField)}
                                </div>
                            </div>
                            <div className="flex items-start gap-3">
                                <Button type="button" variant="outline" onClick={testConnection} disabled={isTesting}>
                                    {isTesting ? "Testing..." : "Test Connection"}
                                </Button>
                                {connectionResult && <MessageBanner variant={connectionResult.success ? "success" : "error"} message={connectionResult.message} />}
                            </div>
                        </div>
                        <aside className="hidden w-[380px] shrink-0 xl:block 2xl:w-[420px]">
                            <LearningBanner title="Select the event broker for the connector" storageKey="event-mesh-broker-config-guide-dismissed" showIcon={false}>
                                <p>Connectors enable AI agents that are part of the agent mesh to publish and subscribe to messages on an event broker.</p>
                                <p className="mt-3">
                                    Select the event broker configured for the agent mesh or select a different event broker in the environment. For a custom event broker connection, enter the connection details for the event broker.
                                </p>
                                <Button type="button" variant="link" className="mt-3 h-auto !p-0 text-sm font-semibold" onClick={() => window.open(DOC_LINKS.eventMeshConnector, "_blank", "noopener,noreferrer")}>
                                    Learn more about Event Mesh Connectors
                                    <ExternalLink className="ml-1 size-3" />
                                </Button>
                            </LearningBanner>
                        </aside>
                    </div>
                </>
            );

            const renderEventMeshToolsStep = () => <EventMeshConnectorWorkspace schemaInfo={schemaInfo} validationError={validationError} onClearValidationError={onClearValidationError} showValidation={toolErrorsVisible} />;

            return (
                <EventMeshStepper.Provider variant="horizontal" labelOrientation="horizontal">
                    {({ methods: stepperMethods }) => {
                        stepperMethodsRef.current = stepperMethods;
                        return (
                            <>
                                <div className={`sticky top-0 z-10 mb-6 bg-(--background-w10) px-8 pt-8 pb-4 ${wizardStep === "review" ? "border-b" : ""}`}>
                                    <EventMeshStepper.Navigation>
                                        <EventMeshStepper.Step of="config" onClick={() => goToReachedStep("config")}>
                                            <EventMeshStepper.Title>Event Broker Credentials</EventMeshStepper.Title>
                                        </EventMeshStepper.Step>
                                        <EventMeshStepper.Step of="tools" onClick={() => goToReachedStep("tools")}>
                                            <EventMeshStepper.Title>Tool Configuration</EventMeshStepper.Title>
                                        </EventMeshStepper.Step>
                                        <EventMeshStepper.Step of="review" onClick={() => goToReachedStep("review")}>
                                            <EventMeshStepper.Title>Summary</EventMeshStepper.Title>
                                        </EventMeshStepper.Step>
                                    </EventMeshStepper.Navigation>
                                </div>
                                {renderWizardForm(
                                    <EventMeshStepper.Panel>
                                        {wizardStep === "config" && renderEventMeshConfigStep()}
                                        {wizardStep === "tools" && renderEventMeshToolsStep()}
                                        {wizardStep === "review" && <EventMeshConnectorReview connectorType={schemaInfo.name} schemaInfo={schemaInfo} />}
                                    </EventMeshStepper.Panel>,
                                    true
                                )}
                            </>
                        );
                    }}
                </EventMeshStepper.Provider>
            );
        }

        return (
            <form id="connector-form" className={PAGE_COMMON_CLASSES} onSubmit={methods.handleSubmit(handleFormSubmit, handleInvalidSubmit)}>
                {renderConfigStep()}
            </form>
        );
    };

    return (
        <StoredSecretsContext.Provider value={fieldsWithStoredSecrets}>
            <FormProvider {...methods}>{renderWizardContent()}</FormProvider>
        </StoredSecretsContext.Provider>
    );
};
