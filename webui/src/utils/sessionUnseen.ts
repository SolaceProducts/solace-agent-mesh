// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Session } from "@/types";

export const toEpochMs = (v: string | number | null | undefined): number => {
    if (v == null) return Number.NaN;
    if (typeof v === "number") return v < 10000000000 ? v * 1000 : v;
    const n = Number(v);
    if (Number.isFinite(n)) return n < 10000000000 ? n * 1000 : n;
    const parsed = Date.parse(v);
    return Number.isFinite(parsed) ? parsed : Number.NaN;
};

export const hasUnseenUpdates = (session: Session): boolean => {
    const updated = toEpochMs(session.updatedAt);
    const baseline = session.lastViewedAt != null ? toEpochMs(session.lastViewedAt) : toEpochMs(session.createdAt);
    return Number.isFinite(updated) && Number.isFinite(baseline) && updated > baseline;
};

export const hasUnseenShare = (item: { sharedAt: string; viewedAt?: string | null }): boolean => {
    const shared = toEpochMs(item.sharedAt);
    if (!Number.isFinite(shared)) return false;
    if (item.viewedAt == null) return true;
    const viewed = toEpochMs(item.viewedAt);
    if (!Number.isFinite(viewed)) return true;
    return viewed < shared;
};
