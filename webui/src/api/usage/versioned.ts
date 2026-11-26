// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageDetailFor, UsageKind, UsageListParams, UsageListResponse, UsageSavings, UsageSelf, UsageSubjectRef, UsageSummary, UsageWindowParams } from "./types";
import { getSelfUsageSavingsV2, getSelfUsageV2, getUsageDetailV2, getUsageSavingsV2, getUsageSummaryV2, listUsageV2 } from "./v2";

export interface UsageApi {
    getSummary: (windowParams: UsageWindowParams) => Promise<UsageSummary>;
    list: <K extends UsageKind>(kind: K, params: UsageListParams<K>) => Promise<UsageListResponse<K>>;
    getDetail: <K extends UsageKind>(kind: K, ref: UsageSubjectRef, windowParams: UsageWindowParams) => Promise<UsageDetailFor<K>>;
    getSelf: (windowParams: UsageWindowParams) => Promise<UsageSelf>;
    getSavings: (windowParams: UsageWindowParams) => Promise<UsageSavings>;
    getSelfSavings: (windowParams: UsageWindowParams) => Promise<UsageSavings>;
}

export const usageApi: UsageApi = { getSummary: getUsageSummaryV2, list: listUsageV2, getDetail: getUsageDetailV2, getSelf: getSelfUsageV2, getSavings: getUsageSavingsV2, getSelfSavings: getSelfUsageSavingsV2 };
