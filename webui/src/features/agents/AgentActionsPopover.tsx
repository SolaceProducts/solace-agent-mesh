// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useMemo, useState } from "react";
import { useBooleanFlagDetails } from "@openfeature/react-sdk";

import { useRemoteAgents } from "@/api/remote-agents";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { entityNeedsCleanupOnDelete, isDeployFailed } from "@/utils/deployment";
import { SCOPES } from "@/utils/scopes";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import { UserAccessDialog } from "@/features/userManagement/UserAccessDialog";

import { ErrorDialog } from "@/common";
import type { Agent, AgentCardInfo, AgentState } from "@/types";

import { AgentDeploymentDialog } from "./AgentDeploymentDialog";
import { DeleteAgentConfirmationDialog } from "./DeleteAgentConfirmationDialog";
import { useAgentDeployment, useAgentLookup, useRemoteAgentDeployment } from "./hooks";
import { isRemoteNotRunning } from "./utils/filters";

interface ExternalActionsCtx {
    userAgent: Agent;
    chatAction: MenuAction | null;
    viewAccessAction: MenuAction | null;
    showChatWithAgent: boolean;
    selectedState: AgentState;
    isRecoveryState: boolean;
    isOutOfSync: boolean;
    recoveryActions: MenuAction[];
    deployOrUpdateAction: MenuAction;
    undeployAction: MenuAction;
    updateAction: MenuAction;
    deleteAction: MenuAction;
    handleEdit: () => void;
    navigate: (path: string) => void;
}

interface RegularActionsCtx {
    userAgent?: Agent;
    chatAction: MenuAction | null;
    viewAccessAction: MenuAction | null;
    showChatWithAgent: boolean;
    selectedState: AgentState;
    isRecoveryState: boolean;
    isOutOfSync: boolean;
    recoveryActions: MenuAction[];
    deployOrUpdateAction: MenuAction;
    undeployAction: MenuAction;
    updateAction: MenuAction;
    deleteAction: MenuAction;
    handleOpen: () => void;
    handleEdit: () => void;
}

function buildExternalActions(ctx: ExternalActionsCtx): MenuAction[] {
    const out: MenuAction[] = [];
    if (ctx.chatAction) out.push(ctx.chatAction);
    if (ctx.selectedState === "inactive") {
        out.push({ id: "openAgent", label: "Open Agent", onClick: () => ctx.navigate(`/agent-mesh/external/${ctx.userAgent.id}`), iconPosition: "left" });
    }
    out.push({ id: "edit", label: "Edit", onClick: ctx.handleEdit, iconPosition: "left", divider: ctx.showChatWithAgent });
    if (ctx.viewAccessAction) out.push(ctx.viewAccessAction);
    if (ctx.isRecoveryState) {
        out.push(...ctx.recoveryActions);
        return out;
    }
    if (ctx.selectedState === "inactive") {
        out.push(ctx.deployOrUpdateAction, ctx.deleteAction);
    } else if (ctx.selectedState === "active") {
        if (ctx.isOutOfSync) out.push(ctx.updateAction);
        out.push(ctx.undeployAction);
    }
    return out;
}

function buildRegularActions(ctx: RegularActionsCtx): MenuAction[] {
    const out: MenuAction[] = [];
    if (ctx.chatAction) out.push(ctx.chatAction);
    if (ctx.selectedState === "inactive") {
        out.push({ id: "openConfig", label: "Open Agent", onClick: ctx.handleOpen, iconPosition: "left" });
    }
    if (ctx.userAgent) {
        out.push({ id: "edit", label: "Edit", onClick: ctx.handleEdit, iconPosition: "left", divider: ctx.showChatWithAgent });
        if (ctx.viewAccessAction) out.push(ctx.viewAccessAction);
    }
    if (ctx.isRecoveryState) {
        out.push(...ctx.recoveryActions);
        return out;
    }
    if (ctx.selectedState === "inactive") {
        out.push(ctx.deployOrUpdateAction, ctx.deleteAction);
    } else if (ctx.selectedState === "active" && ctx.userAgent) {
        if (ctx.isOutOfSync) out.push(ctx.updateAction);
        out.push(ctx.undeployAction);
    }
    return out;
}

interface AgentActionsPopoverProps {
    agent: AgentCardInfo;
    children: React.ReactNode;
    selectedState: AgentState;
    userAgent?: Agent;
    chatWithAgent?: (agent: AgentCardInfo) => void;
    onAgentDeleted?: () => void;
    onDeployed?: (agentName: string) => void;
    onDeploymentChanged?: () => void;
    viewOnly?: boolean;
    showViewAccess?: boolean;
}

