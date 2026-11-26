// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { isDeployFailed } from "@/utils/deployment";

import type { Agent } from "@/types/models";

import { ConfirmationDialog } from "@/common";

interface AgentDeploymentDialogProps {
    open: boolean;
    agentName: string;
    agent: Agent;
    action: "deploy" | "undeploy" | "update";
    onConfirm: () => void;
    onClose: () => void;
}

export const AgentDeploymentDialog: React.FC<AgentDeploymentDialogProps> = ({ open, agentName, agent, action, onConfirm, onClose }) => {
    const isRecoveryScenario = isDeployFailed(agent.deploymentStatus);

    const getTitle = () => {
        switch (action) {
            case "deploy":
                return agent.syncStatus === "out_of_sync" ? "Deploy Updates" : "Deploy Agent";
            case "undeploy":
                return "Undeploy Agent";
            case "update":
                return isRecoveryScenario ? "Redeploy Agent" : "Deploy Updates";
        }
    };

    const getActionLabel = () => {
        switch (action) {
            case "deploy":
            case "update":
                return isRecoveryScenario ? "Redeploy" : "Deploy";
            case "undeploy":
                return "Undeploy";
        }
    };

    const getMessage = () => {
        switch (action) {
            case "deploy":
                return agent.syncStatus === "out_of_sync" ? (
                    <span>
                        Are you ready to deploy updates for <span className="font-semibold">{agentName}</span>? This will update the running agent with the latest changes.
                    </span>
                ) : (
                    <span>
                        Are you ready to deploy <span className="font-semibold">{agentName}</span>? This will make the agent available for use.
                    </span>
                );
            case "undeploy":
                return agent.syncStatus === "out_of_sync" ? (
                    <span>
                        Are you sure you want to undeploy <span className="font-semibold">{agentName}</span>? Any deployed configuration will be lost and only the draft configuration will remain.
                    </span>
                ) : (
                    <span>
                        Are you sure you want to undeploy <span className="font-semibold">{agentName}</span>?
                    </span>
                );
            case "update":
                if (isRecoveryScenario) {
                    return agent.syncStatus === "out_of_sync" ? (
                        <span>
                            Are you ready to redeploy <span className="font-semibold">{agentName}</span>? This will reinstall the agent using the latest configuration, including any undeployed changes.
                        </span>
                    ) : (
                        <span>
                            Are you ready to redeploy <span className="font-semibold">{agentName}</span>? This will reinstall the agent using the latest configuration.
                        </span>
                    );
                }
                return (
                    <span>
                        Are you ready to deploy updates for <span className="font-semibold">{agentName}</span>? This will update the running agent with the latest changes.
                    </span>
                );
        }
    };

    return (
        <ConfirmationDialog
            title={getTitle()}
            open={open}
            onConfirm={onConfirm}
            onCancel={onClose}
            onOpenChange={open => !open && onClose()}
            actionLabels={{
                confirm: getActionLabel(),
            }}
            content={getMessage()}
        />
    );
};
