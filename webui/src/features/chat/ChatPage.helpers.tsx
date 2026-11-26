// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { GitFork, Loader2, PanelRightIcon, RefreshCcw } from "lucide-react";

import type { MessageFE } from "@/types/chat";
import type { CollaborativeUser } from "@/types/collaboration";
import type { SharedLinkUserInfo } from "@/types/share";

import { ShareButton } from "@/features/share/ShareButton";

import { type ButtonWithCallback, EmptyState } from "@/common";
import type { AgentCardInfo } from "@/types";
import { Button } from "@/ui";

import { ChatSessionDialog } from "./ChatSessionDialog";
import { ChatWelcomeScreen } from "./ChatWelcomeScreen";
import { RecentChatsToggleButton } from "./recentChats";
import { UserPresenceAvatars } from "./UserPresenceAvatars";

interface SharingButtonsParams {
    sessionId: string | null;
    sessionName: string | null;
    isCollaborativeSession: boolean;
    collaborativeUsers: CollaborativeUser[];
    sharedEditorUsers: CollaborativeUser[];
    currentUserEmail: string | undefined;
    isForkingChat: boolean;
    isMobile: boolean;
    handleForkCollaborativeChat: () => Promise<void>;
    onShare: () => void;
}

export const presenceAvatarsForSession = ({
    isCollaborativeSession,
    collaborativeUsers,
    sharedEditorUsers,
    currentUserEmail,
}: Pick<SharingButtonsParams, "isCollaborativeSession" | "collaborativeUsers" | "sharedEditorUsers" | "currentUserEmail">): React.ReactNode | null => {
    if (isCollaborativeSession && collaborativeUsers.length > 0) {
        return <UserPresenceAvatars key="presence-avatars" users={collaborativeUsers} currentUserId={currentUserEmail} />;
    }
    if (sharedEditorUsers.length > 0) {
        return <UserPresenceAvatars key="presence-avatars" users={sharedEditorUsers} />;
    }
    return null;
};

export const forkOrShareButton = ({ sessionId, sessionName, isCollaborativeSession, isForkingChat, isMobile, handleForkCollaborativeChat, onShare }: SharingButtonsParams): React.ReactNode => {
    if (isCollaborativeSession) {
        return (
            <Button key="fork-button" variant="outline" size="sm" onClick={handleForkCollaborativeChat} disabled={isForkingChat} title="Save a personal copy of this conversation">
                {isForkingChat ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <GitFork className="mr-2 h-4 w-4" />}
                {!isMobile && "Continue in New Chat"}
            </Button>
        );
    }
    return <ShareButton key="share-button" sessionId={sessionId ?? ""} sessionTitle={sessionName ?? "New Chat"} onClick={onShare} />;
};

export const mobileSidePanelButton = (setIsMobileSidePanelOpen: (open: boolean) => void): React.ReactNode => (
    <Button key="mobile-side-panel" variant="ghost" onClick={() => setIsMobileSidePanelOpen(true)} className="h-10 w-10 p-0" tooltip="Show Activity Panel">
        <PanelRightIcon className="size-5" />
    </Button>
);

interface ComputeHeaderButtonsParams extends SharingButtonsParams {
    chatSharingEnabled: boolean;
    setIsMobileSidePanelOpen: (open: boolean) => void;
}

export const computeHeaderButtons = (params: ComputeHeaderButtonsParams): React.ReactNode[] | undefined => {
    const buttons: React.ReactNode[] = [];
    if (params.sessionId && params.chatSharingEnabled) {
        const avatars = presenceAvatarsForSession(params);
        if (avatars) buttons.push(avatars);
        buttons.push(forkOrShareButton(params));
    }
    if (params.isMobile) {
        buttons.push(mobileSidePanelButton(params.setIsMobileSidePanelOpen));
    }
    return buttons.length > 0 ? buttons : undefined;
};

