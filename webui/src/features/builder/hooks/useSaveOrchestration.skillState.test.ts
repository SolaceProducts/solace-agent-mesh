// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { BuildOperationResult } from "@/api/builder/applyTypes";

import type { BuildManifest } from "./useBuilderManifest";
import { deployAfterSave, type OverwriteGuard, runPerKindSaves, runServerApply, savedOrPartial, type SaveResult, type SaveRun, shouldSkipSave } from "./useSaveOrchestration";

const rememberedIds = (remember: { mock: { calls: unknown[][] } }): string[] => remember.mock.calls.flatMap(([written]) => (written as Array<{ id: string }>).map(w => w.id));

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type = "skill"): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session" }) as Comp;
const manifest = (components: Comp[]): BuildManifest => ({ name: "build", components }) as BuildManifest;
const summary = { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 };

const AGENT = "haiku_bot";
const agentOp = (status: "planned" | "succeeded" | "failed" = "succeeded"): BuildOperationResult =>
    ({ kind: "agent", name: AGENT, action: "update", status, resourceId: "agent-1", ...(status === "failed" ? { error: { message: "plan rejected", errorId: "e", httpStatus: 422 } } : {}) }) as BuildOperationResult;

type Layout = "skills-only" | "skill+agent" | "two skills+agent";
const LAYOUTS: Record<Layout, { components: Comp[]; skills: string[] }> = {
    "skills-only": { components: [comp("S1")], skills: ["S1"] },
    "skill+agent": { components: [comp("S1"), comp(AGENT, "agent")], skills: ["S1"] },
    "two skills+agent": { components: [comp("S1"), comp("S2"), comp(AGENT, "agent")], skills: ["S1", "S2"] },
};

type Outcome = "accepted" | "declined" | "plan rejected" | "plan throws" | "a commit fails" | "the build request rejects" | "the build returns failed operations" | "a commit fails and the build request rejects";

interface Scenario {
    commits: Map<string, ReturnType<typeof vi.fn>>;
    run: SaveRun;
    guard: OverwriteGuard & { confirm: ReturnType<typeof vi.fn> };
    remember: ReturnType<typeof vi.fn>;
    apply: ReturnType<typeof vi.fn>;
}

function scenario(layout: Layout, outcome: Outcome, results = new Map<string, SaveResult>()): Scenario {
    const { components, skills } = LAYOUTS[layout];
    const failing = outcome === "a commit fails" || outcome === "a commit fails and the build request rejects";
    const commits = new Map(skills.map(name => [name, vi.fn(failing && name === "S1" ? () => Promise.reject(Object.assign(new Error("Could not update skill"), { status: 403 })) : () => Promise.resolve())]));
    const remember = vi.fn().mockResolvedValue(undefined);
    const confirm = vi.fn(() => (outcome === "declined" ? Promise.reject(new Error("User cancelled")) : Promise.resolve()));
    const guard = { owns: () => false, wrote: () => false, remember, confirm } as OverwriteGuard & { confirm: ReturnType<typeof vi.fn> };
    const apply = vi.fn(async (vars: { dryRun?: boolean }) => {
        if (vars.dryRun) {
            if (outcome === "plan throws") throw Object.assign(new Error("plan unavailable"), { status: 503 });
            return { operations: [outcome === "plan rejected" ? agentOp("failed") : agentOp("planned")], summary };
        }
        if (outcome === "the build request rejects" || outcome === "a commit fails and the build request rejects") throw Object.assign(new Error("build request timed out"), { status: 504 });
        return { operations: [outcome === "the build returns failed operations" ? agentOp("failed") : agentOp()], summary };
    });
    const run: SaveRun = {
        changed: [],
        declined: [],
        notWritten: [],
        ordered: components,
        results,
        skip: c =>
            shouldSkipSave(
                c,
                name => results.has(name),
                () => false
            ),
        attribute: () => {},
        fetchConfigByFilename: vi.fn().mockResolvedValue("apps: []"),
        saveOne: async (_yaml, c) => (c.type === "skill" ? { name: c.name, type: "skill", id: `id-${c.name}`, commit: commits.get(c.name)! } : null),
    };
    return { commits, run, guard, remember, apply };
}

