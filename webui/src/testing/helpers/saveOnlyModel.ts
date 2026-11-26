// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ownsResource } from "@/features/builder/builderOwnership";
import { shouldAutoSaveOnly } from "@/features/builder/builderUtils";
import { saveOnlyEditState, withSavedVersions } from "@/features/builder/componentDirtyState";
import { cancellationOutcome, decideWrite, declineAfterStart, entriesSignature, latchReleased, type ReleaseState, retryAction, turnsEndedAfter } from "@/features/builder/saveOnlyPolicy";

export type Kind = "connector" | "skill";
export type Build = Kind | "both";

export const MODEL_EVENTS = ["turnStart", "turnEnd", "editC", "editS", "editCS", "listCatchUp", "otherC", "otherS", "cycle", "reload", "switch", "retry", "yes", "no", "breakWrite"] as const;
export type ModelEvent = (typeof MODEL_EVENTS)[number];

type Banner = "none" | "cancelled" | "failed";

interface Part {
    kind: Kind;
    name: string;
    filename: string;
    ver: number;
    listVer: number;
    lastRead?: number;
    manifestRecord?: number;
    overlay?: number;
    saved: boolean;
    platform: { content: number; stamp: number };
    ownedStamp: number | null;
    othersWroteSinceMine: boolean;
    lastSent: number;
}

interface Pending {
    readAt: Record<string, number>;
    sent: string[];
    answered: string[];
    asked: string[];
}

export interface Model {
    asks: boolean;
    parts: Part[];
    incomplete: boolean;
    banner: Banner;
    latch: boolean;
    agentResponded: boolean;
    responding: boolean;
    turnsEnded: number;
    declinedAt: string | null;
    declinedSent: string[];
    release: ReleaseState;
    readEntries: string[];
    pending: Pending | null;
    failNext: boolean;
    failed: boolean;
    acted: number;
    touched: string[];
    writes: number;
    violations: string[];
}

const entryAt = (p: Part, version: number) => `${p.name}@${version}`;
const hasKind = (m: Model, kind: Kind) => m.parts.some(p => p.kind === kind);
const partOf = (m: Model, name: string): Part => m.parts.find(p => p.name === name) as Part;
const allSaved = (m: Model) => m.parts.every(p => p.saved);
const changed = (m: Model, name: string, change: Partial<Part>): Model => ({ ...m, parts: m.parts.map(p => (p.name === name ? { ...p, ...change } : p)) });

const listedVersions = (m: Model) => entriesSignature(m.parts.map(p => entryAt(p, p.listVer)));

const keyOfList = (m: Model) => entriesSignature(m.parts.map(p => entryAt(p, Math.max(p.listVer, p.lastRead ?? 0))));

const keyOfRead = (m: Model, readAt: Record<string, number>) => entriesSignature(m.parts.map(p => entryAt(p, readAt[p.name])));

const keyOfTruth = (m: Model) => entriesSignature(m.parts.map(p => entryAt(p, p.ver)));

function editsWith(m: Model, versionOf: (p: Part) => number) {
    const components = m.parts.map(p => ({ name: p.name, type: p.kind, origin: "session", filename: p.filename, deployed_version: p.manifestRecord }));
    const overlay = new Map(m.parts.flatMap(p => (p.overlay === undefined ? [] : [[p.filename, p.overlay] as const])));
    return saveOnlyEditState(withSavedVersions(components, overlay), new Map(m.parts.map(p => [p.filename, versionOf(p)])), true);
}

const ownsPart = (p: Part) => p.ownedStamp !== null && ownsResource({ kind: p.kind, stamp: String(p.ownedStamp) }, p.kind, String(p.platform.stamp));

function hold(m: Model, name: string, version: number): Model {
    return changed(m, name, { overlay: version, manifestRecord: version, saved: true, lastSent: version });
}

