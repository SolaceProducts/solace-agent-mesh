// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { A2AEventSSEPayload } from "@/types";

function eventIdentity(ev: A2AEventSSEPayload): string | null {
    const p = ev.full_payload as
        | {
              params?: { message?: { messageId?: string } };
              result?: { status?: { message?: { messageId?: string } }; message?: { messageId?: string } };
          }
        | undefined;
    const messageId = p?.result?.status?.message?.messageId ?? p?.result?.message?.messageId ?? p?.params?.message?.messageId;
    return messageId ? `msg:${messageId}` : null;
}

export function mergeDedupeEvents(backfill: A2AEventSSEPayload[], live: A2AEventSSEPayload[]): A2AEventSSEPayload[] {
    const seen = new Set<string>();
    const out: A2AEventSSEPayload[] = [];
    for (const ev of [...backfill, ...live]) {
        const key = eventIdentity(ev) ?? `${ev.message_id ?? ""}|${ev.timestamp}|${ev.direction}|${ev.solace_topic}|${ev.payload_summary?.method ?? ""}`;
        if (seen.has(key)) continue;
        seen.add(key);
        out.push(ev);
    }
    return out.sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
}

export const REHYDRATE_MAX_ATTEMPTS = 8;
export const REHYDRATE_DELAY_MS = 1000;

export function eventTreeIsComplete(events?: A2AEventSSEPayload[]): boolean {
    return !!events?.some(ev => ev.direction === "task" || ev.direction === "error_response");
}
