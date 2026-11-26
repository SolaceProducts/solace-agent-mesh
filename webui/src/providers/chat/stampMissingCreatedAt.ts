// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { MessageFE } from "@/types";

export function stampMissingCreatedAt(messages: MessageFE[], now: number): MessageFE[] {
    if (!messages.some(m => m.createdAt == null)) return messages;
    return messages.map(m => (m.createdAt == null ? { ...m, createdAt: now } : m));
}
