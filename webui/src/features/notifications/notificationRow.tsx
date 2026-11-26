// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const VIEW_CHAT_LABEL = "View Chat";

export const RESPOND_IN_CHAT_LABEL = "Respond in Chat";

export const NEEDS_RESPONSE_LABEL = "Needs response soon";

export function UnreadIndicator() {
    return <span aria-label="Unread" className="absolute inset-y-0.5 left-0 w-0.75 rounded-full bg-(--info-wMain)" />;
}
