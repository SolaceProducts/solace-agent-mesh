// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Info } from "lucide-react";

import { useGateways } from "@/api/gateways";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { deferPanelClose } from "@/hooks/useRadixPointerEventsFix";

import { PageSidePanelWrapper } from "@/common/layout";

import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import type { Gateway } from "@/types/models";

import { EmptyState, MessageBanner, MorePopoverButton, PaginationControls } from "@/common";

import { SyncStatusTooltip } from "./common";
import { GatewayMorePopover } from "./GatewayMorePopover";
import { GatewaysAvailableDialog } from "./GatewaysAvailableDialog";
import { GatewaySidePanel } from "./GatewaySidePanel";
import { GatewayStatusIndicator } from "./GatewayStatusIndicator";
import { GATEWAY_TYPE_INFO } from "./GatewayTypeChip";
import { isConsumeOnlyEventMeshEntrypoint } from "./isConsumeOnlyEventMeshEntrypoint";

const EMPTY_GATEWAYS: Gateway[] = [];

export const GatewaysView = () => {
    const { navigate } = useChatContext();
    const { data: gateways = EMPTY_GATEWAYS, isLoading, error: errorObj, refetch } = useGateways();
    const error = errorObj?.message ?? null;
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreateGateways = userHasCapability(CAPABILITIES.createGateways);

    const [searchTerm, setSearchTerm] = useState<string>("");
    const [currentPage, setCurrentPage] = useState<number>(1);
    const [selectedGateway, setSelectedGateway] = useState<Gateway | null>(null);
    const [isSidePanelOpen, setIsSidePanelOpen] = useState<boolean>(false);
    const [screenHeight, setScreenHeight] = useState<number>(typeof window !== "undefined" ? window.innerHeight : 768);

    const itemsPerPage = screenHeight >= 900 ? 20 : 10;

    useEffect(() => {
        const handleResize = () => {
            if (typeof window !== "undefined") {
                setScreenHeight(window.innerHeight);
            }
        };

        window.addEventListener("resize", handleResize);
        return () => window.removeEventListener("resize", handleResize);
    }, []);

    const filteredGateways = useMemo(() => {
        if (!gateways || gateways.length === 0) return [];

        const result = searchTerm.trim() ? gateways.filter(gateway => gateway.name.toLowerCase().includes(searchTerm.toLowerCase())) : gateways;

        return result.slice().sort((a, b) => a.name.localeCompare(b.name));
    }, [gateways, searchTerm]);

    const totalPages = Math.ceil(filteredGateways.length / itemsPerPage);
    const effectiveCurrentPage = Math.min(currentPage, Math.max(totalPages, 1));
    const startIndex = (effectiveCurrentPage - 1) * itemsPerPage;
    const endIndex = startIndex + itemsPerPage;
    const currentGateways = filteredGateways?.slice(startIndex, endIndex) || [];

    useEffect(() => {
        setCurrentPage(1);
    }, [searchTerm]);

    useEffect(() => {
        if (!selectedGateway) return;
        const updatedGateway = gateways?.find(gateway => gateway.id === selectedGateway.id);
        if (updatedGateway && updatedGateway !== selectedGateway) {
            setSelectedGateway(updatedGateway);
        }
    }, [gateways, selectedGateway]);

    const handlePageChange = (page: number) => {
        if (page >= 1 && page <= totalPages) {
            setCurrentPage(page);
        }
    };

    const handleSelectGateway = useCallback((gateway: Gateway) => {
        setSelectedGateway(gateway);
        setIsSidePanelOpen(true);
    }, []);

    const handleCloseSidePanel = useCallback(() => {
        setIsSidePanelOpen(false);
    }, []);

    const handleCloseComplete = useCallback(() => {
        setSelectedGateway(null);
    }, []);

    const handleSidePanelUpdate = useCallback(() => {
        refetch();
    }, [refetch]);

    const onGatewayDeleted = useCallback(() => {
        refetch();
        deferPanelClose(() => {
            setSelectedGateway(null);
            setIsSidePanelOpen(false);
        });
    }, [refetch]);

    const handleEditGateway = useCallback(
        (gateway: Gateway) => {
            navigate(`/entrypoints/${gateway.id}/edit`);
        },
        [navigate]
    );

    const handleOpenGateway = useCallback(
        (gateway: Gateway) => {
            navigate(`/entrypoints/${gateway.id}`);
        },
        [navigate]
    );

    if (isLoading) {
        return (
            <div className="flex h-full w-full overflow-hidden">
                <div className="flex flex-1 flex-col">
                    <EmptyState variant="loading" title="Loading entrypoints..." />
                </div>
            </div>
        );
    }

    return (
        <div className="flex h-full w-full">
            <div className="flex min-w-0 flex-1 flex-col">
                {error && (
                    <div className="p-6">
                        <MessageBanner variant="error" message={`Error loading entrypoints: ${error}`} />
                    </div>
                )}
                {(currentGateways?.length > 0 || searchTerm) && (
                    <div className="p-6">
                        <SearchInput value={searchTerm} onChange={setSearchTerm} placeholder="Filter by name" testid="gatewaySearchInput" />
                    </div>
                )}
                <div className="flex min-h-0 flex-1 flex-col overflow-hidden px-6 pb-6">
                    <div className="flex min-h-0 flex-1 flex-col">
                        {currentGateways.length > 0 ? (
                            <div className="min-h-0 flex-1 overflow-y-auto rounded-xs border">
                                <Table containerClassName="overflow-x-visible">
                                    <TableHeader sticky>
                                        <TableRow>
                                            <TableHead className="font-semibold">
                                                <div className="pl-4">Name</div>
                                            </TableHead>
                                            <TableHead className="font-semibold sm:w-1/6">Status</TableHead>
                                            <TableHead className="hidden font-semibold sm:table-cell sm:w-1/6">Type</TableHead>
                                            <TableHead className="hidden font-semibold sm:table-cell sm:w-1/6">Created By</TableHead>
                                            <TableHead className="w-12 font-semibold"></TableHead>
                                        </TableRow>
                                    </TableHeader>
                                    <TableBody borderLastRow>
                                        {currentGateways.map(gateway => (
                                            <TableRow key={gateway.id} onClick={() => handleSelectGateway(gateway)} className="cursor-pointer hover:bg-(--secondary-w10)/50" data-state={selectedGateway?.id === gateway.id ? "selected" : undefined}>
                                                <TableCell className="max-w-0">
                                                    <Button
                                                        testid={`gateway-name-${gateway.id}`}
                                                        title={gateway.name}
                                                        variant="link"
                                                        onClick={e => {
                                                            e.stopPropagation();
                                                            handleOpenGateway(gateway);
                                                        }}
                                                        className="min-w-0 justify-start"
                                                    >
                                                        {(() => {
                                                            const Icon = GATEWAY_TYPE_INFO[gateway.type]?.icon;
                                                            return Icon ? <Icon size="16" /> : null;
                                                        })()}
                                                        <span className="min-w-0 truncate">{gateway.name}</span>
                                                    </Button>
                                                </TableCell>
                                                <TableCell>
                                                    <div className="flex items-center gap-2">
                                                        {gateway.deploymentStatus && <GatewayStatusIndicator gateway={gateway} badgeClassName="" />}
                                                        {userHasCapability(CAPABILITIES.readGateways) ? <SyncStatusTooltip gateway={gateway} /> : null}
                                                        {isConsumeOnlyEventMeshEntrypoint(gateway) && (
                                                            <Badge data-testid={`gatewayUiNoticeBadge-${gateway.id}`} className="gap-1" tooltip="Entrypoint managed from the command line">
                                                                <Info size={12} />
                                                            </Badge>
                                                        )}
                                                    </div>
                                                </TableCell>
                                                <TableCell data-testid="gatewayRowArea" className="hidden sm:table-cell">
                                                    {GATEWAY_TYPE_INFO[gateway.type]?.label ?? gateway.type}
                                                </TableCell>
                                                <TableCell className="hidden sm:table-cell">{gateway.createdBy ?? "N/A"}</TableCell>
                                                <TableCell className="w-12" onClick={e => e.stopPropagation()}>
                                                    <GatewayMorePopover gateway={gateway} onEdit={() => handleEditGateway(gateway)} onOpenGateway={() => handleOpenGateway(gateway)} onUpdate={handleSidePanelUpdate} onGatewayDeleted={onGatewayDeleted}>
                                                        {MorePopoverButton}
                                                    </GatewayMorePopover>
                                                </TableCell>
                                            </TableRow>
                                        ))}
                                    </TableBody>
                                </Table>
                            </div>
                        ) : (
                            <div className="flex min-h-[300px] flex-1 items-center justify-center">
                                {searchTerm ? (
                                    <EmptyState variant="noImage" title="No entrypoints found" subtitle="Try adjusting your search terms" buttons={[{ text: "Clear Filter", variant: "default", onClick: () => setSearchTerm("") }]} />
                                ) : (
                                    <div className="flex flex-col items-center">
                                        <EmptyState
                                            className="max-w-2xl text-center"
                                            variant="noImage"
                                            title="Connect external systems to your agent mesh"
                                            subtitle="You haven't created any entrypoints yet. Entrypoints enable communication between external platforms and your AI agents."
                                        />
                                        {canCreateGateways && <GatewaysAvailableDialog triggerButton={<Button className="w-min">See Available Entrypoint Types</Button>} />}
                                    </div>
                                )}
                            </div>
                        )}
                    </div>
                </div>

                <PaginationControls currentPage={effectiveCurrentPage} totalPages={totalPages} onPageChange={handlePageChange} />
            </div>

            <PageSidePanelWrapper
                isSidePanelOpen={isSidePanelOpen}
                onClose={handleCloseSidePanel}
                sidepanel={selectedGateway ? <GatewaySidePanel gateway={selectedGateway} onClose={handleCloseSidePanel} onUpdate={handleSidePanelUpdate} /> : null}
                onCloseComplete={handleCloseComplete}
            />
        </div>
    );
};
