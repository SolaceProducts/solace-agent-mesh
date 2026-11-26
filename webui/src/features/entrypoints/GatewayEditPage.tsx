// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentType, type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { Navigate } from "react-router-dom";

import { findGatewayBadge, useCapabilities } from "@/api/capabilities";
import { gatewaysApi, useGateways } from "@/api/gateways";

import { useChatContext, useConfigContext, useUserContext } from "@/providers/contexts";

import { useNavigationBlocker } from "@/common/dialogs";
import { PageContentWrapper, PageFooter } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { isDeployFailed } from "@/utils/deployment";

import { Button } from "@/ui/button";
import { LifecycleBadge } from "@/ui/lifecycleBadge";
import type { CreateGatewayRequest, Gateway, GatewaySchema } from "@/types/models";

import { EmptyState, ErrorDialog, Header, LoadingBlocker, MessageBanner, PageLayout } from "@/common";

import { ConsumeOnlyEntrypointNotice } from "./ConsumeOnlyEntrypointNotice";
import { supportsCustomEndpoint } from "./customEndpoint";
import { GatewayDeploymentDialog } from "./GatewayDeploymentDialog";
import { GatewayEdit } from "./GatewayEdit";
import { GoogleChatEdit, isHttpsBase } from "./googleChat";
import { GoogleChatSetupBanner } from "./GoogleChatSetupBanner";
import { type DeployConfirmation, useDeployConfirmation } from "./hooks";
import { isConsumeOnlyEventMeshEntrypoint } from "./isConsumeOnlyEventMeshEntrypoint";
import { TeamsGatewaySetupBanner } from "./TeamsGatewaySetupBanner";
import { WhatsAppGatewaySetupBanner } from "./WhatsAppGatewaySetupBanner";

const CREATE_SETUP_BANNERS: Record<string, ComponentType> = {
    google_chat: GoogleChatSetupBanner,
    teams: TeamsGatewaySetupBanner,
    whatsapp: WhatsAppGatewaySetupBanner,
};

const createSetupBanner = (isNew: boolean, type: string | null): ReactNode => {
    const Banner = isNew && type ? CREATE_SETUP_BANNERS[type] : undefined;
    if (!Banner) return undefined;
    return (
        <div className="mt-6">
            <Banner />
        </div>
    );
};

const deployDialogAction = (isNew: boolean, gateway: Gateway): "deploy" | "update" => {
    const wasDeployed = gateway.deploymentStatus === "deployed" || isDeployFailed(gateway.deploymentStatus);
    return !isNew && wasDeployed ? "update" : "deploy";
};

const pageTitle = (isNew: boolean, gateway: Gateway | null): string => (isNew ? "Create Entrypoint" : `Edit ${gateway?.name ?? "Entrypoint"}`);

function PageTitle({ title, badge }: Readonly<{ title: string; badge: ReturnType<typeof findGatewayBadge> }>) {
    if (!badge) return <>{title}</>;
    return (
        <span className="flex items-center gap-2">
            {title}
            <LifecycleBadge>{badge}</LifecycleBadge>
        </span>
    );
}

const createBlocked = (schemaInfo: GatewaySchema | null, isNew: boolean, googleChatEndpointBase: string | undefined): boolean => schemaInfo?.type === "google_chat" && isNew && !isHttpsBase(googleChatEndpointBase);

function PendingDeployDialog({ stored, isNew, confirmation }: Readonly<{ stored: Gateway; isNew: boolean; confirmation: DeployConfirmation }>) {
    const { pending, confirm, close } = confirmation;
    if (!pending) return null;
    return (
        <GatewayDeploymentDialog open gatewayName={pending.name} gateway={{ ...stored, ...pending, type: stored.type, values: { ...stored.values, ...pending.values } }} action={deployDialogAction(isNew, stored)} onConfirm={confirm} onClose={close} />
    );
}

const deployFailedDetail = (deployError?: string): string => (deployError ? `: ${deployError}` : ". Check the entrypoint's deployment status for details.");

