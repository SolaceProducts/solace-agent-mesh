// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Plus } from "lucide-react";

import { findConnectorBadge, useCapabilities } from "@/api/capabilities";

import { useChatContext } from "@/providers/contexts";

import { Button } from "@/ui/button";
import { Card } from "@/ui/card";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/ui/dialog";
import { LifecycleBadge } from "@/ui/lifecycleBadge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/ui/tabs";

import { EmptyState } from "@/common";
import type { Connector, ConnectorSchema, ConnectorSubType, ConnectorType } from "@/types";
import { CONNECTOR_APP_TYPE } from "@/types";

import { ConnectorIcon } from "./ConnectorIcon";

const getLabels = (schema: ConnectorSchema) => {
    const [label, type] = schema.name.split(" | ");
    if (schema.type === "mcp") return { label, subLabel: "Convert any MCP server into a connector for your agents." };
    if (schema.type === "api") return { label, subLabel: "Allow agents to connect to any existing API with a spec." };
    if (schema.type === "slack") return { label, subLabel: "Messaging" };

    const isCustom = !CONNECTOR_APP_TYPE.includes(schema.type);
    return { label, subLabel: isCustom || schema.type === "event_mesh" ? schema.description : type };
};

export interface ConnectorsAvailableDialogProps {
    triggerButton?: React.ReactNode;
}

export const ConnectorsAvailableDialog = ({ triggerButton }: ConnectorsAvailableDialogProps) => {
    const { handleCreateConnector, connectorSchemas, platformConfigured, connectorSchemasRefetch } = useChatContext();
    const { data: capabilities } = useCapabilities();

    const appsConnectorSchemas = (connectorSchemas?.filter(schema => CONNECTOR_APP_TYPE.includes(schema.type)) ?? []).sort((a, b) => {
        if (a.type === "event_mesh") return -1;
        if (b.type === "event_mesh") return 1;
        return 0;
    });
    const customConnectorSchemas = connectorSchemas?.filter(schema => !CONNECTOR_APP_TYPE.includes(schema.type)) ?? [];

    const handleSelect = (type: ConnectorType, subtype: ConnectorSubType) => {
        const connector: Connector = { id: "new", name: "", description: "", type, subtype, values: {} };
        handleCreateConnector(connector);
    };

    const renderSchemaCard = (schema: ConnectorSchema) => {
        const badge = findConnectorBadge(capabilities, schema.type, schema.experimental ?? false, schema.subtype);
        const { label, subLabel } = getLabels(schema);
        return (
            <Card
                key={`${schema.type}-${schema.subtype}`}
                data-testid={`connector-type-${schema.type}-${schema.subtype}`}
                onCardSelect={() => handleSelect(schema.type, schema.subtype)}
                noPadding
                className="flex w-full flex-row items-center gap-4 p-4 text-left"
            >
                <div className="shrink-0">
                    <ConnectorIcon icon={schema.icon} name={label} size="sm" />
                </div>
                <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                        <span className="font-semibold">{label}</span>
                        {badge && <LifecycleBadge>{badge}</LifecycleBadge>}
                    </div>
                    {subLabel && <div className="mt-0.5 text-sm text-(--secondary-text-wMain)">{subLabel}</div>}
                </div>
            </Card>
        );
    };

    const renderGrid = (schemas: ConnectorSchema[], emptyMessage: string, singleColumn = false) => {
        if (schemas.length === 0) return <EmptyState title={emptyMessage} variant="notFound" />;
        return (
            <div className="pr-1">
                <div className={`grid gap-3 ${singleColumn ? "grid-cols-1" : "grid-cols-2"}`}>{schemas.map(renderSchemaCard)}</div>
            </div>
        );
    };

    return (
        <Dialog onOpenChange={open => open && connectorSchemasRefetch()}>
            <DialogTrigger asChild>
                {triggerButton ?? (
                    <Button title={!platformConfigured ? "Platform Service must be configured to create connectors" : "Create Connector"} testid="createConnector" variant="outline" disabled={!platformConfigured}>
                        <Plus />
                        Create Connector
                    </Button>
                )}
            </DialogTrigger>

            <DialogContent className="sm:max-w-225">
                <DialogHeader>
                    <DialogTitle>Select Connector to Create</DialogTitle>
                    <DialogDescription>Choose from the following apps or create a custom connector with an existing API or MCP server.</DialogDescription>
                </DialogHeader>

                <Tabs defaultValue="apps" className="mt-2 flex w-full flex-col">
                    <TabsList className="h-12 w-full justify-start rounded-none border-b bg-transparent p-0">
                        <TabsTrigger
                            value="apps"
                            className="h-full rounded-none border-b-2 border-transparent px-6 text-(--secondary-text-wMain) data-[state=active]:border-b-(--brand-wMain) data-[state=active]:bg-transparent data-[state=active]:text-(--primary-text-wMain) data-[state=active]:shadow-none"
                        >
                            Apps
                        </TabsTrigger>
                        <TabsTrigger
                            value="custom"
                            className="h-full rounded-none border-b-2 border-transparent px-6 text-(--secondary-text-wMain) data-[state=active]:border-b-(--brand-wMain) data-[state=active]:bg-transparent data-[state=active]:text-(--primary-text-wMain) data-[state=active]:shadow-none"
                        >
                            Custom
                        </TabsTrigger>
                    </TabsList>

                    <div className="h-100 overflow-hidden">
                        <TabsContent value="apps" className="h-full overflow-y-auto pt-4">
                            {renderGrid(appsConnectorSchemas, "No App Connector Schemas Found")}
                        </TabsContent>

                        <TabsContent value="custom" className="h-full overflow-y-auto pt-4">
                            {renderGrid(customConnectorSchemas, "No Custom Connector Schemas Found", true)}
                        </TabsContent>
                    </div>
                </Tabs>

                <DialogFooter>
                    <DialogClose asChild>
                        <Button variant="outline" testid="closeAvailableConnectorsDialog" title="Close">
                            Close
                        </Button>
                    </DialogClose>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};
