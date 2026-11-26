// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { NotificationItem } from "@/api/notifications";

export function isHilPending(n: NotificationItem): boolean {
    if (!n.requestId || !n.surface) return false;
    return n.promptState === "pending" || (n.status === "unread" && n.promptState !== "resolved");
}

export function isResolvedHil(n: NotificationItem): boolean {
    return Boolean(n.requestId && n.promptState === "resolved");
}

export function resolvedHilLabel(n: NotificationItem): string {
    const by = n.promptAnsweredBy ? ` by ${n.promptAnsweredBy}` : "";
    switch (n.promptOutcome) {
        case "approved":
            return `Approved${by}`;
        case "declined":
            return `Declined${by}`;
        case "answered":
            return `Answered${by}`;
        case "cancelled":
            return `Cancelled${by}`;
        case "withdrawn":
            return "No longer needed";
        case "failed":
            return "Not delivered";
        default:
            return "Expired";
    }
}