function writePart(m: Model, name: string, state: Pending): Model {
    const p = partOf(m, name);
    const version = state.readAt[name];
    if (p.platform.content === version) return hold(m, name, version);
    if (m.failNext) return { ...m, failNext: false, failed: true };
    const stamp = p.platform.stamp + 1;
    const unasked = m.asks && p.othersWroteSinceMine && !state.answered.includes(name);
    const violations = unasked ? [...m.violations, `I1: replaced somebody else's ${p.kind} unasked`] : m.violations;
    const written = changed({ ...m, violations, writes: m.writes + 1, touched: [...m.touched, name] }, name, { platform: { content: version, stamp }, ownedStamp: stamp, othersWroteSinceMine: false });
    return hold(written, name, version);
}

function performWrites(m: Model, state: Pending): Model {
    const skillsFirst = [...state.sent].sort((a, b) => Number(partOf(m, b).kind === "skill") - Number(partOf(m, a).kind === "skill"));
    const written = skillsFirst.reduce<Model>((model, name) => writePart(model, name, state), { ...m, pending: null });
    return written.failed ? { ...written, incomplete: true, banner: "failed" } : written;
}

function askedAbout(m: Model, state: Pending): string[] {
    if (!m.asks) return [];
    return state.sent.filter(name => {
        const p = partOf(m, name);
        return p.platform.content !== state.readAt[name] && decideWrite(ownsPart(p)) === "confirm" && !state.answered.includes(name);
    });
}

function settleWrites(m: Model, state: Pending): Model {
    const asked = askedAbout(m, state);
    if (asked.length === 0) return performWrites(m, state);
    const owned = asked.filter(name => partOf(m, name).ownedStamp === partOf(m, name).platform.stamp);
    const violations = owned.length > 0 ? [...m.violations, "I7: asked about a copy the session owns"] : m.violations;
    return { ...m, violations, touched: [...m.touched, ...asked], pending: { ...state, asked } };
}

function save(m: Model, automatic: boolean): Model {
    if (m.pending) return m;
    const readAt = Object.fromEntries(m.parts.map(p => [p.name, p.ver]));
    const read: Model = {
        ...m,
        declinedAt: declineAfterStart(m.declinedAt, automatic),
        incomplete: false,
        failed: false,
        banner: "none",
        parts: m.parts.map(p => ({ ...p, lastRead: p.ver })),
        readEntries: m.parts.map(p => entryAt(p, p.ver)),
    };
    const leftOut = editsWith(read, p => p.ver).leftOut;
    const sent = read.parts.filter(p => !leftOut.includes(p.name)).map(p => p.name);
    const acting = sent.some(name => partOf(read, name).platform.content !== readAt[name]);
    return settleWrites({ ...read, acted: read.acted + (acting ? 1 : 0) }, { readAt, sent, answered: [], asked: [] });
}

function settle(m: Model, depth = 0): Model {
    const edits = editsWith(m, p => p.listVer);
    const now: ReleaseState = { versions: listedVersions(m), turnsEnded: m.turnsEnded, hasSkill: edits.hasSkill };
    const saving = m.pending !== null;
    const next: Model = saving ? m : { ...m, latch: latchReleased(m.release, now, new Set(m.readEntries)) ? false : m.latch, release: now };
    const fires = shouldAutoSaveOnly({
        saveOnlyBuild: true,
        readyToSave: true,
        agentResponded: m.agentResponded,
        allSaved: allSaved(m),
        isSaving: saving,
        isResponding: m.responding,
        hasUnsavedEdits: edits.hasUnsavedEdits,
        allMatchLastSave: edits.allMatchLastSave,
        hasSkill: edits.hasSkill,
        declined: m.declinedAt === keyOfList(m),
        saveOnlyTriggered: next.latch,
        autoSaveTriggered: false,
    });
    if (!fires || depth > 4) return next;
    return settle(save({ ...next, latch: true }, true), depth + 1);
}

function mounted(m: Model): Model {
    const parts = m.parts.map(p => ({ ...p, listVer: p.ver, lastRead: undefined, overlay: undefined, saved: false }));
    const fresh: Model = { ...m, parts, incomplete: false, banner: "none", latch: false, agentResponded: false, responding: false, turnsEnded: 0, declinedAt: null, declinedSent: [], pending: null, readEntries: [] };
    return { ...fresh, release: { versions: listedVersions(fresh), turnsEnded: 0, hasSkill: editsWith(fresh, p => p.listVer).hasSkill } };
}

