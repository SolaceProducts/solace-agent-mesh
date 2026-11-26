// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { BuildManifest } from "./useBuilderManifest";
import { runServerApply, type SaveRun } from "./useSaveOrchestration";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session" }) as Comp;
const manifest = (components: Comp[]): BuildManifest => ({ name: "build", components }) as BuildManifest;

function makeRun(components: Comp[], saveOne: SaveRun["saveOne"]) {
    const attributed: (Comp | null)[] = [];
    const run: SaveRun = {
        changed: [],
        declined: [],
        notWritten: [],
        ordered: components,
        results: new Map(),
        skip: () => false,
        attribute: c => attributed.push(c),
        fetchConfigByFilename: vi.fn().mockResolvedValue("apps: []"),
        saveOne,
    };
    return { run, lastAttributed: () => attributed[attributed.length - 1] };
}

const okApply = (names: string[]) =>
    vi.fn().mockResolvedValue({
        operations: names.map(n => ({ kind: "agent", name: n, action: "create", status: "succeeded", resourceId: `id-${n}` })),
        summary: { created: names.length, updated: 0, unchanged: 0, failed: 0, skipped: 0 },
    });

const throwingSkill = (message: string) =>
    vi.fn(async (_yaml: string, c: Comp) => {
        if (c.type === "skill") throw new Error(message);
        return null;
    }) as unknown as SaveRun["saveOne"];

