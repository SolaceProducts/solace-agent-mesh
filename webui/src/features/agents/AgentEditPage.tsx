// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Sparkles } from "lucide-react";
import { useLocation } from "react-router-dom";

import { agentsApi, invalidateAgentRelations, type SavedAgent, useAgents } from "@/api/agents";
import { useConnectors } from "@/api/connectors";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { useNavigationBlocker } from "@/common/dialogs";
import type { ProductAnalyticsErrorSource, ProductAnalyticsFormField } from "@/common/product-analytics";
import { isOperationalStatus } from "@/common/status";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";

import { EmptyState, ErrorDialog, Header, LoadingBlocker, MessageBanner, PageLayout } from "@/common";
import type { Agent } from "@/types";

const EMPTY_AGENTS: Agent[] = [];

import { PageContentWrapper, PageFooter } from "@/common/layout";

import { AgentCardEdit } from "./AgentCardEdit";
import { AgentConnectorsEdit } from "./AgentConnectorsEdit";
import { AgentCreateFromPromptDialog } from "./AgentCreateFromPromptDialog";
import { trackAgentFormErrors } from "./agentFormAnalytics";
import { AgentInfoEdit } from "./AgentInfoEdit";
import { AgentInstructionsEdit } from "./AgentInstructionsEdit";
import { AgentModelEdit } from "./AgentModelEdit";
import { AgentSkillsEdit } from "./AgentSkillsEdit";
import { AgentToolsetsEdit } from "./AgentToolsetsEdit";
import { AgentView } from "./AgentView";
import { agentManagementUrl } from "./utils/filters";

interface MissingRequirement {
    label: string;
    field: ProductAnalyticsFormField;
}

const MISSING_INSTRUCTIONS: MissingRequirement = { label: "Instructions", field: "systemPrompt" };
const MISSING_MODEL: MissingRequirement = { label: "Model", field: "selectedModel" };

export const EDIT_MODE = {
    AGENT_INFO: "AGENT_INFO",
    AGENT_INSTRUCTIONS: "AGENT_INSTRUCTIONS",
    AGENT_TOOLS: "AGENT_TOOLS",
    AGENT_SKILLS: "AGENT_SKILLS",
    AGENT_CARD: "AGENT_CARD",
    AGENT_CONNECTORS: "AGENT_CONNECTORS",
    AGENT_MODEL: "AGENT_MODEL",
} as const;
export type EditMode = (typeof EDIT_MODE)[keyof typeof EDIT_MODE];

function isAgentDirty(agent: Agent | null, initialAgent: Agent | null): boolean {
    if (!agent || !initialAgent) {
        return false;
    }

    const current = {
        name: agent.name,
        description: agent.description,
        systemPrompt: agent.systemPrompt,
        connectors: agent.connectors,
        toolsets: agent.toolsets,
        toolsetExcludedTools: agent.toolsetExcludedTools,
        toolsetConfigs: agent.toolsetConfigs,
        modelProvider: agent.modelProvider,
        skills: agent.skills,
        skillIds: agent.skillIds,
        skillRefs: agent.skillRefs,
        inputModes: agent.inputModes,
        outputModes: agent.outputModes,
    };
    const initial = {
        name: initialAgent.name,
        description: initialAgent.description,
        systemPrompt: initialAgent.systemPrompt,
        connectors: initialAgent.connectors,
        toolsets: initialAgent.toolsets,
        toolsetExcludedTools: initialAgent.toolsetExcludedTools,
        toolsetConfigs: initialAgent.toolsetConfigs,
        modelProvider: initialAgent.modelProvider,
        skills: initialAgent.skills,
        skillIds: initialAgent.skillIds,
        skillRefs: initialAgent.skillRefs,
        inputModes: initialAgent.inputModes,
        outputModes: initialAgent.outputModes,
    };

    return JSON.stringify(current) !== JSON.stringify(initial);
}

const deployFailureDetail = (saved: NonNullable<SavedAgent>) => (saved.deployError ? `: ${saved.deployError}` : ". Check the agent's deployment status for details.");

