// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { NotificationItem } from "@/api/notifications";

import { categoryOf, type NotificationCategories } from "./notificationCategory";

export function NotificationCategoryCaption({ n, categories }: Readonly<{ n: NotificationItem; categories: NotificationCategories }>) {
    const category = categoryOf(n, categories);
    if (!category) {
        return null;
    }
    return <span className="text-[10px] leading-4 font-medium tracking-wide text-(--secondary-text-wMain) uppercase">{category.label}</span>;
}