export const GatewayEditPage = () => {
    const { navigate, addNotification, useLoaderData, gatewaySchemas } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { googleChatEndpointBase } = useConfigContext();
    const { data: capabilities } = useCapabilities();
    const { refetch: gatewaysRefetch } = useGateways();
    const { gatewayId, isNew } = useLoaderData();
    const queryParams = new URLSearchParams(window.location.hash.split("?")[1]);
    const type = queryParams.get("type");

    const [gatewayToEdit, setGatewayToEdit] = useState<Gateway | null>(null);
    const [schemaInfo, setSchemaInfo] = useState<GatewaySchema | null>(null);
    const [errorObject, setErrorObject] = useState<{
        title: string;
        message: string;
    } | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const [isFormDirty, setIsFormDirty] = useState(false);
    const shouldDeployAfterCreateRef = useRef(false);
    const { allowNavigation, NavigationBlocker, setBlockingEnabled } = useNavigationBlocker();

    useEffect(() => {
        setBlockingEnabled(isFormDirty);
    }, [isFormDirty, setBlockingEnabled]);

    useEffect(() => {
        const loadGateway = async () => {
            if (isNew && type) {
                const schema = gatewaySchemas.find(s => s.type === type);
                if (schema) {
                    setGatewayToEdit({
                        name: "",
                        description: "",
                        type: type,
                        values: {},
                    });
                    setSchemaInfo(schema);
                }
            } else if (gatewayId) {
                setIsLoading(true);
                try {
                    const gateway = await gatewaysApi.get(gatewayId);
                    setGatewayToEdit(gateway);
                    const schema = gatewaySchemas.find(s => s.type === gateway.type);
                    setSchemaInfo(schema || null);
                } catch (err) {
                    console.error("Failed to load entrypoint:", err);
                } finally {
                    setIsLoading(false);
                }
            }
        };

        if (gatewaySchemas.length > 0) {
            loadGateway();
        }
    }, [gatewayId, isNew, type, gatewaySchemas]);

    const handleSuccess = useCallback(
        (message: string, _deployed: boolean = false, severity: "success" | "warning" = "success") => {
            setBlockingEnabled(false);
            setIsFormDirty(false);
            allowNavigation(() => {
                navigate("/entrypoints");
                addNotification(message, severity);
            });
        },
        [navigate, addNotification, allowNavigation, setBlockingEnabled, setIsFormDirty]
    );

    const isGoogleChat = schemaInfo?.type === "google_chat";

    const performSave = useCallback(
        async (updatedGateway: Gateway, deploy: boolean, opts?: { rethrow?: boolean }) => {
            setIsLoading(true);
            try {
                const gatewayType = type || gatewayToEdit?.type || "";
                const writableGatewayFields: CreateGatewayRequest = {
                    name: updatedGateway.name,
                    description: updatedGateway.description,
                    type: gatewayType,
                    publicUrl: (updatedGateway as any).publicUrl,
                    values: updatedGateway.values,
                    deploy,
                };

                if (isNew && supportsCustomEndpoint(gatewayType, updatedGateway.values) && updatedGateway.slug) {
                    writableGatewayFields.slug = updatedGateway.slug;
                }

                const saved = await gatewaysApi.save(isNew ? undefined : gatewayId, writableGatewayFields);

                await gatewaysRefetch();

                if (deploy) {
                    const verb = isNew ? "created" : "saved";
                    if (saved?.deploymentStatus === "deploy_failed") {
                        handleSuccess(`Entrypoint ${verb} but not deployed${deployFailedDetail(saved.deployError)}`, true, "warning");
                    } else {
                        handleSuccess(`Entrypoint ${verb} and deployed successfully`, true);
                    }
                } else {
                    handleSuccess(isNew ? "Entrypoint created" : "Entrypoint updated");
                }
            } catch (error) {
                if (opts?.rethrow) throw error;
                setErrorObject({
                    title: "Entrypoint Save Failed",
                    message: getErrorMessage(error, "An unknown error occurred while saving the entrypoint."),
                });
            } finally {
                setIsLoading(false);
            }
        },
        [isNew, type, gatewayToEdit, gatewaysRefetch, gatewayId, handleSuccess]
    );

    const deployConfirmation = useDeployConfirmation(gateway => performSave(gateway, true, { rethrow: isGoogleChat }));
    const askToDeploy = deployConfirmation.ask;

    const handleSave = useCallback(
        async (updatedGateway: Gateway) => {
            const deploy = shouldDeployAfterCreateRef.current;
            shouldDeployAfterCreateRef.current = false;
            if (deploy) return askToDeploy(updatedGateway);
            await performSave(updatedGateway, false, { rethrow: isGoogleChat });
        },
        [performSave, isGoogleChat, askToDeploy]
    );

    const handleCancel = useCallback(() => {
        allowNavigation(() => {
            if (isNew) {
                navigate("/entrypoints");
            } else {
                navigate(`/entrypoints/${gatewayId}`);
            }
        });
    }, [isNew, navigate, allowNavigation, gatewayId]);

    if (!userHasCapability(isNew ? CAPABILITIES.createGateways : CAPABILITIES.updateGateways)) {
        return <Navigate to="/entrypoints" replace />;
    }

    if (!isNew && gatewayToEdit && isConsumeOnlyEventMeshEntrypoint(gatewayToEdit)) {
        return <ConsumeOnlyEntrypointNotice gatewayName={gatewayToEdit.name} />;
    }

    const canDeployGateways = userHasCapability(CAPABILITIES.deployGateways);

    const title = pageTitle(isNew, gatewayToEdit);
    const titleNode = <PageTitle title={title} badge={findGatewayBadge(capabilities, schemaInfo?.type ?? "")} />;
    const isSubmitDisabled = isLoading || !gatewayToEdit || !schemaInfo || createBlocked(schemaInfo, isNew, googleChatEndpointBase);

    return (
        <PageLayout>
            <Header
                title={titleNode}
                breadcrumbs={[
                    {
                        label: "Entrypoints",
                        onClick: () => navigate("/entrypoints"),
                    },
                    { label: title },
                ]}
            />
            {gatewayToEdit && schemaInfo ? (
                <>
                    {!isNew && gatewayToEdit.deploymentStatus === "deployed" && <MessageBanner variant="info" message="This entrypoint is currently deployed. After saving, you will need to deploy the updates manually." />}
                    <PageContentWrapper>
                        {isGoogleChat ? (
                            <GoogleChatEdit schemaInfo={schemaInfo} initialGatewayToEdit={gatewayToEdit} isNew={isNew} onSubmit={handleSave} onDirtyStateChange={setIsFormDirty} footer={createSetupBanner(isNew, type)} />
                        ) : (
                            <GatewayEdit schemaInfo={schemaInfo} initialGatewayToEdit={gatewayToEdit} isNew={isNew} onSubmit={handleSave} onDirtyStateChange={setIsFormDirty} footer={createSetupBanner(isNew, type)} />
                        )}
                    </PageContentWrapper>
                    {errorObject && <ErrorDialog title={errorObject.title} open={!!errorObject} error={errorObject.message} errorSource="api" onOpenChange={open => !open && setErrorObject(null)} />}
                    <PendingDeployDialog stored={gatewayToEdit} isNew={isNew} confirmation={deployConfirmation} />
                </>
            ) : null}
            {!schemaInfo && !isLoading ? (
                <EmptyState
                    data-testid="gatewayTypeNotFound"
                    variant="error"
                    title="Entrypoint Type Not Found"
                    buttons={[
                        {
                            text: "Go To Entrypoints",
                            variant: "default",
                            onClick: () => navigate("/entrypoints"),
                        },
                    ]}
                />
            ) : null}
            <NavigationBlocker />
            <LoadingBlocker isLoading={isLoading} />
            <PageFooter>
                <Button data-testid="gatewayCancel" title="Discard Changes" variant="ghost" onClick={handleCancel}>
                    Discard Changes
                </Button>
                <Button
                    data-testid="saveGatewayButton"
                    type="submit"
                    form="gateway-form"
                    variant="outline"
                    onClick={() => {
                        shouldDeployAfterCreateRef.current = false;
                    }}
                    disabled={isSubmitDisabled}
                >
                    {isNew ? "Create" : "Save"}
                </Button>
                {canDeployGateways && (
                    <Button
                        data-testid={isNew ? "createAndDeployGatewayButton" : "saveAndDeployGatewayButton"}
                        type="submit"
                        form="gateway-form"
                        onClick={() => {
                            shouldDeployAfterCreateRef.current = true;
                        }}
                        disabled={isSubmitDisabled}
                    >
                        {isNew ? "Create and Deploy" : "Save and Deploy"}
                    </Button>
                )}
            </PageFooter>
        </PageLayout>
    );
};
