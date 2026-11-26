// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type DeployFreshness = "clean" | "stale" | "unknown";

export type SendDecision = "send" | "skip";

export type WriteDecision = "write" | "confirm";

export function decideSend(component: { type?: string; freshness: DeployFreshness }): SendDecision {
    return component.type === "connector" && component.freshness === "clean" ? "skip" : "send";
}

export function decideWrite(owned: boolean): WriteDecision {
    return owned ? "write" : "confirm";
}

export interface ReleaseState {
    versions: string;
    turnsEnded: number;
    hasSkill: boolean;
}

function byCodeUnit(a: string, b: string): number {
    if (a === b) return 0;
    return a < b ? -1 : 1;
}

export function entriesSignature(entries: readonly string[]): string {
    return JSON.stringify([...entries].sort(byCodeUnit));
}

function entriesOf(signature: string): string[] {
    return JSON.parse(signature) as string[];
}

export function latchReleased(prev: ReleaseState, next: ReleaseState, readBySave: ReadonlySet<string>): boolean {
    const seen = new Set(entriesOf(prev.versions));
    const newEdit = entriesOf(next.versions).some(entry => !seen.has(entry) && !readBySave.has(entry));
    const turnEnded = next.hasSkill && next.turnsEnded !== prev.turnsEnded;
    return newEdit || turnEnded;
}

export function turnsEndedAfter(ended: number, wasResponding: boolean, isResponding: boolean): number {
    return ended + (wasResponding && !isResponding ? 1 : 0);
}

export const SAVE_CANCELLED_MESSAGE = "Save cancelled. Try again to review and save.";
export const SESSION_LEFT_MESSAGE = "The Builder moved to another session";

export function declineKeyOf(entries: readonly string[]): string {
    return entriesSignature(entries);
}

export function declineAfterStart(declinedAt: string | null, automatic: boolean | undefined): string | null {
    return automatic ? declinedAt : null;
}

export type CancellationOutcome = { kind: "left"; declinedAt: string | null; banner: null } | { kind: "declined"; declinedAt: string; banner: string };

export function cancellationOutcome(err: unknown, declinedAt: string | null, readKey: string): CancellationOutcome {
    const leftSession = err instanceof Error && err.message === SESSION_LEFT_MESSAGE;
    if (leftSession) return { kind: "left", declinedAt, banner: null };
    return { kind: "declined", declinedAt: readKey, banner: SAVE_CANCELLED_MESSAGE };
}

export function declinedLeavesButton(declined: boolean, buildActivateOnScreen: boolean): boolean {
    return declined && buildActivateOnScreen;
}

export function retryAction(allSaved: boolean, saveIncomplete: boolean, credentialAwaitingSave = false): "save" | "deploy" {
    return !allSaved || saveIncomplete || credentialAwaitingSave ? "save" : "deploy";
}