describe("runServerApply — a skill failure is one component, not the build", () => {
    test("still sends the build request, and records what it wrote", async () => {
        const components = [comp("MySkill", "skill"), comp("MyAgent", "agent")];
        const { run } = makeRun(components, throwingSkill("duplicate skill name"));
        const applyBuild = okApply(["MyAgent"]);

        const outcome = await runServerApply(run, manifest(components), applyBuild);

        expect(applyBuild).toHaveBeenCalledTimes(1);
        expect(applyBuild.mock.calls[0][0].components.map((c: { name: string }) => c.name)).toEqual(["MyAgent"]);
        expect(run.results.get("MyAgent")).toBeTruthy();
        expect(outcome.partialFailures?.map(f => f.name)).toEqual(["MySkill"]);
    });

    test("keeps what the build changed, so it can still be deployed", async () => {
        const components = [comp("MySkill", "skill"), comp("MyAgent", "agent")];
        const { run } = makeRun(components, throwingSkill("boom"));

        const outcome = await runServerApply(run, manifest(components), okApply(["MyAgent"]));

        expect(run.changed).toEqual(["MyAgent"]);
        expect(outcome.changedOnPlatform).toEqual(["MyAgent"]);
        expect(outcome.summary?.created).toBe(1);
    });

    test("keeps going after one bad skill and reports both", async () => {
        const components = [comp("SkillA", "skill"), comp("SkillB", "skill"), comp("MyAgent", "agent")];
        const saveOne = vi.fn(async (_yaml: string, c: Comp) => {
            if (c.name === "SkillA") throw new Error("bundle artifact missing");
            if (c.name === "SkillB") throw new Error("name is reserved");
            return null;
        }) as unknown as SaveRun["saveOne"];
        const { run } = makeRun(components, saveOne);

        const outcome = await runServerApply(run, manifest(components), okApply(["MyAgent"]));

        expect(saveOne).toHaveBeenCalledTimes(2);
        expect(outcome.partialFailures?.map(f => f.name)).toEqual(["SkillA", "SkillB"]);
    });

    test("carries the original error object, preserving its classification", async () => {
        const components = [comp("MySkill", "skill"), comp("MyAgent", "agent")];
        const original = Object.assign(new Error("conflict"), { status: 409, errorId: "abc" });
        const saveOne = vi.fn(async (_yaml: string, c: Comp) => {
            if (c.type === "skill") throw original;
            return null;
        }) as unknown as SaveRun["saveOne"];
        const { run } = makeRun(components, saveOne);

        const outcome = await runServerApply(run, manifest(components), okApply(["MyAgent"]));

        expect(outcome.partialFailures?.[0].error).toBe(original);
        expect((outcome.partialFailures?.[0].error as { status?: number }).status).toBe(409);
    });

    test("attributes the failure to the skill, not to whatever ran last", async () => {
        const components = [comp("MySkill", "skill"), comp("MyAgent", "agent")];
        const { run, lastAttributed } = makeRun(components, throwingSkill("boom"));

        await runServerApply(run, manifest(components), okApply(["MyAgent"]));

        expect(lastAttributed()?.name).toBe("MySkill");
    });

    test("attributes the FIRST failure when several skills fail", async () => {
        const components = [comp("SkillA", "skill"), comp("SkillB", "skill")];
        const saveOne = vi.fn(async () => {
            throw new Error("nope");
        }) as unknown as SaveRun["saveOne"];
        const { run, lastAttributed } = makeRun(components, saveOne);

        await runServerApply(run, manifest(components), okApply([]));

        expect(lastAttributed()?.name).toBe("SkillA");
    });

    test("a skill-only build still reports the failure", async () => {
        const components = [comp("MySkill", "skill")];
        const saveOne = vi.fn(async () => {
            throw new Error("upload rejected");
        }) as unknown as SaveRun["saveOne"];
        const { run } = makeRun(components, saveOne);
        const applyBuild = okApply([]);

        const outcome = await runServerApply(run, manifest(components), applyBuild);

        expect(applyBuild).not.toHaveBeenCalled();
        expect(outcome.partialFailures?.map(f => f.name)).toEqual(["MySkill"]);
    });

    test("a build failure still throws, and lists the skill failure alongside", async () => {
        const components = [comp("MySkill", "skill"), comp("MyAgent", "agent")];
        const { run } = makeRun(components, throwingSkill("skill blew up"));
        const applyBuild = vi.fn().mockResolvedValue({
            operations: [{ kind: "agent", name: "MyAgent", action: "create", status: "failed", error: { message: "agent blew up", errorId: "e1", httpStatus: 422 } }],
            summary: { created: 0, updated: 0, unchanged: 0, failed: 1, skipped: 0 },
        });

        const err = await runServerApply(run, manifest(components), applyBuild).catch((e: Error) => e);

        expect((err as Error).message).toMatch(/agent blew up/);
        expect((err as Error).message).toMatch(/Also failed — MySkill: skill blew up/);
    });

    test("reports nothing extra when every skill saves", async () => {
        const components = [comp("MySkill", "skill"), comp("MyAgent", "agent")];
        const saveOne = vi.fn(async (_yaml: string, c: Comp) => (c.type === "skill" ? { name: c.name, type: "skill", id: "s1" } : null)) as unknown as SaveRun["saveOne"];
        const { run } = makeRun(components, saveOne);

        const outcome = await runServerApply(run, manifest(components), okApply(["MyAgent"]));

        expect(outcome.savedOutsideTheBuild).toBe(1);
        expect(outcome.partialFailures).toBeUndefined();
    });

    test("does not count a failed skill as saved", async () => {
        const components = [comp("MySkill", "skill"), comp("MyAgent", "agent")];
        const { run } = makeRun(components, throwingSkill("boom"));

        const outcome = await runServerApply(run, manifest(components), okApply(["MyAgent"]));

        expect(outcome.savedOutsideTheBuild).toBe(0);
    });
});

describe("runServerApply — a save that declined part of an edit", () => {
    test("records the component as declined whatever the action, and never as matched", async () => {
        const components = [comp("Updated", "agent"), comp("Untouched", "agent"), comp("Clean", "agent")];
        const { run } = makeRun(components, vi.fn() as unknown as SaveRun["saveOne"]);
        const applyBuild = vi.fn().mockResolvedValue({
            operations: [
                { kind: "gateway", name: "Updated", action: "update", status: "succeeded", resourceId: "id-u", changesDeclined: true },
                { kind: "a2a_agent", name: "Untouched", action: "unchanged", status: "succeeded", resourceId: "id-t", changesDeclined: true },
                { kind: "agent", name: "Clean", action: "unchanged", status: "succeeded", resourceId: "id-c" },
            ],
            summary: { created: 0, updated: 1, unchanged: 2, failed: 0, skipped: 0 },
        });

        const outcome = await runServerApply(run, manifest(components), applyBuild);

        expect(run.declined).toEqual(["Updated", "Untouched"]);
        expect(outcome.matchedOnPlatform).toEqual(["Clean"]);
    });
});
