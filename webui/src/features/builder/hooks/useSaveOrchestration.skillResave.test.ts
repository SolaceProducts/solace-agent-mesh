// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { BuildOperationResult } from "@/api/builder/applyTypes";
import type { SavedSkill, SkillSaveAction } from "@/api/skills";

import type { PlannedOverwrite } from "../serverApply";

import type { BuildManifest } from "./useBuilderManifest";
import { type OverwriteGuard, runPerKindSaves, runServerApply, type SaveResult, type SaveRun, saveSkillComponent, type SkillSaveContext } from "./useSaveOrchestration";

const rememberedIds = (remember: { mock: { calls: unknown[][] } }): string[] => remember.mock.calls.flatMap(([written]) => (written as Array<{ id: string }>).map(w => w.id));

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type = "skill"): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session" }) as Comp;
const manifest = (components: Comp[]): BuildManifest => ({ name: "build", components }) as BuildManifest;

const SKILL_YAML = `name: haiku-style
description: Writes haiku in the classic 5-7-5 form.
instruction_content: |
  Write 5-7-5.
`;

function makeGuard(written: string[] = [], confirm: OverwriteGuard["confirm"] | null = vi.fn().mockResolvedValue(undefined)) {
    const remember = vi.fn().mockResolvedValue(undefined);
    const guard: OverwriteGuard = { owns: ({ id }) => written.includes(id), wrote: id => written.includes(id), remember, ...(confirm ? { confirm } : {}) };
    return { guard, remember, confirm: confirm as ReturnType<typeof vi.fn> };
}

describe("saveSkillComponent — who may replace an existing skill", () => {
    type Request = { config: { name: string }; deferReplace?: boolean; confirmReplace?: (existing: { id: string; name: string }) => Promise<void> };
    const saveWith = (response: SavedSkill) => {
        const mutateAsync = vi.fn(async (_request: Request) => response);
        return { mutation: { mutateAsync } as never, requestOf: () => mutateAsync.mock.calls[0][0] };
    };
    const updated: SavedSkill = { id: "skill-9", name: "haiku-style", action: "updated" };
    const existing = { id: "skill-9", name: "haiku-style" };
    const fetchArtifact = vi.fn();

    test("with no guard there is nothing to ask, so a differing existing skill is a conflict", async () => {
        const { mutation, requestOf } = saveWith(updated);

        await saveSkillComponent(SKILL_YAML, "haiku-style", mutation, fetchArtifact);

        expect(requestOf().confirmReplace).toBeUndefined();
        expect(requestOf().deferReplace).toBeUndefined();
    });

    test("one the Builder wrote is replaced without asking", async () => {
        const { mutation, requestOf } = saveWith(updated);
        const { guard, confirm } = makeGuard(["skill-9"]);

        await saveSkillComponent(SKILL_YAML, "haiku-style", mutation, fetchArtifact, { guard });

        await expect(requestOf().confirmReplace!(existing)).resolves.toBeUndefined();
        expect(confirm).not.toHaveBeenCalled();
    });

    test("one it did not write is asked about, naming the skill and what is replaced", async () => {
        const { mutation, requestOf } = saveWith(updated);
        const { guard, confirm } = makeGuard(["skill-other"]);

        await saveSkillComponent(SKILL_YAML, "haiku-style", mutation, fetchArtifact, { guard });

        await expect(requestOf().confirmReplace!(existing)).resolves.toBeUndefined();
        expect(confirm).toHaveBeenCalledWith([{ name: "haiku-style", kind: "skill", changedFields: ["bundle"] } satisfies PlannedOverwrite]);
    });

    test("a declined question rejects, so the save is cancelled rather than replaced", async () => {
        const { mutation, requestOf } = saveWith(updated);
        const { guard } = makeGuard([], vi.fn().mockRejectedValue(new Error("User cancelled")));

        await saveSkillComponent(SKILL_YAML, "haiku-style", mutation, fetchArtifact, { guard });

        await expect(requestOf().confirmReplace!(existing)).rejects.toThrow("User cancelled");
    });

    test("a guard with no question wired (the user opted out) replaces", async () => {
        const { mutation, requestOf } = saveWith(updated);

        await saveSkillComponent(SKILL_YAML, "haiku-style", mutation, fetchArtifact, { guard: makeGuard([], null).guard });

        await expect(requestOf().confirmReplace!(existing)).resolves.toBeUndefined();
    });

    test("asks the save to defer the replacement when told to", async () => {
        const { mutation, requestOf } = saveWith(updated);

        await saveSkillComponent(SKILL_YAML, "haiku-style", mutation, fetchArtifact, { guard: makeGuard().guard, deferReplace: true });

        expect(requestOf().deferReplace).toBe(true);
        expect(requestOf().confirmReplace).toBeUndefined();
    });
});

