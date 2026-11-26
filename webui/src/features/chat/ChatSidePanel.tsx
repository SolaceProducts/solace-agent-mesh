// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo } from "react";
import { FileText, Link2, Network, PanelRightIcon } from "lucide-react";

import type { SidePanelTabId } from "@/providers/contexts";
import { useChatContext, useUIModeContext } from "@/providers/contexts";

import FlowChartPanel from "@/features/activities/flowChart/FlowChartPanel";
import { useReconciledActivityTask } from "@/features/activities/useReconciledActivityTask";

import { Button, Tabs, TabsContent, TabsList, TabsTrigger } from "@/ui";
import { hasDocumentSearchResults, hasSourcesWithUrls } from "@/utils";

import { FlowChartDetails } from "../activities/FlowChartDetails";

import { ArtifactPanel } from "./artifact/ArtifactPanel";
import { DocumentSourcesPanel } from "./rag/DocumentSourcesPanel";
import { RAGInfoPanel } from "./rag/RAGInfoPanel";

interface ChatSidePanelProps {
    onCollapsedToggle: (isSidePanelCollapsed: boolean) => void;
    isSidePanelCollapsed: boolean;
    setIsSidePanelCollapsed: (isSidePanelCollapsed: boolean) => void;
    isSidePanelTransitioning?: boolean;
}

const ActivityPanelContent: React.FC = () => {
    const { taskIdInSidePanel, activityEventTasks, isResponding, currentTaskId } = useChatContext();

    const { visualizedTask, isLoadingTask, reload } = useReconciledActivityTask(taskIdInSidePanel ?? null, activityEventTasks, isResponding && currentTaskId === taskIdInSidePanel);

    useEffect(() => {
        const handleCompleted = (event: Event) => {
            const completedTaskId = (event as CustomEvent<{ taskId?: string }>).detail?.taskId;
            if (!completedTaskId || completedTaskId !== taskIdInSidePanel) {
                return;
            }
            reload();
        };
        globalThis.addEventListener("background-task-completed", handleCompleted);
        return () => globalThis.removeEventListener("background-task-completed", handleCompleted);
    }, [taskIdInSidePanel, reload]);

    const emptyStateMessage = (() => {
        if (isLoadingTask) {
            return "Loading activity data...";
        }
        if (!taskIdInSidePanel) {
            return "No task selected to display";
        }
        if (!visualizedTask) {
            return "No activity data available for the selected task";
        }
        return null;
    })();

    if (!emptyStateMessage && visualizedTask) {
        return (
            <div className="flex h-full flex-col">
                <FlowChartDetails task={visualizedTask} />
                <FlowChartPanel processedSteps={visualizedTask.steps || []} isRightPanelVisible={false} />
            </div>
        );
    }

    return (
        <div className="flex h-full items-center justify-center p-4">
            <div className="text-center text-(--secondary-text-wMain)">
                <Network className="mx-auto mb-4 h-12 w-12" />
                <div className="text-lg font-medium">Activity</div>
                <div className="mt-2 text-sm">{emptyStateMessage}</div>
            </div>
        </div>
    );
};

