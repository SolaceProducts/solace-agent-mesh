// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";

import { useConnectors } from "@/api/connectors";

import { useChatContext } from "@/providers/contexts";

import { PAGE_COMMON_CLASSES, PageContentWrapper, PageFooter, PageSection } from "@/common/layout";

import { Button } from "@/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/ui/tabs";

import { InputSchemaDisplay } from "@/features/connectors/components/InputSchemaDisplay";

import { EmptyState, Header, PageLayout } from "@/common";

import { EventMeshAdvancedSettingsSummary } from "./components/EventMeshAdvancedSettingsSummary";
import { ConnectorDisplayField } from "./ConnectorDisplayField";
import { EVENT_MESH_SECTION_TITLE } from "./eventMeshStyles";
import { EVENT_MESH_RESPONSE_MODE_OPTIONS, type EventMeshOperation, responseModeValue } from "./eventMeshUtils";

type SchemaViewMode = "table" | "json";

export const EventMeshToolDetailPage = () => {
    const { navigate, useLoaderData } = useChatContext();
    const { data: connectors = [], isLoading: connectorsLoading } = useConnectors();
    const { connectorId, toolName } = useLoaderData();
    const [schemaViewMode, setSchemaViewMode] = useState<SchemaViewMode>("table");

    const connector = useMemo(() => connectors.find(c => c.id === connectorId) ?? null, [connectorId, connectors]);

    const operation = useMemo((): EventMeshOperation | null => {
        if (connector?.type !== "event_mesh") return null;
        const operations = Array.isArray(connector.values?.operations) ? (connector.values.operations as unknown as EventMeshOperation[]) : [];
        return operations.find(op => op.tool_name === decodeURIComponent(toolName)) ?? null;
    }, [connector, toolName]);

    const handleBack = () => navigate(`/connectors/${connectorId}`);

    const toolNameStr = operation?.tool_name ?? toolName;
    const responseMode = responseModeValue(operation?.wait_for_response);

    const body = (() => {
        if (connectorsLoading) return <EmptyState variant="loading" title="Loading Tool..." />;
        if (!connector) return <EmptyState variant="error" title="Connector Not Found" buttons={[{ text: "Go To Connectors", variant: "default", onClick: () => navigate("/connectors") }]} />;
        if (!operation) return <EmptyState variant="error" title="Tool Not Found" buttons={[{ text: "Back to Connector", variant: "default", onClick: handleBack }]} />;
        return (
            <PageContentWrapper>
                <div className={PAGE_COMMON_CLASSES}>
                    <PageSection className="gap-6">
                        <ConnectorDisplayField label="Tool Description" value={operation.tool_description} fieldType="textarea" fieldName="tool_description" />
                    </PageSection>

                    <PageSection className="mt-8">
                        <div className={`${EVENT_MESH_SECTION_TITLE} mb-2`}>Message Routing and Delivery</div>
                        <div className="space-y-6">
                            <ConnectorDisplayField label="Topic Address" value={operation.topic} fieldName="topic" />
                            <ConnectorDisplayField label="Response Mode" value={responseMode} fieldType="select" options={EVENT_MESH_RESPONSE_MODE_OPTIONS} fieldName="response_mode" />
                        </div>
                    </PageSection>

                    <PageSection className="mt-8">
                        <EventMeshAdvancedSettingsSummary operation={operation} />
                    </PageSection>

                    <PageSection className="mt-8">
                        <div className={`${EVENT_MESH_SECTION_TITLE} pb-2`}>Input Schema</div>
                        {Object.keys(operation.input_schema?.properties ?? {}).length > 0 ? (
                            <div className="rounded border">
                                <div className="flex justify-end border-b px-4 py-3">
                                    <Tabs value={schemaViewMode} onValueChange={v => setSchemaViewMode(v as SchemaViewMode)}>
                                        <TabsList className="bg-transparent p-0">
                                            <TabsTrigger value="table" title="Table View" className="rounded-none rounded-l-md px-3 data-[state=active]:z-10">
                                                Table View
                                            </TabsTrigger>
                                            <TabsTrigger value="json" title="JSON View" className="rounded-none rounded-r-md border-y border-r border-l-0 px-3 data-[state=active]:z-10">
                                                JSON View
                                            </TabsTrigger>
                                        </TabsList>
                                    </Tabs>
                                </div>
                                <InputSchemaDisplay schema={operation.input_schema as Record<string, unknown>} viewMode={schemaViewMode} />
                            </div>
                        ) : (
                            <div className="py-4 text-(--secondary-text-wMain)">No input schema defined for this tool</div>
                        )}
                    </PageSection>
                </div>
            </PageContentWrapper>
        );
    })();

    return (
        <PageLayout>
            <Header title={toolNameStr} breadcrumbs={[{ label: "Agent Mesh", onClick: () => navigate("/connectors") }, { label: connector?.name ?? "Connector", onClick: () => navigate(`/connectors/${connectorId}`) }, { label: toolNameStr }]} />
            {body}
            <PageFooter>
                <Button data-testid="closeButton" variant="outline" title="Close" onClick={handleBack}>
                    Close
                </Button>
            </PageFooter>
        </PageLayout>
    );
};
