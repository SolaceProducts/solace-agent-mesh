// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useRef, useState } from "react";
import { cva } from "class-variance-authority";
import { AlertCircle, MessageCircle, Share2 } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";

import { useMarkSessionViewed, useRecentSessions } from "@/api/sessions";
import { useMarkShareViewed, useSharedWithMe } from "@/api/share";

import { useChatContext, useConfigContext } from "@/providers/contexts";

import { clickableNodeProps } from "@/utils/nodeInteraction";

import type { SharedWithMeItem } from "@/types/share";

import { useSessionActions, useTitleAnimation } from "@/features/chat/hooks";
import { isSessionUserVisible } from "@/features/chat/recentChats/recentChatsPageUtils";
import { MAX_RECENT_CHATS } from "@/features/navigation/constants";

import { SessionIcon } from "@/common";
import { useBuilderEnabled, useChatRoute, useIsAutoTitleGenerationEnabled, useIsChatSharingEnabled } from "@/hooks";
import type { Session } from "@/types";
import { Spinner, Tooltip, TooltipContent, TooltipTrigger } from "@/ui";
import { hasUnseenShare, hasUnseenUpdates, resolveAgentSessionRoute, toEpochMs } from "@/utils";

import { mergeRecentEntries, type RecentEntry } from "./recentEntries";
import { SessionActionMenu } from "./SessionActionMenu";

const sessionButtonStyles = cva(["relative", "flex", "h-[42px]", "w-full", "cursor-pointer", "items-center", "gap-2", "pr-4", "pl-6", "text-left", "transition-colors", "hover:bg-(--darkSurface-bgHover)"], {
    variants: {
        active: {
            true: "bg-(--darkSurface-bgActive)",
            false: "",
        },
    },
    defaultVariants: { active: false },
});

const sessionTextStyles = cva(["block", "truncate", "text-sm", "transition-opacity", "duration-300"], {
    variants: {
        active: {
            true: "font-bold text-(--darkSurface-text)",
            false: "text-(--darkSurface-textMuted)",
        },
        animation: {
            pulseGenerate: "animate-pulse-slow",
            pulseWait: "animate-pulse opacity-50",
            none: "opacity-100",
        },
    },
    defaultVariants: { active: false, animation: "none" },
});

interface SessionNameProps {
    session: Session;
    respondingSessionId: string | null;
    isActive: boolean;
    hasRunningBackgroundTask?: boolean;
}

function SessionName({ session, respondingSessionId, isActive, hasRunningBackgroundTask }: SessionNameProps) {
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
        return isThisSessionResponding && isNewChat;
    }, [session.name, session.id, respondingSessionId, isGenerating, autoTitleGenerationEnabled]);

    const animationVariant = useMemo((): "pulseGenerate" | "pulseWait" | "none" => {
        if (hasRunningBackgroundTask) {
            return "pulseGenerate";
        }
        if (isGenerating || isAnimating) {
            return isWaitingForTitle ? "pulseGenerate" : "pulseWait";
        }
        if (isWaitingForTitle) {
            return "pulseGenerate";
        }
        return "none";
    }, [isWaitingForTitle, isAnimating, isGenerating, hasRunningBackgroundTask]);

    return <span className={sessionTextStyles({ active: isActive, animation: animationVariant })}>{animatedName}</span>;
}

interface RecentChatsListProps {
    maxItems?: number;
    agentId?: string;
}

