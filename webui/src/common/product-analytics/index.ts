// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export { productAnalyticsClient } from "./client";
export { ProductAnalyticsGate, ProductAnalyticsPageViews } from "./ProductAnalyticsGate";
export { RouteChangeTracker } from "./RouteChangeTracker";
export { trackFormErrors } from "./trackFormErrors";
export type {
    LlmErrorCategory,
    ProductAnalyticsErrorSource,
    ProductAnalyticsEventName,
    ProductAnalyticsFormErrorType,
    ProductAnalyticsFormField,
    ProductAnalyticsFormName,
    ProductAnalyticsSetupPath,
    ProductAnalyticsSignInFailure,
    ProductAnalyticsSuggestionLabel,
} from "./types";