const go = (s: Scenario, layout: Layout, withGuard = true) => runServerApply(s.run, manifest(LAYOUTS[layout].components), s.apply as never, withGuard ? s.guard : undefined);
const savedNames = (s: Scenario) => [...s.run.results.keys()].sort((a, b) => a.localeCompare(b));
const commitCalls = (s: Scenario) => Object.fromEntries([...s.commits].map(([name, fn]) => [name, fn.mock.calls.length]));

describe("a deferred skill replacement — what is left after each way a save can end", () => {
    const rows: [Layout, Outcome, RegExp | null, string[], boolean][] = [
        ["skill+agent", "accepted", null, ["S1"], true],
        ["skill+agent", "declined", /^User cancelled$/, [], false],
        ["skill+agent", "plan rejected", /plan rejected/, [], false],
        ["skill+agent", "plan throws", /plan unavailable/, [], false],
        ["skill+agent", "a commit fails", null, [], true],
        ["skill+agent", "the build request rejects", /build request timed out[\s\S]*Already updated before the failure: S1\./, ["S1"], true],
        ["skill+agent", "a commit fails and the build request rejects", /build request timed out[\s\S]*Also failed — S1: Could not update skill/, [], true],
        ["skill+agent", "the build returns failed operations", /plan rejected[\s\S]*Already updated before the failure: S1\./, ["S1"], true],
        ["two skills+agent", "the build returns failed operations", /plan rejected[\s\S]*Already updated before the failure: S1, S2\./, ["S1", "S2"], true],
        ["skills-only", "accepted", null, ["S1"], false],
        ["skills-only", "declined", /^User cancelled$/, [], false],
        ["skills-only", "a commit fails", null, [], false],
        ["two skills+agent", "accepted", null, ["S1", "S2"], true],
        ["two skills+agent", "declined", /^User cancelled$/, [], false],
        ["two skills+agent", "plan rejected", /plan rejected/, [], false],
        ["two skills+agent", "a commit fails", null, ["S2"], true],
        ["two skills+agent", "the build request rejects", /Already updated before the failure: S1, S2\./, ["S1", "S2"], true],
    ];

    test.each(rows)("%s, %s", async (layout, outcome, thrown, inResults, buildSent) => {
        const s = scenario(layout, outcome);

        const settled = await go(s, layout).then(
            value => ({ value }),
            (error: Error) => ({ error })
        );

        if (thrown) {
            expect("error" in settled && settled.error.message).toMatch(thrown);
        } else {
            expect("error" in settled).toBe(false);
        }
        expect(savedNames(s).filter(name => name.startsWith("S"))).toEqual(inResults);
        const written = outcome === "declined" || outcome === "plan rejected" || outcome === "plan throws" ? [] : LAYOUTS[layout].skills;
        expect(commitCalls(s)).toEqual(Object.fromEntries(LAYOUTS[layout].skills.map(name => [name, written.includes(name) ? 1 : 0])));
        expect(s.apply.mock.calls.some(([vars]) => !vars.dryRun)).toBe(buildSent);
        if (outcome === "plan rejected" || outcome === "plan throws") expect(s.guard.confirm).not.toHaveBeenCalled();
        expect(rememberedIds(s.remember).filter((id: string) => id.startsWith("id-"))).toEqual(inResults.map(name => `id-${name}`));
    });

    test.each([
        ["skill+agent", "a commit fails"],
        ["skills-only", "a commit fails"],
        ["two skills+agent", "a commit fails"],
    ] as [Layout, Outcome][])("%s, %s: the failure is reported against the skill, which counts as not written", async (layout, outcome) => {
        const s = scenario(layout, outcome);

        const result = await go(s, layout);

        expect(result.partialFailures).toEqual([{ name: "S1", error: expect.objectContaining({ status: 403 }) }]);
        expect(s.run.notWritten).toEqual(["S1"]);
    });
});

