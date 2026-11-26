// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

export const CAPABILITIES = {
    readAgents: "readAgents",
    createAgents: "createAgents",
    updateAgents: "updateAgents",
    deleteAgents: "deleteAgents",
    deployAgents: "deployAgents",
    readConnectors: "readConnectors",
    createConnectors: "createConnectors",
    updateConnectors: "updateConnectors",
    deleteConnectors: "deleteConnectors",
    readToolsets: "readToolsets",
    createToolsets: "createToolsets",
    updateToolsets: "updateToolsets",
    deleteToolsets: "deleteToolsets",
    readSkills: "readSkills",
    createSkills: "createSkills",
    updateSkills: "updateSkills",
    deleteSkills: "deleteSkills",
    readWorkflows: "readWorkflows",
    createWorkflows: "createWorkflows",
    updateWorkflows: "updateWorkflows",
    deleteWorkflows: "deleteWorkflows",
    readGateways: "readGateways",
    createGateways: "createGateways",
    updateGateways: "updateGateways",
    deleteGateways: "deleteGateways",
    deployGateways: "deployGateways",
    readGatewayDeployments: "readGatewayDeployments",
    readModelConfig: "readModelConfig",
    createModelConfig: "createModelConfig",
    updateModelConfig: "updateModelConfig",
    deleteModelConfig: "deleteModelConfig",
    readEvaluations: "readEvaluations",
    createEvaluations: "createEvaluations",
    updateEvaluations: "updateEvaluations",
    deleteEvaluations: "deleteEvaluations",
    invokeEvaluations: "invokeEvaluations",
    useBuilder: "useBuilder",
    readArtifacts: "readArtifacts",
    readFeedback: "readFeedback",
    readBudgets: "readBudgets",
    createBudgets: "createBudgets",
    updateBudgets: "updateBudgets",
    deleteBudgets: "deleteBudgets",
    assignBudgets: "assignBudgets",
} as const;

export type Capability = (typeof CAPABILITIES)[keyof typeof CAPABILITIES];

export interface UserContextValue {
    userHasCapability: (capability: Capability, defaultValue?: boolean) => boolean;
    CAPABILITIES: typeof CAPABILITIES;
    capabilitiesLoaded: boolean;
    scopes: readonly string[];
    hasScope: (required: string) => boolean;
    username?: string | null;
    isSuperAdmin: boolean;
}

export const UserContext = createContext<UserContextValue | undefined>(undefined);

export const useUserContext = () => {
    const context = useContext(UserContext);
    if (context === undefined) {
        throw new Error("useUserContext must be used within a UserProvider");
    }
    return context;
};
