// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo } from "react";
import { X } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { PageLabel } from "@/common/layout";

import { Button } from "@/ui/button";

import { Metadata } from "@/common";
import type { Connector } from "@/types";

import { ConnectorViewValues } from "./ConnectorViewValues";

interface ConnectorSidePanelProps {
    connector: Connector;
    onClose?: () => void;
}

export const ConnectorSidePanel: React.FC<ConnectorSidePanelProps> = ({ connector, onClose }) => {
    const { getConnectorSchema, handleViewConnector } = useChatContext();

    const schema = useMemo(() => getConnectorSchema(connector.type, connector.subtype), [connector, getConnectorSchema]);

    const handleOpen = () => {
        handleViewConnector(connector);
    };

    return connector ? (
        <div className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="flex h-16 items-center justify-between border-b px-4">
                <div className="truncate text-xl font-semibold" title={connector.name}>
                    {connector.name}
                </div>
                <Button variant="ghost" tooltip="Close" onClick={onClose}>
                    <X />
                </Button>
            </div>

            <div className="flex-1 overflow-y-auto p-4">
                <div className="mb-4 flex flex-col gap-4 rounded-sm bg-(--secondary-w10) p-4">
                    <div className="text-base font-semibold">Connector Details</div>
                    <div>
                        <PageLabel>Type</PageLabel>
                        <div>{`${schema?.name ?? "N/A"}`}</div>
                    </div>
                    <div>
                        <PageLabel>Description</PageLabel>
                        <div className="break-word line-clamp-3">{connector.description?.replaceAll(/[\r\n]+/g, " ") ?? "N/A"}</div>
                    </div>
                    <Button data-testid="openConnectorButton" title="Open Connector" onClick={handleOpen}>
                        Open Connector
                    </Button>
                </div>
                <ConnectorViewValues connector={connector} />
                <div className="mt-4 py-4 font-semibold">Agent Usage</div>
                <div>{connector.agentCount || 0} </div>
                {connector?.id ? <Metadata metadata={connector} /> : null}
            </div>
        </div>
    ) : null;
};
