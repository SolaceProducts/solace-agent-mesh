// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import {
    cancellationOutcome,
    decideSend,
    decideWrite,
    declineAfterStart,
    declinedLeavesButton,
    declineKeyOf,
    entriesSignature,
    latchReleased,
    type ReleaseState,
    retryAction,
    SAVE_CANCELLED_MESSAGE,
    SESSION_LEFT_MESSAGE,
    turnsEndedAfter,
} from "./saveOnlyPolicy";

describe("decideSend", () => {
    test.each([
        ["a connector proven unchanged since its last save", "connector", "clean", "skip"],
        ["a connector edited since", "connector", "stale", "send"],
        ["a connector with no record of a save", "connector", "unknown", "send"],
        ["a skill, even when its YAML is unchanged: its bundle also packs other artifacts", "skill", "clean", "send"],
        ["a skill edited since", "skill", "stale", "send"],
        ["a skill with no record", "skill", "unknown", "send"],
        ["an agent is none of the save-only kinds", "agent", "clean", "send"],
    ] as const)("%s", (_label, type, freshness, expected) => {
        expect(decideSend({ type, freshness })).toBe(expected);
    });
});

describe("decideWrite", () => {
    test.each([
        ["the platform holds what the Builder wrote", true, "write"],
        ["anything else is asked about", false, "confirm"],
    ] as const)("%s", (_label, owned, expected) => {
        expect(decideWrite(owned)).toBe(expected);
    });
});

describe("latchReleased", () => {
    const at = (versions: string, turnsEnded: number, hasSkill = false): ReleaseState => ({ versions: entriesSignature(versions === "" ? [] : versions.split("|")), turnsEnded, hasSkill });

    test.each([
        ["nothing changed", at("", 0), at("", 0), false],
        ["a first edit", at("", 0), at("db@2", 0), true],
        ["a further edit", at("db@2", 0), at("db@3", 0), true],
        ["the edit recorded as saved: the signature emptying is not an edit", at("db@2", 0), at("", 0), false],
        ["the same edit seen again", at("db@2", 0), at("db@2", 0), false],
        ["a turn ends, no skill: nothing new to save", at("", 0), at("", 1), false],
        ["a turn ends, a skill in the build: it goes to the save", at("", 0, true), at("", 1, true), true],
        ["a turn ends with a skill, after the edit was recorded", at("db@2", 1, true), at("", 2, true), true],
        ["a skill, nothing changed", at("", 3, true), at("", 3, true), false],
        ["the edits shrink to some of what they were: one write was refused, the other landed", at("db@2|TestSkill@2", 1, true), at("TestSkill@2", 1, true), false],
        ["the same edits in another order", at("a@2|b@2", 0), at("b@2|a@2", 0), false],
        ["a further component is edited beside one already seen", at("db@2", 0), at("db@2|TestSkill@3", 0), true],
        ["an edit is made again at a later version, beside one that is gone", at("db@2|TestSkill@2", 0), at("TestSkill@3", 0), true],
    ])("%s", (_label, prev, next, expected) => {
        expect(latchReleased(prev, next, new Set())).toBe(expected);
    });

    test.each([
        ["the list catches up with a version a save read that recorded nothing", at("", 0, true), at("TestSkill@2", 0, true), ["TestSkill@2"], false],
        ["the list catches up with what a save read, and a further edit lands too", at("", 0), at("db@2|TestSkill@3", 0), ["db@2"], true],
        ["an edit the save did not read", at("", 0), at("db@3", 0), ["db@2"], true],
        ["a turn ends, whatever the save read", at("", 0, true), at("TestSkill@2", 1, true), ["TestSkill@2"], true],
    ])("%s", (_label, prev, next, read, expected) => {
        expect(latchReleased(prev, next, new Set(read))).toBe(expected);
    });

    test("a component whose name holds a bar is one entry, so the list catching up with what a save read is no edit", () => {
        const prev: ReleaseState = { versions: entriesSignature([]), turnsEnded: 0, hasSkill: false };
        const next: ReleaseState = { versions: entriesSignature(["a|b@2", "c@1"]), turnsEnded: 0, hasSkill: false };
        expect(latchReleased(prev, next, new Set(["a|b@2", "c@1"]))).toBe(false);
    });
});