export function headerLeadingAction({ showRecentChatsPanel, onToggleRecentChats }: { showRecentChatsPanel: boolean; onToggleRecentChats: () => void }): React.ReactNode {
    if (showRecentChatsPanel) {
        return (
            <div className="flex items-center gap-2">
                <RecentChatsToggleButton onToggle={onToggleRecentChats} />
                <div className="h-6 border-r" />
                <ChatSessionDialog />
            </div>
        );
    }
    return <ChatSessionDialog />;
}

export interface AgentsRefetch {
    refetch: () => void;
    isRefetching: boolean;
}

interface ChatAgentGateParams {
    isLoadingAgents: boolean;
    hasAgentsError?: boolean;
    agents: AgentCardInfo[];
    agentsRefetch: AgentsRefetch;
}

export function chatAgentGate({ isLoadingAgents, hasAgentsError, agents, agentsRefetch }: ChatAgentGateParams): React.ReactNode | null {
    if (isLoadingAgents) {
        return <EmptyState variant="loading" title="Loading agents" />;
    }
    const chatAgents = agents.filter(agent => !agent.isWorkflow && !agent.isInternal);
    if (chatAgents.length > 0) {
        return null;
    }
    const refreshButtons: ButtonWithCallback[] = [{ icon: <RefreshCcw className="h-4 w-4" />, text: "Refresh", variant: "outline", disabled: agentsRefetch.isRefetching, onClick: agentsRefetch.refetch }];
    if (hasAgentsError) {
        return <EmptyState variant="error" title="Unable to Load Agents" buttons={refreshButtons} />;
    }
    return <EmptyState variant="notFound" title="No Agents Found" subtitle="Contact your administrator if you should have access to chat with an agent." buttons={refreshButtons} />;
}

interface ChatBodyStateParams {
    isLoadingSession: boolean;
    noAgentSpecified: boolean;
    isAwaitingPinnedAgent: boolean;
    pinnedAgentTimedOut: boolean;
    isWelcomeState: boolean;
    isResponding: boolean;
    isLoadingAgents: boolean;
    hasAgentsError?: boolean;
    agents: AgentCardInfo[];
    selectedAgentName?: string;
    agentsRefetch: AgentsRefetch;
}

export function chatBodyState({
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
    agentsRefetch,
}: ChatBodyStateParams): React.ReactNode | null {
    if (isLoadingSession) {
        return <EmptyState variant="loading" title="Loading Session" />;
    }
    if (noAgentSpecified) {
        return <EmptyState variant="error" title="No agent specified" subtitle="This chat needs an 'agent' specified in its address." />;
    }
    if (isAwaitingPinnedAgent) {
        return pinnedAgentTimedOut ? <EmptyState variant="error" title="Agent unavailable" subtitle="This agent does not exist or is currently unavailable." /> : <EmptyState variant="loading" title="Agent connecting..." />;
    }
    if (isWelcomeState && !isResponding) {
        return chatAgentGate({ isLoadingAgents, hasAgentsError, agents, agentsRefetch }) ?? <ChatWelcomeScreen agents={agents} selectedAgentName={selectedAgentName} autoFocusComposer />;
    }
    return null;
}

export type ShareNotification =
    { variant: "shared-with-users"; names: string[]; timestamp: number; accessLevel: "viewer" | "editor" } | { variant: "role-changed"; names: string[]; timestamp: number; fromAccessLevel: "viewer" | "editor"; toAccessLevel: "viewer" | "editor" };

const toDisplayName = (email: string): string => {
    const emailName = email.split("@")[0] || email;
    return emailName.replace(/[._-]/g, " ").replace(/\b\w/g, c => c.toUpperCase());
};

