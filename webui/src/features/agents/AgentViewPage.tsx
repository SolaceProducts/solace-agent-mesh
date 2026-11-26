// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";
import { Pencil } from "lucide-react";

import { useAgents, useUserAgentCards } from "@/api/agents";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { PageContentWrapper, PageFooter } from "@/common/layout";

import { isDeployFailed } from "@/utils/deployment";

import { Button } from "@/ui/button";

import { DuplicateNameWarningBanner } from "@/features/agents/DuplicateNameWarningBanner";
import { useDuplicateDisplayName } from "@/features/agents/hooks";

import { EmptyState, ErrorDialog, Header, PageLayout } from "@/common";

import { AgentDeploymentDialog } from "./AgentDeploymentDialog";
import { AgentView } from "./AgentView";
import { useAgentDeployment } from "./hooks/useAgentDeployment";
import { agentManagementTabFor, agentManagementUrl } from "./utils/filters";

export function AgentViewPage() {
    const { handleEditAgent, navigate, useLoaderData } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { data: userAgents = [], isLoading: userAgentsLoading } = useAgents();
    const { data: userAgentCards = [] } = useUserAgentCards();
    const { agentId } = useLoaderData();
    const { error, setError, handleDeploy } = useAgentDeployment();

    const [deploymentDialogOpen, setDeploymentDialogOpen] = useState<boolean>(false);
    const [deploymentAction, setDeploymentAction] = useState<"deploy" | "undeploy" | "update" | null>(null);

    const userAgent = useMemo(() => {
        const agent = userAgents.find(agent => agent.id === agentId);
        return agent ? { ...agent } : null;
    }, [userAgents, agentId]);

    const isDeployFailedState = isDeployFailed(userAgent?.deploymentStatus);

    const agentCardInfo = useMemo(() => {
        if (!agentId) return null;
        const normalizedAgentId = `agent_${agentId.replaceAll("-", "_")}`;
        return userAgentCards?.find(card => card.name === normalizedAgentId) || null;
    }, [agentId, userAgentCards]);

    const { checkAgentDuplicates } = useDuplicateDisplayName();
    const duplicateInfo = agentCardInfo ? checkAgentDuplicates(agentCardInfo) : { hasDuplicate: false, duplicateCount: 0, displayName: null, duplicateAgentIds: [] };

    const openDeploymentDialog = useCallback(() => {
        if (userAgent) {
            const action = isDeployFailedState || userAgent.syncStatus === "out_of_sync" ? "update" : "deploy";
            setDeploymentAction(action);
            setDeploymentDialogOpen(true);
        }
    }, [userAgent, isDeployFailedState]);

    const onDeploy = useCallback(async () => {
        if (userAgent && deploymentAction) {
            const success = await handleDeploy(userAgent, deploymentAction);
            if (!success) return;
            setDeploymentDialogOpen(false);
            const tab = deploymentAction === "undeploy" ? "undeployed" : "deployed";
            navigate(agentManagementUrl(userAgent.id, tab));
        }
    }, [userAgent, deploymentAction, handleDeploy, navigate]);

    const handleClose = useCallback(() => {
        if (userAgent) {
            navigate(agentManagementUrl(userAgent.id, agentManagementTabFor(userAgent.deploymentStatus, userAgent.runtimeStatus)));
        } else {
            navigate("/agents");
        }
    }, [userAgent, navigate]);

    const headerButtons = useMemo(() => {
        if (!userAgent) return [];
        const title = (() => {
            if (isDeployFailedState) return "Redeploy";
            if (userAgent.syncStatus === "out_of_sync") return "Deploy Updates";
            return "Deploy";
        })();
        const buttons = [];
        if (userHasCapability(CAPABILITIES.deployAgents)) {
            buttons.push(
                <Button key="deploy" variant="outline" onClick={openDeploymentDialog} data-testid="deployAgentButton" title={title}>
                    {title}
                </Button>
            );
        }
        if (userHasCapability(CAPABILITIES.updateAgents)) {
            buttons.push(
                <Button
                    key="edit"
                    variant="ghost"
                    onClick={() => {
                        handleEditAgent(userAgent);
                    }}
                    data-testid="editAgentButton"
                    title="Edit Agent"
                >
                    <Pencil />
                    Edit
                </Button>
            );
        }
        return buttons;
    }, [userAgent, handleEditAgent, openDeploymentDialog, isDeployFailedState, userHasCapability, CAPABILITIES]);

    return (
        <PageLayout>
            <Header
                title={userAgent?.name || "N/A"}
                breadcrumbs={[
                    {
                        label: "Agents",
                        onClick: handleClose,
                    },
                    { label: userAgent?.name || "N/A" },
                ]}
                buttons={headerButtons}
            />
            {userAgent ? (
                <>
                    <PageContentWrapper>
                        {duplicateInfo.hasDuplicate && duplicateInfo.displayName && (
                            <DuplicateNameWarningBanner agentName={duplicateInfo.displayName} count={duplicateInfo.duplicateCount} duplicateAgentIds={duplicateInfo.duplicateAgentIds} dismissible className="mb-6" />
                        )}

                        <AgentView agent={userAgent} />
                    </PageContentWrapper>
                    {error && <ErrorDialog title="Agent Deployment Error" open={!!error} error={error} errorSource="api" onOpenChange={open => !open && setError(null)} />}
                    {userAgent && deploymentAction && (
                        <AgentDeploymentDialog
                            open={deploymentDialogOpen}
                            agentName={userAgent.name}
                            agent={userAgent}
                            action={deploymentAction}
                            onConfirm={() => {
                                onDeploy();
                            }}
                            onClose={() => setDeploymentDialogOpen(false)}
                        />
                    )}
                </>
            ) : null}
            {userAgentsLoading && <EmptyState variant="loading" title="Loading..." />}
            {userAgents.length > 0 && !userAgent && !userAgentsLoading && <EmptyState variant="error" title="Agent Not Found" buttons={[{ text: "Go To Agents", variant: "default", onClick: handleClose }]} />}
            <PageFooter>
                <Button variant="outline" testid="closeButton" title="Close" onClick={handleClose}>
                    Close
                </Button>
            </PageFooter>
        </PageLayout>
    );
}