function saveNotice(saved: SavedAgent, deploy: boolean, notDeployed: string, deployed: string, plain: string): { message: string; type: "success" | "warning" } {
    if (deploy && saved?.deploymentStatus === "deploy_failed") return { message: `${notDeployed}${deployFailureDetail(saved)}`, type: "warning" };
    return { message: deploy ? deployed : plain, type: "success" };
}

export function AgentEditPage() {
    const { agentToEdit: initialAgentToEdit, setAgentToEdit: setInitialAgentToEdit, addNotification, navigate, useLoaderData, handleCreateAgent } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { refetch: connectorsRefetch } = useConnectors();
    const { data: userAgents = EMPTY_AGENTS, refetch: userAgentsRefetch } = useAgents();
    const queryClient = useQueryClient();
    const { agentId, isNew } = useLoaderData();

    const [agentToEdit, setAgentToEdit] = useState<Agent | null>(null);
    const [initialAgent, setInitialAgent] = useState<Agent | null>(null);
    const [editMode, setEditMode] = useState<EditMode | null>(null);
    const [errorObject, setErrorObject] = useState<{ title: string; message: string; errorSource: ProductAnalyticsErrorSource } | null>(null);
    const [isSaving, setIsSaving] = useState(false);
    const [hasAttemptedSave, setHasAttemptedSave] = useState(false);
    const { allowNavigation, NavigationBlocker, setBlockingEnabled } = useNavigationBlocker();

    const location = useLocation();
    const prompt = location.state;

    const [isCreateAgentDialogOpen, setIsAgentCreateDialogOpen] = useState(false);

    useEffect(() => {
        let agentToEdit = null;
        if (isNew) {
            const newAgent = { ...(initialAgentToEdit ?? { name: "", description: "" }) };
            if (!newAgent.modelProvider?.length) {
                newAgent.modelProvider = ["general"];
            }
            agentToEdit = newAgent;
        } else {
            const agent = userAgents.find(agent => agent.id === agentId);
            if (agent) {
                agentToEdit = agent;
            }
        }

        if (agentToEdit) {
            setAgentToEdit(agentToEdit);
            setInitialAgent(agentToEdit);
        } else {
            setAgentToEdit(null);
            setInitialAgent(null);
        }

        setEditMode(null);
        setErrorObject(null);
        setBlockingEnabled(false);
        setHasAttemptedSave(false);
    }, [agentId, isNew, userAgents, initialAgentToEdit, setBlockingEnabled]);

    useEffect(() => {
        setBlockingEnabled(isNew || prompt || isAgentDirty(agentToEdit, initialAgent));
    }, [agentToEdit, initialAgent, isNew, prompt, setBlockingEnabled]);

    const handleApply = (agent: Agent) => {
        setAgentToEdit(agent);
        setEditMode(null);
    };

    const handleDiscard = () => {
        setEditMode(null);
    };

    const handleSuccess = useCallback(
        (message: string, agentId: string | undefined, tab: "deployed" | "undeployed", type: "success" | "warning" = "success") => {
            setBlockingEnabled(false);
            allowNavigation(() => {
                navigate(agentManagementUrl(agentId, tab));
                addNotification(message, type);
                setInitialAgentToEdit(null);
                connectorsRefetch();
            });
        },
        [navigate, addNotification, allowNavigation, setInitialAgentToEdit, connectorsRefetch, setBlockingEnabled]
    );

    const validatePreSave = useCallback((agent: Agent, isNew: boolean): { kind: "ok" } | { kind: "error"; title: string; message: string } | { kind: "incomplete"; missing: MissingRequirement[] } => {
        const isStandard = !agent.type || agent.type === "standard";
        const missing: MissingRequirement[] = [];
        if (isNew && isStandard && !agent.systemPrompt) missing.push(MISSING_INSTRUCTIONS);
        if (isNew && isStandard && (!agent.modelProvider || agent.modelProvider.length === 0)) {
            missing.push(MISSING_MODEL);
        }
        if (missing.length > 0) return { kind: "incomplete", missing };
        return { kind: "ok" };
    }, []);

    const submitAgent = useCallback(
        async (agent: Agent, isNew: boolean, deploy: boolean) => {
            const writableAgentFields = {
                name: agent.name,
                description: agent.description,
                systemPrompt: agent.systemPrompt,
                skills: agent.skills,
                skillIds: agent.skillIds,
                skillRefs: agent.skillRefs,
                toolsets: agent.toolsets,
                toolsetExcludedTools: agent.toolsetExcludedTools,
                toolsetConfigs: agent.toolsetConfigs,
                connectors: agent.connectors,
                inputModes: agent.inputModes,
                outputModes: agent.outputModes,
                modelProvider: agent.modelProvider,
                additionalConfigurations: agent.additionalConfigurations,
                deploy,
            };
            if (isNew) {
                const created = await agentsApi.create({ ...writableAgentFields, type: agent.type });
                userAgentsRefetch().catch(err => console.warn("post-save refetch failed:", err));
                invalidateAgentRelations(queryClient);
                const tab: "deployed" | "undeployed" = deploy ? "deployed" : "undeployed";
                const notice = saveNotice(created, deploy, "Agent created but not deployed", "Agent created and deployed successfully", "Agent created successfully");
                handleSuccess(notice.message, created?.id, tab, notice.type);
            } else {
                const saved = await agentsApi.update(agent.id!, writableAgentFields);
                userAgentsRefetch().catch(err => console.warn("post-save refetch failed:", err));
                invalidateAgentRelations(queryClient);
                const isOperational = isOperationalStatus(agent.runtimeStatus?.status);
                const tab: "deployed" | "undeployed" = deploy || agent.deploymentStatus === "disconnected" || (agent.deploymentStatus === "deployed" && isOperational) ? "deployed" : "undeployed";
                const notice = saveNotice(saved, deploy, "Agent saved but not deployed", "Agent saved and deployed successfully", "Agent updated successfully");
                handleSuccess(notice.message, agent.id, tab, notice.type);
            }
        },
        [userAgentsRefetch, handleSuccess, queryClient]
    );

    const handleSave = useCallback(
        async (isNew: boolean, deploy: boolean = false) => {
            if (!agentToEdit) return;
            const validation = validatePreSave(agentToEdit, isNew);
            if (validation.kind === "error") {
                setErrorObject({ title: validation.title, message: validation.message, errorSource: "client" });
                return;
            }
            if (validation.kind === "incomplete") {
                if (!hasAttemptedSave) setHasAttemptedSave(true);
                trackAgentFormErrors(Object.fromEntries(validation.missing.map(({ field }) => [field, { type: "too_small" }])));
                const labels = validation.missing.map(({ label }) => label);
                addNotification(`Please complete the required field${labels.length === 1 ? "" : "s"}: ${labels.join(", ")}.`, "warning");
                return;
            }
            setIsSaving(true);
            try {
                await submitAgent(agentToEdit, isNew, deploy);
            } catch (error) {
                const errorMessage = getErrorMessage(error, "An error occurred. Please try again.");
                const title = isNew ? "Agent Creation Failed" : "Agent Update Failed";
                setErrorObject({ title, message: errorMessage, errorSource: "api" });
            } finally {
                setIsSaving(false);
            }
        },
        [agentToEdit, hasAttemptedSave, submitAgent, validatePreSave, addNotification]
    );

    const handleCancel = useCallback(() => {
        allowNavigation(() => {
            navigate(-1);
            setInitialAgentToEdit(null);
        });
    }, [navigate, allowNavigation, setInitialAgentToEdit]);

    const getEditModeComponent = (agent: Agent, editMode: EditMode | null) => {
        switch (editMode) {
            case EDIT_MODE.AGENT_INFO:
                return <AgentInfoEdit agent={agent} onApply={handleApply} onDiscard={handleDiscard} />;
            case EDIT_MODE.AGENT_INSTRUCTIONS:
                return <AgentInstructionsEdit agent={agent} onApply={handleApply} onDiscard={handleDiscard} />;
            case EDIT_MODE.AGENT_TOOLS:
                return <AgentToolsetsEdit agent={agent} onApply={handleApply} onDiscard={handleDiscard} />;
            case EDIT_MODE.AGENT_SKILLS:
                return <AgentSkillsEdit agent={agent} onApply={handleApply} onDiscard={handleDiscard} />;
            case EDIT_MODE.AGENT_CONNECTORS:
                return <AgentConnectorsEdit agent={agent} onApply={handleApply} onDiscard={handleDiscard} />;
            case EDIT_MODE.AGENT_MODEL:
                return <AgentModelEdit agent={agent} onApply={handleApply} onDiscard={handleDiscard} />;
            case EDIT_MODE.AGENT_CARD:
                return <AgentCardEdit agent={agent} onApply={handleApply} onDiscard={handleDiscard} />;
            default:
                return null;
        }
    };

    const title = isNew ? "Create Agent" : `Edit ${agentToEdit?.name ?? "N/A"}`;

    return (
        <PageLayout>
            <Header title={title} breadcrumbs={[{ label: "Agents", onClick: () => navigate("/agents") }, { label: title }]} />
            {agentToEdit && (
                <>
                    <AgentCreateFromPromptDialog isOpen={isCreateAgentDialogOpen} prompt={prompt} isRegenerating={true} onClose={() => setIsAgentCreateDialogOpen(false)} onCreate={(agent, prompt) => handleCreateAgent(agent, prompt, true)} />
                    {!editMode && prompt && (
                        <div>
                            <MessageBanner
                                className="border-none !bg-(--secondary-w10) py-6"
                                icon={<Sparkles className="ml-4 stroke-(--brand-w100)" />}
                                message={
                                    <div className="flex w-full items-center justify-between">
                                        <div className="space-x-2">
                                            <p className="flex items-center text-base">
                                                Generated from prompt:&nbsp; &quot;<span className="inline-block max-w-6xl truncate">{prompt}</span>&quot;
                                            </p>
                                            <p>Finish setting up your agent below. Or if you're not quite happy with this starting point, try adding more details to your prompt and generating again.</p>
                                        </div>
                                        <p></p>
                                        <Button className="border-(--primary-wMain)" variant="outline" onClick={() => setIsAgentCreateDialogOpen(true)}>
                                            Edit Prompt
                                        </Button>
                                    </div>
                                }
                                variant="info"
                            />
                        </div>
                    )}
                    {editMode && getEditModeComponent(agentToEdit, editMode)}
                    {!editMode && (
                        <>
                            <PageContentWrapper>
                                <AgentView
                                    agent={agentToEdit}
                                    setEditMode={setEditMode}
                                    showInstructionsError={hasAttemptedSave && !agentToEdit.systemPrompt}
                                    showModelError={hasAttemptedSave && (!agentToEdit.modelProvider || agentToEdit.modelProvider.length === 0)}
                                />
                            </PageContentWrapper>
                            <PageFooter>
                                <Button testid="agentCancel" title="Discard Changes" variant="ghost" onClick={handleCancel}>
                                    Discard Changes
                                </Button>
                                {userHasCapability(isNew ? CAPABILITIES.createAgents : CAPABILITIES.updateAgents) && (
                                    <Button testid="agentSave" title="Save" variant="outline" onClick={() => handleSave(isNew)} disabled={isSaving || !agentToEdit}>
                                        {isNew ? "Create" : "Save"}
                                    </Button>
                                )}
                                {userHasCapability(isNew ? CAPABILITIES.createAgents : CAPABILITIES.updateAgents) && userHasCapability(CAPABILITIES.deployAgents) && (
                                    <Button testid="agentSaveAndDeploy" title="Save and Deploy" onClick={() => handleSave(isNew, true)} disabled={isSaving || !agentToEdit || !agentToEdit.systemPrompt}>
                                        {isNew ? "Create and Deploy" : "Save and Deploy"}
                                    </Button>
                                )}
                            </PageFooter>
                        </>
                    )}
                    {errorObject ? <ErrorDialog open={!!errorObject} title={errorObject.title} error={errorObject.message} errorSource={errorObject.errorSource} onOpenChange={open => !open && setErrorObject(null)} /> : null}
                </>
            )}
            {!agentToEdit && <EmptyState variant="error" title="Agent Not Found" buttons={[{ text: "Go To Agents", variant: "default", onClick: () => navigate("/agents") }]} />}
            <NavigationBlocker />
            <LoadingBlocker isLoading={isSaving} />
        </PageLayout>
    );
}
