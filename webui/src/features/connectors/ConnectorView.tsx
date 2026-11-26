// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useChatContext } from "@/providers/contexts";

import { PAGE_COMMON_CLASSES, PageContentWrapper, PageSection } from "@/common/layout";

import { useConnectorTools } from "@/features/agents/hooks";
import { ToolCard } from "@/features/connectors/components/ToolCard";
import { ConnectorDisplayField } from "@/features/connectors/ConnectorDisplayField";
import { ConnectorViewValues } from "@/features/connectors/ConnectorViewValues";

import { Metadata } from "@/common";
import type { Connector } from "@/types";

interface ConnectorViewProps {
    connector: Connector;
}

export const ConnectorView: React.FC<ConnectorViewProps> = ({ connector }) => {
    const { getConnectorSchema, navigate } = useChatContext();
    const { type, subtype, description } = connector;

    const schema = getConnectorSchema(type, subtype);

    const {
        tools: selectedTools,
        totalCount: totalToolsCount,
        isLoading: isLoadingTools,
    } = useConnectorTools({
        connector,
        enabled: true,
    });

    const hasTools = !!((type === "mcp" && connector.values.selected_tools) || (type === "api" && connector.values.allow_list) || (type === "event_mesh" && Array.isArray(connector.values.operations) && connector.values.operations.length > 0));

    const containerClassName = hasTools ? "w-full max-w-full" : PAGE_COMMON_CLASSES;

    const toolPlural = totalToolsCount === 1 ? "" : "s";
    const toolCountLabel = type === "event_mesh" ? `${totalToolsCount} tool${toolPlural}` : `${selectedTools.length} of ${totalToolsCount} enabled`;

    const renderConnectorSummary = () => {
        return (
            <div className="flex h-full flex-col">
                <div>
                    <PageSection className="gap-4">
                        <ConnectorDisplayField key="type" fieldName="connector_type" label="Type" value={schema?.name ?? "N/A"} />
                        <ConnectorDisplayField key="description" fieldName="description" label="Description" value={description?.replaceAll(/[\r\n]+/g, " ")} fieldType="textarea" />
                    </PageSection>
                    <ConnectorViewValues connector={connector} />
                </div>
                {connector?.id ? <Metadata metadata={connector} className="!mt-auto" /> : null}
            </div>
        );
    };

    return (
        <PageContentWrapper innerClassName="h-full">
            <div className={`${containerClassName} h-full`}>
                {hasTools ? (
                    <div className="flex h-full gap-8 overflow-hidden">
                        <div className="flex w-[25%] flex-col self-stretch overflow-y-auto">{renderConnectorSummary()}</div>

                        <div className="border-l" />

                        <div className="flex-1 overflow-y-auto">
                            <PageSection>
                                <div className="mb-4 flex items-center justify-between font-semibold">
                                    <span>Tools</span>
                                    {totalToolsCount > 0 && <span className="text-sm font-normal text-(--secondary-text-wMain)">{toolCountLabel}</span>}
                                </div>
                                {isLoadingTools ? (
                                    <div className="text-(--secondary-text-wMain)">Loading tools...</div>
                                ) : selectedTools.length > 0 ? (
                                    <div className="flex flex-col gap-2">
                                        {selectedTools.map(tool => (
                                            <ToolCard key={tool.operationId} tool={tool} clickable={hasTools} onClick={hasTools ? () => navigate(`/connectors/${connector.id}/tools/${type}/${encodeURIComponent(tool.operationId)}`) : undefined} />
                                        ))}
                                    </div>
                                ) : (
                                    <div className="text-(--secondary-text-wMain)">No tools selected</div>
                                )}
                            </PageSection>
                        </div>
                    </div>
                ) : (
                    renderConnectorSummary()
                )}
            </div>
        </PageContentWrapper>
    );
};
