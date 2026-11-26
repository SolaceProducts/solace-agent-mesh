// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { type AutoFixDropReason, type AutoFixEvent, type AutoFixState, autoFixTransition, type HeldAutoFix, heldAutoFixDropReason, initialAutoFixState, MAX_AUTO_FIX_ATTEMPTS } from "./autoFixMachine";

const fix = (key: string, sessionId = "s1"): HeldAutoFix => ({ message: `m-${key}`, displayChip: "chip", key, sessionId, errorText: `err-${key}` });
const state = (over: Partial<Omit<AutoFixState, "spent">> & { spent?: string[] } = {}): AutoFixState => ({ ...initialAutoFixState, ...over, spent: new Set(over.spent ?? []) });

const IDLE = state();
const HELD_A = state({ held: fix("A"), submitted: true });
const SENT_A = state({ submitted: true, attempts: 1, spent: ["A"] });
const EXHAUSTED = state({ exhausted: true, attempts: MAX_AUTO_FIX_ATTEMPTS, spent: ["A", "B"] });
const HELD_B_AFTER_A = state({ held: fix("B"), submitted: true, attempts: 1, spent: ["A"] });

const standing = { isCancelling: false, sessionId: "s1", standingFailures: ["err-A", "err-B", "err-C"] };

interface Expected {
    held: string | null;
    submitted: boolean;
    exhausted: boolean;
    attempts: number;
    spent: string[];
    send?: string;
    dropped?: string;
    accepted?: boolean;
}

const row = (name: string, from: AutoFixState, event: AutoFixEvent, want: Expected) => ({ name, from, event, want });

