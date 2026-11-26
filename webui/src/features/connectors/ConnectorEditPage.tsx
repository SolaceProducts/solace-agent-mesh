// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps, useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronLeft } from "lucide-react";

import { findConnectorBadge, useCapabilities } from "@/api/capabilities";
import { connectorKeys, useConnectors } from "@/api/connectors";

import { useChatContext } from "@/providers/contexts";

import { MessageBanner } from "@/common/banners";
import { ErrorDialog } from "@/common/dialogs";
import { useNavigationBlocker } from "@/common/dialogs/useNavigationBlocker";
import { Header, PageContentWrapper, PageFooter, PageLayout } from "@/common/layout";
import { EmptyState, LoadingBlocker } from "@/common/status";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { LifecycleBadge } from "@/ui/lifecycleBadge";

import { type Connector, CONNECTOR_TYPE_MAP, type ConnectorSchema, type ConnectorSubType, type ConnectorType } from "@/types";

import { CONNECTOR_WIZARD_STEPS, ConnectorEdit, EVENT_MESH_WIZARD_STEPS } from "./ConnectorEdit";
import { ConnectorIcon } from "./ConnectorIcon";
import { stripEventMeshUiFields } from "./eventMeshUtils";
import { isConnectorWizard } from "./hooks";
import { SCHEMA_TYPE_NAME_MAP } from "./hooks/useConnectorSchemas";
import { ConnectorResourceUploadError, fetchConnector, saveConnector, validateConnector } from "./utils";

type WizardStep = "config" | "tools" | "review";
type WizardNextStep = WizardStep | "save";

const EMPTY_CONNECTORS: Connector[] = [];

function getConnectorButtonText(nextStep?: WizardNextStep, isNew?: boolean, isWizard?: boolean): string {
    if (!isWizard) {
        return isNew ? "Create" : "Save";
    }

    const stepTitle = CONNECTOR_WIZARD_STEPS.find(step => step.id === nextStep)?.title;

    if (stepTitle) {
        return `Next: ${stepTitle}`;
    }

    return isNew ? "Create" : "Save";
}

function buildConnectorTitle(isNew: boolean | undefined, connectorType: ConnectorType | undefined, connectorName: string | undefined): string {
    if (!isNew) return `Edit ${connectorName ?? "Connector"}`;
    const label = connectorType ? (SCHEMA_TYPE_NAME_MAP[CONNECTOR_TYPE_MAP[connectorType]] ?? "") : "";
    return label ? `Create ${label} Connector` : "Create Connector";
}

interface ConnectorLoaderInput {
    isNew?: boolean;
    connectorId?: string;
    type?: string;
    subtype?: string;
    initialConnectorToEdit: Connector | null;
    connectors: Connector[];
    connectorsFetched: boolean;
    getConnectorSchema: (type: string, subtype: string) => ConnectorSchema | undefined;
}

function resolveConnectorBase({ isNew, connectorId, type, subtype, initialConnectorToEdit, connectors, connectorsFetched }: ConnectorLoaderInput): Connector | null | "pending" {
    if (isNew) return { id: "new", name: "", description: "", type: type as ConnectorType, subtype: subtype as ConnectorSubType, values: {} };
    if (initialConnectorToEdit) return initialConnectorToEdit;
    if (!connectorId) return null;
    const found = connectors.find(c => c.id === connectorId) ?? null;
    if (!found && !connectorsFetched) return "pending";
    return found;
}

