// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { MessageCircle, Share2 } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { useRecentSessions } from "@/api/sessions";
import { useMarkShareViewed, useSharedWithMe } from "@/api/share";

import { useChatContext, useThemeContext } from "@/providers/contexts";

import { useBuilderEnabled } from "@/hooks/useBuilderEnabled";
import { useIsChatSharingEnabled } from "@/hooks/useIsChatSharingEnabled";

import type { SharedWithMeItem } from "@/types/share";

import { isSessionUserVisible } from "@/features/chat/recentChats/recentChatsPageUtils";
import { mergeRecentEntries } from "@/features/chat/recentChats/recentEntries";
import { MAX_RECENT_CHATS } from "@/features/navigation/constants";

import { SessionIcon } from "@/common";
import type { Session } from "@/types";
import { Popover, PopoverContent, PopoverTrigger, Spinner } from "@/ui";
import { hasUnseenShare, resolveAgentSessionRoute } from "@/utils";

import { iconStyles, iconWrapperStyles, navButtonStyles } from "./navigationStyles";

const FLYOUT_MAX_ITEMS = 8;
const RECENT_SESSIONS_FETCH_LIMIT_SHARED_WITH_EXPANDED_LIST = MAX_RECENT_CHATS;

const CLOSE_GRACE_MS = 150;

const flyoutSessionButtonStyles = "relative flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-(--darkSurface-bgHover)";

const unseenBarStyles = "absolute top-1/2 left-0.5 h-[24px] w-1 -translate-y-1/2 rounded-sm bg-(--info-wMain)";

export interface RecentChatsFlyoutProps {
    readonly label?: string;
}

