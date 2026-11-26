// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { zodResolver } from "@hookform/resolvers/zod";
import { Loader2, Plug, X } from "lucide-react";
import { useForm } from "react-hook-form";
import { z } from "zod";

import { useConnectors } from "@/api/connectors";

import { getSectionTitle } from "@/common/layout";

import { Button } from "@/ui/button";

import type { Agent } from "@/types";

import { AgentConnectorCard } from "./AgentAbilityCard";
import { AgentConnectorsDialog } from "./AgentConnectorsDialog";
import { agentConnectorsSchema } from "./agentEditSchemas";
import { trackAgentFormErrors } from "./agentFormAnalytics";
import { AgentEditSection, AgentEditWrapper, AgentEmptySection } from "./common";
import { useConnectorRuntimeToolNames } from "./hooks";

type AgentConnectorsFormData = z.infer<typeof agentConnectorsSchema>;

interface AgentToolsEditProps {
    agent: Agent;
    onApply: (agent: Agent) => void;
    onDiscard: () => void;
}

export const AgentConnectorsEdit = ({ agent, onApply, onDiscard }: AgentToolsEditProps) => {
    const { data: connectors = [], isLoading: connectorsLoading } = useConnectors();
    const runtimeToolNamesByConnector = useConnectorRuntimeToolNames();

    const { handleSubmit, watch, setValue } = useForm<AgentConnectorsFormData>({
        resolver: zodResolver(agentConnectorsSchema),
        defaultValues: {
            selectedConnectors: agent.connectors || [],
        },
        mode: "onSubmit",
        reValidateMode: "onChange",
    });

    const selectedConnectors = watch("selectedConnectors");

    const onSubmit = (data: AgentConnectorsFormData) => {
        const selectedAgentConnectors = connectors?.filter(connector => connector.id && data.selectedConnectors.includes(connector.id)) || [];
        onApply({ ...agent, connectors: selectedAgentConnectors.map(connector => connector.id || "") });
    };

    const handleToggleConnector = (connectorId: string) => {
        if (selectedConnectors.includes(connectorId)) {
            setValue(
                "selectedConnectors",
                selectedConnectors.filter(id => id !== connectorId)
            );
        } else {
            setValue("selectedConnectors", [...selectedConnectors, connectorId]);
        }
    };

    if (connectorsLoading) {
        return (
            <div className="flex flex-1 items-center justify-center">
                <Loader2 className="size-8 animate-spin text-(--brand-wMain)" />
            </div>
        );
    }

    return connectorsLoading ? null : (
        <AgentEditWrapper sectionTitle={getSectionTitle(Plug, "Connectors")} agent={agent} onSubmit={handleSubmit(onSubmit, trackAgentFormErrors)} onDiscard={onDiscard}>
            <AgentEditSection>
                <div className="flex items-center justify-between">
                    <div>Select connectors to allow your agent to interact with external systems.</div>
                    <AgentConnectorsDialog connectors={connectors} selectedConnectors={selectedConnectors} onToggleConnector={handleToggleConnector} />
                </div>

                <div className="mt-8 flex flex-col gap-2">
                    {selectedConnectors.length > 0 ? (
                        selectedConnectors.map(connectorId => {
                            const connector = connectors?.find(c => c.id === connectorId);
                            if (!connector) return null;

                            return (
                                <AgentConnectorCard
                                    key={connectorId}
                                    connector={connector}
                                    className="w-full"
                                    showTools={true}
                                    iconSize="sm"
                                    runtimeToolNames={connector.id ? runtimeToolNamesByConnector.get(connector.id) : undefined}
                                    actionButton={
                                        <Button variant="ghost" title="Remove" testid={`remove-connector-${connector.id}`} onClick={() => handleToggleConnector(connector.id || "")}>
                                            <X />
                                        </Button>
                                    }
                                />
                            );
                        })
                    ) : (
                        <AgentEmptySection message="No connectors assigned to this agent" />
                    )}
                </div>
            </AgentEditSection>
        </AgentEditWrapper>
    );
};