describe("a deferred skill replacement — asking", () => {
    test("two foreign skills are listed in one confirmation, together with the build's overwrite", async () => {
        const s = scenario("two skills+agent", "accepted");

        await go(s, "two skills+agent");

        expect(s.guard.confirm).toHaveBeenCalledTimes(1);
        expect(s.guard.confirm.mock.calls[0][0].map((o: { name: string }) => o.name)).toEqual(["S1", "S2", AGENT]);
    });

    test.each(["no confirmation wired (opted out)", "no guard at all"])("a skills-only build with %s replaces without asking", async label => {
        const s = scenario("two skills+agent", "accepted");
        const skillsOnly = [comp("S1"), comp("S2")];
        const { confirm: _unused, ...rest } = s.guard;
        const guard: OverwriteGuard | undefined = label.startsWith("no guard") ? undefined : rest;

        await runServerApply({ ...s.run, ordered: skillsOnly }, manifest(skillsOnly), s.apply as never, guard);

        expect(commitCalls(s)).toEqual({ S1: 1, S2: 1 });
        expect(s.apply).not.toHaveBeenCalled();
    });

    test("one skill whose upload fails does not stop the others or the build", async () => {
        const s = scenario("two skills+agent", "a commit fails");

        await go(s, "two skills+agent");

        expect(commitCalls(s)).toEqual({ S1: 1, S2: 1 });
        expect(savedNames(s)).toContain("S2");
        expect(s.apply.mock.calls.some(([vars]) => !vars.dryRun)).toBe(true);
    });

    test("a skill that failed to be created does not turn a declined replacement into a failure", async () => {
        const s = scenario("two skills+agent", "declined");
        const saveOne = s.run.saveOne;
        s.run.saveOne = async (yaml, c, ids, skill) => {
            if (c.name === "S2") throw Object.assign(new Error("create failed"), { status: 422 });
            return saveOne(yaml, c, ids, skill);
        };

        await expect(go(s, "two skills+agent")).rejects.toMatchObject({ message: "User cancelled" });
    });
});

describe("a deferred skill replacement — Retry", () => {
    test.each([
        ["declined", "accepted"],
        ["plan rejected", "accepted"],
        ["plan throws", "accepted"],
        ["a commit fails", "accepted"],
    ] as [Outcome, Outcome][])("after %s, the next save asks about and replaces it", async (first, second) => {
        const results = new Map<string, SaveResult>();
        const firstRun = scenario("skill+agent", first, results);
        await go(firstRun, "skill+agent").catch(() => {});
        expect(commitCalls(firstRun).S1).toBe(first === "a commit fails" ? 1 : 0);
        expect(results.has("S1")).toBe(false);

        const retry = scenario("skill+agent", second, results);
        await go(retry, "skill+agent");

        expect(commitCalls(retry)).toEqual({ S1: 1 });
        expect(results.get("S1")).toEqual({ name: "S1", type: "skill", id: "id-S1", action: "updated" });
        if (first !== "a commit fails") expect(retry.guard.confirm.mock.calls[0][0].map((o: { name: string }) => o.name)).toContain("S1");
    });

    test.each([
        ["the build request rejects", true],
        ["the build returns failed operations", true],
        ["a commit fails and the build request rejects", false],
    ] as [Outcome, boolean][])("after %s, Retry saves the skill again and replaces it (it was written before: %s)", async (first, wasWritten) => {
        const results = new Map<string, SaveResult>();
        const firstRun = scenario("skill+agent", first, results);
        await go(firstRun, "skill+agent").catch(() => {});
        expect(results.has("S1")).toBe(wasWritten);

        const retry = scenario("skill+agent", "accepted", results);
        await go(retry, "skill+agent");

        expect(commitCalls(retry)).toEqual({ S1: 1 });
        expect(results.get("S1")).toEqual({ name: "S1", type: "skill", id: "id-S1", action: "updated" });
        expect(retry.apply.mock.calls.some(([vars]) => !vars.dryRun)).toBe(true);
    });

    test("a skills-only build declined once is replaced, and asked about again, on Retry", async () => {
        const results = new Map<string, SaveResult>();
        const firstRun = scenario("skills-only", "declined", results);
        await go(firstRun, "skills-only").catch(() => {});

        const retry = scenario("skills-only", "accepted", results);
        await go(retry, "skills-only");

        expect(retry.guard.confirm).toHaveBeenCalledTimes(1);
        expect(commitCalls(retry)).toEqual({ S1: 1 });
    });

    test("once replaced, the next save looks the skill up again, and the service leaves it alone when nothing changed", async () => {
        const results = new Map<string, SaveResult>();
        await go(scenario("skill+agent", "accepted", results), "skill+agent");

        const next = scenario("skill+agent", "accepted", results);
        next.run.saveOne = async (_yaml, c) => (c.type === "skill" ? { name: c.name, type: "skill", id: `id-${c.name}`, action: "unchanged" } : null);
        const saveOne = vi.spyOn(next.run, "saveOne");
        await go(next, "skill+agent");

        expect(saveOne.mock.calls.filter(([, c]) => c.name === "S1")).toHaveLength(1);
        expect(commitCalls(next)).toEqual({ S1: 0 });
        expect(next.guard.confirm.mock.calls[0]?.[0].map((o: { name: string }) => o.name) ?? []).not.toContain("S1");
        expect(results.get("S1")).toEqual({ name: "S1", type: "skill", id: "id-S1", action: "unchanged" });
    });

    test("once replaced and then edited, the next save replaces it again", async () => {
        const results = new Map<string, SaveResult>();
        await go(scenario("skill+agent", "accepted", results), "skill+agent");

        const next = scenario("skill+agent", "accepted", results);
        await go(next, "skill+agent");

        expect(commitCalls(next)).toEqual({ S1: 1 });
    });
});

