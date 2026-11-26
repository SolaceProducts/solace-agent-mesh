// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";
import { ChevronLeft } from "lucide-react";
import type { UseFormReturn } from "react-hook-form";
import { useLoaderData } from "react-router-dom";

import type { RemoteAgentResponse } from "@/api/remote-agents";
import { remoteAgentsApi, useRemoteAgents } from "@/api/remote-agents";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { useNavigationBlocker } from "@/common/dialogs";
import { PageFooter } from "@/common/layout";
import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { defineStepper } from "@/ui/stepper";

import { ErrorDialog, Header, MessageBanner, PageLayout } from "@/common";
import type { ExternalAgentCard } from "@/types";

import type { AdditionalInfoFormData } from "./external/AdditionalInfoStep";
import { AdditionalInfoStep } from "./external/AdditionalInfoStep";
import { transformRemoteAgentToFormData, transformToCreateRemoteAgentRequest, withFieldsTheFormKeeps } from "./external/agentDataTransform";
import type { ConnectAgentFormData, ConnectAgentFormMethods } from "./external/ConnectAgentForm";
import { ConnectAgentForm } from "./external/ConnectAgentForm";
import { buildConnectSuccessMessage, mapConnectError, runDeployAfterSave as runDeployAfterSaveHelper } from "./external/connectAgentHelpers";
import { ReviewAgentStep } from "./external/ReviewAgentStep";

const STEP_IDS = {
    CONNECT: "connect",
    ADDITIONAL_INFO: "additional-info",
    REVIEW: "review",
} as const;

const { Stepper } = defineStepper({ id: STEP_IDS.CONNECT, title: "Provide Agent Location" }, { id: STEP_IDS.ADDITIONAL_INFO, title: "Enter Additional Information" }, { id: STEP_IDS.REVIEW, title: "Review Agent" });

type StepId = (typeof STEP_IDS)[keyof typeof STEP_IDS];

function ConnectStep({
    onFormReady,
    defaultValues,
    fetchedAgentCard,
    onAgentCardFetched,
    onFetchError,
    isEditMode,
    onChange,
    initialRemoteAgent,
}: {
    onFormReady: (methods: ConnectAgentFormMethods) => void;
    defaultValues: Partial<ConnectAgentFormData>;
    fetchedAgentCard: ExternalAgentCard | null;
    onAgentCardFetched: (card: ExternalAgentCard) => void;
    onFetchError: () => void;
    isEditMode?: boolean;
    onChange?: () => void;
    initialRemoteAgent?: RemoteAgentResponse;
}) {
    return (
        <ConnectAgentForm
            onFormReady={onFormReady}
            defaultValues={defaultValues}
            fetchedAgentCard={fetchedAgentCard}
            onAgentCardFetched={onAgentCardFetched}
            onFetchError={onFetchError}
            isEditMode={isEditMode}
            onChange={onChange}
            initialRemoteAgent={initialRemoteAgent}
        />
    );
}

interface StepFooterProps {
    backButton?: {
        label: string;
        onClick: () => void;
    };
    cancelButton: {
        onClick: () => void;
    };
    nextButton: {
        label: string;
        onClick: () => void | Promise<void>;
        disabled?: boolean;
    };
}

function StepFooter({ backButton, cancelButton, nextButton }: StepFooterProps) {
    return (
        <PageFooter>
            {backButton ? (
                <div className="flex w-full items-center justify-between">
                    <Button variant="ghost" onClick={backButton.onClick} data-testid="externalAgentStepBack">
                        <ChevronLeft className="mr-2 size-4" />
                        {backButton.label}
                    </Button>
                    <div className="flex gap-4">
                        <Button variant="ghost" onClick={cancelButton.onClick} data-testid="externalAgentStepCancel">
                            Discard Changes
                        </Button>
                        <Button onClick={nextButton.onClick} disabled={nextButton.disabled} data-testid="externalAgentStepNext">
                            {nextButton.label}
                        </Button>
                    </div>
                </div>
            ) : (
                <>
                    <Button variant="ghost" onClick={cancelButton.onClick} data-testid="externalAgentStepCancel">
                        Discard Changes
                    </Button>
                    <Button onClick={nextButton.onClick} disabled={nextButton.disabled} data-testid="externalAgentStepNext">
                        {nextButton.label}
                    </Button>
                </>
            )}
        </PageFooter>
    );
}

