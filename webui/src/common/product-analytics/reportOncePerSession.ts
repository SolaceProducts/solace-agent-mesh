// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { report, reportingSessionId } from "./report";
import type { ProductAnalyticsEventName } from "./types";

const KEY_PREFIX = "sam.productAnalytics.sessionOnce.";

const claimedWithoutStorage = new Set<string>();

export function reportOncePerSession(event: ProductAnalyticsEventName, properties: Record<string, unknown> = {}): void {
    if (!claim(event)) return;
    report(event, properties);
}

function claim(event: ProductAnalyticsEventName): boolean {
    const session = reportingSessionId();
    if (!session) return true;

    const key = `${KEY_PREFIX}${session}.${event}`;
    if (claimedWithoutStorage.has(key)) return false;
    try {
        if (sessionStorage.getItem(key) !== null) return false;
        sessionStorage.setItem(key, "1");
    } catch {
        claimedWithoutStorage.add(key);
    }
    return true;
}
