// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";
import { Pencil, Upload } from "lucide-react";

import { toolsetsApi, useCustomToolsets } from "@/api/toolsets";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { PageFooter } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { downloadBlob } from "@/utils/download";

import { Button } from "@/ui/button";

import { EmptyState, Header } from "@/common";
import type { Toolset } from "@/types";

import { ToolsetAgentUsage } from "./ToolsetAgentUsage";
import { ToolsetView } from "./ToolsetView";

const renderToolsetBody = ({ customToolsetsLoading, toolset, currentTab, navigate }: { customToolsetsLoading: boolean; toolset: Toolset | null; currentTab: "details" | "agentUsage"; navigate: (path: string) => void }) => {
    if (customToolsetsLoading) {
        return <EmptyState variant="loading" title="Loading Toolset..." />;
    }
    if (!toolset) {
        return <EmptyState variant="error" title="Toolset Not Found" buttons={[{ text: "Go To Toolsets", variant: "default", onClick: () => navigate("/toolsets") }]} />;
    }
    return (
        <>
            {currentTab === "details" && <ToolsetView toolset={toolset} />}
            {currentTab === "agentUsage" && <ToolsetAgentUsage toolset={toolset} />}
        </>
    );
};

export const ToolsetViewPage = () => {
    const { navigate, handleEditCustomToolset, useLoaderData, displayError } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { data: customToolsets = [], isLoading: customToolsetsLoading } = useCustomToolsets();
    const { toolsetId } = useLoaderData();

    const [currentTab, setCurrentTab] = useState<"details" | "agentUsage">("details");

    const toolset = useMemo(() => {
        return customToolsets?.find(p => p.id === toolsetId) ?? null;
    }, [toolsetId, customToolsets]);

    const handleExport = useCallback(async () => {
        try {
            downloadBlob(await toolsetsApi.downloadBundle(toolsetId), toolset?.zipFilename ?? "package.zip");
        } catch (error) {
            displayError({ title: "Export Failed", error: getErrorMessage(error, "Could not download toolset file."), errorSource: "api" });
        }
    }, [toolsetId, toolset?.zipFilename, displayError]);

    const tabs = useMemo(() => {
        return [
            { id: "details", label: "Details", isActive: currentTab === "details", onClick: () => setCurrentTab("details") },
            { id: "agentUsage", label: `Agent Usage (${toolset?.agents?.length ?? 0})`, isActive: currentTab === "agentUsage", onClick: () => setCurrentTab("agentUsage") },
        ];
    }, [currentTab, toolset]);

    const headerButtons = useMemo(() => {
        if (currentTab !== "details") return undefined;
        return [
            <Button key="export" data-testid="exportToolsetButton" variant="ghost" onClick={handleExport} title="Export Toolset File" disabled={!toolset?.zipFilename}>
                <Upload />
                Export Toolset File
            </Button>,
            ...(userHasCapability(CAPABILITIES.updateToolsets)
                ? [
                      <Button key="edit" data-testid="editToolsetButton" variant="ghost" onClick={() => toolset && handleEditCustomToolset(toolset)} title="Edit Toolset" disabled={!toolset}>
                          <Pencil />
                          Edit
                      </Button>,
                  ]
                : []),
        ];
    }, [currentTab, toolset, handleEditCustomToolset, handleExport, userHasCapability, CAPABILITIES]);

    const title = toolset?.name ?? "Toolset";

    return (
        <div className="flex h-full w-full flex-col overflow-hidden min-[896px]:min-w-4xl">
            <Header title={title} breadcrumbs={[{ label: "Toolsets", onClick: () => navigate("/toolsets") }, { label: title }]} buttons={headerButtons} tabs={tabs} />

            {renderToolsetBody({ customToolsetsLoading, toolset, currentTab, navigate })}

            <PageFooter>
                <Button variant="outline" onClick={() => navigate("/toolsets")}>
                    Close
                </Button>
            </PageFooter>
        </div>
    );
};
