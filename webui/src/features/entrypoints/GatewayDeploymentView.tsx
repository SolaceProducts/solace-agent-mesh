// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";
import { AlertCircle, History } from "lucide-react";

import { gatewaysApi } from "@/api/gateways";

import { getSectionTitle, PAGE_COMMON_CLASSES, PageSection } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { getDeploymentActionLabel } from "@/utils/deployment";
import { formatTimestamp } from "@/utils/format";

import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import type { Gateway, GatewayDeployment } from "@/types/models";

import { EmptyState, MessageBanner } from "@/common";

interface GatewayDeploymentViewProps {
    gateway: Gateway;
}

export const GatewayDeploymentView = ({ gateway }: GatewayDeploymentViewProps) => {
    const [deployments, setDeployments] = useState<GatewayDeployment[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    const fetchDeployments = useCallback(async () => {
        if (!gateway.id) return;

        setIsLoading(true);
        try {
            setDeployments(await gatewaysApi.getDeployments(gateway.id));
        } catch (err) {
            setError(getErrorMessage(err, "Failed to load deployment history."));
        } finally {
            setIsLoading(false);
        }
    }, [gateway.id]);

    useEffect(() => {
        fetchDeployments();

        return () => {
            setDeployments([]);
            setError(null);
        };
    }, [fetchDeployments]);

    if (isLoading) {
        return (
            <div className={PAGE_COMMON_CLASSES}>
                <PageSection className="h-[calc(100vh-200px)]">
                    <EmptyState variant="loading" title="Loading Deployment History..." className="" />
                </PageSection>
            </div>
        );
    }

    return (
        <div className={PAGE_COMMON_CLASSES}>
            {error && <MessageBanner variant="error" className="mb-4" message={error} />}
            <PageSection className="flex flex-col">
                {getSectionTitle(History, "Deployment History")}
                {deployments.length > 0 ? (
                    <div className="mt-8 max-h-[calc(100vh-300px)] overflow-y-auto border">
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead className="px-2 font-semibold">Deployment Action</TableHead>
                                    <TableHead className="px-2 font-semibold">Status</TableHead>
                                    <TableHead className="hidden px-2 font-semibold sm:table-cell">Initiated By</TableHead>
                                    <TableHead className="hidden px-2 font-semibold sm:table-cell">Time</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody borderLastRow>
                                {deployments.map(deployment => (
                                    <TableRow key={deployment.id} data-testid={`deploymentHistoryRow-${deployment.action}`}>
                                        <TableCell>{getDeploymentActionLabel(deployment.action)}</TableCell>
                                        <TableCell>
                                            <div className="flex items-center gap-2 py-2">
                                                <span className="capitalize">{deployment.status ?? "Unknown"}</span>
                                                {deployment.errorMessage && (
                                                    <Popover>
                                                        <PopoverTrigger asChild>
                                                            <AlertCircle size={16} className="shrink-0 cursor-pointer text-(--error-wMain)" />
                                                        </PopoverTrigger>
                                                        <PopoverContent side="top" sideOffset={8} className="max-w-md">
                                                            <div className="p-4 text-sm">
                                                                <div className="font-semibold text-(--error-wMain)">Error</div>
                                                                <div className="mt-1">{deployment.errorMessage}</div>
                                                            </div>
                                                        </PopoverContent>
                                                    </Popover>
                                                )}
                                            </div>
                                        </TableCell>
                                        <TableCell className="hidden sm:table-cell">{deployment.createdBy}</TableCell>
                                        <TableCell className="hidden sm:table-cell">{formatTimestamp(deployment.createdAt)}</TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    </div>
                ) : (
                    <div className="mt-8 flex h-[100px] items-center justify-center rounded-xs bg-(--secondary-w10)">
                        <span className="text-(--secondary-text-wMain)">No deployment history available</span>
                    </div>
                )}
            </PageSection>
        </div>
    );
};
