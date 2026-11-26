// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { classifyErrorCategory } from "./errorCategory";
import { report } from "./report";
import { currentRoutePattern } from "./routePattern";
import type { ProductAnalyticsErrorSource } from "./types";

export function reportErrorDisplayed(message: string, source: ProductAnalyticsErrorSource, traceId?: string): void {
    report("error:displayed", {
        errorCategory: classifyErrorCategory(message),
        errorSource: source,
        page: currentRoutePattern(),
        ...(traceId ? { traceId } : {}),
    });
}