function useConnectorLoader(input: ConnectorLoaderInput) {
    const { getConnectorSchema } = input;
    const [connectorToEdit, setConnectorToEdit] = useState<Connector | null>(null);
    const [schemaInfo, setSchemaInfo] = useState<ConnectorSchema | null>(null);
    const [errorObject, setErrorObject] = useState<{ title: string; message: string } | null>(null);
    const [isLoadingConnector, setIsLoadingConnector] = useState(true);

    const initTarget = `${input.isNew ? "new" : "edit"}|${input.connectorId ?? ""}|${input.type ?? ""}|${input.subtype ?? ""}|${input.initialConnectorToEdit?.id ?? ""}`;
    const initializedTargetRef = useRef<string | null>(null);

    useEffect(() => {
        if (initializedTargetRef.current === initTarget) return;
        const base = resolveConnectorBase(input);
        if (base === "pending") return;

        let cancelled = false;
        const load = async () => {
            setIsLoadingConnector(true);
            if (base) {
                const { connector, error } = await fetchConnector(base);
                if (cancelled) return;
                if (error) setErrorObject({ title: "Failed to Load Connector", message: error });
                if (connector) {
                    setConnectorToEdit(connector);
                    setSchemaInfo(getConnectorSchema(connector.type, connector?.subtype) ?? null);
                }
            } else {
                setConnectorToEdit(null);
                setSchemaInfo(null);
            }
            if (cancelled) return;
            initializedTargetRef.current = initTarget;
            setIsLoadingConnector(false);
        };
        void load();
        return () => {
            cancelled = true;
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [initTarget, input.connectors, input.connectorsFetched]);

    return { connectorToEdit, setConnectorToEdit, schemaInfo, errorObject, setErrorObject, isLoadingConnector };
}

interface ConnectorEditTitleProps {
    schemaInfo: ConnectorSchema | null;
    titleText: string;
    lifecycleBadge?: string | null;
}

function ConnectorEditTitle({ schemaInfo, titleText, lifecycleBadge }: ConnectorEditTitleProps) {
    return (
        <div className="flex min-w-0 items-center gap-3">
            {schemaInfo?.icon && <ConnectorIcon icon={schemaInfo.icon} name={schemaInfo.name} size="sm" />}
            <span className="min-w-0 truncate font-medium">{titleText}</span>
            {lifecycleBadge && <LifecycleBadge>{lifecycleBadge}</LifecycleBadge>}
        </div>
    );
}

interface ConnectorEditContentProps {
    isLoadingConnector: boolean;
    connectorToEdit: Connector | null;
    schemaInfo: ConnectorSchema | null;
    isNew?: boolean;
    isWizard: boolean;
    connectorEditProps: ComponentProps<typeof ConnectorEdit>;
    errorObject: { title: string; message: string } | null;
    onCloseError: () => void;
    onGoToConnectors: () => void;
}

function ConnectorEditContent({ isLoadingConnector, connectorToEdit, schemaInfo, isNew, isWizard, connectorEditProps, errorObject, onCloseError, onGoToConnectors }: ConnectorEditContentProps) {
    if (isLoadingConnector) {
        return <LoadingBlocker isLoading={true} />;
    }

    if (!connectorToEdit || !schemaInfo) {
        return <EmptyState data-testid="connectorTypeNotFound" variant="error" title="Connector Type Not Found" buttons={[{ text: "Go To Connectors", variant: "default", onClick: onGoToConnectors }]} />;
    }

    return (
        <>
            {!isNew && (connectorToEdit.agentCount ?? 0) > 0 && (
                <MessageBanner
                    variant="info"
                    message={
                        <span>
                            This connector is used by <span className="font-semibold">{connectorToEdit.agentCount} agent(s)</span>. Saving changes will trigger a redeployment for any associated agents that are currently deployed.
                        </span>
                    }
                />
            )}
            {isWizard ? (
                <div className="min-h-0 flex-1 overflow-y-auto overscroll-y-contain">
                    <ConnectorEdit {...connectorEditProps} />
                </div>
            ) : (
                <PageContentWrapper>
                    <ConnectorEdit {...connectorEditProps} />
                </PageContentWrapper>
            )}
            {errorObject ? <ErrorDialog title={errorObject.title} open={!!errorObject} error={errorObject.message} errorSource="api" onOpenChange={open => !open && onCloseError()} /> : null}
        </>
    );
}

interface ConnectorEditFooterProps {
    isLoadingConnector: boolean;
    isWizard: boolean;
    currentWizardStep: WizardStep;
    connectorType?: string;
    nextStep: WizardNextStep;
    isNew?: boolean;
    isLoading: boolean;
    canSave: boolean;
    onBack: () => void;
    onCancel: () => void;
}

function ConnectorEditFooter({ isLoadingConnector, isWizard, currentWizardStep, connectorType, nextStep, isNew, isLoading, canSave, onBack, onCancel }: ConnectorEditFooterProps) {
    if (isLoadingConnector) return null;

    const steps = connectorType === "event_mesh" ? EVENT_MESH_WIZARD_STEPS : CONNECTOR_WIZARD_STEPS;
    const backStepIndex = steps.findIndex(s => s.id === currentWizardStep) - 1;
    const backStepLabel = backStepIndex >= 0 ? `Back: ${steps[backStepIndex].title}` : "Back";
    const showBackButton = isWizard && (currentWizardStep === "tools" || currentWizardStep === "review");

    return (
        <PageFooter className={showBackButton ? "!justify-between" : ""}>
            {showBackButton && (
                <Button data-testid="connectorBack" variant="ghost" onClick={onBack} disabled={isLoading} className="h-8 gap-2 !pr-4 !pl-1 text-sm font-medium text-(--primary-wMain)">
                    <ChevronLeft className="size-6 text-(--primary-wMain)" />
                    {backStepLabel}
                </Button>
            )}
            <div className="flex gap-2">
                <Button data-testid="connectorCancel" title={isNew ? "Cancel" : "Discard Changes"} variant="ghost" onClick={onCancel} className="h-8 !px-4 text-sm font-medium text-(--primary-wMain)">
                    {isNew ? "Cancel" : "Discard Changes"}
                </Button>
                <Button data-testid="saveConnectorButton" type="submit" form="connector-form" disabled={isLoading || !canSave}>
                    {getConnectorButtonText(nextStep, isNew, isWizard)}
                </Button>
            </div>
        </PageFooter>
    );
}

export const ConnectorEditPage = () => {
    const { navigate, useLoaderData, connectorToEdit: initialConnectorToEdit, setConnectorToEdit: setInitialConnectorToEdit, addNotification, getConnectorSchema } = useChatContext();
    const { data: connectors = EMPTY_CONNECTORS, isFetched: connectorsFetched } = useConnectors();
    const queryClient = useQueryClient();
    const { data: capabilities } = useCapabilities();
    const { connectorId, isNew, type, subtype } = useLoaderData();

    const { connectorToEdit, setConnectorToEdit, schemaInfo, errorObject, setErrorObject, isLoadingConnector } = useConnectorLoader({
        isNew,
        connectorId,
        type,
        subtype,
        initialConnectorToEdit,
        connectors,
        connectorsFetched,
        getConnectorSchema,
    });
    const [validationError, setValidationError] = useState<string | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const { allowNavigation, NavigationBlocker, setBlockingEnabled } = useNavigationBlocker();

    const [nextStep, setNextStep] = useState<WizardNextStep>("save");
    const [currentWizardStep, setCurrentWizardStep] = useState<WizardStep>("config");

    const handleSuccess = useCallback(
        (message: string) => {
            setInitialConnectorToEdit(null);
            setIsLoading(false);

            setBlockingEnabled(false);
            allowNavigation(() => {
                navigate("/connectors", { replace: true });
                addNotification(message);
            });
        },
        [navigate, addNotification, allowNavigation, setInitialConnectorToEdit, setBlockingEnabled]
    );

    const notifyWarnings = (warnings: readonly string[]) => {
        for (const warning of warnings) {
            addNotification(warning, "warning");
        }
    };

    const executeSave = async (updatedConnector: Connector & { resources?: FileList; _isValidationStep?: boolean }) => {
        updatedConnector.type = type || connectorToEdit?.type || updatedConnector.type;
        updatedConnector.subtype = subtype || connectorToEdit?.subtype || updatedConnector.subtype;

        if (updatedConnector.type === "event_mesh" && updatedConnector.values) {
            updatedConnector.values = stripEventMeshUiFields(updatedConnector.values);
        }

        setIsLoading(true);
        setValidationError(null);

        try {
            const typeResult = await validateConnector(updatedConnector, updatedConnector._isValidationStep);

            if (!typeResult.success) {
                setValidationError(typeResult.error!);
                return;
            }

            if (typeResult.updatedConnector) {
                setConnectorToEdit(typeResult.updatedConnector);
            }

            if (typeResult.nextStep) {
                setNextStep(typeResult.nextStep);

                if (typeResult.nextStep !== "save") {
                    return;
                }
            }

            const connectorToSave = typeResult.updatedConnector || updatedConnector;
            const saved = await saveConnector(connectorToSave, isNew, connectorId);
            notifyWarnings(saved.warnings);

            await queryClient.invalidateQueries({ queryKey: connectorKeys.lists() });
            handleSuccess(isNew ? "Connector created successfully" : "Connector updated successfully");
        } catch (error) {
            if (error instanceof ConnectorResourceUploadError) {
                notifyWarnings(error.warnings);
            }
            const errorMessage = getErrorMessage(error, "An error occurred. Please try again.");
            const title = isNew ? "Connector Creation Failed" : "Connector Update Failed";
            setErrorObject({ title, message: errorMessage });
        } finally {
            setIsLoading(false);
        }
    };

    const handleSave = async (updatedConnector: Connector & { resources?: FileList; _isValidationStep?: boolean }) => {
        await executeSave(updatedConnector);
    };

    const handleCancel = useCallback(() => {
        allowNavigation(() => {
            if (isNew) {
                navigate("/connectors");
            } else {
                navigate(-1);
            }
            setInitialConnectorToEdit(null);
        });
    }, [isNew, navigate, allowNavigation, setInitialConnectorToEdit]);

    const handleWizardStepChange = useCallback((step: WizardStep) => {
        setCurrentWizardStep(step);
        const stepToNextActionMap: Record<WizardStep, WizardNextStep> = {
            config: "tools",
            tools: "review",
            review: "save",
        };
        setNextStep(stepToNextActionMap[step]);
    }, []);

    const wizardStepBackRef = useRef<(() => void) | null>(null);
    const handleBack = useCallback(() => {
        if (wizardStepBackRef.current) {
            wizardStepBackRef.current();
        }
    }, []);

    const isWizard = schemaInfo ? isConnectorWizard(schemaInfo) : false;
    const connectorType = (type ?? connectorToEdit?.type) as ConnectorType | undefined;
    const titleText = buildConnectorTitle(isNew, connectorType, connectorToEdit?.name);
    const lifecycleBadge = findConnectorBadge(capabilities, schemaInfo?.type ?? "", schemaInfo?.experimental ?? false, schemaInfo?.subtype);

    const connectorEditProps = {
        schemaInfo: schemaInfo!,
        initialConnectorToEdit: connectorToEdit!,
        isNew,
        existingConnectors: connectors,
        onSubmit: handleSave,
        onWizardStepChange: handleWizardStepChange,
        onExposeBackHandler: (backFn: () => void) => {
            wizardStepBackRef.current = backFn;
        },
        validationError,
        onDirtyStateChange: setBlockingEnabled,
        onClearValidationError: () => setValidationError(null),
    };

    return (
        <PageLayout>
            <Header
                title={<ConnectorEditTitle schemaInfo={schemaInfo} titleText={titleText} lifecycleBadge={lifecycleBadge} />}
                breadcrumbs={[
                    {
                        label: "Connectors",
                        onClick: () => navigate("/connectors"),
                    },
                    { label: titleText },
                ]}
            />
            <ConnectorEditContent
                isLoadingConnector={isLoadingConnector}
                connectorToEdit={connectorToEdit}
                schemaInfo={schemaInfo}
                isNew={isNew}
                isWizard={isWizard}
                connectorEditProps={connectorEditProps}
                errorObject={errorObject}
                onCloseError={() => setErrorObject(null)}
                onGoToConnectors={() => navigate("/connectors")}
            />
            <NavigationBlocker />
            <LoadingBlocker isLoading={isLoading} />
            <ConnectorEditFooter
                isLoadingConnector={isLoadingConnector}
                isWizard={isWizard}
                currentWizardStep={currentWizardStep}
                connectorType={connectorToEdit?.type}
                nextStep={nextStep}
                isNew={isNew}
                isLoading={isLoading}
                canSave={!!connectorToEdit && !!schemaInfo}
                onBack={handleBack}
                onCancel={handleCancel}
            />
        </PageLayout>
    );
};