describe("saveSkillComponent — what it reports", () => {
    const fetchArtifact = vi.fn();

    test.each(["created", "updated", "unchanged"] as SkillSaveAction[])("carries the %s action and the id of the skill", async action => {
        const mutation = { mutateAsync: vi.fn().mockResolvedValue({ id: "skill-9", name: "haiku-style", action }) } as never;

        const result = await saveSkillComponent(SKILL_YAML, "haiku-style", mutation, fetchArtifact);

        expect(result).toEqual({ name: "haiku-style", type: "skill", id: "skill-9", action });
    });

    test("carries a deferred replacement as its upload, with no action yet", async () => {
        const commit = vi.fn();
        const mutation = { mutateAsync: vi.fn().mockResolvedValue({ id: "skill-9", name: "haiku-style", action: "replace_deferred", commit }) } as never;

        const result = await saveSkillComponent(SKILL_YAML, "haiku-style", mutation, fetchArtifact, { deferReplace: true });

        expect(result).toEqual({ name: "haiku-style", type: "skill", id: "skill-9", commit });
    });

    test.each([
        ["a config with no name and description", "foo: bar", /missing required name\/description/],
        ["a response with no id", SKILL_YAML, /No ID returned/],
    ])("rejects %s", async (_label, yamlText, message) => {
        const mutation = { mutateAsync: vi.fn().mockResolvedValue({}) } as never;

        await expect(saveSkillComponent(yamlText, "haiku-style", mutation, fetchArtifact)).rejects.toThrow(message);
    });
});

const summary = { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 };
const agentOp = (extra: Partial<BuildOperationResult> = {}): BuildOperationResult => ({ kind: "agent", name: "haiku_bot", action: "update", status: "succeeded", resourceId: "agent-1", ...extra }) as BuildOperationResult;

function makeRun(components: Comp[], saveOne: SaveRun["saveOne"]) {
    const run: SaveRun = {
        changed: [],
        declined: [],
        notWritten: [],
        ordered: components,
        results: new Map(),
        skip: () => false,
        attribute: () => {},
        fetchConfigByFilename: vi.fn().mockResolvedValue(SKILL_YAML),
        saveOne,
    };
    return run;
}

type SaveOneArgs = [string, Comp, string[] | undefined, SkillSaveContext | undefined];
const skillSaves = (result: (comp: Comp) => SaveResult | Promise<SaveResult>) => vi.fn(async (...[, c]: SaveOneArgs): Promise<SaveResult | null> => (c.type === "skill" ? result(c) : null));
const savedAs = (action: SkillSaveAction | undefined, id = "skill-9") => skillSaves(c => ({ name: c.name, type: "skill", id, action }));
const deferred = (commit: () => Promise<void> = async () => {}, id = "skill-9") => skillSaves(c => ({ name: c.name, type: "skill", id, commit }));