const TABLE = [
    row("report, idle, Builder idle: sent", IDLE, { type: "report", fix: fix("A"), responding: false }, { held: null, submitted: true, exhausted: false, attempts: 1, spent: ["A"], send: "A", accepted: true }),
    row("report, idle, Builder responding: held, nothing spent", IDLE, { type: "report", fix: fix("A"), responding: true }, { held: "A", submitted: true, exhausted: false, attempts: 0, spent: [], accepted: true }),
    row("report, held A, same failure: still the one hold", HELD_A, { type: "report", fix: fix("A"), responding: true }, { held: "A", submitted: true, exhausted: false, attempts: 0, spent: [], accepted: true }),
    row("report, held A, different failure while idle: held, never sent around the hold", HELD_A, { type: "report", fix: fix("B"), responding: false }, { held: "B", submitted: true, exhausted: false, attempts: 0, spent: [], accepted: true }),
    row("report, held A, different failure while responding: latest wins", HELD_A, { type: "report", fix: fix("B"), responding: true }, { held: "B", submitted: true, exhausted: false, attempts: 0, spent: [], accepted: true }),
    row("report, sent A, same failure: refused, nothing sent twice", SENT_A, { type: "report", fix: fix("A"), responding: false }, { held: null, submitted: true, exhausted: false, attempts: 1, spent: ["A"], accepted: false }),
    row("report, sent A, a new failure: sent, budget now 2", SENT_A, { type: "report", fix: fix("B"), responding: false }, { held: null, submitted: true, exhausted: false, attempts: 2, spent: ["A", "B"], send: "B", accepted: true }),
    row("report, sent A, a new failure while responding: held", SENT_A, { type: "report", fix: fix("B"), responding: true }, { held: "B", submitted: true, exhausted: false, attempts: 1, spent: ["A"], accepted: true }),
    row(
        "report, budget spent: refused and the card gives up",
        state({ submitted: true, attempts: 2, spent: ["A", "B"] }),
        { type: "report", fix: fix("C"), responding: false },
        { held: null, submitted: false, exhausted: true, attempts: 2, spent: ["A", "B"], accepted: false }
    ),
    row(
        "report, budget spent and the failure was sent before: still gives up",
        state({ submitted: true, attempts: 2, spent: ["A", "B"] }),
        { type: "report", fix: fix("A"), responding: false },
        { held: null, submitted: false, exhausted: true, attempts: 2, spent: ["A", "B"], accepted: false }
    ),
    row(
        "report, exhausted, a new failure: held and the card says fixing again",
        state({ exhausted: true, attempts: 1, spent: ["A"] }),
        { type: "report", fix: fix("B"), responding: true },
        { held: "B", submitted: true, exhausted: false, attempts: 1, spent: ["A"], accepted: true }
    ),

    row("turn ends, held A, its failure stands: sent, budget spent", HELD_A, { type: "turnEnded", turn: standing }, { held: null, submitted: true, exhausted: false, attempts: 1, spent: ["A"], send: "A" }),
    row("turn ends, held A, Stop pending: dropped", HELD_A, { type: "turnEnded", turn: { ...standing, isCancelling: true } }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [], dropped: "stopped" }),
    row("turn ends, held A, session changed: dropped", HELD_A, { type: "turnEnded", turn: { ...standing, sessionId: "s2" } }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [], dropped: "session changed" }),
    row(
        "turn ends, held A, nothing stands: dropped, budget untouched",
        HELD_A,
        { type: "turnEnded", turn: { ...standing, standingFailures: [] } },
        { held: null, submitted: false, exhausted: false, attempts: 0, spent: [], dropped: "failure no longer stands" }
    ),
    row(
        "turn ends, held A, only a DIFFERENT failure stands: dropped",
        HELD_A,
        { type: "turnEnded", turn: { ...standing, standingFailures: ["err-B"] } },
        { held: null, submitted: false, exhausted: false, attempts: 0, spent: [], dropped: "failure no longer stands" }
    ),
    row("turn ends, held B after A sent, its failure stands: second fix sent, budget 2", HELD_B_AFTER_A, { type: "turnEnded", turn: standing }, { held: null, submitted: true, exhausted: false, attempts: 2, spent: ["A", "B"], send: "B" }),
    row("turn ends, nothing held: nothing happens", SENT_A, { type: "turnEnded", turn: standing }, { held: null, submitted: true, exhausted: false, attempts: 1, spent: ["A"] }),

    row("Stop, held A: dropped", HELD_A, { type: "cancelled" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [], dropped: "stopped" }),
    row("Stop, sent A: nothing to discard", SENT_A, { type: "cancelled" }, { held: null, submitted: true, exhausted: false, attempts: 1, spent: ["A"] }),
    row("Stop, idle", IDLE, { type: "cancelled" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [] }),

    row("save starts, held A: dropped", HELD_A, { type: "saveStarted" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [], dropped: "a save started" }),
    row("save starts, sent A: the sent fix stands", SENT_A, { type: "saveStarted" }, { held: null, submitted: true, exhausted: false, attempts: 1, spent: ["A"] }),

    row("Try again, held A (deploy-only branch too): everything resets", HELD_A, { type: "retry" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [], dropped: "retry" }),
    row("Try again, sent A: budget and keys reset", SENT_A, { type: "retry" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [] }),
    row("Try again, exhausted: reset", EXHAUSTED, { type: "retry" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [] }),

    row("content changed, sent A: the card stops saying fixing, budget stays", SENT_A, { type: "contentChanged" }, { held: null, submitted: false, exhausted: false, attempts: 1, spent: ["A"] }),
    row("content changed, held A: the hold goes with its failure", HELD_A, { type: "contentChanged" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [], dropped: "content changed" }),
    row("content changed, idle: nothing happens", IDLE, { type: "contentChanged" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [] }),

    row("refusal fixed, sent A: the Builder is still on it, nothing refilled", SENT_A, { type: "refusalFixed" }, { held: null, submitted: true, exhausted: false, attempts: 1, spent: ["A"], accepted: false }),
    row("refusal fixed, held A: the hold stands, nothing refilled", HELD_A, { type: "refusalFixed" }, { held: "A", submitted: true, exhausted: false, attempts: 0, spent: [], accepted: false }),
    row("refusal fixed, held B after A sent: the hold stands, budget untouched", HELD_B_AFTER_A, { type: "refusalFixed" }, { held: "B", submitted: true, exhausted: false, attempts: 1, spent: ["A"], accepted: false }),
    row("refusal fixed, budget spent and the card gave up: refilled, spent kept", EXHAUSTED, { type: "refusalFixed" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: ["A", "B"], accepted: true }),
    row("refusal fixed, A sent and the Builder answered: refilled, spent kept", state({ attempts: 1, spent: ["A"] }), { type: "refusalFixed" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: ["A"], accepted: true }),
    row("refusal fixed, idle: nothing to refill", IDLE, { type: "refusalFixed" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [], accepted: true }),

    row("watchdog, sent A: gives up", SENT_A, { type: "watchdog" }, { held: null, submitted: false, exhausted: true, attempts: 1, spent: ["A"] }),
    row("hold limit, held A: dropped and gives up", HELD_A, { type: "watchdog" }, { held: null, submitted: false, exhausted: true, attempts: 0, spent: [], dropped: "hold limit" }),
    row("watchdog, idle: nothing to give up on", IDLE, { type: "watchdog" }, { held: null, submitted: false, exhausted: false, attempts: 0, spent: [] }),
];