function switched(m: Model): Model {
    const parts = m.parts.map(p => ({ ...p, overlay: undefined, ownedStamp: null, othersWroteSinceMine: true }));
    return { ...m, parts, declinedAt: null, declinedSent: [], latch: false, agentResponded: m.responding, incomplete: m.pending ? false : m.incomplete, banner: m.banner === "cancelled" ? "none" : m.banner, pending: null };
}

const edited =
    (...kinds: Kind[]) =>
    (m: Model): Model | null =>
        kinds.every(kind => hasKind(m, kind)) ? settle({ ...m, parts: m.parts.map(p => (kinds.includes(p.kind) ? { ...p, ver: p.ver + 1, listVer: m.responding ? p.listVer : p.ver + 1 } : p)) }) : null;

const replacedBy =
    (kind: Kind) =>
    (m: Model): Model | null => {
        if (!hasKind(m, kind)) return null;
        const name = m.parts.find(p => p.kind === kind)?.name as string;
        const p = partOf(m, name);
        return settle({ ...changed(m, name, { platform: { content: -1, stamp: p.platform.stamp + 1 }, othersWroteSinceMine: true }) });
    };

function retried(m: Model): Model | null {
    if (m.pending || m.banner === "none") return null;
    return settle(retryAction(allSaved(m), m.incomplete) === "save" ? save(m, false) : m);
}

function declined(m: Model, pending: Pending): Model {
    const outcome = cancellationOutcome(new Error("User cancelled"), m.declinedAt, keyOfRead(m, pending.readAt));
    const asked = outcome.kind === "declined";
    return { ...m, pending: null, declinedAt: outcome.declinedAt, declinedSent: pending.sent, incomplete: asked, banner: asked ? "cancelled" : m.banner };
}

const TRANSITIONS: Record<ModelEvent, (m: Model) => Model | null> = {
    turnStart: m => (m.responding ? null : settle({ ...m, responding: true, agentResponded: true })),
    turnEnd: m => (m.responding ? settle({ ...m, responding: false, turnsEnded: turnsEndedAfter(m.turnsEnded, true, false) }) : null),
    editC: edited("connector"),
    editS: edited("skill"),
    editCS: edited("connector", "skill"),
    listCatchUp: m => (m.parts.every(p => p.listVer === p.ver) ? null : settle({ ...m, parts: m.parts.map(p => ({ ...p, listVer: p.ver })) })),
    otherC: replacedBy("connector"),
    otherS: replacedBy("skill"),
    cycle: m => settle({ ...m, latch: false, banner: "none" }),
    reload: m => settle(mounted(m)),
    switch: m => settle(switched(m)),
    retry: retried,
    yes: m => (m.pending ? settle(settleWrites({ ...m, pending: null }, { ...m.pending, answered: [...m.pending.answered, ...m.pending.asked], asked: [] })) : null),
    no: m => (m.pending ? settle(declined(m, m.pending)) : null),
    breakWrite: m => (m.failNext ? null : { ...m, failNext: true }),
};

export function step(m: Model, event: ModelEvent): Model | null {
    return TRANSITIONS[event]({ ...m, acted: 0, touched: [] });
}

export function eventsOf(build: Build): ModelEvent[] {
    const lacks: Record<Build, ModelEvent[]> = { connector: ["editS", "editCS", "otherS"], skill: ["editC", "editCS", "otherC"], both: [] };
    return MODEL_EVENTS.filter(event => !lacks[build].includes(event));
}