export function RecentChatsFlyout({ label = "Recent Chats" }: RecentChatsFlyoutProps) {
    const navigate = useNavigate();
    const { handleSwitchSession, agentSessionRoutes, sessionListExcludeAgentIds } = useChatContext();
    const { currentTheme } = useThemeContext();
    const isDark = currentTheme === "dark";

    const [open, setOpen] = useState(false);
    const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    const cancelClose = useCallback(() => {
        if (closeTimerRef.current) {
            clearTimeout(closeTimerRef.current);
            closeTimerRef.current = null;
        }
    }, []);

    const scheduleClose = useCallback(() => {
        cancelClose();
        closeTimerRef.current = setTimeout(() => setOpen(false), CLOSE_GRACE_MS);
    }, [cancelClose]);

    const openNow = useCallback(() => {
        cancelClose();
        setOpen(true);
    }, [cancelClose]);

    useEffect(() => {
        return () => {
            cancelClose();
        };
    }, [cancelClose]);

    const { data: rawSessions, isLoading, isError } = useRecentSessions(RECENT_SESSIONS_FETCH_LIMIT_SHARED_WITH_EXPANDED_LIST);
    const chatSharingEnabled = useIsChatSharingEnabled();
    const builderEnabled = useBuilderEnabled();
    const { data: sharedItems = [] } = useSharedWithMe({ enabled: chatSharingEnabled });
    const markShareViewedMutation = useMarkShareViewed();

    const entries = useMemo(() => {
        const excludeSet = new Set(sessionListExcludeAgentIds ?? []);
        const sessions: Session[] = (rawSessions ?? []).filter(s => isSessionUserVisible(s, builderEnabled) && (s.agentId == null || !excludeSet.has(s.agentId)));
        return mergeRecentEntries(sessions, sharedItems, { maxItems: FLYOUT_MAX_ITEMS, includeShared: chatSharingEnabled });
    }, [rawSessions, sharedItems, chatSharingEnabled, sessionListExcludeAgentIds, builderEnabled]);

    const handleIconClick = useCallback(() => {
        setOpen(false);
        navigate("/recent-chats");
    }, [navigate]);

    const handleSessionClick = useCallback(
        async (session: Session) => {
            const route = resolveAgentSessionRoute(session, agentSessionRoutes) ?? "/chat";
            setOpen(false);
            if (route !== "/chat") {
                window.dispatchEvent(
                    new CustomEvent("agent-session-navigate", {
                        detail: { sessionId: session.id, route, agentId: session.agentId, isEphemeralTest: session.isEphemeralTest === true },
                    })
                );
                return;
            }
            navigate("/chat");
            try {
                await handleSwitchSession(session.id);
            } catch (err) {
                console.error("RecentChatsFlyout: failed to switch session", err);
            }
        },
        [agentSessionRoutes, handleSwitchSession, navigate]
    );

    const handleSharedClick = useCallback(
        async (item: SharedWithMeItem) => {
            setOpen(false);
            markShareViewedMutation.mutate(item.id);
            if (item.accessLevel === "resource_editor" && item.sessionId) {
                navigate("/chat");
                try {
                    await handleSwitchSession(item.sessionId);
                } catch (err) {
                    console.error("RecentChatsFlyout: failed to switch to shared session", err);
                }
                return;
            }
            navigate(`/shared-chat/${item.id}`);
        },
        [handleSwitchSession, markShareViewedMutation, navigate]
    );

    const renderBody = () => {
        if (isLoading && entries.length === 0) {
            return (
                <div className="flex items-center justify-center px-3 py-4">
                    <Spinner />
                </div>
            );
        }
        if (isError && entries.length === 0) {
            return <div className="flex flex-col items-center px-3 py-4 text-xs text-(--darkSurface-textMuted)">Could not load chats</div>;
        }
        if (entries.length === 0) {
            return (
                <div className="flex flex-col items-center px-3 py-4 text-xs text-(--darkSurface-textMuted)">
                    <MessageCircle className="mb-2 h-6 w-6" />
                    No recent chats
                </div>
            );
        }
        return (
            <ul className="flex flex-col" aria-label={label}>
                {entries.map(entry => {
                    if (entry.kind === "shared") {
                        const { shared } = entry;
                        return (
                            <li key={`shared-${shared.id}`}>
                                <button type="button" onClick={() => handleSharedClick(shared)} className={flyoutSessionButtonStyles}>
                                    {hasUnseenShare(shared) && <span aria-label="Unseen updates" className={unseenBarStyles} />}
                                    <Share2 className="h-4 w-4 flex-shrink-0 text-(--darkSurface-textMuted)" aria-label="Shared with me" />
                                    <span className="min-w-0 flex-1 truncate text-sm text-(--darkSurface-textMuted)">{shared.title}</span>
                                </button>
                            </li>
                        );
                    }

                    const { session } = entry;
                    const trimmed = session.name?.trim() ?? "";
                    const displayName = trimmed.length > 0 ? trimmed : "New Chat";
                    return (
                        <li key={session.id}>
                            <button type="button" onClick={() => handleSessionClick(session)} className={flyoutSessionButtonStyles}>
                                <SessionIcon session={session} className="text-(--darkSurface-textMuted)" />
                                <span className="min-w-0 flex-1 truncate text-sm text-(--darkSurface-textMuted)">{displayName}</span>
                            </button>
                        </li>
                    );
                })}
            </ul>
        );
    };

    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <button type="button" aria-label={label} onClick={handleIconClick} onMouseEnter={openNow} onMouseLeave={scheduleClose} onFocus={openNow} onBlur={scheduleClose} className={navButtonStyles()}>
                    <div className={iconWrapperStyles({ active: false, withMargin: true })}>
                        <MessageCircle className={iconStyles({ active: false })} />
                    </div>
                </button>
            </PopoverTrigger>
            <PopoverContent
                side="right"
                align="start"
                sideOffset={isDark ? 1 : 0}
                alignOffset={-1}
                onMouseEnter={openNow}
                onMouseLeave={scheduleClose}
                onFocusCapture={cancelClose}
                onBlurCapture={scheduleClose}
                onOpenAutoFocus={e => e.preventDefault()}
                onCloseAutoFocus={e => e.preventDefault()}
                className="w-72 rounded-l-none border border-l-0 border-(--secondary-w70) bg-(--darkSurface-bg) py-1 text-(--darkSurface-text)"
            >
                <div className="px-3 py-2">
                    <span className="text-sm font-bold text-(--darkSurface-textMuted)">{label}</span>
                </div>
                {renderBody()}
            </PopoverContent>
        </Popover>
    );
}
