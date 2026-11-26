// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

export type SessionAction = "goToProject" | "rename" | "renameWithAI" | "move" | "share" | "delete";

export const UI_MODE = {
    full: "full",
    agentMode: "agentMode",
} as const;

export type UIModeName = (typeof UI_MODE)[keyof typeof UI_MODE];

interface UIModeCapabilities {
    showNavigationSidebar: boolean;
    showRecentChatsPanel: boolean;
    showAgentSelector: boolean;
    showActivityPanel: boolean;

    allowPrompts: boolean;
    allowMentions: boolean;
    allowSpeech: boolean;
    allowedSessionActions: ReadonlyArray<SessionAction>;

    seedWelcomeBubble: boolean;
}

export interface UIMode extends UIModeCapabilities {
    mode: UIModeName;
    pinnedAgentName: string | null;
}

export const ALL_SESSION_ACTIONS: ReadonlyArray<SessionAction> = ["goToProject", "rename", "renameWithAI", "move", "share", "delete"];

const AGENT_SESSION_ACTIONS: ReadonlyArray<SessionAction> = ["rename", "renameWithAI", "delete"];

export const FULL_UI_MODE: UIMode = {
    mode: UI_MODE.full,
    pinnedAgentName: null,
    showNavigationSidebar: true,
    showRecentChatsPanel: false,
    showAgentSelector: true,
    showActivityPanel: true,
    allowPrompts: true,
    allowMentions: true,
    allowSpeech: true,
    seedWelcomeBubble: true,
    allowedSessionActions: ALL_SESSION_ACTIONS,
};

export const agentUIMode = (pinnedAgentName: string | null): UIMode => ({
    mode: UI_MODE.agentMode,
    showNavigationSidebar: false,
    showRecentChatsPanel: true,
    showAgentSelector: false,
    showActivityPanel: false,
    allowPrompts: false,
    allowMentions: true,
    allowSpeech: false,
    pinnedAgentName,
    seedWelcomeBubble: false,
    allowedSessionActions: AGENT_SESSION_ACTIONS,
});

export const DEFAULT_UI_MODE: UIMode = FULL_UI_MODE;

export const UIModeContext = createContext<UIMode>(FULL_UI_MODE);

export const useUIModeContext = (): UIMode => useContext(UIModeContext);