export function start(build: Build, asks: boolean, recordKept: boolean, owned: boolean): Model {
    const part = (kind: Kind): Part => ({
        kind,
        name: kind === "skill" ? "s" : "c",
        filename: kind === "skill" ? "s.yaml" : "c.yaml",
        ver: 1,
        listVer: 1,
        manifestRecord: recordKept ? 1 : undefined,
        saved: false,
        platform: { content: 1, stamp: 1 },
        ownedStamp: owned ? 1 : null,
        othersWroteSinceMine: !owned,
        lastSent: 1,
    });
    const kinds: Kind[] = build === "both" ? ["skill", "connector"] : [build];
    const initial: Model = {
        asks,
        parts: kinds.map(part),
        incomplete: false,
        banner: "none",
        latch: false,
        agentResponded: false,
        responding: false,
        turnsEnded: 0,
        declinedAt: null,
        declinedSent: [],
        release: { versions: entriesSignature([]), turnsEnded: 0, hasSkill: false },
        readEntries: [],
        pending: null,
        failNext: false,
        failed: false,
        acted: 0,
        touched: [],
        writes: 0,
        violations: [],
    };
    return mounted(initial);
}

export function observe(m: Model): { writes: number; asking: boolean; retryOffered: boolean } {
    return { writes: m.writes, asking: m.pending !== null, retryOffered: m.banner !== "none" };
}

const effectiveRecord = (p: Part) => p.overlay ?? p.manifestRecord;

type Invariant = (before: Model, event: ModelEvent, after: Model) => string | null;

const isEdit = (event: ModelEvent) => event.startsWith("edit");

const I2: Invariant = (_before, event, after) => {
    const listShowsIt = after.parts.every(p => p.listVer === p.ver) && !after.responding && after.agentResponded;
    const settled = !after.pending && !after.failed && after.declinedAt !== keyOfList(after);
    const unsaved = after.parts.filter(p => p.lastSent !== p.ver).map(p => p.name);
    if (!["turnEnd", "listCatchUp"].includes(event) || !listShowsIt || !settled || unsaved.length === 0) return null;
    return `I2: an edit to ${unsaved.join(", ")} was not saved once its turn ended and the list showed it`;
};

const I3: Invariant = (before, event, after) => {
    const declineStands = before.declinedAt !== null && before.declinedAt === keyOfTruth(before) && !before.pending;
    if (!declineStands || after.pending === null || ["retry", "reload", "switch"].includes(event) || isEdit(event)) return null;
    return "I3: asked again after a decline, with nothing changed and no retry";
};

const I4: Invariant = (before, event, after) => {
    const hadSomethingToRetry = !allSaved(before) || before.incomplete;
    const unsaved = after.parts.some(p => p.lastSent !== p.ver);
    if (event !== "retry" || !hadSomethingToRetry || after.pending || after.failed || !unsaved) return null;
    return "I4: Try again left an edit unsaved";
};

const I5: Invariant = (_before, _event, after) => (after.acted > 1 ? `I5: ${after.acted} saves that wrote or asked, started by one event` : null);

const I6: Invariant = (before, event, after) => {
    const provenClean = new Set(before.parts.filter(p => p.kind === "connector" && effectiveRecord(p) === p.ver).map(p => p.name));
    const sent = after.touched.filter(name => provenClean.has(name));
    if (!["turnStart", "turnEnd", "cycle", "otherC", "otherS", "retry"].includes(event) || sent.length === 0) return null;
    return `I6: a connector the record says the platform holds was sent after ${event}`;
};

const I8: Invariant = (before, event, after) => (event === "listCatchUp" && before.parts.every(p => p.lastRead === p.ver) && after.acted > 0 ? "I8: the list catching up with versions a save had read started a save" : null);

const I9: Invariant = (_before, _event, after) => {
    const held = after.declinedAt !== null && after.declinedAt === keyOfTruth(after);
    const masked = after.parts.filter(p => p.lastSent !== p.ver && !after.declinedSent.includes(p.name)).map(p => p.name);
    return held && masked.length > 0 ? `I9: the decline holds an edit to ${masked.join(", ")} that the declined save did not send` : null;
};

const INVARIANTS: Invariant[] = [I2, I3, I4, I5, I6, I8, I9];

export function breaches(before: Model, event: ModelEvent, after: Model): string[] {
    const noted = after.violations.filter(v => !before.violations.includes(v));
    return [...noted, ...INVARIANTS.flatMap(invariant => invariant(before, event, after) ?? [])];
}
