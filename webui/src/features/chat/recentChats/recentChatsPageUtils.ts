// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { BUILDER_AGENT_NAME } from "@/providers/contexts/BuilderContext";

import { toEpochMs } from "@/utils/sessionUnseen";

import type { SharedWithMeItem } from "@/types/share";

import { NO_PROJECT_LABEL, NO_PROJECT_VALUE, UNRESOLVED_PROJECT_FILTER } from "@/features/chat/projectFilter";

import type { Project, Session } from "@/types";

export type Tab = "chat" | "scheduler" | "shared";

export function collectMessageTexts(tasks: Array<{ messageBubbles: string }>): string[] {
    const texts: string[] = [];
    for (const task of tasks) {
        let bubbles: Array<{ text?: string }>;
        try {
            bubbles = JSON.parse(task.messageBubbles);
        } catch (error) {
            console.warn("Skipping task with unparseable messageBubbles:", error);
            continue;
        }
        if (!Array.isArray(bubbles)) continue;
        for (const bubble of bubbles) {
            const text = (bubble.text || "").trim();
            if (text) texts.push(text);
        }
    }
    return texts;
}

export function isSessionUserVisible(session: Session, builderEnabled: boolean): boolean {
    return !session.isEphemeralTest && (builderEnabled || session.agentId !== BUILDER_AGENT_NAME);
}

export function isSessionInTab(session: Session, activeTab: Tab): boolean {
    return activeTab === "scheduler" ? session.source === "scheduler" : session.source !== "scheduler";
}

export function resolveProjectFilterId(selectedProject: string, sessions: Session[], projects: Project[]): string | null {
    if (selectedProject === "all") return null;
    if (selectedProject === NO_PROJECT_LABEL) return NO_PROJECT_VALUE;
    const projectId = projects.find(project => project.name === selectedProject)?.id ?? sessions.find(session => session.project?.name === selectedProject)?.project?.id;
    return projectId ?? UNRESOLVED_PROJECT_FILTER;
}

export function filterSessions(sessions: Session[], activeTab: Tab, selectedProject: string, builderEnabled: boolean): Session[] {
    let result = sessions.filter(session => isSessionUserVisible(session, builderEnabled));
    result = result.filter(session => isSessionInTab(session, activeTab));

    if (selectedProject === "all") return result;
    if (selectedProject === NO_PROJECT_LABEL) return result.filter(session => !session.project?.name);
    return result.filter(session => session.project?.name === selectedProject);
}

export function sortSharedChats(items: SharedWithMeItem[]): SharedWithMeItem[] {
    return [...items].sort((a, b) => {
        const diff = (toEpochMs(b.sharedAt) || 0) - (toEpochMs(a.sharedAt) || 0);
        return diff === 0 ? b.id.localeCompare(a.id) : diff;
    });
}