interface StepConfig {
    backLabel?: string;
    prevStep?: StepId;
    nextLabel: string;
    nextStep?: StepId;
    requiresValidation: boolean;
}

const getStepConfig = (isEdit: boolean): Record<StepId, StepConfig> => ({
    [STEP_IDS.CONNECT]: {
        nextLabel: "Next: Enter Additional Information",
        nextStep: STEP_IDS.ADDITIONAL_INFO,
        requiresValidation: true,
    },
    [STEP_IDS.ADDITIONAL_INFO]: {
        backLabel: "Back: Provide Agent Location",
        prevStep: STEP_IDS.CONNECT,
        nextLabel: "Next: Review Agent",
        nextStep: STEP_IDS.REVIEW,
        requiresValidation: true,
    },
    [STEP_IDS.REVIEW]: {
        backLabel: "Back: Enter Additional Information",
        prevStep: STEP_IDS.ADDITIONAL_INFO,
        nextLabel: isEdit ? "Update Agent" : "Connect Agent",
        requiresValidation: false,
    },
});

export function AgentConnectExternalPage() {
    const loaderData = useLoaderData() as { remoteAgentId?: string; isEdit?: boolean } | undefined;
    const { navigate, addNotification } = useChatContext();
    const { refetch: remoteAgentsRefetch } = useRemoteAgents();
    const [formMethods, setFormMethods] = useState<ConnectAgentFormMethods | null>(null);
    const [formData, setFormData] = useState<Partial<ConnectAgentFormData>>({});
    const [fetchedAgentCard, setFetchedAgentCard] = useState<ExternalAgentCard | null>(null);
    const [additionalInfoFormData, setAdditionalInfoFormData] = useState<Partial<AdditionalInfoFormData>>({});
    const [additionalInfoFormMethods, setAdditionalInfoFormMethods] = useState<UseFormReturn<AdditionalInfoFormData> | null>(null);
    const [isConnecting, setIsConnecting] = useState(false);
    const [errorObject, setErrorObject] = useState<{ title: string; message: string } | null>(null);
    const [isLoadingAgent, setIsLoadingAgent] = useState(false);
    const [remoteAgentDeploymentStatus, setRemoteAgentDeploymentStatus] = useState<string | undefined>(undefined);
    const [cardFetchError, setCardFetchError] = useState<string | null>(null);
    const [initialRemoteAgent, setInitialRemoteAgent] = useState<RemoteAgentResponse | undefined>(undefined);
    const { allowNavigation, NavigationBlocker, setBlockingEnabled } = useNavigationBlocker();
    const [hasChanges, setHasChanges] = useState(false);

    const isEditMode = loaderData?.isEdit ?? false;
    const remoteAgentId = loaderData?.remoteAgentId;

    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canWriteExternalAgent = userHasCapability(isEditMode ? CAPABILITIES.updateAgents : CAPABILITIES.createAgents);
    const canDeployExternalAgent = userHasCapability(CAPABILITIES.deployAgents);

    useEffect(() => {
        if (isEditMode && remoteAgentId) {
            setIsLoadingAgent(true);
            remoteAgentsApi
                .get(remoteAgentId, true)
                .then(response => {
                    if (!response) {
                        throw new Error("Remote agent data is undefined");
                    }
                    setInitialRemoteAgent(response);

                    const { connectFormData, additionalInfoFormData } = transformRemoteAgentToFormData(response);
                    setFormData(connectFormData);
                    setAdditionalInfoFormData(additionalInfoFormData);
                    setRemoteAgentDeploymentStatus(response.deploymentStatus);

                    if (response.cardData) {
                        setFetchedAgentCard(response.cardData);
                        setCardFetchError(null);
                    } else if (response.cardFetchError) {
                        console.warn("Failed to fetch agent card:", response.cardFetchError);
                        setCardFetchError(response.cardFetchError);
                        setFetchedAgentCard(null);
                    } else {
                        setFetchedAgentCard(null);
                        setCardFetchError(null);
                    }
                })
                .catch(error => {
                    console.error("Failed to load remote agent:", error);
                    setCardFetchError(null);
                    setErrorObject({
                        title: "Failed to Load Agent",
                        message: getErrorMessage(error, "Could not load agent data for editing."),
                    });
                })
                .finally(() => {
                    setIsLoadingAgent(false);
                });
        }
    }, [isEditMode, remoteAgentId]);

    useEffect(() => {
        if (isLoadingAgent) {
            setBlockingEnabled(false);
            return;
        }
        setBlockingEnabled(hasChanges);
    }, [hasChanges, isLoadingAgent, setBlockingEnabled]);

    const markAsChanged = useCallback(() => {
        setHasChanges(true);
    }, []);

    const handleCancel = useCallback(() => {
        navigate(-1);
    }, [navigate]);

    const handleFormReady = useCallback((methods: ConnectAgentFormMethods) => {
        setFormMethods(methods);
    }, []);

    const clearFetchedAgentCard = useCallback(() => {
        setFetchedAgentCard(null);
    }, []);

    const saveFormData = useCallback(() => {
        if (formMethods) {
            const data = formMethods.getValues();
            setFormData(data);
        }
    }, [formMethods]);

    const handleAdditionalInfoFormReady = useCallback((methods: UseFormReturn<AdditionalInfoFormData>) => {
        setAdditionalInfoFormMethods(methods);
    }, []);

    const saveAdditionalInfoFormData = useCallback(() => {
        if (additionalInfoFormMethods) {
            setAdditionalInfoFormData(additionalInfoFormMethods.getValues());
        }
    }, [additionalInfoFormMethods]);

    const validateAndSave = useCallback(async (): Promise<boolean> => {
        if (!formMethods) return false;

        const isValid = await formMethods.trigger();
        if (isValid) {
            saveFormData();
            return true;
        }
        return false;
    }, [formMethods, saveFormData]);

    const validateAndSaveAdditionalInfo = useCallback(async (): Promise<boolean> => {
        if (!additionalInfoFormMethods) return false;

        const isValid = await additionalInfoFormMethods.trigger();
        if (isValid) {
            saveAdditionalInfoFormData();
            return true;
        }
        return false;
    }, [additionalInfoFormMethods, saveAdditionalInfoFormData]);

    const saveAgent = useCallback(
        async (request: ReturnType<typeof transformToCreateRemoteAgentRequest>): Promise<string | undefined> => {
            if (isEditMode && remoteAgentId) {
                await remoteAgentsApi.update(remoteAgentId, withFieldsTheFormKeeps(request, initialRemoteAgent));
                return remoteAgentId;
            }
            const created = await remoteAgentsApi.create(request);
            return created?.id;
        },
        [isEditMode, remoteAgentId, initialRemoteAgent]
    );

    const handleConnectAgent = useCallback(
        async (stepperMethods?: any, deploy: boolean = false) => {
            setIsConnecting(true);

            try {
                const request = transformToCreateRemoteAgentRequest(formData, additionalInfoFormData, fetchedAgentCard);
                const resolvedAgentId = await saveAgent(request);

                const deployResult = deploy && resolvedAgentId ? await runDeployAfterSaveHelper(resolvedAgentId, remoteAgentsApi.deploy) : { failed: false, message: undefined };

                remoteAgentsRefetch().catch(err => console.warn("post-save remote-agents refetch failed:", err));

                allowNavigation(() => {
                    const params = new URLSearchParams();
                    if (resolvedAgentId) params.set("id", resolvedAgentId);
                    params.set("tab", deploy && !deployResult.failed ? "deployed" : "undeployed");
                    navigate(`/agents?${params.toString()}`);

                    if (deployResult.failed) {
                        const detailSuffix = deployResult.message ? `: ${deployResult.message}` : "";
                        reportAndNotifyError(addNotification, `Agent ${isEditMode ? "saved" : "connected"} but not deployed${detailSuffix}. Retry from the agent row.`, "api");
                        return;
                    }
                    addNotification(buildConnectSuccessMessage(isEditMode, deploy), "success");
                });
            } catch (error: any) {
                console.error(`Failed to ${isEditMode ? "update" : "connect"} agent:`, error);
                const { errorTitle, errorMessage, shouldNavigateToStep2 } = mapConnectError(error, isEditMode, formData, formMethods, additionalInfoFormMethods, stepperMethods);
                if (shouldNavigateToStep2 && stepperMethods) {
                    stepperMethods.goTo(STEP_IDS.ADDITIONAL_INFO);
                } else {
                    setErrorObject({ title: errorTitle, message: errorMessage });
                }
            } finally {
                setIsConnecting(false);
            }
        },
        [formData, additionalInfoFormData, fetchedAgentCard, formMethods, additionalInfoFormMethods, navigate, addNotification, remoteAgentsRefetch, isEditMode, allowNavigation, saveAgent]
    );

    const renderStepContent = useCallback(
        (stepId: StepId) => {
            switch (stepId) {
                case STEP_IDS.CONNECT:
                    return (
                        <ConnectStep
                            onFormReady={handleFormReady}
                            defaultValues={formData}
                            fetchedAgentCard={fetchedAgentCard}
                            onAgentCardFetched={setFetchedAgentCard}
                            onFetchError={clearFetchedAgentCard}
                            isEditMode={isEditMode}
                            onChange={markAsChanged}
                            initialRemoteAgent={initialRemoteAgent}
                        />
                    );
                case STEP_IDS.ADDITIONAL_INFO:
                    return (
                        <AdditionalInfoStep
                            agentCardLocation={formData.agentCardLocation}
                            fetchedAgentCard={fetchedAgentCard}
                            onFormReady={handleAdditionalInfoFormReady}
                            defaultValues={additionalInfoFormData}
                            onChange={markAsChanged}
                            initialRemoteAgent={initialRemoteAgent}
                        />
                    );
                case STEP_IDS.REVIEW:
                    return <ReviewAgentStep formData={formData} additionalInfoFormData={additionalInfoFormData} fetchedAgentCard={fetchedAgentCard} />;
                default:
                    return null;
            }
        },
        [handleFormReady, handleAdditionalInfoFormReady, formData, additionalInfoFormData, fetchedAgentCard, clearFetchedAgentCard, isEditMode, markAsChanged, initialRemoteAgent]
    );

    const renderStepFooter = useCallback(
        (stepId: StepId, methods: any) => {
            const config = getStepConfig(isEditMode)[stepId];
            if (!config) return null;

            const handleBack = config.prevStep
                ? () => {
                      saveFormData();
                      saveAdditionalInfoFormData();
                      methods.goTo(config.prevStep);
                  }
                : undefined;

            const handleNext = async () => {
                if (config.requiresValidation) {
                    let isValid = false;
                    if (stepId === STEP_IDS.CONNECT) {
                        isValid = await validateAndSave();
                    } else if (stepId === STEP_IDS.ADDITIONAL_INFO) {
                        isValid = await validateAndSaveAdditionalInfo();
                    }

                    if (!isValid) return;
                }

                if (stepId === STEP_IDS.CONNECT && config.nextStep) {
                    const currentFormData = formMethods?.getValues();
                    const needsFetch = currentFormData?.agentCardLocation !== "none" && !fetchedAgentCard;

                    if (needsFetch && formMethods?.fetchAgentCard) {
                        try {
                            await formMethods.fetchAgentCard();
                        } catch (err) {
                            return;
                        }
                    }
                }

                if (config.nextStep) {
                    saveFormData();
                    methods.goTo(config.nextStep);
                } else {
                    handleConnectAgent(methods, false);
                }
            };

            if (stepId === STEP_IDS.REVIEW) {
                return (
                    <PageFooter>
                        <div className="flex w-full items-center justify-between">
                            {handleBack && config.backLabel ? (
                                <Button variant="ghost" onClick={handleBack} data-testid="externalAgentStepBack">
                                    <ChevronLeft className="mr-2 size-4" />
                                    {config.backLabel}
                                </Button>
                            ) : (
                                <div />
                            )}
                            <div className="flex gap-4">
                                <Button variant="ghost" onClick={handleCancel} data-testid="externalAgentStepCancel">
                                    Discard Changes
                                </Button>
                                {canWriteExternalAgent && (
                                    <Button variant="outline" onClick={() => handleConnectAgent(methods, false)} disabled={isConnecting} data-testid="externalAgentConnect">
                                        {isConnecting ? (isEditMode ? "Updating..." : "Connecting...") : isEditMode ? "Update Agent" : "Connect Agent"}
                                    </Button>
                                )}
                                {canWriteExternalAgent && canDeployExternalAgent && (
                                    <Button onClick={() => handleConnectAgent(methods, true)} disabled={isConnecting} data-testid="externalAgentConnectAndDeploy">
                                        {isConnecting ? (isEditMode ? "Updating..." : "Connecting...") : isEditMode ? "Update and Deploy" : "Connect and Deploy"}
                                    </Button>
                                )}
                            </div>
                        </div>
                    </PageFooter>
                );
            }

            return (
                <StepFooter
                    backButton={
                        handleBack && config.backLabel
                            ? {
                                  label: config.backLabel,
                                  onClick: handleBack,
                              }
                            : undefined
                    }
                    cancelButton={{ onClick: handleCancel }}
                    nextButton={{
                        label: config.nextLabel,
                        onClick: handleNext,
                        disabled: false,
                    }}
                />
            );
        },
        [validateAndSave, validateAndSaveAdditionalInfo, saveFormData, saveAdditionalInfoFormData, handleCancel, handleConnectAgent, formMethods, fetchedAgentCard, isConnecting, isEditMode, canWriteExternalAgent, canDeployExternalAgent]
    );

    if (isLoadingAgent) {
        return (
            <PageLayout>
                <Header title={isEditMode ? "Edit External Agent" : "Connect External Agent"} breadcrumbs={[{ label: "Agents", onClick: () => navigate("/agents") }, { label: isEditMode ? "Edit External Agent" : "Connect External Agent" }]} />
                <div className="flex flex-1 items-center justify-center">
                    <p className="text-(--secondary-text-wMain)">Loading agent data...</p>
                </div>
            </PageLayout>
        );
    }

    return (
        <PageLayout>
            <Header title={isEditMode ? "Edit External Agent" : "Connect External Agent"} breadcrumbs={[{ label: "Agents", onClick: () => navigate("/agents") }, { label: isEditMode ? "Edit External Agent" : "Connect External Agent" }]} />

            <Stepper.Provider className="flex flex-1 flex-col overflow-hidden">
                {({ methods }) => (
                    <>
                        <div className="px-10 pt-8">
                            <Stepper.Navigation>
                                <Stepper.Step of={STEP_IDS.CONNECT}>
                                    <Stepper.Title>Provide Agent Location</Stepper.Title>
                                    <Stepper.Description>Step 1</Stepper.Description>
                                </Stepper.Step>
                                <Stepper.Step of={STEP_IDS.ADDITIONAL_INFO}>
                                    <Stepper.Title>Enter Additional Information</Stepper.Title>
                                    <Stepper.Description>Step 2</Stepper.Description>
                                </Stepper.Step>
                                <Stepper.Step of={STEP_IDS.REVIEW}>
                                    <Stepper.Title>Review Agent</Stepper.Title>
                                    <Stepper.Description>Step 3</Stepper.Description>
                                </Stepper.Step>
                            </Stepper.Navigation>
                        </div>

                        {isEditMode && remoteAgentDeploymentStatus === "deployed" && (
                            <MessageBanner variant="info" message="This external agent is currently deployed. After saving, you will need to deploy the updates manually." className="mx-6 my-6" />
                        )}

                        {cardFetchError && (
                            <MessageBanner
                                variant="warning"
                                message={`Could not automatically fetch agent card using stored credentials: ${cardFetchError}. You can click "Fetch Agent Card" to retry with updated credentials.`}
                                dismissible
                                onDismiss={() => setCardFetchError(null)}
                                className="mx-6 my-6"
                            />
                        )}

                        <div className="flex-1 overflow-y-auto px-10 pt-8 pb-8 leading-5">
                            <div className="flex flex-col gap-8">{renderStepContent(methods.current.id as StepId)}</div>
                        </div>

                        {renderStepFooter(methods.current.id as StepId, methods)}
                    </>
                )}
            </Stepper.Provider>
            {errorObject && <ErrorDialog open={!!errorObject} title={errorObject.title} error={errorObject.message} errorSource="api" onOpenChange={open => !open && setErrorObject(null)} />}
            <NavigationBlocker />
        </PageLayout>
    );
}