export function buildShareNotifications(shareUsers: SharedLinkUserInfo[]): { notifications: ShareNotification[]; editorUsers: CollaborativeUser[] } {
    const origGroupKey = (ts: number, level: string) => `orig:${ts}:${level}`;
    const origGroupMap = new Map<string, ShareNotification & { variant: "shared-with-users" }>();
    const changeGroupKey = (ts: number, from: string, to: string) => `change:${ts}:${from}:${to}`;
    const changeGroupMap = new Map<string, ShareNotification & { variant: "role-changed" }>();

    for (const user of shareUsers) {
        const displayName = toDisplayName(user.userEmail);

        if (user.originalAccessLevel && user.originalAddedAt) {
            const origLevel = user.originalAccessLevel === "resource_editor" ? ("editor" as const) : ("viewer" as const);
            const origAddedAtMs = Date.parse(user.originalAddedAt) || 0;
            const oKey = origGroupKey(origAddedAtMs, user.originalAccessLevel);
            if (!origGroupMap.has(oKey)) {
                origGroupMap.set(oKey, { variant: "shared-with-users", timestamp: origAddedAtMs, names: [], accessLevel: origLevel });
            }
            origGroupMap.get(oKey)!.names.push(displayName);

            const newLevel = user.accessLevel === "resource_editor" ? ("editor" as const) : ("viewer" as const);
            const addedAtMs = Date.parse(user.addedAt) || 0;
            const cKey = changeGroupKey(addedAtMs, user.originalAccessLevel, user.accessLevel);
            if (!changeGroupMap.has(cKey)) {
                changeGroupMap.set(cKey, { variant: "role-changed", timestamp: addedAtMs, names: [], fromAccessLevel: origLevel, toAccessLevel: newLevel });
            }
            changeGroupMap.get(cKey)!.names.push(displayName);
        } else {
            const level = user.accessLevel === "resource_editor" ? ("editor" as const) : ("viewer" as const);
            const addedAtMs = Date.parse(user.addedAt) || 0;
            const oKey = origGroupKey(addedAtMs, user.accessLevel);
            if (!origGroupMap.has(oKey)) {
                origGroupMap.set(oKey, { variant: "shared-with-users", timestamp: addedAtMs, names: [], accessLevel: level });
            }
            origGroupMap.get(oKey)!.names.push(displayName);
        }
    }

    const notifications: ShareNotification[] = [...Array.from(origGroupMap.values()), ...Array.from(changeGroupMap.values())].sort((a, b) => a.timestamp - b.timestamp);
    const editorUsers: CollaborativeUser[] = shareUsers
        .filter(u => u.accessLevel === "resource_editor")
        .map(u => ({ id: u.userEmail.toLowerCase(), name: toDisplayName(u.userEmail), email: u.userEmail, role: "collaborator" as const, isOnline: true }));

    return { notifications, editorUsers };
}

export function buildCollaborativeUsers(messages: MessageFE[], sessionOwnerEmail?: string | null, sessionOwnerName?: string | null): CollaborativeUser[] {
    const userMap = new Map<string, CollaborativeUser>();

    if (sessionOwnerEmail) {
        userMap.set(sessionOwnerEmail.toLowerCase(), {
            id: sessionOwnerEmail.toLowerCase(),
            name: sessionOwnerName || sessionOwnerEmail,
            email: sessionOwnerEmail,
            role: "collaborator",
            isOnline: true,
        });
    }

    for (const msg of messages) {
        if (msg.isUser && msg.senderEmail && !userMap.has(msg.senderEmail.toLowerCase())) {
            userMap.set(msg.senderEmail.toLowerCase(), {
                id: msg.senderEmail.toLowerCase(),
                name: msg.senderDisplayName || msg.senderEmail,
                email: msg.senderEmail,
                role: "collaborator",
                isOnline: true,
            });
        }
    }

    return Array.from(userMap.values());
}

export function computeShareNotificationInsertions(notifications: ShareNotification[], messages: MessageFE[]): Array<ShareNotification & { insertAfterIndex: number }> {
    return notifications.map(notification => {
        let insertAfterIndex = -1;
        for (let i = messages.length - 1; i >= 0; i--) {
            const msg = messages[i];
            if (msg.createdAt && msg.createdAt <= notification.timestamp) {
                insertAfterIndex = i;
                break;
            }
        }
        if (insertAfterIndex === -1) {
            insertAfterIndex = messages.length - 1;
        }
        return { ...notification, insertAfterIndex };
    });
}
