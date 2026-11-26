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

type SchemaViewMode = "table" | "json";

export const MCPToolDetailPage = () => {
    const { navigate, useLoaderData } = useChatContext();
    const { data: connectors = [], isLoading: connectorsLoading } = useConnectors();
    const { connectorId, toolName } = useLoaderData();
    const [schemaViewMode, setSchemaViewMode] = useState<SchemaViewMode>("table");

    const connector = useMemo(() => {
        return connectors?.find(c => c.id === connectorId) || null;
    }, [connectorId, connectors]);

    const tool = useMemo(() => {
        if (!connector || connector.type !== "mcp" || !connector.values.manifest) {
            return null;
        }

        try {
            const manifest = typeof connector.values.manifest === "string" ? JSON.parse(connector.values.manifest) : connector.values.manifest;

            const decodedToolName = decodeURIComponent(toolName);
            const foundTool = manifest.find((t: any) => t.name === decodedToolName);

            return foundTool || null;
        } catch (error) {
            console.error("Failed to parse manifest:", error);
            return null;
        }
    }, [connector, toolName]);

    const handleBack = () => {
        navigate(`/connectors/${connectorId}`);
    };

    const toolNameStr = tool?.name || toolName;

    let body;
    if (connectorsLoading) {
        body = <EmptyState variant="loading" title="Loading Tool..." />;
    } else if (!connector) {
        body = <EmptyState variant="error" title="Connector Not Found" buttons={[{ text: "Go To Connectors", variant: "default", onClick: () => navigate("/connectors") }]} />;
    } else if (!tool) {
        body = <EmptyState variant="error" title="Tool Not Found" buttons={[{ text: "Back to Connector", variant: "default", onClick: handleBack }]} />;
    } else {
        body = (
            <PageContentWrapper>
                <div className={PAGE_COMMON_CLASSES}>
                    <PageSection>
                        <div className="mb-6 grid grid-rows-[auto_1fr] gap-2">
                            <div className="text-(--secondary-text-wMain)">Description</div>
                            <div className="break-all whitespace-pre-wrap">{tool.description || "No description available"}</div>
                        </div>
                    </PageSection>

                    <PageSection>
                        <div className="pb-2 text-(--secondary-text-wMain)">Input Schema</div>
                        {tool.inputSchema ? (
                            <div className="rounded border">
                                <div className="flex justify-end border-b bg-(--background-w10) px-4 py-3">
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
                                <InputSchemaDisplay schema={tool.inputSchema} viewMode={schemaViewMode} />
                            </div>
                        ) : (
                            <div className="py-4">
                                <div className="text-(--secondary-text-wMain)">No input schema defined for this tool</div>
                            </div>
                        )}
                    </PageSection>
                </div>
            </PageContentWrapper>
        );
    }

    return (
        <PageLayout>
            <Header title={toolNameStr} breadcrumbs={[{ label: "Connectors", onClick: () => navigate("/connectors") }, { label: connector?.name ?? "Connector", onClick: () => navigate(`/connectors/${connectorId}`) }, { label: toolNameStr }]} />

            {body}

            <PageFooter>
                <Button data-testid="closeButton" variant="outline" title="Close" onClick={handleBack}>
                    Close
                </Button>
            </PageFooter>
        </PageLayout>
    );
};
