// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { type BudgetStatus, budgetStatusKeys, useBudgetStatus } from "@/api/budget-status";

import { useUserContext } from "@/providers/contexts";
import { useSSESubscription } from "@/providers/SSEProvider";

import { MessageBanner } from "@/common/banners/MessageBanner";

import { budgetWarnDismissalKey } from "./budgetWarnDismissalKey";

const resetDateFormat = new Intl.DateTimeFormat("en-US", { dateStyle: "long", timeZone: "UTC" });

function resetClause(periodEnd: string): string {
    const resetAt = Date.parse(periodEnd);
    return Number.isNaN(resetAt) ? "" : ` It resets on ${resetDateFormat.format(resetAt)} (UTC).`;
}

function periodMarker(periodEnd: string): string {
    const endsAt = Date.parse(periodEnd);
    return Number.isNaN(endsAt) ? periodEnd : String(endsAt);
}

function readDismissal(key: string): string | null {
    try {
        return globalThis.localStorage.getItem(key);
    } catch {
        return null;
    }
}

function writeDismissal(key: string, marker: string) {
    try {
        globalThis.localStorage.setItem(key, marker);
    } catch {}
}

function useBudgetStatusEvents() {
    const queryClient = useQueryClient();
    const onStatus = useCallback(
        async (event: MessageEvent) => {
            let pushed: BudgetStatus;
            try {
                pushed = JSON.parse(event.data);
            } catch {
                await queryClient.invalidateQueries({ queryKey: budgetStatusKeys.all });
                return;
            }
            await queryClient.cancelQueries({ queryKey: budgetStatusKeys.all });
            queryClient.setQueryData<BudgetStatus>(budgetStatusKeys.all, pushed);
        },
        [queryClient]
    );
    useSSESubscription({ endpoint: "/api/v1/sse/notifications", eventType: "budget_status", onMessage: onStatus });
}

export function BudgetStatusBanner() {
    useBudgetStatusEvents();
    const { data: status } = useBudgetStatus();
    const { username } = useUserContext();
    const storageKey = username ? budgetWarnDismissalKey(username) : null;
    const [dismissedThisPage, setDismissedThisPage] = useState<string | null>(null);
    const storedDismissal = useMemo(() => (storageKey ? readDismissal(storageKey) : null), [storageKey]);

    if (!status) return null;

    const reset = resetClause(status.periodEnd);

    if (status.meshLimitReached) {
        return <MessageBanner variant="error" message={`This deployment's usage budget is used up, so new requests are paused.${reset}`} />;
    }

    if (status.ownLevel === "limit") {
        return <MessageBanner variant="error" message={`You've used up your usage budget, so new requests are paused.${reset}`} />;
    }

    if (status.ownLevel !== "warn" || !storageKey) return null;

    const warnMarker = `${periodMarker(status.periodEnd)}:${status.capKey ?? ""}:warn`;
    const dismissalMarker = `${storageKey}:${warnMarker}`;
    if (dismissedThisPage === dismissalMarker || storedDismissal === warnMarker) return null;

    const dismiss = () => {
        writeDismissal(storageKey, warnMarker);
        setDismissedThisPage(dismissalMarker);
    };

    return <MessageBanner variant="warning" message={`You're close to your usage budget for this period.${reset}`} dismissible onDismiss={dismiss} />;
}
