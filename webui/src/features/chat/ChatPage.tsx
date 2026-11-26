// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { type PanelImperativeHandle, type PanelSize, useDefaultLayout } from "react-resizable-panels";
import { useLocation, useNavigate } from "react-router-dom";

import { api } from "@/api/client";
import { useShareLink, useShareUsers } from "@/api/share";

import { UI_MODE, useChatContext, useProjectContext, useUIModeContext } from "@/providers/contexts";

import { FADE_OUT_DURATION_MS, SLIDE_OUT_DURATION_MS } from "@/hooks/useTurnDividerAnimation";

import type { ChatMessageListRef } from "@/ui/chat/chat-message-list";
import type { CollaborativeUser } from "@/types/collaboration";

import { useChatAgents, useInlineTimelineActive, useSessionActions, useTitleAnimation } from "@/features/chat/hooks";
import { PendingChatRequests } from "@/features/notifications/PendingChatRequests";
import { usePendingChatRequests } from "@/features/notifications/usePendingChatRequests";

import { Header, LoadingMessageRow, PageLayout } from "@/common";
import { useIsAutoTitleGenerationEnabled, useIsChatSharingEnabled, useIsMobile, usePinnedAgentResolution, useTurnDividerAnimation } from "@/hooks";
import type { Session } from "@/types";
import { CHAT_STYLES, ChatMessageList, ResizableHandle, ResizablePanel, ResizablePanelGroup, Sheet, SheetContent, SheetTitle, Tooltip, TooltipContent, TooltipTrigger } from "@/ui";

import { BudgetStatusBanner } from "./BudgetStatusBanner";
import { ChatInputArea } from "./ChatInputArea";
import { ChatMessage } from "./ChatMessage";
import { buildCollaborativeUsers, buildShareNotifications, chatBodyState, computeHeaderButtons, computeShareNotificationInsertions, headerLeadingAction, type ShareNotification } from "./ChatPage.helpers";
import { ChatSidePanel } from "./ChatSidePanel";
import { ProjectBadge } from "./file/ProjectBadge";
import { RecentChatsSidePanel, SessionActionMenu } from "./recentChats";
import { ShareNotificationMessage } from "./ShareNotificationMessage";

const COLLAPSED_STYLE = { height: 0, overflow: "hidden" } as const;
const NO_OVERFLOW_ANCHOR_STYLE = { overflowAnchor: "none" } as const;
const OVERFLOW_ANCHOR_AUTO_STYLE = { overflowAnchor: "auto" } as const;
const COLLAPSED_SIZE = 4;
const PANEL_SIZES = {
    chatPanelSizes: {
        default: 50,
        min: 30,
        max: 96,
    },
    sidePanelSizes: {
        default: 50,
        min: 20,
        max: 70,
    },
};

const CHAT_PANEL_GROUP_ID = "chat-side-panel";

