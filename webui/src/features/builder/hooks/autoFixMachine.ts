// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const MAX_AUTO_FIX_ATTEMPTS = 2;

export interface HeldAutoFix {
    readonly message: string;
    readonly displayChip: string;
    readonly key: string;
    readonly sessionId: string;
    readonly errorText: string;
}

export interface AutoFixState {
    readonly held: HeldAutoFix | null;
    readonly submitted: boolean;
    readonly exhausted: boolean;
    readonly attempts: number;
    readonly spent: ReadonlySet<string>;
}

export const initialAutoFixState: AutoFixState = { held: null, submitted: false, exhausted: false, attempts: 0, spent: new Set() };

export interface TurnEnd {
    isCancelling: boolean;
    sessionId: string;
    standingFailures: readonly string[];
}

export type AutoFixEvent =
    { type: "report"; fix: HeldAutoFix; responding: boolean } | { type: "turnEnded"; turn: TurnEnd } | { type: "cancelled" } | { type: "retry" } | { type: "saveStarted" } | { type: "contentChanged" } | { type: "refusalFixed" } | { type: "watchdog" };

export type AutoFixDropReason = "stopped" | "session changed" | "failure no longer stands" | "a save started" | "retry" | "content changed" | "hold limit";

export interface AutoFixOutcome {
    state: AutoFixState;
    send?: HeldAutoFix;
    dropped?: AutoFixDropReason;
    accepted?: boolean;
}

export function heldAutoFixDropReason(held: HeldAutoFix, now: TurnEnd): AutoFixDropReason | null {
    if (now.isCancelling) return "stopped";
    if (held.sessionId !== now.sessionId) return "session changed";
    if (!now.standingFailures.includes(held.errorText)) return "failure no longer stands";
    return null;
}

function sent(state: AutoFixState, fix: HeldAutoFix): AutoFixState {
    return { held: null, submitted: true, exhausted: false, attempts: state.attempts + 1, spent: new Set(state.spent).add(fix.key) };
}

function dropHeld(state: AutoFixState, dropped: AutoFixDropReason): AutoFixOutcome {
    return state.held ? { state: { ...state, held: null, submitted: false }, dropped } : { state };
}

function onReport(state: AutoFixState, event: Extract<AutoFixEvent, { type: "report" }>): AutoFixOutcome {
    if (state.attempts >= MAX_AUTO_FIX_ATTEMPTS) return { state: { ...state, held: null, submitted: false, exhausted: true }, accepted: false };
    if (state.spent.has(event.fix.key)) return { state, accepted: false };
    if (event.responding || state.held) return { state: { ...state, held: event.fix, submitted: true, exhausted: false }, accepted: true };
    return { state: sent(state, event.fix), send: event.fix, accepted: true };
}

function onTurnEnded(state: AutoFixState, turn: TurnEnd): AutoFixOutcome {
    if (!state.held) return { state };
    const reason = heldAutoFixDropReason(state.held, turn);
    if (reason) return dropHeld(state, reason);
    return { state: sent(state, state.held), send: state.held };
}

function onContentChanged(state: AutoFixState): AutoFixOutcome {
    if (state.held) return dropHeld(state, "content changed");
    return state.submitted ? { state: { ...state, submitted: false } } : { state };
}

function onRefusalFixed(state: AutoFixState): AutoFixOutcome {
    if (state.held || state.submitted) return { state, accepted: false };
    if (state.attempts === 0 && !state.exhausted) return { state, accepted: true };
    return { state: { ...state, attempts: 0, exhausted: false }, accepted: true };
}

function onWatchdog(state: AutoFixState): AutoFixOutcome {
    if (!state.submitted && !state.held) return { state };
    return { state: { ...state, held: null, submitted: false, exhausted: true }, dropped: state.held ? "hold limit" : undefined };
}

export function autoFixTransition(state: AutoFixState, event: AutoFixEvent): AutoFixOutcome {
    switch (event.type) {
        case "report":
            return onReport(state, event);
        case "turnEnded":
            return onTurnEnded(state, event.turn);
        case "cancelled":
            return dropHeld(state, "stopped");
        case "saveStarted":
            return dropHeld(state, "a save started");
        case "retry":
            return { state: initialAutoFixState, dropped: state.held ? "retry" : undefined };
        case "contentChanged":
            return onContentChanged(state);
        case "refusalFixed":
            return onRefusalFixed(state);
        case "watchdog":
            return onWatchdog(state);
    }
}
