// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useState } from "react";

import { useChatContext } from "@/providers/contexts";

import { deferPanelClose } from "@/hooks/useRadixPointerEventsFix";

import { PageSidePanelWrapper } from "@/common/layout";

import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import { EmptyState, MorePopoverButton, OnboardingBanner, PaginationControls } from "@/common";
import { type Connector, CONNECTOR_TYPE_MAP } from "@/types";
import { DOC_LINKS } from "@/utils";

import { ConnectorIcon } from "./ConnectorIcon";
import { ConnectorMorePopover } from "./ConnectorMorePopover";
import { ConnectorOnboardingView } from "./ConnectorOnboardingView";
import { ConnectorSidePanel } from "./ConnectorSidePanel";
import { SCHEMA_TYPE_NAME_MAP } from "./hooks/useConnectorSchemas";
import { useConnectorsPaginated } from "./hooks/useConnectorsPaginated";
import { PlatformConfigIndicator } from "./PlatformConfigIndicator";

const CONNECTOR_STORAGE_KEY = "sam-connector-onboarding-dismissed";
const CONNECTOR_HEADER = "Connectors extend your agents with enterprise integrations and real-time data access.";
const CONNECTOR_DESCRIPTION = "Set up integrations to databases, APIs, or SaaS tools, and your agent will retrieve data on demand. Perfect for scenarios that need current information or access to your organization's private data.";
const CONNECTOR_LEARN_MORE_TEXT = "Learn how to create connectors";
const CONNECTOR_URL = DOC_LINKS.buildingConnectors;

