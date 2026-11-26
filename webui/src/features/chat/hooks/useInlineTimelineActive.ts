// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { useBooleanFlagDetails } from "@openfeature/react-sdk";

import { useChatContext } from "@/providers/contexts";

import type { MessageFE } from "@/types";

import { hasLiveActivityUpdates } from "../activityIndicator";

export function useInlineTimelineActive(messages: readonly MessageFE[], hideInlineActivityForMessageId?: string): boolean {
    const { isResponding } = useChatContext();
    const { value: showThinkingContentEnabled } = useBooleanFlagDetails("show_thinking_content", false);

    return useMemo(() => {
        if (!isResponding) return false;
        const last = messages.at(-1);
        if (hideInlineActivityForMessageId && last?.metadata?.messageId === hideInlineActivityForMessageId) return false;
        return hasLiveActivityUpdates(last, showThinkingContentEnabled);
    }, [isResponding, messages, hideInlineActivityForMessageId, showThinkingContentEnabled]);
}
