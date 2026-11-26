// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useRef } from "react";
import { AlertCircle, Info, Loader2, MessageSquare, UserLock } from "lucide-react";
import { useDefaultLayout } from "react-resizable-panels";
import { useNavigate } from "react-router-dom";

import { useMarkShareViewed } from "@/api/share";

import { SharedChatProvider } from "@/providers/SharedChatProvider";

import { ChatMessage } from "@/features/chat";
import { formatDateYMD, useSharedSession } from "@/features/share/hooks";
import { SharedSidePanel } from "@/features/share/SharedSidePanel";

import { Header, PageLayout } from "@/common";
import { Button, CHAT_STYLES, ResizableHandle, ResizablePanel, ResizablePanelGroup, Spinner, Tooltip, TooltipContent, TooltipTrigger } from "@/ui";

export function SharedChatViewPage() {
    const shared = useSharedSession();
    const { defaultLayout, onLayoutChanged } = useDefaultLayout({ id: "shared-chat-view-side-panel", panelIds: ["shared-chat-view-messages-panel", "shared-chat-view-side-panel"] });
    const navigate = useNavigate();
    const { mutate: markShareViewed } = useMarkShareViewed();

    const markedShareIdRef = useRef<string | null>(null);
    const loadedShareId = shared.session ? shared.shareId : null;
    useEffect(() => {
        if (!loadedShareId || markedShareIdRef.current === loadedShareId) return;
        markedShareIdRef.current = loadedShareId;
        markShareViewed(loadedShareId);
    }, [loadedShareId, markShareViewed]);

    if (shared.loading) {
        return (
            <PageLayout className="items-center justify-center">
                <Spinner size="large" variant="primary">
                    <p className="mt-4 text-sm text-(--secondary-text-wMain)">Loading shared chat...</p>
                </Spinner>
            </PageLayout>
        );
    }

    if (shared.error) {
        return (
            <PageLayout className="items-center justify-center gap-4 p-8">
                <AlertCircle className="h-16 w-16 text-(--error-wMain)" />
                <h1 className="text-2xl font-semibold">Unable to View Shared Chat</h1>
                <p className="max-w-md text-center text-(--secondary-text-wMain)">{shared.error}</p>
                <Button variant="outline" onClick={() => shared.navigate("/chat")}>
                    Go to Chat
                </Button>
            </PageLayout>
        );
    }

    if (!shared.session) {
        return (
            <PageLayout className="items-center justify-center gap-4 p-8">
                <AlertCircle className="h-16 w-16 text-(--secondary-text-wMain)" />
                <h1 className="text-2xl font-semibold">Shared Chat Not Found</h1>
                <p className="text-(--secondary-text-wMain)">This shared chat may have been deleted or the link is invalid.</p>
                <Button variant="outline" onClick={() => shared.navigate("/chat")}>
                    Go to Chat
                </Button>
            </PageLayout>
        );
    }

    const { session } = shared;

    const headerButtons = [
        <div key="shared-info" className="flex items-center gap-2 text-sm text-(--secondary-text-wMain)">
            <Tooltip>
                <TooltipTrigger asChild>
                    <span className="inline-flex cursor-pointer">
                        <UserLock className="h-4 w-4 text-(--secondary-wMain)" />
                    </span>
                </TooltipTrigger>
                <TooltipContent>
                    Shared by <span className="font-bold">{session.tasks[0]?.userId || "Unknown"}</span> on <span className="font-bold">{formatDateYMD(session.createdAt)}</span>
                </TooltipContent>
            </Tooltip>
            <span className="text-xs text-(--secondary-text-wMain)">Viewer</span>
            {session.snapshotTime && (
                <>
                    <div className="h-4 w-px bg-(--secondary-w40)" />
                    <span className="text-xs text-(--secondary-text-wMain)">Snapshot from {formatDateYMD(session.snapshotTime)}</span>
                </>
            )}
        </div>,
        session?.isOwner && session?.sessionId ? (
            <Button key="go-to-chat" variant="outline" size="sm" onClick={() => shared.navigate(`/chat?sessionId=${session.sessionId}`)}>
                <MessageSquare className="mr-2 h-4 w-4" />
                Go to Chat
            </Button>
        ) : null,
    ].filter(Boolean) as ReactNode[];

    return (
        <SharedChatProvider
            artifacts={shared.convertedArtifacts}
            ragData={shared.ragData}
            sessionId={shared.sessionIdForProvider}
            shareId={shared.shareId || ""}
            onOpenSidePanelTab={shared.handleProviderTabOpen}
            onSetTaskIdInSidePanel={shared.setSelectedTaskId}
            onSwitchSession={sid => navigate("/chat", { state: { switchToSession: sid } })}
            onNewSession={() => navigate("/chat", { state: { newChat: true } })}
        >
            <PageLayout className="relative">
                <div>
                    <Header title={session.title} buttons={headerButtons} />
                </div>

                <div className="flex min-h-0 flex-1">
                    <div className="min-h-0 flex-1 overflow-x-auto">
                        <ResizablePanelGroup orientation="horizontal" id="shared-chat-view-side-panel" defaultLayout={defaultLayout} onLayoutChanged={onLayoutChanged} className="h-full">
                            <ResizablePanel defaultSize={`${shared.isSidePanelCollapsed ? 96 : 70}%`} minSize="50%" id="shared-chat-view-messages-panel">
                                <div className="flex h-full w-full flex-col">
                                    <div className="flex min-h-0 flex-1 flex-col py-6">
                                        <main className="h-full overflow-y-auto px-6">
                                            <div className="mx-auto max-w-3xl space-y-4">
                                                {shared.messages.length === 0 ? (
                                                    <div className="py-12 text-center text-(--secondary-text-wMain)">
                                                        <p>No messages in this shared chat.</p>
                                                    </div>
                                                ) : (
                                                    shared.messages.map((message, index) => {
                                                        const isLastWithTaskId = !!(message.taskId && shared.lastMessageIndexByTaskId.get(message.taskId) === index);
                                                        return (
                                                            <div key={message.metadata?.messageId || `msg-${index}`}>
                                                                <ChatMessage message={message} isLastWithTaskId={isLastWithTaskId} />
                                                            </div>
                                                        );
                                                    })
                                                )}
                                            </div>
                                        </main>

                                        <div style={CHAT_STYLES}>
                                            <div className="mx-auto flex max-w-3xl items-center gap-3 rounded-lg border border-(--secondary-w20) bg-(--background-w20) px-4 py-3 shadow-sm">
                                                <Info className="h-5 w-5 flex-shrink-0 text-(--secondary-text-wMain)" />
                                                <span className="text-sm text-(--secondary-text-wMain)">This chat is read-only. To build off of it, continue a new conversation.</span>
                                                {!(session?.isOwner && session?.sessionId) && (
                                                    <Button variant="outline" size="sm" onClick={shared.handleForkChat} disabled={shared.isForking} className="ml-auto flex-shrink-0">
                                                        {shared.isForking ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <MessageSquare className="mr-2 h-4 w-4" />}
                                                        Continue in New Chat
                                                    </Button>
                                                )}
                                            </div>
                                        </div>
                                    </div>
                                </div>
                            </ResizablePanel>

                            <ResizableHandle />

                            <ResizablePanel defaultSize={`${shared.isSidePanelCollapsed ? 4 : 30}%`} minSize={`${shared.isSidePanelCollapsed ? 4 : 20}%`} maxSize={`${shared.isSidePanelCollapsed ? 4 : 50}%`} id="shared-chat-view-side-panel">
                                <SharedSidePanel
                                    isCollapsed={shared.isSidePanelCollapsed}
                                    activeTab={shared.activeSidePanelTab}
                                    onTabChange={shared.setActiveSidePanelTab}
                                    onToggle={shared.toggleSidePanel}
                                    onOpenTab={shared.openSidePanelTab}
                                    hasRagSources={shared.hasRagSources}
                                    handleSharedArtifactDownload={shared.handleSharedArtifactDownload}
                                    session={session}
                                    selectedTaskId={shared.selectedTaskId}
                                    onTaskSelect={shared.setSelectedTaskId}
                                    ragData={shared.ragData}
                                />
                            </ResizablePanel>
                        </ResizablePanelGroup>
                    </div>
                </div>
            </PageLayout>
        </SharedChatProvider>
    );
}
