// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type RespondedAction = "start" | "cancel" | "stale";

let respondedPlans: ReadonlyMap<string, RespondedAction> = new Map();
const listeners = new Set<() => void>();

export function markPlanResponded(planId: string, action: RespondedAction) {
    const next = new Map(respondedPlans);
    next.set(planId, action);
    respondedPlans = next;
    listeners.forEach(listener => listener());
}

export function subscribeRespondedPlans(listener: () => void) {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

export function getRespondedPlansSnapshot(): ReadonlyMap<string, RespondedAction> {
    return respondedPlans;
}

export function __resetRespondedPlansForTest() {
    respondedPlans = new Map();
    listeners.forEach(listener => listener());
}
