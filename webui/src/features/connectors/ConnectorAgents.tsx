// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { useChatContext } from "@/providers/contexts";

import { PageContentWrapper } from "@/common/layout";
import { NewTabLink } from "@/common/NewTabLink";

import { Table, TableBody, TableCell, TableRow } from "@/ui/table";

import { EmptyState } from "@/common";
import type { Connector } from "@/types";

import { useConnectorAgents } from "./hooks/useConnectorAgents";

interface ConnectorAgentsProps {
    connector: Connector;
}

export const ConnectorAgents: React.FC<ConnectorAgentsProps> = ({ connector }) => {
    const { navigate } = useChatContext();
    const { agents, isLoading } = useConnectorAgents({ connector });

    if (isLoading || connector.agentCount === 0) {
        return isLoading ? (
            <EmptyState variant="loading" title="Loading agent usage..." />
        ) : (
            <EmptyState variant="noImage" title="No agents use this connector" buttons={[{ text: "Go To Agents", variant: "default", onClick: () => navigate("/agents") }]} />
        );
    }

    return (
        <PageContentWrapper>
            {agents?.length > 0 && (
                <div className="min-h-0 flex-1 overflow-y-auto pr-2">
                    <div className="rounded-xs border">
                        <Table>
                            <TableBody borderLastRow>
                                {agents.map(agent => (
                                    <TableRow key={agent.id} className="hover:bg-inherit">
                                        <TableCell className="p-2">
                                            <div className="grid min-w-0">
                                                <div className="flex">
                                                    <NewTabLink to={`/agent-mesh/${agent.id}`} data-testid={`agentLink-${agent.id}`} label={agent.name} />
                                                </div>
                                                <div className="ml-3 truncate text-(--secondary-text-wMain)">{agent.description}</div>
                                            </div>
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    </div>
                </div>
            )}
        </PageContentWrapper>
    );
};
