// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";

import { type FreeModelStatus, useFreeModelStatus } from "@/api/activation";

import { reportOnce } from "@/common/product-analytics/report";

import { useFreeModel } from "./useFreeModel";

export const WARN_FROM_PERCENT_USED = 80;

const ALL_USED = 100;

const MAX_PERCENT_USED_WHILE_ALLOWANCE_LEFT = 99;

export type CreditsTone = "ok" | "low" | "out";

export function creditsTone(used: number): CreditsTone {
    if (used >= ALL_USED) return "out";
    if (used >= WARN_FROM_PERCENT_USED) return "low";
    return "ok";
}

export function percentUsed(remainingPercent: number | null): number | null {
    if (remainingPercent === null) return null;
    if (remainingPercent <= 0) return ALL_USED;
    return Math.max(0, Math.min(MAX_PERCENT_USED_WHILE_ALLOWANCE_LEFT, Math.round(ALL_USED - remainingPercent)));
}

export function useReportCreditsTone(tone: CreditsTone | undefined) {
    useEffect(() => {
        if (tone === "low") reportOnce("free_model:credits_low");
        if (tone === "out") reportOnce("free_model:credits_out");
    }, [tone]);
}

export interface FreeCreditsSummary {
    used: number;
    tone: CreditsTone;
    label: string;
}

const SIGN_IN_AGAIN: FreeCreditsSummary = { used: 0, tone: "low", label: "Sign in again" };
const PAUSED: FreeCreditsSummary = { used: 0, tone: "out", label: "The free model is paused" };

export function freeCreditsSummary({ status, remainingPercent }: FreeModelStatus): FreeCreditsSummary | null {
    if (status === "reauthorization_required") return SIGN_IN_AGAIN;
    if (status === "paused") return PAUSED;
    const used = status === "available" ? percentUsed(remainingPercent) : null;
    if (used === null) return null;
    const tone = creditsTone(used);
    if (tone === "out") return { used, tone, label: "No free credits left" };
    return { used, tone, label: `${used}% of free credits used` };
}

export function useFreeCreditsSummary(): FreeCreditsSummary | null {
    const { inUse } = useFreeModel();
    const { data: status } = useFreeModelStatus(inUse);
    const summary = inUse && status ? freeCreditsSummary(status) : null;
    useReportCreditsTone(status?.status === "available" ? summary?.tone : undefined);
    return summary;
}