describe("turnsEndedAfter", () => {
    test.each([
        ["a turn begins", 2, false, true, 2],
        ["a turn ends", 2, true, false, 3],
        ["still responding", 2, true, true, 2],
        ["still idle", 2, false, false, 2],
    ])("%s", (_label, ended, was, is, expected) => {
        expect(turnsEndedAfter(ended, was, is)).toBe(expected);
    });
});

describe("declineKeyOf", () => {
    test.each([
        ["the same versions", ["db@2"], ["db@2"], true],
        ["the same versions, entered by the list after it caught up", ["db@2", "TestSkill@2"], ["db@2", "TestSkill@2"], true],
        ["another edit", ["db@2"], ["db@3"], false],
        ["the list has not caught up with what the save read", ["db@2"], ["db@1"], false],
        ["a component is added", ["db@2"], ["db@2", "TestSkill@1"], false],
        ["a component is renamed", ["a@2"], ["b@2"], false],
        ["the components in another order: the agent reordered the manifest", ["TestSkill@2", "db@1"], ["db@1", "TestSkill@2"], true],
        ["a name holding a bar is not two components", ["a|b@1", "c@1"], ["a", "b@1|c@1"], false],
        ["names that collate equal in another order: the order is ignored", ["\u00e9@1", "e\u0301@1"], ["e\u0301@1", "\u00e9@1"], true],
    ])("%s", (_label, entries, otherEntries, equal) => {
        expect(declineKeyOf(entries) === declineKeyOf(otherEntries)).toBe(equal);
    });
});

describe("declineAfterStart", () => {
    test.each([
        ["the auto-save keeps the decline", true, "key", "key"],
        ["Build & Activate, Try again and plan approval lift it", false, "key", null],
        ["a save started with no option lifts it", undefined, "key", null],
        ["nothing to lift", false, null, null],
        ["the auto-save with nothing declined", true, null, null],
    ])("%s", (_label, automatic, declinedAt, expected) => {
        expect(declineAfterStart(declinedAt, automatic)).toBe(expected);
    });
});

describe("cancellationOutcome", () => {
    const DECLINED = new Error("User cancelled");
    const LEFT = new Error(SESSION_LEFT_MESSAGE);

    test.each([
        ["the author declined the question", DECLINED, "earlier", { kind: "declined", declinedAt: "key-the-save-read", banner: SAVE_CANCELLED_MESSAGE }],
        ["the question was withdrawn by something that is not an Error", "User cancelled", "earlier", { kind: "declined", declinedAt: "key-the-save-read", banner: SAVE_CANCELLED_MESSAGE }],
        ["the Builder moved to another session: nobody there asked, so the decline stands as it was", LEFT, "earlier", { kind: "left", declinedAt: "earlier", banner: null }],
        ["the Builder moved to another session with nothing declined", LEFT, null, { kind: "left", declinedAt: null, banner: null }],
    ])("%s", (_label, err, declinedAt, expected) => {
        expect(cancellationOutcome(err, declinedAt, "key-the-save-read")).toEqual(expected);
    });
});

describe("declinedLeavesButton", () => {
    test.each([
        ["a decline where Build & Activate is on screen leaves the button and no banner", true, true, true],
        ["a decline where it is not keeps the banner with Try again", true, false, false],
        ["a failure where it is on screen keeps its banner", false, true, false],
        ["a failure where it is not keeps its banner", false, false, false],
    ])("%s", (_label, declined, onScreen, expected) => {
        expect(declinedLeavesButton(declined, onScreen)).toBe(expected);
    });
});

describe("retryAction", () => {
    test.each([
        ["nothing saved yet", false, false, false, "save"],
        ["nothing saved, and the last save did not finish", false, true, false, "save"],
        ["everything saved, but the last save was declined or failed", true, true, false, "save"],
        ["everything saved and the last save finished: only deploy", true, false, false, "deploy"],
        ["everything saved, but a credential may have been given in the chat since", true, false, true, "save"],
    ] as const)("%s", (_label, allSaved, incomplete, credentialAwaitingSave, expected) => {
        expect(retryAction(allSaved, incomplete, credentialAwaitingSave)).toBe(expected);
    });

    test("a credential nobody can have changed is not asked about", () => {
        expect(retryAction(true, false)).toBe("deploy");
    });
});
