// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { FileText, Link2, Network, PanelRightIcon } from "lucide-react";

import type { SharedSessionView } from "@/types/share";

import { ArtifactPanel } from "@/features/chat/artifact/ArtifactPanel";
import { DocumentSourcesPanel } from "@/features/chat/rag/DocumentSourcesPanel";
import { RAGInfoPanel } from "@/features/chat/rag/RAGInfoPanel";
import { SharedWorkflowPanel } from "@/features/share/SharedWorkflowPanel";

import type { ArtifactInfo, RAGSearchResult } from "@/types";
import { Button, Tabs, TabsContent, TabsList, TabsTrigger } from "@/ui";
import { cn, hasDocumentSearchResults } from "@/utils";

interface SharedSidePanelProps {
    isCollapsed: boolean;
    activeTab: "files" | "workflow" | "sources";
    onTabChange: (tab: "files" | "workflow" | "sources") => void;
    onToggle: () => void;
    onOpenTab: (tab: "files" | "workflow" | "sources") => void;
    hasRagSources: boolean;
    handleSharedArtifactDownload: (artifact: ArtifactInfo) => Promise<void>;
    session: SharedSessionView | null;
    selectedTaskId: string | null;
    onTaskSelect: (taskId: string | null) => void;
    ragData: RAGSearchResult[];
}

export function SharedSidePanel({ isCollapsed, activeTab, onTabChange, onToggle, onOpenTab, hasRagSources, handleSharedArtifactDownload, session, selectedTaskId, onTaskSelect, ragData }: SharedSidePanelProps) {
    if (isCollapsed) {
        return (
            <div className="flex h-full w-full flex-col items-center border-l bg-(--background-w10) py-4">
                <Button variant="ghost" size="sm" onClick={onToggle} className="h-10 w-10 p-0" tooltip="Expand Panel">
                    <PanelRightIcon className="size-5" />
                </Button>

                <div className="my-4 h-px w-8 bg-(--secondary-w40)"></div>

                <Button variant="ghost" size="sm" onClick={() => onOpenTab("files")} className="mb-2 h-10 w-10 p-0" tooltip="Files">
                    <FileText className="size-5" />
                </Button>

                <Button variant="ghost" size="sm" onClick={() => onOpenTab("workflow")} className="mb-2 h-10 w-10 p-0" tooltip="Workflow">
                    <Network className="size-5" />
                </Button>

                {hasRagSources && (
                    <Button variant="ghost" size="sm" onClick={() => onOpenTab("sources")} className="h-10 w-10 p-0" tooltip="Sources">
                        <Link2 className="size-5" />
                    </Button>
                )}
            </div>
        );
    }

    return (
        <div className="flex h-full flex-col border-l bg-(--background-w10)">
            <div className="m-1 min-h-0 flex-1">
                <Tabs value={activeTab} onValueChange={value => onTabChange(value as "files" | "workflow" | "sources")} className="flex h-full flex-col">
                    <div className="@container flex gap-2 p-2">
                        <Button variant="ghost" onClick={onToggle} className="shrink-0 p-1" tooltip="Collapse Panel">
                            <PanelRightIcon className="size-5" />
                        </Button>
                        <TabsList className="flex min-w-0 flex-1 bg-transparent p-0">
                            <TabsTrigger
                                value="files"
                                title="Files"
                                className="relative min-w-0 flex-1 cursor-pointer rounded-none rounded-l-md border border-r-0 border-(--secondary-w20) bg-(--background-w20) px-2 data-[state=active]:z-10 data-[state=active]:bg-(--background-w10)"
                            >
                                <FileText className="h-4 w-4 shrink-0" />
                                <span className="ml-1.5 hidden truncate @[240px]:inline">Files</span>
                            </TabsTrigger>
                            <TabsTrigger
                                value="workflow"
                                title="Workflow"
                                className={cn(
                                    "relative min-w-0 flex-1 cursor-pointer rounded-none border border-(--secondary-w20) bg-(--background-w20) px-2 data-[state=active]:z-10 data-[state=active]:bg-(--background-w10)",
                                    hasRagSources ? "border-r-0" : "rounded-r-md"
                                )}
                            >
                                <Network className="h-4 w-4 shrink-0" />
                                <span className="ml-1.5 hidden truncate @[240px]:inline">Workflow</span>
                            </TabsTrigger>
                            {hasRagSources && (
                                <TabsTrigger
                                    value="sources"
                                    title="Sources"
                                    className="relative min-w-0 flex-1 cursor-pointer rounded-none rounded-r-md border border-(--secondary-w20) bg-(--background-w20) px-2 data-[state=active]:z-10 data-[state=active]:bg-(--background-w10)"
                                >
                                    <Link2 className="h-4 w-4 shrink-0" />
                                    <span className="ml-1.5 hidden truncate @[240px]:inline">Sources</span>
                                </TabsTrigger>
                            )}
                        </TabsList>
                    </div>
                    <div className="min-h-0 flex-1">
                        <TabsContent value="files" className="m-0 h-full">
                            <div className="h-full">
                                <ArtifactPanel readOnly={true} onDownloadOverride={handleSharedArtifactDownload} />
                            </div>
                        </TabsContent>
                        <TabsContent value="workflow" className="m-0 h-full">
                            <div className="h-full">
                                <SharedWorkflowPanel taskEvents={session?.taskEvents} selectedTaskId={selectedTaskId} onTaskSelect={onTaskSelect} />
                            </div>
                        </TabsContent>
                        {hasRagSources && (
                            <TabsContent value="sources" className="m-0 h-full">
                                <div className="h-full">{hasDocumentSearchResults(ragData) ? <DocumentSourcesPanel ragData={ragData} enabled={true} readOnly={true} /> : <RAGInfoPanel ragData={ragData} enabled={true} />}</div>
                            </TabsContent>
                        )}
                    </div>
                </Tabs>
            </div>
        </div>
    );
}
