// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentCardInfo } from "@/types";

import { hasAnyTraceReadReach } from "./feedbackScope";
import { hasStrictScope } from "./scopeMatcher";
import { SCOPES } from "./scopes";

export const ACTIVITY_MONITOR_DISPLAY_NAME = "Activity Monitor";

export function resolveActivityMonitor(cards: readonly AgentCardInfo[] | undefined): AgentCardInfo | undefined {
    return cards?.find(card => card.displayName === ACTIVITY_MONITOR_DISPLAY_NAME || card.name === ACTIVITY_MONITOR_DISPLAY_NAME);
}

export function canActivityMonitorReadTask(taskOwnerUserId: string | undefined, username: string | null | undefined, scopes: readonly string[]): boolean {
    if (username != null && taskOwnerUserId === username) return true;
    return hasTenantOrTraceReach(scopes);
}

export function hasTenantOrTraceReach(scopes: readonly string[]): boolean {
    return hasStrictScope(scopes, SCOPES.activity.readAny) || hasAnyTraceReadReach(scopes);
}
