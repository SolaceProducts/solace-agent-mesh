// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { toEpochMs } from "@/utils/sessionUnseen";

import type { SharedWithMeItem } from "@/types/share";

import type { Session } from "@/types";

export type SessionEntry = { kind: "session"; timestamp: number; session: Session };
export type SharedEntry = { kind: "shared"; timestamp: number; shared: SharedWithMeItem };
export type RecentEntry = SessionEntry | SharedEntry;

export interface MergeRecentEntriesOptions {
    maxItems: number;
    includeShared: boolean;
}

export function mergeRecentEntries(sessions: Session[], sharedItems: SharedWithMeItem[], { maxItems, includeShared }: MergeRecentEntriesOptions): RecentEntry[] {
    const sessionEntries: RecentEntry[] = sessions.map(session => ({
        kind: "session",
        timestamp: toTimestamp(session.updatedAt),
        session,
    }));

    const sharedEntries: RecentEntry[] = includeShared
        ? sharedItems.map(shared => ({
              kind: "shared",
              timestamp: toTimestamp(shared.sharedAt),
              shared,
          }))
        : [];

    return [...sessionEntries, ...sharedEntries].sort((a, b) => b.timestamp - a.timestamp).slice(0, maxItems);
}

function toTimestamp(value: string | null | undefined): number {
    const ms = toEpochMs(value);
    return Number.isFinite(ms) ? ms : 0;
}
