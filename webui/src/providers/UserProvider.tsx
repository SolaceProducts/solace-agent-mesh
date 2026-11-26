// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";

import { api } from "@/api/client";

import { useConfigContext } from "@/providers/contexts";

import { useErrorDialog } from "@/common/dialogs";

import { hasAnyFeedbackReadReach } from "@/utils/feedbackScope";
import { hasScope as hasScopeMatch } from "@/utils/scopeMatcher";
import { isSuperAdmin as isSuperAdminMatch, SCOPES } from "@/utils/scopes";

import { CAPABILITIES, type Capability, UserContext, type UserContextValue } from "./contexts/UserContext";

const CAPABILITY_SCOPES: Record<Capability, readonly string[]> = {
    readAgents: [SCOPES.agent_builder.read],
    createAgents: [SCOPES.agent_builder.create],
    updateAgents: [SCOPES.agent_builder.update],
    deleteAgents: [SCOPES.agent_builder.delete],
    deployAgents: [SCOPES.agent_builder.deploy],

    readConnectors: [SCOPES.connector.read],
    createConnectors: [SCOPES.connector.create],
    updateConnectors: [SCOPES.connector.update],
    deleteConnectors: [SCOPES.connector.delete],

    readToolsets: [SCOPES.toolset.read],
    createToolsets: [SCOPES.toolset.create],
    updateToolsets: [SCOPES.toolset.update],
    deleteToolsets: [SCOPES.toolset.delete],

    readSkills: [SCOPES.skill.read],
    createSkills: [SCOPES.skill.create],
    updateSkills: [SCOPES.skill.update],
    deleteSkills: [SCOPES.skill.delete],

    readWorkflows: [SCOPES.workflow_builder.read],
    createWorkflows: [SCOPES.workflow_builder.create],
    updateWorkflows: [SCOPES.workflow_builder.update],
    deleteWorkflows: [SCOPES.workflow_builder.delete],

    readGateways: [SCOPES.entrypoint.read],
    createGateways: [SCOPES.entrypoint.create],
    updateGateways: [SCOPES.entrypoint.update],
    deleteGateways: [SCOPES.entrypoint.delete],
    deployGateways: [SCOPES.entrypoint.deploy],
    readGatewayDeployments: [SCOPES.deployment.read],

    readModelConfig: [SCOPES.model_config.read],
    createModelConfig: [SCOPES.model_config.create],
    updateModelConfig: [SCOPES.model_config.update],
    deleteModelConfig: [SCOPES.model_config.delete],

    readEvaluations: [SCOPES.evaluation.read],
    createEvaluations: [SCOPES.evaluation.create],
    updateEvaluations: [SCOPES.evaluation.update],
    deleteEvaluations: [SCOPES.evaluation.delete],
    invokeEvaluations: [SCOPES.evaluation.invoke],

    useBuilder: [SCOPES.builder.use],
    readArtifacts: [],
    readFeedback: [SCOPES.agent.readFeedback, SCOPES.workflow.readFeedback],

    readBudgets: [SCOPES.budget.read],
    createBudgets: [SCOPES.budget.create],
    updateBudgets: [SCOPES.budget.update],
    deleteBudgets: [SCOPES.budget.delete],
    assignBudgets: [SCOPES.budget.assign],
};

const deriveCapabilities = (scopes: readonly string[]): Capability[] =>
    (Object.keys(CAPABILITY_SCOPES) as Capability[]).filter(capability => {
        if (capability === CAPABILITIES.readFeedback) return hasAnyFeedbackReadReach(scopes);
        const required = CAPABILITY_SCOPES[capability];
        return required.length === 0 || required.some(scope => hasScopeMatch(scopes, scope));
    });

const NON_PERSISTENCE_CAPABILITIES: Capability[] = [CAPABILITIES.readAgents, CAPABILITIES.readConnectors, CAPABILITIES.readArtifacts, CAPABILITIES.readToolsets];

interface UserResponse {
    username?: string;
    authenticated?: boolean;
    authMethod?: string;
    scopes?: string[];
}

export function UserProvider({ children }: { children: ReactNode }) {
    const { persistenceEnabled } = useConfigContext();
    const [capabilities, setCapabilities] = useState<Capability[] | null>(null);
    const [scopes, setScopes] = useState<readonly string[]>([]);
    const [username, setUsername] = useState<string | null>(null);
    const { ErrorDialog, setError } = useErrorDialog();

    useEffect(() => {
        const controller = new AbortController();

        const fetchUser = async () => {
            if (!persistenceEnabled) {
                setScopes([]);
                setCapabilities(NON_PERSISTENCE_CAPABILITIES);
                return;
            }
            try {
                const user = await api.webui.get<UserResponse>("/api/v1/user", { signal: controller.signal });
                const grantedScopes = user.scopes ?? [];
                setUsername(user.username || null);
                setScopes(grantedScopes);
                setCapabilities(deriveCapabilities(grantedScopes));
            } catch (error) {
                if (controller.signal.aborted) return;
                console.error("Failed to fetch user scopes:", error);
                setScopes([]);
                setCapabilities([]);
                setError({
                    title: "Couldn't load your permissions",
                    error: "Features you have access to may be hidden. Refresh the browser to try again.",
                    errorSource: "api",
                });
            }
        };

        fetchUser();

        return () => controller.abort();
    }, [persistenceEnabled, setError]);

    const userHasCapability = useCallback(
        (capability: Capability, defaultValue = false) => {
            return capabilities ? capabilities.includes(capability) : defaultValue;
        },
        [capabilities]
    );

    const hasScope = useCallback((required: string) => hasScopeMatch(scopes, required), [scopes]);
    const isSuperAdmin = useMemo(() => isSuperAdminMatch(scopes), [scopes]);

    const value = useMemo<UserContextValue>(
        () => ({ userHasCapability, CAPABILITIES, capabilitiesLoaded: capabilities !== null, scopes, hasScope, username, isSuperAdmin }),
        [userHasCapability, capabilities, scopes, hasScope, username, isSuperAdmin]
    );

    return (
        <UserContext.Provider value={value}>
            {children}
            <ErrorDialog />
        </UserContext.Provider>
    );
}
