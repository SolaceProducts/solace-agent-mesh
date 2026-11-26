// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageKind, UsageListParams, UsageSubjectRef, UsageWindowParams } from "./types";

export const usageKeys = {
    all: ["usage"] as const,
    summary: (window: UsageWindowParams) => [...usageKeys.all, "summary", window] as const,
    allLists: ["usage", "list"] as const,
    lists: (kind: UsageKind) => [...usageKeys.allLists, kind] as const,
    list: <K extends UsageKind>(kind: K, params: UsageListParams<K>) => [...usageKeys.lists(kind), params] as const,
    detail: (kind: UsageKind, ref: UsageSubjectRef | null, window: UsageWindowParams) => [...usageKeys.all, "detail", kind, ref, window] as const,
    self: (window: UsageWindowParams) => [...usageKeys.all, "self", window] as const,
    savings: (window: UsageWindowParams) => [...usageKeys.all, "savings", window] as const,
    selfSavings: (window: UsageWindowParams) => [...usageKeys.all, "selfSavings", window] as const,
};