export const AgentActionsPopover: React.FC<AgentActionsPopoverProps> = ({ agent, userAgent: providedUserAgent, selectedState, chatWithAgent, onAgentDeleted, onDeployed, onDeploymentChanged, viewOnly = false, showViewAccess = false, children }) => {
    const { handleDeleteAgent, handleEditAgent, handleViewAgent, navigate } = useChatContext();
    const { data: remoteAgents = [] } = useRemoteAgents();
    const { userHasCapability, CAPABILITIES, hasScope } = useUserContext();
    const { isLoading, error, setError, handleDeploy } = useAgentDeployment();
    const { handleDeployRemoteAgent, error: remoteDeploymentError, setError: setRemoteDeploymentError } = useRemoteAgentDeployment();
    const canEditAgents = userHasCapability(CAPABILITIES.updateAgents);
    const canDeleteAgents = userHasCapability(CAPABILITIES.deleteAgents);
    const canDeployAgents = userHasCapability(CAPABILITIES.deployAgents);
    const { value: rbacEnabled } = useBooleanFlagDetails("rbac", false);

    const [confirmDeleteOpen, setConfirmDeleteOpen] = useState<boolean>(false);
    const [deploymentDialogOpen, setDeploymentDialogOpen] = useState<boolean>(false);
    const [deploymentAction, setDeploymentAction] = useState<"deploy" | "undeploy" | "update">("deploy");
    const [menuOpen, setMenuOpen] = useState<boolean>(false);
    const [viewAccessOpen, setViewAccessOpen] = useState<boolean>(false);

    const agentToDisplay = selectedState === "active" ? { ...(agent.deployed ?? agent), syncStatus: agent.syncStatus } : agent;

    const lookedUpAgent = useAgentLookup(agent.displayName);
    const userAgent = providedUserAgent ?? lookedUpAgent;

    const handleOpen = useCallback(() => {
        if (userAgent) {
            handleViewAgent(userAgent);
        }
    }, [userAgent, handleViewAgent]);

    const handleEdit = useCallback(() => {
        if (userAgent) {
            if (agent.type === "external") {
                navigate(`/agents/external/${userAgent.id}/edit`);
            } else {
                handleEditAgent(userAgent);
            }
        }
    }, [userAgent, agent.type, handleEditAgent, navigate]);

    const openDeploymentDialog = useCallback((action: "deploy" | "undeploy" | "update") => {
        setDeploymentAction(action);
        setDeploymentDialogOpen(true);
        setMenuOpen(false);
    }, []);

    const onDeploy = useCallback(
        async (action: "deploy" | "undeploy" | "update") => {
            if (!userAgent) return;
            let success = false;
            if (agent.type === "external") {
                const remoteAgent = remoteAgents.find(ra => ra.id === userAgent.id);
                if (remoteAgent) {
                    success = await handleDeployRemoteAgent(remoteAgent, action === "update" ? "deploy" : action);
                }
            } else {
                success = await handleDeploy(userAgent, action);
            }
            if (!success) return;
            if (action !== "undeploy") onDeployed?.(agent.name);
            onDeploymentChanged?.();
        },
        [userAgent, agent.type, agent.name, handleDeploy, handleDeployRemoteAgent, remoteAgents, onDeployed, onDeploymentChanged]
    );

    const handleDelete = useCallback(async () => {
        if (!userAgent) return;
        const deleted = await handleDeleteAgent(userAgent);
        if (deleted) onAgentDeleted?.();
    }, [userAgent, handleDeleteAgent, onAgentDeleted]);

    const canReadAgent = userHasCapability(CAPABILITIES.readAgents);
    const canViewAccess = showViewAccess && rbacEnabled && (canReadAgent || hasScope(SCOPES.rbac.read)) && Boolean(userAgent?.id);

    const menuActions = useMemo((): MenuAction[] => {
        if (isLoading) return [];

        const showChatWithAgent = selectedState === "active" && !!chatWithAgent;
        const chatAction: MenuAction | null = showChatWithAgent ? { id: "chatWithAgent", label: "Chat with Agent", onClick: () => chatWithAgent(agent), iconPosition: "left" } : null;
        const viewAccessAction: MenuAction | null = canViewAccess
            ? {
                  id: "viewAccess",
                  label: "View Access",
                  onClick: () => {
                      setViewAccessOpen(true);
                      setMenuOpen(false);
                  },
                  iconPosition: "left",
              }
            : null;

        if (viewOnly) {
            return [chatAction, viewAccessAction].filter((a): a is MenuAction => a !== null);
        }

        const isDeployFailedState = isDeployFailed(agentToDisplay.status);
        const isRecoveryState = (isDeployFailedState || agentToDisplay.status === "stopped") && !isRemoteNotRunning(agentToDisplay);
        const isOutOfSync = agentToDisplay.syncStatus === "out_of_sync";

        const recoveryActions: MenuAction[] = [
            {
                id: isOutOfSync ? "update" : "redeploy",
                label: isOutOfSync ? "Deploy Updates" : "Redeploy",
                onClick: () => openDeploymentDialog("update"),
                iconPosition: "left",
                divider: true,
            },
            { id: "delete", label: "Delete", onClick: () => setConfirmDeleteOpen(true), iconPosition: "left", divider: true },
        ];
        const deployOrUpdateAction: MenuAction = isOutOfSync
            ? { id: "update", label: "Deploy Updates", onClick: () => openDeploymentDialog("update"), iconPosition: "left", divider: true }
            : { id: "deploy", label: "Deploy", onClick: () => openDeploymentDialog("deploy"), iconPosition: "left", divider: true };
        const undeployAction: MenuAction = { id: "undeploy", label: "Undeploy", onClick: () => openDeploymentDialog("undeploy"), iconPosition: "left", divider: true };
        const updateAction: MenuAction = { id: "update", label: "Deploy Updates", onClick: () => openDeploymentDialog("update"), iconPosition: "left", divider: true };
        const deleteAction: MenuAction = { id: "delete", label: "Delete", onClick: () => setConfirmDeleteOpen(true), iconPosition: "left", divider: true };

        const built =
            userAgent && agent.type === "external"
                ? buildExternalActions({
                      userAgent,
                      chatAction,
                      viewAccessAction,
                      showChatWithAgent,
                      selectedState,
                      isRecoveryState,
                      isOutOfSync,
                      recoveryActions,
                      deployOrUpdateAction,
                      undeployAction,
                      updateAction,
                      deleteAction,
                      handleEdit,
                      navigate,
                  })
                : buildRegularActions({
                      userAgent,
                      chatAction,
                      viewAccessAction,
                      showChatWithAgent,
                      selectedState,
                      isRecoveryState,
                      isOutOfSync,
                      recoveryActions,
                      deployOrUpdateAction,
                      undeployAction,
                      updateAction,
                      deleteAction,
                      handleOpen,
                      handleEdit,
                  });

        return built.filter(action => {
            if (action.id === "edit") return canEditAgents;
            if (action.id === "delete") return canDeleteAgents;
            if (["deploy", "update", "redeploy", "undeploy"].includes(action.id)) return canDeployAgents;
            if (action.id === "openConfig" || action.id === "openAgent") return canReadAgent;
            return true;
        });
    }, [isLoading, agentToDisplay, selectedState, canEditAgents, canDeleteAgents, canDeployAgents, canReadAgent, handleOpen, handleEdit, userAgent, openDeploymentDialog, chatWithAgent, agent, navigate, setConfirmDeleteOpen, viewOnly, canViewAccess]);

    if (menuActions.length === 0) {
        return null;
    }

    return (
        <>
            <Popover open={menuOpen} onOpenChange={setMenuOpen}>
                <PopoverTrigger asChild>{children}</PopoverTrigger>
                <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0} onClick={e => e.stopPropagation()}>
                    <Menu actions={menuActions} />
                </PopoverContent>
            </Popover>

            <ErrorDialog open={!!error} title="Agent Deployment Error" error={error ?? ""} errorSource="api" onOpenChange={open => !open && setError(null)} />
            <ErrorDialog open={!!remoteDeploymentError} title="Remote Agent Deployment Error" error={remoteDeploymentError ?? ""} errorSource="api" onOpenChange={open => !open && setRemoteDeploymentError(null)} />

            <DeleteAgentConfirmationDialog open={confirmDeleteOpen} onOpenChange={setConfirmDeleteOpen} agentName={agent.displayName ?? ""} needsCleanup={entityNeedsCleanupOnDelete(agentToDisplay)} onConfirm={handleDelete} />

            {userAgent && (
                <AgentDeploymentDialog
                    open={deploymentDialogOpen}
                    agentName={agentToDisplay.displayName || agentToDisplay.name}
                    agent={userAgent}
                    action={deploymentAction}
                    onConfirm={() => {
                        onDeploy(deploymentAction);
                        setDeploymentDialogOpen(false);
                    }}
                    onClose={() => setDeploymentDialogOpen(false)}
                />
            )}

            <UserAccessDialog target={viewAccessOpen && userAgent?.id ? { agentId: userAgent.id } : null} name={agent.displayName ?? agent.name} onOpenChange={setViewAccessOpen} />
        </>
    );
};
