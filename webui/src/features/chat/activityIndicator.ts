// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function filterActivityUpdates<T extends { type: string }>(updates: readonly T[] | undefined, showThinking: boolean): T[] {
    const list = updates ?? [];
    return showThinking ? [...list] : list.filter(u => u.type !== "thinking");
}

export function dedupeConsecutiveUpdates<T extends { type: string; text: string }>(updates: readonly T[]): T[] {
    return updates.filter((update, index) => update.type === "thinking" || index === 0 || update.text !== updates[index - 1].text);
}

const BUBBLES_WITHOUT_TIMELINE = new Set(["compaction_notification", "deep_research_plan"]);

type ActivityMessage = {
    isUser?: boolean;
    isComplete?: boolean;
    isStatusBubble?: boolean;
    taskId?: string;
    parts?: readonly { kind?: string; data?: { type?: string } | unknown }[];
    progressUpdates?: readonly { type: string }[];
};

const rendersItsOwnBubble = (message: ActivityMessage): boolean =>
    !!message.parts?.some(part => {
        if (part.kind !== "data") return false;
        const type = (part.data as { type?: string } | undefined)?.type;
        return !!type && BUBBLES_WITHOUT_TIMELINE.has(type);
    });

export function hasVisibleActivityUpdates(lastMessage: ActivityMessage | undefined, showThinking: boolean): boolean {
    if (!lastMessage || lastMessage.isUser || lastMessage.isComplete) return false;
    return filterActivityUpdates(lastMessage.progressUpdates, showThinking).length > 0;
}

export function hasLiveActivityUpdates(lastMessage: ActivityMessage | undefined, showThinking: boolean): boolean {
    if (!lastMessage) return false;
    if (!hasVisibleActivityUpdates(lastMessage, showThinking)) return false;
    if (!lastMessage.taskId) return false;
    if (lastMessage.isStatusBubble) return false;
    return !rendersItsOwnBubble(lastMessage);
}
