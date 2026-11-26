// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const productAnalyticsKeys = {
    all: ["productAnalytics"] as const,
    consent: () => [...productAnalyticsKeys.all, "consent"] as const,
};
