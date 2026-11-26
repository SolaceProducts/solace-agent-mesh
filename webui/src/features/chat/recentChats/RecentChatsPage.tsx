// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CalendarDays, Check, Loader2, MessageCircle, Plus, Share2, X } from "lucide-react";
import { useInView } from "react-intersection-observer";
import { useNavigate, useSearchParams } from "react-router-dom";

import { sessionKeys, useInfiniteSessions, useMarkSessionViewed } from "@/api/sessions";
import { useMarkShareViewed, useSharedWithMe } from "@/api/share";

import { UI_MODE, useChatContext, useConfigContext, useProjectContext, useUIModeContext } from "@/providers/contexts";

import { clickableNodeProps } from "@/utils/nodeInteraction";

import type { SharedWithMeItem } from "@/types/share";

import { useSessionActions, useTitleAnimation } from "@/features/chat/hooks";
import { NO_PROJECT_LABEL } from "@/features/chat/projectFilter";

import { EmptyState, Header, PageLayout, SessionIcon } from "@/common";
import { useBuilderEnabled, useChatRoute, useIsAutoTitleGenerationEnabled, useIsChatSharingEnabled, useIsSchedulerEnabled } from "@/hooks";
import type { Session } from "@/types";
import { Button, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Spinner, Tabs, TabsList, TabsTrigger, Tooltip, TooltipContent, TooltipTrigger } from "@/ui";
import { formatRelativeTime, formatTimestamp, hasUnseenShare, hasUnseenUpdates, resolveAgentSessionRoute } from "@/utils";

import { ProjectBadge } from "../file/ProjectBadge";

import { filterSessions, resolveProjectFilterId, sortSharedChats, type Tab } from "./recentChatsPageUtils";
import { RecentChatsSidePanel } from "./RecentChatsSidePanel";
import { RecentChatsToggleButton } from "./RecentChatsToggleButton";
import { SessionActionMenu } from "./SessionActionMenu";
import { sessionCardStyles, sessionTitleStyles } from "./sessionCardStyles";
import { SessionSearch } from "./SessionSearch";

const PAGE_SIZE = 20;
const BACKGROUND_TASK_POLL_MS = 10_000;

interface SessionNameProps {
    session: Session;
    respondingSessionId: string | null;
    isSelected: boolean;
}

const SessionName: React.FC<SessionNameProps> = ({ session, respondingSessionId, isSelected }) => {
    const autoTitleGenerationEnabled = useIsAutoTitleGenerationEnabled();

    const displayName = useMemo(() => {
        if (session.name && session.name.trim()) {
            return session.name;
        }
        return "New Chat";
    }, [session.name]);

    const { text: animatedName, isAnimating, isGenerating } = useTitleAnimation(displayName, session.id);

    const isWaitingForTitle = useMemo(() => {
        if (isGenerating) {
            return true;
        }
        if (!autoTitleGenerationEnabled) {
            return false;
        }
        const isNewChat = !session.name || session.name === "New Chat";
        const isThisSessionResponding = respondingSessionId === session.id;
        const hasBackgroundTaskWithNewTitle = session.hasRunningBackgroundTask && isNewChat;
        return (isThisSessionResponding && isNewChat) || hasBackgroundTaskWithNewTitle;
    }, [session.name, session.id, respondingSessionId, isGenerating, autoTitleGenerationEnabled, session.hasRunningBackgroundTask]);

    const animationVariant = useMemo((): "pulseGenerate" | "pulseWait" | "none" => {
        if (isGenerating || isAnimating) {
            return isWaitingForTitle ? "pulseGenerate" : "pulseWait";
        }
        if (isWaitingForTitle) return "pulseGenerate";
        return "none";
    }, [isWaitingForTitle, isAnimating, isGenerating]);

    const titleRef = useRef<HTMLSpanElement>(null);
    const [isTruncated, setIsTruncated] = useState(false);
    useEffect(() => {
        const el = titleRef.current;
        if (!el) return;
        const check = () => setIsTruncated(el.scrollWidth > el.clientWidth);
        check();
        const observer = new ResizeObserver(check);
        observer.observe(el);
        return () => observer.disconnect();
    }, [animatedName]);

    const titleNode = (
        <span ref={titleRef} className={sessionTitleStyles({ active: isSelected, animation: animationVariant })} data-testid="session-title">
            {animatedName}
        </span>
    );

    if (!isTruncated) {
        return titleNode;
    }

    return (
        <Tooltip>
            <TooltipTrigger asChild>{titleNode}</TooltipTrigger>
            <TooltipContent className="max-w-[480px]">{animatedName}</TooltipContent>
        </Tooltip>
    );
};

