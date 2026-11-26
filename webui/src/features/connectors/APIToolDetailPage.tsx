// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import * as TabsPrimitive from "@radix-ui/react-tabs";
import jsyaml from "js-yaml";

import { useConnectors } from "@/api/connectors";
import { connectorsApi } from "@/api/connectors/versioned";

import { useChatContext } from "@/providers/contexts";

import { PAGE_COMMON_CLASSES, PageContentWrapper, PageFooter, PageSection } from "@/common/layout";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { Tabs, TabsContent, TabsList } from "@/ui/tabs";

import { getMethodBadgeClassName } from "@/features/connectors/components/MethodBadge";
import { ParametersDisplay } from "@/features/connectors/components/ParametersDisplay";
import { RequestBodyDisplay } from "@/features/connectors/components/RequestBodyDisplay";
import { extractTools } from "@/features/connectors/utils";
import { getResource } from "@/features/connectors/utils/resourcesCache";

import { Header, PageLayout } from "@/common";
import type { Tool } from "@/types";

function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
    return (
        <TabsPrimitive.Trigger
            data-slot="tabs-trigger"
            data-testid={props.value}
            aria-label={props.value}
            title={props.title}
            className={cn(
                "relative box-border inline-flex h-full min-h-[48px] max-w-[360px] min-w-[90px] shrink-0 cursor-pointer flex-col items-center justify-center overflow-hidden border-b-2 border-b-transparent px-4 py-3 text-center align-middle break-words whitespace-normal text-(--secondary-text-wMain) select-none hover:border-b-(--primary-w10) hover:text-(--primary-text-wMain) data-[state=active]:border-b-(--brand-w100) data-[state=active]:font-medium data-[state=active]:text-(--primary-text-wMain)",
                className
            )}
            {...props}
        />
    );
}

const generateUrlExample = (tool: Tool | null): string | null => {
    if (!tool) return null;
    let url = tool.endpoint || "";
    if (tool.parameters) {
        const queryParams = tool.parameters.filter(param => param.in === "query");
        if (queryParams.length > 0) {
            const queryString = queryParams.map(param => `${param.name}={${param.name}}`).join("&");
            url += (url.includes("?") ? "&" : "?") + queryString;
        }
    }
    return url;
};

export const APIToolDetailPage = () => {
    const { navigate, useLoaderData } = useChatContext();
    const { data: connectors = [] } = useConnectors();
    const { connectorId, operationId } = useLoaderData();
    const [selectedTool, setSelectedTool] = useState<Tool | null>(null);
    const [spec, setSpec] = useState<any>(null);

    const connector = useMemo(() => {
        return connectors?.find(c => c.id === connectorId) || null;
    }, [connectorId, connectors]);

    useEffect(() => {
        const loadToolDetails = async () => {
            if (!connector || connector.type !== "api") return;

            const resp = await getResource(connectorsApi.resourcePath(connector.id));
            if (resp.status === 200) {
                const contentType = resp.contentType ?? "application/json";
                const specText = resp.text;
                const spec = contentType.includes("json") ? JSON.parse(specText) : jsyaml.load(specText);
                const allTools = extractTools(spec);
                const tool = allTools.find(tool => tool.operationId === operationId) ?? null;
                setSpec(spec);
                setSelectedTool(tool);
            }
        };
        loadToolDetails();
    }, [connector, operationId]);

    const handleBack = () => {
        navigate(`/connectors/${connectorId}`);
    };

    return (
        <PageLayout>
            <Header
                title={selectedTool?.name || "Tool Detail"}
                breadcrumbs={[{ label: "Connectors", onClick: () => navigate("/connectors") }, { label: connector?.name || "Connector", onClick: () => navigate(`/connectors/${connectorId}`) }, { label: selectedTool?.name || "Tool Detail" }]}
            />
            <PageContentWrapper>
                <div className={PAGE_COMMON_CLASSES}>
                    <PageSection>
                        <div className="grid grid-rows-[auto_1fr] gap-2">
                            <div className="text-(--secondary-text-wMain)">Path</div>
                            <div className="flex items-center gap-2">
                                <span className={getMethodBadgeClassName(selectedTool?.method || "get")}>{selectedTool?.method.toUpperCase()}</span>
                                <pre>{generateUrlExample(selectedTool) || "N/A"}</pre>
                            </div>
                        </div>
                        <div className="grid grid-rows-[auto_1fr] gap-2">
                            <div className="text-(--secondary-text-wMain)">Description</div>
                            <div className="break-words whitespace-pre-wrap">{selectedTool?.description || "No description available"}</div>
                        </div>
                    </PageSection>
                    <PageSection className="mt-4">
                        <Tabs defaultValue="query" className="w-full">
                            <TabsList className="h-[48px] w-full justify-start rounded-none border-b-1 border-b-(--secondary-w20) bg-transparent">
                                <TabsTrigger value="query">Query Params</TabsTrigger>
                                <TabsTrigger value="path">Path Variables</TabsTrigger>
                                <TabsTrigger value="headers">Headers & Cookies</TabsTrigger>
                                <TabsTrigger value="body">Body</TabsTrigger>
                            </TabsList>

                            <TabsContent value="query">
                                <div className="mt-4">
                                    <ParametersDisplay label="Query Parameters" spec={spec} parameters={(selectedTool?.parameters || []).filter(param => param.in === "query")} />
                                </div>
                            </TabsContent>

                            <TabsContent value="path">
                                <div className="mt-4">
                                    <ParametersDisplay label="Path Variables" spec={spec} parameters={(selectedTool?.parameters || []).filter(param => param.in === "path")} />
                                </div>
                            </TabsContent>

                            <TabsContent value="headers">
                                <div className="mt-4">
                                    <ParametersDisplay label="Headers" spec={spec} parameters={(selectedTool?.parameters || []).filter(param => param.in === "header" || param.in === "cookie")} />
                                </div>
                            </TabsContent>

                            <TabsContent value="body">
                                <div className="mt-4">
                                    <RequestBodyDisplay requestBody={selectedTool?.requestBody} spec={spec} />
                                </div>
                            </TabsContent>
                        </Tabs>
                    </PageSection>
                </div>
            </PageContentWrapper>
            <PageFooter>
                <Button data-testid="closeButton" variant="outline" title="Close" onClick={handleBack}>
                    Close
                </Button>
            </PageFooter>
        </PageLayout>
    );
};