describe("autoFixTransition: every entry point against every state", () => {
    test.each(TABLE.map(r => [r.name, r] as const))("%s", (_name, r) => {
        const out = autoFixTransition(r.from, r.event);

        expect({
            held: out.state.held?.key ?? null,
            submitted: out.state.submitted,
            exhausted: out.state.exhausted,
            attempts: out.state.attempts,
            spent: [...out.state.spent].sort(),
            send: out.send?.key,
            dropped: out.dropped,
            accepted: out.accepted,
        }).toEqual({ ...r.want });
    });

    test("a no-op returns the very state it was given, so nothing re-renders", () => {
        for (const [from, event] of [
            [IDLE, { type: "contentChanged" }],
            [IDLE, { type: "watchdog" }],
            [IDLE, { type: "cancelled" }],
            [IDLE, { type: "saveStarted" }],
            [IDLE, { type: "refusalFixed" }],
            [SENT_A, { type: "refusalFixed" }],
            [HELD_A, { type: "refusalFixed" }],
            [SENT_A, { type: "turnEnded", turn: standing }],
            [SENT_A, { type: "report", fix: fix("A"), responding: false }],
        ] as const) {
            const out = autoFixTransition(from, event);
            if (event.type !== "report") expect(out.state).toBe(from);
        }
    });

    test("a transition never changes the state it was given", () => {
        const from = state({ held: fix("A"), submitted: true, attempts: 1, spent: ["Z"] });
        const snapshot = JSON.stringify({ ...from, spent: [...from.spent] });

        for (const event of TABLE.map(r => r.event)) autoFixTransition(from, event);

        expect(JSON.stringify({ ...from, spent: [...from.spent] })).toBe(snapshot);
    });
});

describe("autoFixTransition: the platform check and a save failure share one budget", () => {
    const platform = fix("platform-check|K1");
    const writeFailure = fix("Save agent|kept|boom");

    test("a refusal fixed refills the budget but never forgets what was sent, so a save failure is sent once", () => {
        let at = autoFixTransition(IDLE, { type: "report", fix: platform, responding: false });
        expect(at.send?.key).toBe(platform.key);
        at = autoFixTransition(at.state, { type: "contentChanged" });
        at = autoFixTransition(at.state, { type: "refusalFixed" });
        expect(at.state.attempts).toBe(0);

        at = autoFixTransition(at.state, { type: "report", fix: writeFailure, responding: false });
        expect(at.send?.key).toBe(writeFailure.key);

        at = autoFixTransition(at.state, { type: "contentChanged" });
        at = autoFixTransition(at.state, { type: "refusalFixed" });
        const again = autoFixTransition(at.state, { type: "report", fix: writeFailure, responding: false });

        expect(again.send).toBeUndefined();
        expect(again.accepted).toBe(false);
        expect(again.state.spent.has(writeFailure.key)).toBe(true);
    });

    test("the check passes, the save fails at the write, the Builder re-validates and the check passes again: the same failure is not sent again", () => {
        let at = autoFixTransition(IDLE, { type: "report", fix: writeFailure, responding: false });
        expect(at.send?.key).toBe(writeFailure.key);
        at = autoFixTransition(at.state, { type: "contentChanged" });

        const again = autoFixTransition(at.state, { type: "report", fix: writeFailure, responding: false });

        expect(again.send).toBeUndefined();
        expect(again.accepted).toBe(false);
    });

    test("a refusal that is refused three times over can be asked again once it is fixed", () => {
        let at = autoFixTransition(IDLE, { type: "report", fix: fix("P1"), responding: false });
        at = autoFixTransition(at.state, { type: "contentChanged" });
        at = autoFixTransition(at.state, { type: "report", fix: fix("P2"), responding: false });
        at = autoFixTransition(at.state, { type: "contentChanged" });
        const refused = autoFixTransition(at.state, { type: "report", fix: fix("P3"), responding: false });
        expect(refused.accepted).toBe(false);
        expect(refused.state.exhausted).toBe(true);

        const refilled = autoFixTransition(refused.state, { type: "refusalFixed" });

        expect(refilled.state).toMatchObject({ attempts: 0, exhausted: false });
        expect([...refilled.state.spent].sort()).toEqual(["P1", "P2"]);
    });
});

