// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import type { NotificationCategoryPreference, NotificationItem } from "@/api/notifications";
import { useNotificationCategoryPreferences } from "@/api/notifications";

const ALWAYS_IN_INBOX_CATEGORIES: ReadonlySet<string> = new Set(["hil"]);

export type NotificationCategories = ReadonlyMap<string, NotificationCategoryPreference>;

export function useNotificationCategories(): NotificationCategories {
    const { data } = useNotificationCategoryPreferences();
    return useMemo(() => new Map((data?.data ?? []).map(category => [category.category, category])), [data]);
}

export function categoryOf(n: NotificationItem, categories: NotificationCategories): NotificationCategoryPreference | undefined {
    return n.category ? categories.get(n.category) : undefined;
}

export function turnOffableCategory(n: NotificationItem, categories: NotificationCategories): NotificationCategoryPreference | undefined {
    const category = categoryOf(n, categories);
    if (!category?.enabled || ALWAYS_IN_INBOX_CATEGORIES.has(category.category)) {
        return undefined;
    }
    return category;
}