describe("runServerApply — skills that already exist", () => {
    const applyAgentUpdated = () => vi.fn().mockResolvedValue({ operations: [agentOp()], summary });

    test.each([
        ["created", true, 1],
        ["unchanged", false, 0],
        [undefined, false, 1],
    ] as [SkillSaveAction | undefined, boolean, number][])("a skill saved as %s: remembered as the Builder's is %s, counted as written %s", async (action, remembered, counted) => {
        const components = [comp("haiku-style")];
        const { guard, remember } = makeGuard();

        const run = makeRun(components, savedAs(action));

        const outcome = await runServerApply(run, manifest(components), vi.fn().mockResolvedValue({ operations: [], summary }), guard);

        expect(run.results.get("haiku-style")).toEqual({ name: "haiku-style", type: "skill", id: "skill-9", action });
        expect(rememberedIds(remember)).toEqual(remembered ? ["skill-9"] : []);
        expect(outcome.savedOutsideTheBuild).toBe(counted);
        expect(outcome.updatedInPlace).toEqual([]);
        expect(outcome.partialFailures).toBeUndefined();
    });

    test("the save is handed the context for a deferred replacement, and the skill's id binds the agent that follows", async () => {
        const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
        const saveOne = deferred();
        const run = makeRun(components, saveOne);
        const { guard } = makeGuard();

        await runServerApply(run, manifest(components), applyAgentUpdated(), guard);

        expect(saveOne).toHaveBeenCalledTimes(1);
        expect(saveOne.mock.calls[0][3]).toEqual({ guard, deferReplace: true });
        expect(run.results.get("haiku-style")).toEqual({ name: "haiku-style", type: "skill", id: "skill-9", action: "updated" });
    });

    describe("a replacement waits for the build's plan", () => {
        const log: string[] = [];
        const plan = (ops: BuildOperationResult[] = [agentOp({ status: "planned" })]) =>
            vi.fn(async (vars: { dryRun?: boolean }) => {
                log.push(vars.dryRun ? "dry-run" : "apply");
                return { operations: vars.dryRun ? ops : [agentOp()], summary };
            });
        const commitLogged = () =>
            vi.fn(async () => {
                log.push("commit");
            });

        test("a foreign skill is asked about together with the build's overwrites, then replaced after the plan and before the apply", async () => {
            log.length = 0;
            const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
            const commit = commitLogged();
            const { guard, confirm, remember } = makeGuard([], vi.fn(async () => void log.push("confirm")) as never);

            const outcome = await runServerApply(makeRun(components, deferred(commit)), manifest(components), plan(), guard);

            expect(confirm).toHaveBeenCalledTimes(1);
            expect(log).toEqual(["dry-run", "confirm", "dry-run", "commit", "apply"]);
            expect(rememberedIds(remember)).toContain("skill-9");
            expect(outcome.updatedInPlace).toEqual(["haiku-style", "haiku_bot"]);
            expect(outcome.savedOutsideTheBuild).toBe(1);
        });

        test("one confirmation lists the skill and the agent", async () => {
            const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
            const { guard, confirm } = makeGuard();

            await runServerApply(makeRun(components, deferred()), manifest(components), plan(), guard);

            expect(confirm).toHaveBeenCalledTimes(1);
            expect(confirm.mock.calls[0][0]).toEqual([
                { name: "haiku-style", kind: "skill", changedFields: ["bundle"] },
                { name: "haiku_bot", kind: "agent", changedFields: [] },
            ]);
        });

        test("a skill the Builder wrote is replaced without being asked about", async () => {
            log.length = 0;
            const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
            const commit = commitLogged();
            const { guard, confirm } = makeGuard(["skill-9", "agent-1"]);

            await runServerApply(makeRun(components, deferred(commit)), manifest(components), plan(), guard);

            expect(confirm).not.toHaveBeenCalled();
            expect(log).toEqual(["dry-run", "commit", "apply"]);
        });

        test("declining the one confirmation replaces nothing and applies nothing", async () => {
            log.length = 0;
            const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
            const commit = commitLogged();
            const { guard, remember } = makeGuard([], vi.fn().mockRejectedValue(new Error("User cancelled")));

            await expect(runServerApply(makeRun(components, deferred(commit)), manifest(components), plan(), guard)).rejects.toThrow("User cancelled");

            expect(log).toEqual(["dry-run"]);
            expect(commit).not.toHaveBeenCalled();
            expect(remember).not.toHaveBeenCalled();
        });

        test("a plan the server rejects replaces nothing", async () => {
            log.length = 0;
            const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
            const commit = commitLogged();
            const rejected = [agentOp({ status: "failed", action: "update", error: { message: "bad", errorId: "e", httpStatus: 422 } })];

            await expect(runServerApply(makeRun(components, deferred(commit)), manifest(components), plan(rejected), makeGuard().guard)).rejects.toThrow("bad");

            expect(log).toEqual(["dry-run"]);
            expect(commit).not.toHaveBeenCalled();
        });

        test("with the confirmation opted out there is no plan to wait for: replaced, then applied", async () => {
            log.length = 0;
            const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
            const commit = commitLogged();

            await runServerApply(makeRun(components, deferred(commit)), manifest(components), plan(), makeGuard([], null).guard);

            expect(log).toEqual(["commit", "apply"]);
        });

        test("with no guard at all the skill is replaced, as the other kinds are", async () => {
            log.length = 0;
            const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
            const commit = commitLogged();

            await runServerApply(makeRun(components, deferred(commit)), manifest(components), plan(), undefined);

            expect(log).toEqual(["commit", "apply"]);
        });

        test("an upload that fails is one component, not the build, and is not remembered as written", async () => {
            const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
            const failure = Object.assign(new Error("Could not update skill"), { status: 403 });
            const { guard, remember } = makeGuard();

            const outcome = await runServerApply(makeRun(components, deferred(vi.fn().mockRejectedValue(failure))), manifest(components), plan(), guard);

            expect(outcome.partialFailures).toEqual([{ name: "haiku-style", error: failure }]);
            expect(rememberedIds(remember)).not.toContain("skill-9");
            expect(outcome.updatedInPlace).toEqual(["haiku_bot"]);
        });
    });

    describe("a build with nothing but skills", () => {
        const noBuild = () => vi.fn();

        test("asks once about the foreign skills, then replaces them", async () => {
            const components = [comp("haiku-style")];
            const commit = vi.fn().mockResolvedValue(undefined);
            const { guard, confirm } = makeGuard();

            const outcome = await runServerApply(makeRun(components, deferred(commit)), manifest(components), noBuild(), guard);

            expect(confirm).toHaveBeenCalledWith([{ name: "haiku-style", kind: "skill", changedFields: ["bundle"] }]);
            expect(commit).toHaveBeenCalledTimes(1);
            expect(outcome.updatedInPlace).toEqual(["haiku-style"]);
            expect(outcome.savedOutsideTheBuild).toBe(1);
        });

        test("does not ask about a skill the Builder wrote", async () => {
            const components = [comp("haiku-style")];
            const commit = vi.fn().mockResolvedValue(undefined);
            const { guard, confirm } = makeGuard(["skill-9"]);

            await runServerApply(makeRun(components, deferred(commit)), manifest(components), noBuild(), guard);

            expect(confirm).not.toHaveBeenCalled();
            expect(commit).toHaveBeenCalledTimes(1);
        });

        test("replaces nothing when the user declines", async () => {
            const components = [comp("haiku-style")];
            const commit = vi.fn();
            const { guard } = makeGuard([], vi.fn().mockRejectedValue(new Error("User cancelled")));

            await expect(runServerApply(makeRun(components, deferred(commit)), manifest(components), noBuild(), guard)).rejects.toThrow("User cancelled");

            expect(commit).not.toHaveBeenCalled();
        });
    });

    test("any other failure of the skill save is still one component, not the build", async () => {
        const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
        const failure = Object.assign(new Error("boom"), { status: 500 });
        const apply = applyAgentUpdated();

        const outcome = await runServerApply(makeRun(components, vi.fn().mockRejectedValue(failure)), manifest(components), apply);

        expect(apply).toHaveBeenCalledTimes(1);
        expect(outcome.partialFailures).toEqual([{ name: "haiku-style", error: failure }]);
    });
});