interface SharedChatsListProps {
    sharedChats: SharedWithMeItem[];
    isLoading: boolean;
    chatRoute: string;
    onSwitchSession: (sessionId: string) => Promise<void | boolean> | void;
    onMarkViewed: (shareId: string) => void;
    navigate: (to: string) => void;
}

export const SharedChatsList = ({ sharedChats, isLoading, chatRoute, onSwitchSession, onMarkViewed, navigate }: Readonly<SharedChatsListProps>) => {
    if (isLoading) {
        return (
            <div className="flex justify-center py-8">
                <Spinner size="small" variant="muted" />
            </div>
        );
    }
    if (sharedChats.length === 0) {
        return <EmptyState variant="noImage" title="No shared chats" subtitle="Chats that others share with you will appear here" />;
    }

    const openSharedItem = async (item: SharedWithMeItem) => {
        onMarkViewed(item.id);
        if (item.accessLevel === "resource_editor" && item.sessionId) {
            await onSwitchSession(item.sessionId);
            navigate(chatRoute);
        } else {
            navigate(`/shared-chat/${item.id}`);
        }
    };

    return (
        <div className="flex flex-col gap-2">
            {sharedChats.map(item => (
                <div key={item.id} className={sessionCardStyles({ active: false })}>
                    {hasUnseenShare(item) && <span aria-label="Unseen updates" className="absolute top-1/2 left-1 h-[calc(100%-1rem)] w-1 -translate-y-1/2 rounded-sm bg-(--info-wMain)" />}
                    <div className="flex cursor-pointer items-center gap-4" {...clickableNodeProps(() => openSharedItem(item))}>
                        <div className="flex min-w-0 flex-1 flex-col gap-1">
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <span className={sessionTitleStyles({ active: false, animation: "none" })} data-testid="session-title">
                                        {item.title}
                                    </span>
                                </TooltipTrigger>
                                <TooltipContent className="max-w-[480px]">{item.title}</TooltipContent>
                            </Tooltip>
                            <div className="text-xs font-normal text-(--secondary-text-wMain)">
                                Shared by {item.ownerEmail} · {formatRelativeTime(item.sharedAt)}
                            </div>
                        </div>
                    </div>
                </div>
            ))}
        </div>
    );
};