describe("a skill whose upload failed — what the rest of the save does about it", () => {
    test("the agents that depend on it are counted as not written, so no deploy acts on them", async () => {
        const dependent = { ...comp(AGENT, "agent"), depends_on: ["S1"] } as Comp;
        const unrelated = comp("other_bot", "agent");
        const components = [comp("S1"), dependent, unrelated];
        const s = scenario("skill+agent", "a commit fails");
        s.run.ordered = components;

        await runServerApply(s.run, manifest(components), s.apply as never, s.guard);

        expect(s.run.notWritten).toEqual(["S1", AGENT]);
    });

    test("a save with an unwritten component is not reported as saved", () => {
        expect(savedOrPartial(true, [AGENT], ["S1"])).toEqual({ kind: "failed", changedOnPlatform: [AGENT], notWritten: ["S1"] });
        expect(savedOrPartial(false, [AGENT], [])).toEqual({ kind: "saved", changedOnPlatform: [AGENT] });
    });

    test("and what follows it deploys only what was written, minus what was not", () => {
        const result = savedOrPartial(true, [AGENT, "other_bot"], [AGENT]);

        expect(deployAfterSave(result)).toEqual({ deploy: true, changedOnPlatform: [AGENT, "other_bot"], options: { onlyChanged: true, notWritten: [AGENT] } });
    });
});

describe("a skill saved earlier in the session, then edited — the next save, on either path", () => {
    const earlier = (): Map<string, SaveResult> =>
        new Map<string, SaveResult>([
            ["S1", { name: "S1", type: "skill", id: "id-S1", action: "created" }],
            [AGENT, { name: AGENT, type: "agent", id: "agent-1" }],
        ]);

    const SAVES: Array<[string, (s: Scenario) => Promise<unknown>]> = [
        ["builder_server_apply on", s => runServerApply(s.run, manifest(LAYOUTS["skill+agent"].components), s.apply as never, s.guard)],
        ["builder_server_apply off", s => runPerKindSaves(s.run, LAYOUTS["skill+agent"].components, new Map(), s.guard)],
    ];

    test.each(SAVES)("%s: saves the skill again, so the edit can reach the platform", async (_label, save) => {
        const s = scenario("skill+agent", "accepted", earlier());
        const saveOne = vi.spyOn(s.run, "saveOne");

        await save(s);

        expect(saveOne.mock.calls.filter(([, c]) => c.type === "skill").map(([, c]) => c.name)).toEqual(["S1"]);
    });

    test("builder_server_apply off: an agent saved earlier is still not saved again, as before", async () => {
        const s = scenario("skill+agent", "accepted", earlier());
        const saveOne = vi.spyOn(s.run, "saveOne");

        await SAVES[1][1](s);

        expect(saveOne.mock.calls.filter(([, c]) => c.type === "agent")).toEqual([]);
    });
});