export const ConnectorsView: React.FC = () => {
    const { handleViewConnector, getConnectorSchema } = useChatContext();

    const [screenHeight, setScreenHeight] = useState<number>(globalThis.window === undefined ? 768 : globalThis.window.innerHeight);
    const [selectedConnector, setSelectedConnector] = useState<Connector | null>(null);
    const [isSidePanelOpen, setIsSidePanelOpen] = useState<boolean>(false);

    const itemsPerPage = screenHeight >= 900 ? 20 : 10;

    const { connectors, isLoading, error, pageNumber, setPageNumber, totalPages, totalCount, searchTerm, setSearchTerm, refetch } = useConnectorsPaginated({ enabled: true, pageSize: itemsPerPage });

    useEffect(() => {
        const handleResize = () => {
            if (globalThis.window === undefined) return;
            setScreenHeight(globalThis.window.innerHeight);
        };

        window.addEventListener("resize", handleResize);
        return () => window.removeEventListener("resize", handleResize);
    }, []);

    useEffect(() => {
        if (!selectedConnector) return;
        if (!connectors.some(connector => connector.id === selectedConnector.id)) {
            deferPanelClose(() => {
                setIsSidePanelOpen(false);
                setSelectedConnector(null);
            });
        }
    }, [connectors, selectedConnector]);

    const handlePageChange = (page: number) => {
        if (page >= 1 && page <= totalPages) {
            setPageNumber(page);
        }
    };

    const handleSelectConnector = (connector: Connector | null) => {
        if (connector) {
            setSelectedConnector(connector);
            setIsSidePanelOpen(true);
        }
    };

    const handleCloseSidePanel = () => {
        setSelectedConnector(null);
        setIsSidePanelOpen(false);
    };

    const isInitialLoad = isLoading && totalCount === 0 && !searchTerm;
    if (isInitialLoad) {
        return <EmptyState variant="loading" title="Loading Connectors..." />;
    }

    if (error && connectors.length === 0) {
        return <EmptyState variant="error" title="Failed to Load Connectors" subtitle={error} buttons={[{ text: "Retry", variant: "default", onClick: () => refetch() }]} />;
    }

    if (!searchTerm && totalCount === 0 && !isLoading && !error) {
        return <ConnectorOnboardingView title={CONNECTOR_HEADER} description={CONNECTOR_DESCRIPTION} />;
    }

    return (
        <div className="flex h-full w-full">
            <div className="flex min-w-0 flex-1 flex-col">
                <PlatformConfigIndicator />
                <OnboardingBanner storageKey={CONNECTOR_STORAGE_KEY} header={CONNECTOR_HEADER} description={CONNECTOR_DESCRIPTION} learnMoreText={CONNECTOR_LEARN_MORE_TEXT} learnMoreUrl={CONNECTOR_URL} className="mx-6 mt-6" />
                <SearchInput value={searchTerm} onChange={setSearchTerm} placeholder="Filter by name" testid="connectorSearchInput" className="mx-6 mt-6" />

                <div className="flex min-h-0 flex-1 flex-col overflow-hidden px-6 pb-6">
                    <div className="flex min-h-0 flex-1 flex-col pt-6">
                        {connectors.length > 0 ? (
                            <div className="min-h-0 flex-1 overflow-y-auto rounded-xs border">
                                <Table containerClassName="overflow-x-visible">
                                    <TableHeader sticky>
                                        <TableRow>
                                            <TableHead className="font-semibold">
                                                <div className="pl-4">Name</div>
                                            </TableHead>
                                            <TableHead className="hidden font-semibold sm:table-cell sm:w-1/5">Type</TableHead>
                                            <TableHead className="hidden text-right font-semibold sm:table-cell sm:w-16">Agent Usage</TableHead>
                                            <TableHead className="hidden font-semibold sm:table-cell sm:w-1/5">Created By</TableHead>
                                            <TableHead className="w-12 font-semibold"></TableHead>
                                        </TableRow>
                                    </TableHeader>
                                    <TableBody borderLastRow>
                                        {connectors.map(connector => {
                                            const connectorSchema = getConnectorSchema(connector.type, connector.subtype);
                                            return (
                                                <TableRow
                                                    key={connector.id}
                                                    onClick={() => handleSelectConnector(connector)}
                                                    className="cursor-pointer hover:bg-(--secondary-w10)/50"
                                                    data-state={selectedConnector?.id === connector.id ? "selected" : undefined}
                                                >
                                                    <TableCell className="flex min-w-0 items-center pl-6">
                                                        {connectorSchema && <ConnectorIcon icon={connectorSchema.icon} name={connectorSchema.name} size="sm" />}
                                                        <Button
                                                            testid={`connector-name-${connector.id}`}
                                                            title={connector.name}
                                                            variant="link"
                                                            onClick={e => {
                                                                e.stopPropagation();
                                                                handleViewConnector(connector);
                                                            }}
                                                            className="min-w-0"
                                                        >
                                                            <span className="truncate">{connector.name}</span>
                                                        </Button>
                                                    </TableCell>
                                                    <TableCell className="hidden sm:table-cell">{SCHEMA_TYPE_NAME_MAP[CONNECTOR_TYPE_MAP[connector.type]]}</TableCell>
                                                    <TableCell className="hidden text-right sm:table-cell">{connector.agentCount ?? 0}</TableCell>
                                                    <TableCell className="hidden sm:table-cell">{connector.createdBy ?? "N/A"}</TableCell>
                                                    <TableCell className="w-12">
                                                        <ConnectorMorePopover key="more-popover" connector={connector}>
                                                            {MorePopoverButton}
                                                        </ConnectorMorePopover>
                                                    </TableCell>
                                                </TableRow>
                                            );
                                        })}
                                    </TableBody>
                                </Table>
                            </div>
                        ) : (
                            <div className="flex min-h-[300px] flex-1 items-center justify-center">
                                <EmptyState variant="noImage" title="No connectors found" subtitle="Try adjusting your search terms" buttons={[{ text: "Clear Filter", variant: "default", onClick: () => setSearchTerm("") }]} />
                            </div>
                        )}
                    </div>
                </div>
                <PaginationControls totalPages={totalPages} currentPage={pageNumber} onPageChange={handlePageChange} />
            </div>
            {selectedConnector ? <PageSidePanelWrapper isSidePanelOpen={isSidePanelOpen} onClose={handleCloseSidePanel} sidepanel={<ConnectorSidePanel connector={selectedConnector} onClose={handleCloseSidePanel} />} /> : null}
        </div>
    );
};
