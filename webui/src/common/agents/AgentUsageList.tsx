// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { PageContentWrapper } from "@/common/layout";
import { NewTabLink } from "@/common/NewTabLink";
import { EmptyState } from "@/common/status";

import { Table, TableBody, TableCell, TableRow } from "@/ui/table";

interface AgentUsageListProps {
    agents: Array<{ id: string; name: string; description?: string }>;
    emptyTitle: string;
    onGoToAgents: () => void;
}

export const AgentUsageList: React.FC<AgentUsageListProps> = ({ agents, emptyTitle, onGoToAgents }) => {
    if (agents.length === 0) {
        return <EmptyState variant="noImage" title={emptyTitle} buttons={[{ text: "Go To Agents", variant: "default", onClick: onGoToAgents }]} />;
    }

    return (
        <PageContentWrapper>
            <div className="min-h-0 flex-1 overflow-y-auto pr-2">
                <div className="rounded-xs border">
                    <Table>
                        <TableBody borderLastRow>
                            {agents.map(agent => (
                                <TableRow key={agent.id} className="hover:bg-inherit">
                                    <TableCell className="p-2">
                                        <div className="grid min-w-0">
                                            <div className="flex">
                                                <NewTabLink to={`/agent-mesh/${agent.id}`} label={agent.name} />
                                            </div>
                                            {agent.description && <div className="ml-3 truncate text-(--secondary-text-wMain)">{agent.description}</div>}
                                        </div>
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </div>
            </div>
        </PageContentWrapper>
    );
};
