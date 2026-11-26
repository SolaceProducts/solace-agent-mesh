// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { QueryClient } from "@tanstack/react-query";

import { activationKeys } from "./keys";
import * as activationService from "./service";
import type { FreeModelStatus } from "./types";

const STORAGE_KEY = "sam-free-credits-used-up-at";

function readMark(): number | null {
    try {
        const stored = localStorage.getItem(STORAGE_KEY);
        if (stored === null) return null;
        const remainingPercent = Number(stored);
        return Number.isFinite(remainingPercent) ? remainingPercent : null;
    } catch {
        return null;
    }
}

function writeMark(remainingPercent: number | null) {
    try {
        if (remainingPercent === null) localStorage.removeItem(STORAGE_KEY);
        else localStorage.setItem(STORAGE_KEY, String(remainingPercent));
    } catch {}
}

const USED_UP = 0;

function asUsedUp(status: FreeModelStatus): FreeModelStatus {
    return { ...status, remainingPercent: USED_UP };
}

export function applyUsedUpMark(status: FreeModelStatus): FreeModelStatus {
    const mark = readMark();
    if (mark === null || status.status !== "available") return status;
    if (status.remainingPercent === null || status.remainingPercent > mark) {
        writeMark(null);
        return status;
    }
    return asUsedUp(status);
}

function markUsedUpAt(queryClient: QueryClient, status: FreeModelStatus | undefined): boolean {
    if (status?.status !== "available" || status.remainingPercent === null) return false;
    writeMark(status.remainingPercent);
    queryClient.setQueryData(activationKeys.freeModel(), asUsedUp(status));
    return true;
}

async function readAllowanceAtRefusal(): Promise<FreeModelStatus | undefined> {
    try {
        return await activationService.getFreeModel();
    } catch {
        return undefined;
    }
}

export async function markFreeCreditsUsedUp(queryClient: QueryClient): Promise<void> {
    if (readMark() !== null) return;
    const lastPolled = queryClient.getQueryData<FreeModelStatus>(activationKeys.freeModel());
    if (!markUsedUpAt(queryClient, lastPolled)) return;
    markUsedUpAt(queryClient, await readAllowanceAtRefusal());
}