describe("heldAutoFixDropReason: the order of the reasons", () => {
    const held = fix("A");
    const now = standing;
    test.each([
        ["nothing changed", now, null],
        ["stopped before anything else", { ...now, isCancelling: true, sessionId: "s2", standingFailures: [] }, "stopped"],
        ["another session before a failure that no longer stands", { ...now, sessionId: "s2", standingFailures: [] }, "session changed"],
        ["a failure that no longer stands", { ...now, standingFailures: [] }, "failure no longer stands"],
        ["a different failure standing is not this one", { ...now, standingFailures: ["err-other"] }, "failure no longer stands"],
    ])("%s", (_label, input, want) => {
        expect(heldAutoFixDropReason(held, input)).toBe(want);
    });
});

describe("autoFixTransition: every short sequence of events", () => {
    const DEPTH = 5;
    const EVENTS_BY_TYPE: Record<AutoFixEvent["type"], readonly AutoFixEvent[]> = {
        report: [
            { type: "report", fix: fix("A"), responding: false },
            { type: "report", fix: fix("A"), responding: true },
            { type: "report", fix: fix("B"), responding: false },
            { type: "report", fix: fix("B"), responding: true },
        ],
        turnEnded: [
            { type: "turnEnded", turn: { isCancelling: false, sessionId: "s1", standingFailures: ["err-A", "err-B"] } },
            { type: "turnEnded", turn: { isCancelling: false, sessionId: "s1", standingFailures: ["err-B"] } },
            { type: "turnEnded", turn: { isCancelling: true, sessionId: "s1", standingFailures: ["err-A", "err-B"] } },
            { type: "turnEnded", turn: { isCancelling: false, sessionId: "s2", standingFailures: ["err-A", "err-B"] } },
        ],
        cancelled: [{ type: "cancelled" }],
        saveStarted: [{ type: "saveStarted" }],
        retry: [{ type: "retry" }],
        contentChanged: [{ type: "contentChanged" }],
        watchdog: [{ type: "watchdog" }],
        refusalFixed: [{ type: "refusalFixed" }],
    };
    const ALPHABET = Object.values(EVENTS_BY_TYPE).flat();

    interface Sends {
        sinceRetry: readonly string[];
        sinceRefill: readonly string[];
    }
    const NO_SENDS: Sends = { sinceRetry: [], sinceRefill: [] };

    function violations(before: AutoFixState, event: AutoFixEvent, sendsBefore: Sends): { problems: string[]; sends: Sends; sent: boolean; next: AutoFixState; dropped: AutoFixDropReason | undefined } {
        const out = autoFixTransition(before, event);
        const after = out.state;
        const problems: string[] = [];
        const check = (ok: boolean, what: string) => {
            if (!ok) problems.push(what);
        };
        const refilled = event.type === "retry" || (event.type === "refusalFixed" && after !== before);
        const sinceRetry = event.type === "retry" ? [] : [...sendsBefore.sinceRetry];
        const sinceRefill = refilled ? [] : [...sendsBefore.sinceRefill];
        if (out.send) {
            sinceRetry.push(out.send.key);
            sinceRefill.push(out.send.key);
        }
        const sends: Sends = { sinceRetry, sinceRefill };

        check(new Set(sinceRetry).size === sinceRetry.length, "the same failure was sent twice before Try again");
        check(sinceRefill.length <= MAX_AUTO_FIX_ATTEMPTS, "more sends than the budget");
        check(after.attempts === sinceRefill.length && after.spent.size === sinceRetry.length, "the budget does not match the sends");
        check(after.held === null || after.submitted, "a request is held but the card does not say fixing");
        check(!(after.submitted && after.exhausted), "the card says fixing and offers Try again at once");
        check(after.held === null || after.attempts < MAX_AUTO_FIX_ATTEMPTS, "a request is held with no budget left");
        if (out.send) {
            check(after.held === null && after.submitted && !after.exhausted, "a send did not leave the sent state");
            const fromReport = event.type === "report" && !event.responding && before.held === null;
            const fromTurnEnd =
                event.type === "turnEnded" &&
                before.held !== null &&
                !event.turn.isCancelling &&
                event.turn.sessionId === before.held.sessionId &&
                event.turn.standingFailures.includes(before.held.errorText) &&
                before.attempts < MAX_AUTO_FIX_ATTEMPTS;
            check(fromReport || fromTurnEnd, "a request was sent when it should not have been");
            check(event.type !== "report" || before.attempts < MAX_AUTO_FIX_ATTEMPTS, "a request was sent with no budget");
        }
        if (event.type === "turnEnded" && before.held === null) check(after === before && !out.send, "a turn ending with nothing held changed something");
        if (event.type === "retry") check(after === initialAutoFixState, "Try again did not reset everything");
        if (event.type === "refusalFixed") {
            check(!out.send && !out.dropped, "a refusal fixed did something besides refill");
            check(out.accepted === (before.held === null && !before.submitted), "a refusal fixed reported the wrong outcome");
            check(after.spent === before.spent && after.held === before.held, "a refusal fixed changed what was sent or held");
            if (before.held !== null || before.submitted) check(after === before, "a refusal fixed changed a fix in flight");
            else check(after.attempts === 0 && !after.exhausted, "a refusal fixed did not refill the budget");
        }
        if (["cancelled", "saveStarted", "watchdog", "contentChanged"].includes(event.type)) check(after.held === null, `${event.type} left a request held`);
        if (event.type === "report" && out.accepted === false) check(!out.send && after.held === before.held && after.attempts === before.attempts, "a refused report changed something");
        if (event.type === "report" && out.accepted === true) check(after.submitted && !after.exhausted, "an accepted report did not show fixing");
        if (before.exhausted && !before.held && event.type !== "report" && event.type !== "retry") check(!out.send, "an exhausted card sent a request without a new failure");
        check(before.spent.size >= before.attempts, "the input state was inconsistent or changed");
        return { problems, sends, sent: !!out.send, next: after, dropped: out.dropped };
    }

    test("no sequence breaks an invariant, and the alphabet reaches every kind of state", () => {
        const broken: string[] = [];
        const reached = { held: 0, sentFromReport: 0, sentFromTurnEnd: 0, exhausted: 0, sequences: 0 };
        const droppedFor: Record<AutoFixDropReason, number> = {
            stopped: 0,
            "session changed": 0,
            "failure no longer stands": 0,
            "a save started": 0,
            retry: 0,
            "content changed": 0,
            "hold limit": 0,
        };

        const walk = (before: AutoFixState, sendsSoFar: Sends, trail: string[]) => {
            if (trail.length === DEPTH) return;
            for (const event of ALPHABET) {
                const step = violations(before, event, sendsSoFar);
                const next = step.next;
                const here = [...trail, event.type + (event.type === "report" ? `:${event.fix.key}:${event.responding}` : "")];
                reached.sequences++;
                if (next.held) reached.held++;
                if (step.sent) reached[event.type === "report" ? "sentFromReport" : "sentFromTurnEnd"]++;
                if (next.exhausted) reached.exhausted++;
                if (step.dropped) droppedFor[step.dropped]++;
                if (step.problems.length > 0 && broken.length < 5) broken.push(`${here.join(" > ")}: ${step.problems.join("; ")}`);
                walk(next, step.sends, here);
            }
        };
        walk(initialAutoFixState, NO_SENDS, []);

        expect(broken).toEqual([]);
        expect(reached.sequences).toBe(ALPHABET.length ** 1 + ALPHABET.length ** 2 + ALPHABET.length ** 3 + ALPHABET.length ** 4 + ALPHABET.length ** 5);
        expect(reached.held).toBeGreaterThan(0);
        expect(reached.sentFromReport).toBeGreaterThan(0);
        expect(reached.sentFromTurnEnd).toBeGreaterThan(0);
        expect(reached.exhausted).toBeGreaterThan(0);
        expect(Object.entries(droppedFor).filter(([, count]) => count === 0)).toEqual([]);
    });
});
