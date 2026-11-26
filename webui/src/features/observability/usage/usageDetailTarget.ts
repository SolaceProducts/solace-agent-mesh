// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { isAddressableBookedName, isUsageSubjectId, type UsageKind, type UsageRow, type UsageSubjectRef } from "@/api/usage";

import { isUsageKind } from "./usageKinds";

export interface UsageDetailTarget {
    kind: UsageKind;
    ref: UsageSubjectRef;
}

const SEPARATOR = ":";

export function detailTargetFor(kind: UsageKind, row: Pick<UsageRow, "id" | "bookedName">): UsageDetailTarget | null {
    if (row.id !== null) return isUsageSubjectId(row.id) ? { kind, ref: { id: row.id } } : null;
    if (kind === "users" || row.bookedName === null || !isAddressableBookedName(row.bookedName)) return null;
    return { kind, ref: { bookedName: row.bookedName } };
}

export function serializeDetailTarget({ kind, ref }: UsageDetailTarget): string {
    return "id" in ref ? [kind, "id", ref.id].join(SEPARATOR) : [kind, "name", ref.bookedName].join(SEPARATOR);
}

export function parseDetailTarget(raw: string | null): UsageDetailTarget | null {
    if (raw === null) return null;
    const [kind, by, ...rest] = raw.split(SEPARATOR);
    const value = rest.join(SEPARATOR);
    if (!isUsageKind(kind) || value === "") return null;
    if (by === "id") return isUsageSubjectId(value) ? { kind, ref: { id: value } } : null;
    if (by === "name" && kind !== "users" && isAddressableBookedName(value)) return { kind, ref: { bookedName: value } };
    return null;
}