export const RecentChatsPage: React.FC = () => {
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const { sessionId, handleSwitchSession, handleNewSession, currentTaskId, agentSessionRoutes } = useChatContext();
    const { persistenceEnabled } = useConfigContext();
    const { projects } = useProjectContext();
    const { mode, pinnedAgentName, showRecentChatsPanel } = useUIModeContext();
    const isAgentMode = mode === UI_MODE.agentMode;
    const agentScopeId = isAgentMode ? pinnedAgentName || undefined : undefined;
    const chatRoute = useChatRoute();
    const chatSharingEnabled = useIsChatSharingEnabled();
    const schedulerEnabled = useIsSchedulerEnabled();
    const builderEnabled = useBuilderEnabled();
    const { editingSessionId, editingSessionName, setEditingSessionName, inputRef, beginRename, commitRename, cancelRename, renameWithAI, regeneratingTitleForSession, move, remove, goToProject, share } = useSessionActions();
    const [isRecentChatsOpen, setIsRecentChatsOpen] = useState(false);
    const recentChatsDrawerOpen = showRecentChatsPanel && isRecentChatsOpen;

    const taskToSessionRef = useRef<Map<string, string>>(new Map());
    const [taskMapVersion, setTaskMapVersion] = useState(0);

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

    const [searchParams, setSearchParams] = useSearchParams();
    const initialTab: Tab = !isAgentMode && searchParams.get("tab") === "shared" && chatSharingEnabled ? "shared" : "chat";
    const [activeTab, setActiveTab] = useState<Tab>(initialTab);

    const handleTabChange = useCallback(
        (value: string) => {
            const next = value as Tab;
            setActiveTab(next);
            const nextParams = new URLSearchParams(searchParams);
            if (next === "shared") {
                nextParams.set("tab", "shared");
            } else {
                nextParams.delete("tab");
            }
            setSearchParams(nextParams, { replace: true });
        },
        [searchParams, setSearchParams]
    );

    const sessionSource = activeTab === "shared" ? "chat" : activeTab;
    const { data, fetchNextPage, hasNextPage, isFetchingNextPage } = useInfiniteSessions(PAGE_SIZE, sessionSource, { enabled: activeTab !== "shared", agentId: agentScopeId });
    const { data: sharedChats = [], isLoading: isLoadingShared } = useSharedWithMe({ enabled: activeTab === "shared" });
    const sessions = useMemo(() => data?.pages.flatMap(page => page.data) ?? [], [data]);

    const [selectedProject, setSelectedProject] = useState<string>("all");

    const { ref: loadMoreRef, inView } = useInView({
        threshold: 0,
        triggerOnce: false,
    });

    useEffect(() => {
        if (inView && hasNextPage && !isFetchingNextPage) {
            fetchNextPage();
        }
    }, [inView, hasNextPage, isFetchingNextPage, fetchNextPage]);

    useEffect(() => {
        const hasBackgroundTasks = sessions.some(s => s.hasRunningBackgroundTask);
        if (!hasBackgroundTasks) return;
        const id = setInterval(() => {
            queryClient.invalidateQueries({ queryKey: sessionKeys.lists() });
        }, BACKGROUND_TASK_POLL_MS);
        return () => clearInterval(id);
    }, [sessions, queryClient]);

    const markViewedMutation = useMarkSessionViewed();
    const markShareViewedMutation = useMarkShareViewed();

    const handleSessionClick = async (session: Session) => {
        const route = resolveAgentSessionRoute(session, agentSessionRoutes) || "/chat";
        if (route !== "/chat") {
            window.dispatchEvent(
                new CustomEvent("agent-session-navigate", {
                    detail: { sessionId: session.id, route, agentId: session.agentId, isEphemeralTest: session.isEphemeralTest === true },
                })
            );
        } else {
            await handleSwitchSession(session.id);
            navigate(chatRoute);
            markViewedMutation.mutate(session.id);
        }
    };

    const projectNames = useMemo(() => {
        const uniqueProjectNames = new Set<string>(projects.map(project => project.name));
        let hasUnassignedChats = false;

        sessions.forEach(session => {
            if (session.project?.name) {
                uniqueProjectNames.add(session.project.name);
            } else {
                hasUnassignedChats = true;
            }
        });

        const sortedNames = Array.from(uniqueProjectNames).sort((a, b) => a.localeCompare(b));

        if (hasUnassignedChats) {
            sortedNames.unshift(NO_PROJECT_LABEL);
        }

        return sortedNames;
    }, [sessions, projects]);

    const filteredSessions = useMemo(() => filterSessions(sessions, activeTab, selectedProject, builderEnabled), [sessions, selectedProject, activeTab, builderEnabled]);

    const selectedProjectId = useMemo(() => resolveProjectFilterId(selectedProject, sessions, projects), [selectedProject, sessions, projects]);

    return (
        <PageLayout className="relative">
            {showRecentChatsPanel && (
                <div inert={!isRecentChatsOpen} className={`absolute top-0 left-0 z-20 h-full transition-[transform,visibility] duration-300 ${isRecentChatsOpen ? "visible translate-x-0" : "invisible -translate-x-full delay-300"}`}>
                    <RecentChatsSidePanel viewAllDisabled />
                </div>
            )}
            <div className={`transition-all duration-300 ${recentChatsDrawerOpen ? "ml-100" : "ml-0"}`}>
                <Header
                    title="Recent Chats"
                    leadingAction={showRecentChatsPanel ? <RecentChatsToggleButton onToggle={() => setIsRecentChatsOpen(open => !open)} /> : undefined}
                    buttons={[
                        <Button
                            key="new-chat"
                            onClick={() => {
                                navigate(chatRoute);
                                handleNewSession();
                            }}
                        >
                            <Plus size={16} className="mr-1" />
                            New Chat
                        </Button>,
                    ]}
                />
            </div>

            <div className={`flex flex-1 flex-col overflow-y-auto px-4 py-6 transition-all duration-300 sm:px-8 ${recentChatsDrawerOpen ? "ml-100" : "ml-0"}`}>
                <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
                    <div className="flex flex-col gap-4">
                        {!isAgentMode && (schedulerEnabled || chatSharingEnabled) && (
                            <div className="flex justify-center">
                                <Tabs value={activeTab} onValueChange={handleTabChange}>
                                    <TabsList className="bg-transparent p-0">
                                        <TabsTrigger value="chat" className="rounded-none rounded-l-md px-6">
                                            <MessageCircle className="h-4 w-4 shrink-0" />
                                            Chats
                                        </TabsTrigger>
                                        {schedulerEnabled && (
                                            <TabsTrigger value="scheduler" className={`rounded-none border-l-0 px-6 ${chatSharingEnabled ? "" : "rounded-r-md"}`}>
                                                <CalendarDays className="h-4 w-4 shrink-0" />
                                                Scheduled Tasks
                                            </TabsTrigger>
                                        )}
                                        {chatSharingEnabled && (
                                            <TabsTrigger value="shared" className="rounded-none rounded-r-md border-l-0 px-6">
                                                <Share2 className="h-4 w-4 shrink-0" />
                                                Shared with Me
                                            </TabsTrigger>
                                        )}
                                    </TabsList>
                                </Tabs>
                            </div>
                        )}

                        {activeTab !== "shared" && sessions.length > 0 && (
                            <div className="flex flex-wrap items-center gap-3 sm:gap-4">
                                <div className="w-full sm:min-w-0 sm:flex-1">
                                    <SessionSearch onSessionSelect={handleSessionClick} projectId={selectedProjectId} agentId={agentScopeId} activeTab={activeTab} />
                                </div>

                                {!isAgentMode && persistenceEnabled && activeTab === "chat" && projectNames.length > 0 && (
                                    <div className="flex w-full items-center gap-2 sm:w-auto sm:flex-none">
                                        <label className="text-sm font-medium">Project:</label>
                                        <Select value={selectedProject} onValueChange={setSelectedProject}>
                                            <SelectTrigger className="min-w-0 flex-1 rounded-md sm:w-[200px] sm:flex-none">
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent>
                                                <SelectItem value="all">All Chats</SelectItem>
                                                {projectNames.map(projectName => (
                                                    <SelectItem key={projectName} value={projectName}>
                                                        {projectName}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                    </div>
                                )}
                            </div>
                        )}
                    </div>

                    {activeTab === "shared" && (
                        <SharedChatsList sharedChats={sortSharedChats(sharedChats)} isLoading={isLoadingShared} chatRoute={chatRoute} onSwitchSession={handleSwitchSession} onMarkViewed={markShareViewedMutation.mutate} navigate={navigate} />
                    )}

                    {activeTab !== "shared" && filteredSessions.length > 0 && (
                        <div className="flex flex-col gap-2">
                            {filteredSessions.map(session => (
                                <div key={session.id} className={sessionCardStyles({ active: session.id === sessionId })}>
                                    {hasUnseenUpdates(session) && <span aria-label="Unseen updates" className="absolute top-1/2 left-1 h-[calc(100%-1rem)] w-1 -translate-y-1/2 rounded-sm bg-(--info-wMain)" />}
                                    {editingSessionId === session.id ? (
                                        <div className="flex items-center gap-2">
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
                                                className="min-w-0 flex-1 bg-transparent focus:outline-none"
                                            />
                                            <div className="flex flex-shrink-0 items-center gap-1">
                                                <Button variant="ghost" size="sm" onClick={commitRename} className="h-8 w-8 p-0">
                                                    <Check size={16} />
                                                </Button>
                                                <Button variant="ghost" size="sm" onClick={cancelRename} className="h-8 w-8 p-0">
                                                    <X size={16} />
                                                </Button>
                                            </div>
                                        </div>
                                    ) : (
                                        <div className="flex cursor-pointer items-center gap-4" {...clickableNodeProps(() => handleSessionClick(session))}>
                                            <div className="flex min-w-0 flex-1 flex-col gap-1">
                                                <div className="flex items-center gap-2">
                                                    <SessionIcon session={session} className="text-(--secondary-text-wMain)" />
                                                    <SessionName session={session} respondingSessionId={respondingSessionId} isSelected={session.id === sessionId} />
                                                    {session.hasRunningBackgroundTask && (
                                                        <Tooltip>
                                                            <TooltipTrigger asChild>
                                                                <Loader2 className="h-4 w-4 flex-shrink-0 animate-spin text-(--primary-wMain)" />
                                                            </TooltipTrigger>
                                                            <TooltipContent>Background task running</TooltipContent>
                                                        </Tooltip>
                                                    )}
                                                </div>
                                                <Tooltip>
                                                    <TooltipTrigger asChild>
                                                        <div className="w-fit cursor-default text-xs font-normal text-(--secondary-text-wMain)">Last message {formatRelativeTime(session.updatedAt)}</div>
                                                    </TooltipTrigger>
                                                    <TooltipContent side="bottom">{formatTimestamp(session.updatedAt)}</TooltipContent>
                                                </Tooltip>
                                            </div>
                                            <div className="flex flex-shrink-0 items-center gap-2">
                                                {session.scheduledTaskId && (
                                                    <Tooltip>
                                                        <TooltipTrigger asChild>
                                                            <button
                                                                type="button"
                                                                onPointerDown={e => e.stopPropagation()}
                                                                onClick={e => {
                                                                    e.preventDefault();
                                                                    e.stopPropagation();
                                                                    navigate(`/scheduled-tasks?taskId=${session.scheduledTaskId}`);
                                                                }}
                                                                className="flex cursor-pointer items-center gap-1 rounded-full bg-(--info-w10) px-2 py-0.5 text-xs text-(--info-wMain) hover:bg-(--info-w20)"
                                                            >
                                                                <CalendarDays size={12} />
                                                                <span className="max-w-[160px] truncate">{session.scheduledTaskName ?? "Schedule"}</span>
                                                            </button>
                                                        </TooltipTrigger>
                                                        <TooltipContent>View originating schedule</TooltipContent>
                                                    </Tooltip>
                                                )}
                                                {session.project?.name && <ProjectBadge text={session.project.name} />}
                                                <SessionActionMenu
                                                    session={session}
                                                    onRename={beginRename}
                                                    onRenameWithAI={renameWithAI}
                                                    onMove={move}
                                                    onDelete={remove}
                                                    onGoToProject={goToProject}
                                                    onShare={chatSharingEnabled ? share : undefined}
                                                    isRegeneratingTitle={regeneratingTitleForSession === session.id}
                                                />
                                            </div>
                                        </div>
                                    )}
                                </div>
                            ))}
                        </div>
                    )}

                    {activeTab !== "shared" && filteredSessions.length === 0 && sessions.length > 0 && !isFetchingNextPage && selectedProject !== "all" && (
                        <EmptyState variant="noImage" title="No sessions found for this project" subtitle="Try selecting a different project filter" />
                    )}

                    {activeTab !== "shared" && filteredSessions.length === 0 && !isFetchingNextPage && !(sessions.length > 0 && selectedProject !== "all") && (
                        <EmptyState
                            variant="noImage"
                            title={activeTab === "scheduler" ? "No scheduled task sessions" : "No chat sessions available"}
                            subtitle={activeTab === "scheduler" ? "Sessions from scheduled tasks will appear here" : "Start a new chat to create your first session"}
                            buttons={
                                activeTab === "chat"
                                    ? [
                                          {
                                              icon: <Plus size={16} />,
                                              text: "New Chat",
                                              variant: "default" as const,
                                              onClick: () => {
                                                  navigate(chatRoute);
                                                  handleNewSession();
                                              },
                                          },
                                      ]
                                    : []
                            }
                        />
                    )}

                    {activeTab !== "shared" && hasNextPage && (
                        <div ref={loadMoreRef} className="flex justify-center py-4">
                            {isFetchingNextPage && <Spinner size="small" variant="muted" />}
                        </div>
                    )}
                </div>
            </div>
        </PageLayout>
    );
};
