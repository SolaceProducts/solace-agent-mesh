// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { connectorsApi } from "@/api/connectors/versioned";
import { skillsApi } from "@/api/skills";

import { getErrorMessage } from "@/utils/api";

import { isStampedKind, type WrittenRecord, type WrittenResource } from "./builderOwnership";

export async function readPlatformStamp(resource: { id: string; kind: string }): Promise<string | undefined> {
    try {
        if (resource.kind === "connector") return (await connectorsApi.get(resource.id)).updatedAt;
        if (resource.kind === "skill") return (await skillsApi.get(resource.id)).contentHash || undefined;
        return undefined;
    } catch (err) {
        console.warn("[builder] could not read back a resource the session wrote", { kind: resource.kind, id: resource.id, error: getErrorMessage(err) });
        return undefined;
    }
}

export async function stampWritten(written: ReadonlyArray<{ id: string; kind: string }>): Promise<WrittenResource[]> {
    return Promise.all(written.map(async ({ id, kind }) => ({ id, kind, stamp: await readPlatformStamp({ id, kind }) })));
}

export async function currentPlatformStamps(records: ReadonlyMap<string, WrittenRecord>): Promise<Map<string, string | undefined>> {
    const stamped = [...records].filter(([, record]) => isStampedKind(record.kind) && record.stamp !== undefined);
    const current = await Promise.all(stamped.map(async ([id, record]) => [id, await readPlatformStamp({ id, kind: record.kind as string })] as const));
    return new Map(current);
}
