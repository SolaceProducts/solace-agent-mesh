// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { reportErrorDisplayed } from "./reportErrorDisplayed";
import type { ProductAnalyticsErrorSource } from "./types";

type NotifyFn = (message: string, type?: "success" | "info" | "warning") => void;

export function reportAndNotifyError(addNotification: NotifyFn | undefined, message: string, source: ProductAnalyticsErrorSource): void {
    reportErrorDisplayed(message, source);
    addNotification?.(message, "warning");
}
