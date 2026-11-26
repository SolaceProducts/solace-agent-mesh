// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageKind, UsageListParams, UsageSubjectRef, UsageWindowParams } from "./types";

export function windowQuery(windowParams: UsageWindowParams): URLSearchParams {
    return new URLSearchParams("period" in windowParams ? { period: windowParams.period } : { startDate: windowParams.startDate, endDate: windowParams.endDate });
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isAddressableBookedName(name: string): boolean {
    return name !== "";
}

export function isUsageSubjectId(id: string): boolean {
    return UUID.test(id);
}

export function subjectQuery(kind: UsageKind, ref: UsageSubjectRef, windowParams: UsageWindowParams): string {
    const query = windowQuery(windowParams);
    if ("id" in ref) {
        if (!isUsageSubjectId(ref.id)) throw new Error("A usage subject id is a UUID");
        return `${kind}/${ref.id}?${query.toString()}`;
    }
    if (kind === "users") throw new Error("Users have no byName detail route");
    if (!isAddressableBookedName(ref.bookedName)) throw new Error("Not an addressable booked name");
    query.set("name", ref.bookedName);
    return `${kind}/byName?${query.toString()}`;
}

export function usageListQuery<K extends UsageKind>(params: UsageListParams<K>): URLSearchParams {
    const query = windowQuery(params.window);
    query.set("pageNumber", String(params.pageNumber));
    query.set("pageSize", String(params.pageSize));
    query.set("sort", `${params.sortBy}:${params.sortDir}`);
    const filter = params.name?.trim();
    if (filter) query.set("search", filter);
    if (params.population) query.set("population", params.population);
    if (params.budgetId) query.set("budgetId", params.budgetId);
    if (params.expandDaily) query.set("expand", "daily");
    return query;
}