export const ChatSidePanel: React.FC<ChatSidePanelProps> = ({ onCollapsedToggle, isSidePanelCollapsed, setIsSidePanelCollapsed }) => {
    const { activeSidePanelTab, setActiveSidePanelTab, setPreviewArtifact, taskIdInSidePanel, ragData, ragEnabled, customSidePanelTabs } = useChatContext();
    const { showActivityPanel } = useUIModeContext();

    const filteredRagData = taskIdInSidePanel ? ragData.filter(r => r.taskId === taskIdInSidePanel) : ragData;

    const hasSourcesInSession = useMemo(() => {
        if (!ragData || ragData.length === 0) return false;

        if (hasSourcesWithUrls(ragData)) return true;

        return hasDocumentSearchResults(ragData);
    }, [ragData]);

    useEffect(() => {
        if (!showActivityPanel && activeSidePanelTab === "activity") {
            setActiveSidePanelTab("files");
        }
    }, [showActivityPanel, activeSidePanelTab, setActiveSidePanelTab]);

    const toggleCollapsed = () => {
        const newCollapsed = !isSidePanelCollapsed;
        setIsSidePanelCollapsed(newCollapsed);
        onCollapsedToggle(newCollapsed);
    };

    const handleTabClick = (tab: string) => {
        if (tab === "files") {
            setPreviewArtifact(null);
        }

        setActiveSidePanelTab(tab as SidePanelTabId);
    };

    const handleIconClick = (tab: string) => {
        if (isSidePanelCollapsed) {
            setIsSidePanelCollapsed(false);
            onCollapsedToggle?.(false);
        }

        handleTabClick(tab);
    };

    if (isSidePanelCollapsed) {
        return (
            <div className="flex h-full w-full flex-col items-center border-l bg-(--background-w10) py-4">
                <Button data-testid="expandPanel" variant="ghost" size="sm" onClick={toggleCollapsed} className="h-10 w-10 p-0" tooltip="Expand Panel">
                    <PanelRightIcon className="size-5" />
                </Button>

                <div className="my-4 h-px w-8 bg-(--secondary-w40)"></div>

                <Button variant="ghost" size="sm" onClick={() => handleIconClick("files")} className="mb-2 h-10 w-10 p-0" tooltip="Files">
                    <FileText className="size-5" />
                </Button>

                {showActivityPanel && (
                    <Button variant="ghost" size="sm" onClick={() => handleIconClick("activity")} className={hasSourcesInSession ? "mb-2 h-10 w-10 p-0" : "h-10 w-10 p-0"} tooltip="Activity">
                        <Network className="size-5" />
                    </Button>
                )}

                {hasSourcesInSession && (
                    <Button variant="ghost" size="sm" onClick={() => handleIconClick("rag")} className="h-10 w-10 p-0" tooltip="Sources">
                        <Link2 className="size-5" />
                    </Button>
                )}

                {customSidePanelTabs?.map(tab => {
                    const TabIcon = tab.icon;
                    return (
                        <Button key={tab.id} variant="ghost" size="sm" onClick={() => handleIconClick(tab.id)} className="mt-2 h-10 w-10 p-0" tooltip={tab.label}>
                            <TabIcon className="size-5" />
                        </Button>
                    );
                })}
            </div>
        );
    }

    return (
        <div className="flex h-full flex-col border-l bg-(--background-w10)">
            <div className="m-1 min-h-0 flex-1">
                <Tabs value={activeSidePanelTab} onValueChange={value => handleTabClick(value)} className="flex h-full flex-col">
                    <div className="@container flex gap-2 p-2">
                        <Button data-testid="collapsePanel" variant="ghost" onClick={toggleCollapsed} className="shrink-0 p-1" tooltip="Collapse Panel">
                            <PanelRightIcon className="size-5" />
                        </Button>
                        <TabsList className="flex min-w-0 flex-1 bg-transparent p-0">
                            <TabsTrigger value="files" title="Files" className="relative min-w-0 flex-1 rounded-none rounded-l-md px-2 data-[state=active]:z-10" onClick={() => setPreviewArtifact(null)}>
                                <FileText className="h-4 w-4 shrink-0" />
                                <span className="ml-1.5 hidden truncate @[240px]:inline">Files</span>
                            </TabsTrigger>
                            {showActivityPanel && (
                                <TabsTrigger
                                    value="activity"
                                    title="Activity"
                                    className={`relative min-w-0 flex-1 cursor-pointer rounded-none border-y border-l-0 px-2 data-[state=active]:z-10 ${!hasSourcesInSession && (!customSidePanelTabs || customSidePanelTabs.length === 0) ? "rounded-r-md border-r" : ""}`}
                                >
                                    <Network className="h-4 w-4 shrink-0" />
                                    <span className="ml-1.5 hidden truncate @[240px]:inline">Activity</span>
                                </TabsTrigger>
                            )}
                            {hasSourcesInSession && (
                                <TabsTrigger
                                    value="rag"
                                    title="Sources"
                                    className={`relative min-w-0 flex-1 cursor-pointer rounded-none border border-l-0 px-2 data-[state=active]:z-10 ${!customSidePanelTabs || customSidePanelTabs.length === 0 ? "rounded-r-md" : ""}`}
                                >
                                    <Link2 className="h-4 w-4 shrink-0" />
                                    <span className="ml-1.5 hidden truncate @[240px]:inline">Sources</span>
                                </TabsTrigger>
                            )}
                            {customSidePanelTabs?.map((tab, index) => {
                                const TabIcon = tab.icon;
                                const isLast = index === customSidePanelTabs.length - 1;
                                return (
                                    <TabsTrigger key={tab.id} value={tab.id} title={tab.label} className={`relative min-w-0 flex-1 cursor-pointer rounded-none border border-l-0 px-2 data-[state=active]:z-10 ${isLast ? "rounded-r-md" : ""}`}>
                                        <TabIcon className="h-4 w-4 shrink-0" />
                                        <span className="ml-1.5 hidden truncate @[240px]:inline">{tab.label}</span>
                                    </TabsTrigger>
                                );
                            })}
                        </TabsList>
                    </div>
                    <div className="min-h-0 flex-1">
                        <TabsContent value="files" className="m-0 h-full">
                            <div className="h-full">
                                <ArtifactPanel />
                            </div>
                        </TabsContent>

                        {showActivityPanel && (
                            <TabsContent value="activity" className="m-0 h-full">
                                <div className="h-full">
                                    <ActivityPanelContent />
                                </div>
                            </TabsContent>
                        )}

                        {hasSourcesInSession && (
                            <TabsContent value="rag" className="m-0 h-full">
                                <div className="h-full">{hasDocumentSearchResults(ragData) ? <DocumentSourcesPanel ragData={filteredRagData} enabled={ragEnabled} /> : <RAGInfoPanel ragData={filteredRagData} enabled={ragEnabled} />}</div>
                            </TabsContent>
                        )}

                        {customSidePanelTabs?.map(tab => (
                            <TabsContent key={tab.id} value={tab.id} className="m-0 h-full">
                                <div className="h-full">{tab.content}</div>
                            </TabsContent>
                        ))}
                    </div>
                </Tabs>
            </div>
        </div>
    );
};