export function ChatPage() {
    const queryClient = useQueryClient();
    const { mode, pinnedAgentName, showRecentChatsPanel } = useUIModeContext();
    const { activeProject } = useProjectContext();
    const autoTitleGenerationEnabled = useIsAutoTitleGenerationEnabled();
    const chatSharingEnabled = useIsChatSharingEnabled();
    const isMobile = useIsMobile();
    const location = useLocation();
    const navigate = useNavigate();
    const { isLoading: isLoadingAgents, isRefetching, isError: hasAgentsError, refetch } = useChatAgents();
    const {
        agents,
        sessionId,
        sessionName,
        messages,
        isSidePanelCollapsed,
        setIsSidePanelCollapsed,
        isResponding,
        isLoadingSession,
        currentTaskId,
        isCollaborativeSession,
        currentUserEmail,
        sessionOwnerName,
        sessionOwnerEmail,
        handleSwitchSession,
        handleNewSession,
        turnDividerIndex,
        selectedAgentName,
    } = useChatContext();

    const [isSidePanelTransitioning, setIsSidePanelTransitioning] = useState(false);
    const [isMobileSidePanelOpen, setIsMobileSidePanelOpen] = useState(false);
    const [isForkingChat, setIsForkingChat] = useState(false);
    const { editingSessionId, editingSessionName, setEditingSessionName, inputRef, beginRename, commitRename, cancelRename, renameWithAI, regeneratingTitleForSession, move, remove, goToProject, share } = useSessionActions();
    const [shareNotifications, setShareNotifications] = useState<ShareNotification[]>([]);
    const [sharedEditorUsers, setSharedEditorUsers] = useState<CollaborativeUser[]>([]);

    useEffect(() => {
        const handleShareUpdated = (event: Event) => {
            const detail = (event as CustomEvent).detail;
            if (detail?.sessionId === sessionId) {
                queryClient.invalidateQueries({ queryKey: ["shares"] });
            }
        };
        window.addEventListener("share-updated", handleShareUpdated);
        return () => window.removeEventListener("share-updated", handleShareUpdated);
    }, [sessionId, queryClient]);

    const handleForkCollaborativeChat = useCallback(async () => {
        if (!sessionId || isForkingChat) return;
        setIsForkingChat(true);
        try {
            const response = await api.webui.post(`/api/v1/sessions/${sessionId}/fork`);
            const newSessionId = response?.sessionId || response?.session_id || response?.data?.sessionId || response?.data?.session_id;
            if (newSessionId) {
                window.dispatchEvent(new CustomEvent("new-chat-session"));
                handleSwitchSession(newSessionId);
                navigate("/chat");
            }
        } catch (error) {
            console.error("Failed to fork chat:", error);
        } finally {
            setIsForkingChat(false);
        }
    }, [sessionId, isForkingChat, handleSwitchSession, navigate]);

    const collaborativeTimestampRef = useRef<number>(Date.now());
    const prevIsCollaborativeRef = useRef(isCollaborativeSession);
    useEffect(() => {
        if (isCollaborativeSession && !prevIsCollaborativeRef.current) {
            collaborativeTimestampRef.current = Date.now();
        }
        prevIsCollaborativeRef.current = isCollaborativeSession;
    }, [isCollaborativeSession]);
    const collaborativeTimestamp = collaborativeTimestampRef.current;

    const chatMessageListRef = useRef<ChatMessageListRef>(null);
    const chatSidePanelRef = useRef<PanelImperativeHandle>(null);
    const lastExpandedSizeRef = useRef<number | null>(null);

    const taskToSessionRef = useRef<Map<string, string>>(new Map());
    const [taskMapVersion, setTaskMapVersion] = useState(0);

    const { hasDivider, isHistoryCollapsed, isExitingHistory, newTurnAnchorRef, collapsedUpToIndex } = useTurnDividerAnimation({
        turnDividerIndex,
        messagesLength: messages.length,
        sessionId,
        chatMessageListRef,
    });
    const dividerIdx = hasDivider && turnDividerIndex !== null ? turnDividerIndex : 0;
    const collapseIdx = collapsedUpToIndex ?? 0;
    const prevTurnRef = useRef<HTMLDivElement>(null);
    const [prevTurnHeight, setPrevTurnHeight] = useState(0);
    useEffect(() => {
        if (isExitingHistory && prevTurnRef.current) {
            setPrevTurnHeight(prevTurnRef.current.offsetHeight);
        } else if (!isExitingHistory) {
            setPrevTurnHeight(0);
        }
    }, [isExitingHistory]);

    const shareLinkQuery = useShareLink(!isCollaborativeSession ? sessionId : "");
    const shareLink = shareLinkQuery.data ?? null;
    const shareUsersQuery = useShareUsers(shareLink?.id);
    const shareUsers = useMemo(() => shareUsersQuery.data?.users ?? [], [shareUsersQuery.data?.users]);

    useEffect(() => {
        if (isCollaborativeSession || !sessionId || shareUsers.length === 0) {
            setShareNotifications([]);
            setSharedEditorUsers([]);
            return;
        }
        const { notifications, editorUsers } = buildShareNotifications(shareUsers);
        setShareNotifications(notifications);
        setSharedEditorUsers(editorUsers);
    }, [sessionId, isCollaborativeSession, shareUsers]);

    useEffect(() => {
        if (currentTaskId && !taskToSessionRef.current.has(currentTaskId)) {
            taskToSessionRef.current.set(currentTaskId, sessionId);
            setTaskMapVersion(v => v + 1);
        }
    }, [currentTaskId, sessionId]);

    const respondingSessionId = useMemo(() => {
        if (!currentTaskId) return null;
        return taskToSessionRef.current.get(currentTaskId) || null;
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [currentTaskId, taskMapVersion]);

    const { chatPanelSizes, sidePanelSizes } = PANEL_SIZES;
    const { defaultLayout, onLayoutChanged } = useDefaultLayout({ id: CHAT_PANEL_GROUP_ID, panelIds: isMobile ? ["chat-panel"] : ["chat-panel", "chat-side-panel"] });

    const handleSidepanelToggle = useCallback(
        (collapsed: boolean) => {
            setIsSidePanelTransitioning(true);
            if (chatSidePanelRef.current) {
                if (collapsed) {
                    chatSidePanelRef.current.resize(`${COLLAPSED_SIZE}%`);
                } else {
                    const targetSize = lastExpandedSizeRef.current || sidePanelSizes.default;
                    chatSidePanelRef.current.resize(`${targetSize}%`);
                }
            }
            setTimeout(() => setIsSidePanelTransitioning(false), 300);
        },
        [sidePanelSizes.default]
    );

    const handleSidepanelResize = useCallback(
        (size: PanelSize, _id: string | number | undefined, prevSize: PanelSize | undefined) => {
            const isCollapsed = size.asPercentage <= COLLAPSED_SIZE + 1;
            if (!isCollapsed) {
                lastExpandedSizeRef.current = size.asPercentage;
            }
            if (!prevSize) {
                return;
            }
            const wasCollapsed = prevSize.asPercentage <= COLLAPSED_SIZE + 1;
            if (isCollapsed !== wasCollapsed) {
                setIsSidePanelCollapsed(isCollapsed);
            }
        },
        [setIsSidePanelCollapsed]
    );

    const breadcrumbs = undefined;

    const rawPageTitle = useMemo(() => {
        return sessionName || "New Chat";
    }, [sessionName]);

    const { text: pageTitle, isAnimating: isTitleAnimating, isGenerating: isTitleGenerating } = useTitleAnimation(rawPageTitle, sessionId);

    const isWaitingForTitle = useMemo(() => {
        if (!autoTitleGenerationEnabled) {
            return false;
        }
        const isNewChat = !sessionName || sessionName === "New Chat";
        const isThisSessionResponding = respondingSessionId === sessionId;
        return (isNewChat && isThisSessionResponding) || isTitleGenerating;
    }, [sessionName, sessionId, respondingSessionId, isTitleGenerating, autoTitleGenerationEnabled]);

    const titleAnimationClass = useMemo(() => {
        if (!autoTitleGenerationEnabled) {
            return "opacity-100";
        }
        if (isWaitingForTitle) {
            return "animate-pulse-slow";
        }
        if (isTitleAnimating) {
            return "animate-pulse opacity-50";
        }
        return "opacity-100";
    }, [isWaitingForTitle, isTitleAnimating, autoTitleGenerationEnabled]);

    useEffect(() => {
        if (chatSidePanelRef.current && isSidePanelCollapsed) {
            chatSidePanelRef.current.resize(`${COLLAPSED_SIZE}%`);
        }

        const handleExpandSidePanel = () => {
            if (isMobile) {
                setIsMobileSidePanelOpen(true);
                return;
            }
            if (chatSidePanelRef.current && isSidePanelCollapsed) {
                setIsSidePanelTransitioning(true);

                const targetSize = lastExpandedSizeRef.current || sidePanelSizes.default;
                chatSidePanelRef.current.resize(`${targetSize}%`);

                setIsSidePanelCollapsed(false);

                setTimeout(() => setIsSidePanelTransitioning(false), 300);
            }
        };

        window.addEventListener("expand-side-panel", handleExpandSidePanel);
        return () => {
            window.removeEventListener("expand-side-panel", handleExpandSidePanel);
        };
    }, [isMobile, isSidePanelCollapsed, setIsSidePanelCollapsed, sidePanelSizes.default]);

    const collaborativeUsers = useMemo<CollaborativeUser[]>(() => {
        if (!isCollaborativeSession) return [];
        return buildCollaborativeUsers(messages, sessionOwnerEmail, sessionOwnerName);
    }, [isCollaborativeSession, messages, sessionOwnerEmail, sessionOwnerName]);

    const shareNotificationInsertions = useMemo(() => {
        if (shareNotifications.length === 0 || isCollaborativeSession || messages.length === 0) {
            return [];
        }
        return computeShareNotificationInsertions(shareNotifications, messages);
    }, [shareNotifications, isCollaborativeSession, messages]);

    const renderShareNotifications = useCallback(
        (index: number) =>
            shareNotificationInsertions
                .filter(n => n.insertAfterIndex === index)
                .map((notification, nIdx) =>
                    notification.variant === "role-changed" ? (
                        <ShareNotificationMessage
                            key={`share-notif-${notification.timestamp}-${nIdx}`}
                            variant="role-changed"
                            sharedWith={notification.names}
                            fromAccessLevel={notification.fromAccessLevel}
                            toAccessLevel={notification.toAccessLevel}
                            timestamp={notification.timestamp}
                        />
                    ) : (
                        <ShareNotificationMessage key={`share-notif-${notification.timestamp}-${nIdx}`} variant="shared-with-users" sharedWith={notification.names} accessLevel={notification.accessLevel} timestamp={notification.timestamp} />
                    )
                ),
        [shareNotificationInsertions]
    );

    const lastMessageIndexByTaskId = useMemo(() => {
        const map = new Map<string, number>();
        messages.forEach((message, index) => {
            if (message.taskId) {
                map.set(message.taskId, index);
            }
        });
        return map;
    }, [messages]);

    const liveRequestIds = useMemo(() => new Set(messages.flatMap(m => (m.userInputRequest ? [m.userInputRequest.requestId] : []))), [messages]);
    const chatRequests = usePendingChatRequests(sessionId, liveRequestIds);
    const hasChatRequests = chatRequests.waiting.length > 0 || chatRequests.answered.length > 0;

    const isWelcomeState = useMemo(() => {
        if (hasChatRequests) return false;
        if (messages.length === 0) return true;
        if (messages.length === 1 && !messages[0].isUser && messages[0].metadata?.sessionId === "") return true;
        return false;
    }, [messages, hasChatRequests]);

    const isAgentMode = mode === UI_MODE.agentMode;
    const noAgentSpecified = isAgentMode && !pinnedAgentName && messages.length === 0;
    const isAwaitingPinnedAgent = isAgentMode && !!pinnedAgentName && messages.length === 0 && !selectedAgentName;
    const { timedOut: pinnedAgentTimedOut } = usePinnedAgentResolution(isAwaitingPinnedAgent, pinnedAgentName);

    const inlineTimelineActive = useInlineTimelineActive(messages);

    const [isRecentChatsOpen, setIsRecentChatsOpen] = useState(false);
    const recentChatsDrawerOpen = showRecentChatsPanel && isRecentChatsOpen;

    useEffect(() => {
        const state = location.state as {
            switchToSession?: string;
            newChat?: boolean;
        } | null;
        if (!state) return;

        if (state.switchToSession) {
            handleSwitchSession(state.switchToSession);
        } else if (state.newChat) {
            handleNewSession();
        }

        navigate(location.pathname, { replace: true, state: {} });
    }, [location.state, location.pathname, navigate, handleSwitchSession, handleNewSession]);

    const currentSessionForMenu = useMemo<Session>(
        () => ({
            id: sessionId ?? "",
            name: sessionName ?? null,
            createdAt: "",
            updatedAt: "",
            project: activeProject ? { id: activeProject.id, name: activeProject.name } : null,
        }),
        [sessionId, sessionName, activeProject]
    );

    const headerButtons = computeHeaderButtons({
        chatSharingEnabled,
        sessionId: sessionId ?? null,
        sessionName: sessionName ?? null,
        isCollaborativeSession,
        collaborativeUsers,
        sharedEditorUsers,
        currentUserEmail,
        isForkingChat,
        isMobile,
        handleForkCollaborativeChat,
        onShare: () => share(currentSessionForMenu),
        setIsMobileSidePanelOpen,
    });

    const headerButtonsWithMenu = sessionId
        ? [
              ...(headerButtons ?? []),
              <SessionActionMenu
                  key="session-action-menu"
                  session={currentSessionForMenu}
                  onRename={beginRename}
                  onRenameWithAI={renameWithAI}
                  onMove={move}
                  onDelete={remove}
                  onGoToProject={goToProject}
                  isRegeneratingTitle={regeneratingTitleForSession === sessionId}
              />,
          ]
        : headerButtons;

    return (
        <PageLayout className="relative">
            {showRecentChatsPanel && (
                <div inert={!isRecentChatsOpen} className={`absolute top-0 left-0 z-20 h-full transition-[transform,visibility] duration-300 ${isRecentChatsOpen ? "visible translate-x-0" : "invisible -translate-x-full delay-300"}`}>
                    <RecentChatsSidePanel />
                </div>
            )}
            <div className={`transition-all duration-300 ${recentChatsDrawerOpen ? "ml-100" : "ml-0"}`}>
                <Header
                    title={
                        <div className="flex items-center gap-3">
                            {sessionId && editingSessionId === sessionId ? (
                                <input
                                    ref={inputRef}
                                    type="text"
                                    value={editingSessionName}
                                    onChange={e => setEditingSessionName(e.target.value)}
                                    onKeyDown={e => {
                                        if (e.key === "Enter") {
                                            e.preventDefault();
                                            commitRename();
                                        } else if (e.key === "Escape") {
                                            e.preventDefault();
                                            cancelRename();
                                        }
                                    }}
                                    onBlur={commitRename}
                                    className="font-inherit max-w-[400px] min-w-0 flex-1 border-0 bg-transparent p-0 text-inherit focus:outline-none"
                                />
                            ) : (
                                <Tooltip delayDuration={300}>
                                    <TooltipTrigger className={`font-inherit max-w-[400px] cursor-default truncate border-0 bg-transparent p-0 text-left text-inherit transition-opacity duration-300 hover:bg-transparent ${titleAnimationClass}`}>
                                        {pageTitle}
                                    </TooltipTrigger>
                                    <TooltipContent side="bottom">
                                        <p>{pageTitle}</p>
                                    </TooltipContent>
                                </Tooltip>
                            )}
                            {activeProject && <ProjectBadge text={activeProject.name} className="max-w-[360px]" />}
                        </div>
                    }
                    breadcrumbs={breadcrumbs}
                    leadingAction={headerLeadingAction({ showRecentChatsPanel, onToggleRecentChats: () => setIsRecentChatsOpen(open => !open) })}
                    buttons={headerButtonsWithMenu}
                />
                <BudgetStatusBanner />
            </div>
            <div className={`flex min-h-0 min-w-0 flex-1 transition-all duration-300 ${recentChatsDrawerOpen ? "ml-100" : "ml-0"}`}>
                <div data-testid="chat-content-area" className="min-h-0 min-w-0 flex-1 overflow-x-auto transition-all duration-300">
                    <ResizablePanelGroup
                        orientation="horizontal"
                        id={CHAT_PANEL_GROUP_ID}
                        defaultLayout={defaultLayout}
                        onLayoutChanged={onLayoutChanged}
                        className={isSidePanelTransitioning ? "h-full [&>[data-panel]]:transition-[flex-grow] [&>[data-panel]]:duration-300 [&>[data-panel]]:ease-in-out" : "h-full"}
                    >
                        <ResizablePanel defaultSize={`${chatPanelSizes.default}%`} minSize={`${chatPanelSizes.min}%`} maxSize={`${chatPanelSizes.max}%`} id="chat-panel">
                            <div className="flex h-full w-full min-w-0 flex-col">
                                <div className="flex min-h-0 min-w-0 flex-1 flex-col py-6">
                                    {chatBodyState({
                                        isLoadingSession,
                                        noAgentSpecified,
                                        isAwaitingPinnedAgent,
                                        pinnedAgentTimedOut,
                                        isWelcomeState,
                                        isResponding,
                                        isLoadingAgents,
                                        hasAgentsError,
                                        agents,
                                        selectedAgentName,
                                        agentsRefetch: { refetch, isRefetching },
                                    }) ?? (
                                        <>
                                            <ChatMessageList className="text-base" ref={chatMessageListRef}>
                                                {hasDivider && (
                                                    <div style={isHistoryCollapsed ? COLLAPSED_STYLE : undefined}>
                                                        {isCollaborativeSession && (
                                                            <ShareNotificationMessage variant="shared-with-users" sharedBy={sessionOwnerName || "Someone"} sharedWith={["you"]} accessLevel="editor" timestamp={collaborativeTimestamp} />
                                                        )}
                                                        {messages.slice(0, collapseIdx).map((message, index) => {
                                                            const isLastWithTaskId = !!(message.taskId && lastMessageIndexByTaskId.get(message.taskId) === index);
                                                            const messageKey = message.metadata?.messageId || `temp-${index}`;
                                                            return (
                                                                <div key={messageKey} style={NO_OVERFLOW_ANCHOR_STYLE}>
                                                                    <ChatMessage message={message} isLastWithTaskId={isLastWithTaskId} isStreaming={false} />
                                                                    {renderShareNotifications(index)}
                                                                </div>
                                                            );
                                                        })}
                                                    </div>
                                                )}
                                                {hasDivider && collapseIdx < dividerIdx && (
                                                    <div
                                                        ref={prevTurnRef}
                                                        style={{
                                                            marginTop: isExitingHistory && prevTurnHeight > 0 ? `-${prevTurnHeight}px` : 0,
                                                            opacity: isExitingHistory ? 0 : 1,
                                                            transition: isExitingHistory ? `margin-top ${SLIDE_OUT_DURATION_MS}ms ease-out, opacity ${FADE_OUT_DURATION_MS}ms ease-in` : undefined,
                                                            overflow: "hidden",
                                                        }}
                                                    >
                                                        {messages.slice(collapseIdx, dividerIdx).map((message, i) => {
                                                            const index = i + collapseIdx;
                                                            const isLastWithTaskId = !!(message.taskId && lastMessageIndexByTaskId.get(message.taskId) === index);
                                                            const messageKey = message.metadata?.messageId || `temp-${index}`;
                                                            return (
                                                                <div key={messageKey}>
                                                                    <ChatMessage message={message} isLastWithTaskId={isLastWithTaskId} isStreaming={false} />
                                                                    {renderShareNotifications(index)}
                                                                </div>
                                                            );
                                                        })}
                                                    </div>
                                                )}
                                                {(hasDivider ? messages.slice(dividerIdx) : messages).map((message, i) => {
                                                    const index = hasDivider ? i + dividerIdx : i;
                                                    const isLastWithTaskId = !!(message.taskId && lastMessageIndexByTaskId.get(message.taskId) === index);
                                                    const messageKey = message.metadata?.messageId || `temp-${index}`;
                                                    const isLastMessage = index === messages.length - 1;
                                                    const shouldStream = isLastMessage && isResponding && !message.isUser;
                                                    const isNewTurnStart = hasDivider && i === 0;

                                                    const showCollabBannerInNewSection = isCollaborativeSession && i === 0 && (!hasDivider || isHistoryCollapsed || isExitingHistory);

                                                    return (
                                                        <div key={messageKey} ref={isNewTurnStart ? newTurnAnchorRef : undefined} style={isNewTurnStart ? OVERFLOW_ANCHOR_AUTO_STYLE : undefined}>
                                                            {showCollabBannerInNewSection && (
                                                                <ShareNotificationMessage variant="shared-with-users" sharedBy={sessionOwnerName || "Someone"} sharedWith={["you"]} accessLevel="editor" timestamp={collaborativeTimestamp} />
                                                            )}
                                                            <div>
                                                                <ChatMessage message={message} isLastWithTaskId={isLastWithTaskId} isStreaming={shouldStream} />
                                                            </div>
                                                            {renderShareNotifications(index)}
                                                        </div>
                                                    );
                                                })}
                                                <PendingChatRequests {...chatRequests} />
                                                {isResponding && !inlineTimelineActive && <LoadingMessageRow />}
                                            </ChatMessageList>
                                            <div className="shrink-0" style={CHAT_STYLES}>
                                                <ChatInputArea agents={agents} scrollToBottom={chatMessageListRef.current?.scrollToBottom} autoFocus />
                                            </div>
                                        </>
                                    )}
                                </div>
                            </div>
                        </ResizablePanel>
                        {!isMobile && <ResizableHandle />}
                        {!isMobile && (
                            <ResizablePanel
                                panelRef={chatSidePanelRef}
                                defaultSize={`${sidePanelSizes.default}%`}
                                minSize={`${sidePanelSizes.min}%`}
                                maxSize={`${sidePanelSizes.max}%`}
                                collapsedSize={`${COLLAPSED_SIZE}%`}
                                collapsible={true}
                                onResize={handleSidepanelResize}
                                id="chat-side-panel"
                            >
                                <div className="h-full">
                                    <ChatSidePanel onCollapsedToggle={handleSidepanelToggle} isSidePanelCollapsed={isSidePanelCollapsed} setIsSidePanelCollapsed={setIsSidePanelCollapsed} />
                                </div>
                            </ResizablePanel>
                        )}
                    </ResizablePanelGroup>
                </div>
            </div>
            <Sheet open={isMobile && isMobileSidePanelOpen} onOpenChange={setIsMobileSidePanelOpen}>
                <SheetContent side="right" showClose={false} className="w-screen max-w-none border-l-0 p-0 sm:max-w-none">
                    <SheetTitle className="sr-only">Activity panel</SheetTitle>
                    <ChatSidePanel
                        onCollapsedToggle={() => setIsMobileSidePanelOpen(false)}
                        isSidePanelCollapsed={false}
                        setIsSidePanelCollapsed={collapsed => {
                            if (collapsed) setIsMobileSidePanelOpen(false);
                        }}
                    />
                </SheetContent>
            </Sheet>
        </PageLayout>
    );
}