export function RecentChatsList({ maxItems = MAX_RECENT_CHATS, agentId }: Readonly<RecentChatsListProps>) {
    const navigate = useNavigate();
    const location = useLocation();
    const { sessionId, handleSwitchSession, currentTaskId, agentSessionRoutes, sessionListExcludeAgentIds } = useChatContext();
    const { persistenceEnabled } = useConfigContext();
    const chatSharingEnabled = useIsChatSharingEnabled();
    const chatRoute = useChatRoute();

    const builderEnabled = useBuilderEnabled();
    const { editingSessionId, editingSessionName, setEditingSessionName, inputRef, beginRename, commitRename, cancelRename, renameWithAI, regeneratingTitleForSession, move, remove, goToProject, share } = useSessionActions();

    const { data: rawSessionsData, isLoading, isFetching, isError, refetch } = useRecentSessions(maxItems, agentId);
    const rawSessions = useMemo(() => rawSessionsData ?? [], [rawSessionsData]);

    const sessions = useMemo(() => {
        const excludeSet = new Set(sessionListExcludeAgentIds ?? []);
        return rawSessions.filter(s => isSessionUserVisible(s, builderEnabled) && (!s.agentId || !excludeSet.has(s.agentId)));
    }, [rawSessions, sessionListExcludeAgentIds, builderEnabled]);
    const { data: sharedItems = [] } = useSharedWithMe();
    const markViewedMutation = useMarkSessionViewed();
    const markShareViewedMutation = useMarkShareViewed();

    const lastMarkedViewedRef = useRef<Map<string, number>>(new Map());
    const onChatRoute = location.pathname.startsWith("/chat") || location.pathname.startsWith("/agent-mode/chat");
    useEffect(() => {
        if (!onChatRoute) return;
        if (!sessionId) return;
        const active = sessions.find(s => s.id === sessionId);
        if (!active) return;
        if (!hasUnseenUpdates(active)) return;
        const updatedMs = toEpochMs(active.updatedAt);
        if (!Number.isFinite(updatedMs)) return;
        const lastMarked = lastMarkedViewedRef.current.get(active.id) ?? 0;
        if (lastMarked >= updatedMs) return;
        lastMarkedViewedRef.current.set(active.id, updatedMs);
        markViewedMutation.mutate(active.id);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [onChatRoute, sessionId, sessions.find(s => s.id === sessionId)?.updatedAt]);

    const prevSessionRef = useRef<string | null>(sessionId);
    useEffect(() => {
        const prev = prevSessionRef.current;
        if (prev && prev !== sessionId) {
            markViewedMutation.mutate(prev);
        }
        prevSessionRef.current = sessionId;
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [sessionId]);

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

    const entries = useMemo<RecentEntry[]>(() => mergeRecentEntries(sessions, sharedItems, { maxItems, includeShared: chatSharingEnabled && !agentId }), [sessions, sharedItems, chatSharingEnabled, maxItems, agentId]);

    const handleSessionClick = async (clickedSession: Session) => {
        const route = resolveAgentSessionRoute(clickedSession, agentSessionRoutes) || "/chat";
        if (route !== "/chat") {
            window.dispatchEvent(
                new CustomEvent("agent-session-navigate", {
                    detail: { sessionId: clickedSession.id, route, agentId: clickedSession.agentId, isEphemeralTest: clickedSession.isEphemeralTest === true },
                })
            );
            return;
        }
        navigate(chatRoute);
        await handleSwitchSession(clickedSession.id);
        markViewedMutation.mutate(clickedSession.id);
    };

    const handleSharedClick = async (item: SharedWithMeItem) => {
        markShareViewedMutation.mutate(item.id);
        const isEditor = item.accessLevel === "resource_editor" && item.sessionId;
        if (isEditor && item.sessionId) {
            navigate(chatRoute);
            await handleSwitchSession(item.sessionId);
        } else {
            navigate(`/shared-chat/${item.id}`);
        }
    };

    const recentNotReady = rawSessionsData === undefined && isFetching;
    if ((isLoading || recentNotReady) && entries.length === 0 && persistenceEnabled) {
        return (
            <div className="flex h-full flex-col items-center pt-[25%] text-xs text-(--secondary-text-wMain)">
                <Spinner />
            </div>
        );
    }

    if (isError && entries.length === 0) {
        return (
            <div className="flex h-full flex-col items-center pt-[25%] text-xs text-(--secondary-text-wMain)">
                <AlertCircle className="mb-2 h-6 w-6" />
                Couldn't load chats
                <button onClick={() => refetch()} className="mt-2 underline">
                    Retry
                </button>
            </div>
        );
    }

    if (entries.length === 0) {
        return (
            <div className="flex h-full flex-col items-center pt-[25%] text-xs text-(--secondary-text-wMain)">
                <MessageCircle className="mb-2 h-6 w-6" />
                No recent chats
            </div>
        );
    }

    return (
        <div className="flex flex-col">
            {entries.map(entry => {
                if (entry.kind === "session") {
                    const { session } = entry;
                    const displayName = session.name?.trim() || "New Chat";
                    const isActive = session.id === sessionId;
                    const hasUnseen = !isActive && hasUnseenUpdates(session);

                    if (editingSessionId === session.id) {
                        return (
                            <div key={`session-${session.id}`} className={sessionButtonStyles({ active: isActive })}>
                                <SessionIcon session={session} className="text-(--darkSurface-textMuted)" />
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
                                    className="min-w-0 flex-1 bg-transparent text-sm text-(--darkSurface-text) focus:outline-none"
                                />
                            </div>
                        );
                    }

                    return (
                        <Tooltip key={`session-${session.id}`}>
                            <TooltipTrigger asChild>
                                <div {...clickableNodeProps(() => handleSessionClick(session))} className={`${sessionButtonStyles({ active: isActive })} group`}>
                                    {hasUnseen && <span aria-label="Unseen updates" className="absolute top-1/2 left-[10px] h-[38px] w-1 -translate-y-1/2 rounded-sm bg-(--info-wMain)" />}
                                    <SessionIcon session={session} className="text-(--darkSurface-textMuted)" />
                                    <div className="min-w-0 flex-1">
                                        <SessionName session={session} respondingSessionId={respondingSessionId} isActive={isActive} hasRunningBackgroundTask={session.hasRunningBackgroundTask} />
                                    </div>
                                    <SessionActionMenu
                                        session={session}
                                        onRename={beginRename}
                                        onRenameWithAI={renameWithAI}
                                        onMove={move}
                                        onDelete={remove}
                                        onGoToProject={goToProject}
                                        onShare={chatSharingEnabled ? share : undefined}
                                        isRegeneratingTitle={regeneratingTitleForSession === session.id}
                                        triggerClassName="text-(--darkSurface-textMuted) opacity-0 hover:text-(--darkSurface-text) group-hover:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100 max-md:opacity-100"
                                    />
                                </div>
                            </TooltipTrigger>
                            <TooltipContent side="right">{displayName}</TooltipContent>
                        </Tooltip>
                    );
                }

                const { shared } = entry;
                return (
                    <Tooltip key={`shared-${shared.id}`}>
                        <TooltipTrigger asChild>
                            <button onClick={() => handleSharedClick(shared)} className={sessionButtonStyles({ active: false })}>
                                {hasUnseenShare(shared) && <span aria-label="Unseen updates" className="absolute top-1/2 left-[10px] h-[38px] w-1 -translate-y-1/2 rounded-sm bg-(--info-wMain)" />}
                                <Share2 className="h-4 w-4 flex-shrink-0 text-(--darkSurface-textMuted)" aria-label="Shared with me" />
                                <div className="min-w-0 flex-1">
                                    <span className={sessionTextStyles({ active: false, animation: "none" })}>{shared.title}</span>
                                </div>
                            </button>
                        </TooltipTrigger>
                        <TooltipContent side="right">
                            Shared by {shared.ownerEmail}: {shared.title}
                        </TooltipContent>
                    </Tooltip>
                );
            })}
        </div>
    );
}