describe("runPerKindSaves — the path with builder_server_apply off", () => {
    test("asks inline: the skill gets the guard and no deferral", async () => {
        const components = [comp("haiku-style")];
        const saveOne = savedAs("updated");
        const { guard } = makeGuard();

        await runPerKindSaves(makeRun(components, saveOne), components, new Map(), guard);

        expect(saveOne.mock.calls[0][3]).toEqual({ guard });
    });

    test.each([
        ["created", 1, [], true],
        ["updated", 1, ["haiku-style"], true],
        ["unchanged", 0, [], false],
    ] as [SkillSaveAction, number, string[], boolean][])("a skill saved as %s is counted %s, reported as updated %j, remembered as the Builder's %s", async (action, written, updatedInPlace, remembered) => {
        const components = [comp("haiku-style")];
        const run = makeRun(components, savedAs(action));
        const { guard, remember } = makeGuard();

        const outcome = await runPerKindSaves(run, components, new Map(), guard);

        expect(outcome.writtenCount).toBe(written);
        expect(outcome.updatedInPlace).toEqual(updatedInPlace);
        expect(rememberedIds(remember)).toEqual(remembered ? ["skill-9"] : []);
        expect(run.results.get("haiku-style")?.id).toBe("skill-9");
    });

    test("a skill replaced inline is named when a later component then fails", async () => {
        const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
        const failure = Object.assign(new Error("agent create failed"), { status: 500 });
        const run = makeRun(
            components,
            vi.fn(async (_yaml: string, c: Comp) => {
                if (c.type === "agent") throw failure;
                return { name: c.name, type: "skill", id: "skill-9", action: "updated" } as SaveResult;
            })
        );

        await expect(runPerKindSaves(run, components, new Map(), makeGuard().guard)).rejects.toMatchObject({
            status: 500,
            message: expect.stringMatching(/agent create failed[\s\S]*Already updated before the failure: haiku-style\./),
        });
    });

    test("a decline after a skill was replaced is still a plain cancellation", async () => {
        const components = [comp("haiku-style"), comp("haiku_bot", "agent")];
        const run = makeRun(
            components,
            vi.fn(async (_yaml: string, c: Comp) => {
                if (c.type === "agent") throw new Error("User cancelled");
                return { name: c.name, type: "skill", id: "skill-9", action: "updated" } as SaveResult;
            })
        );

        await expect(runPerKindSaves(run, components, new Map(), makeGuard().guard)).rejects.toThrow(/^User cancelled$/);
    });

    test("a declined replacement stops the save, rather than being counted as saved", async () => {
        const components = [comp("haiku-style")];
        const run = makeRun(components, vi.fn().mockRejectedValue(new Error("User cancelled")));

        await expect(runPerKindSaves(run, components, new Map(), makeGuard().guard)).rejects.toThrow("User cancelled");

        expect(run.results.size).toBe(0);
    });

    test("a conflict the skill save could not resolve stops the save loudly", async () => {
        const components = [comp("haiku-style")];
        const conflict = Object.assign(new Error('A skill named "haiku-style" already exists on the platform with different contents.'), { status: 409 });
        const run = makeRun(components, vi.fn().mockRejectedValue(conflict));

        await expect(runPerKindSaves(run, components, new Map())).rejects.toBe(conflict);
    });
});
