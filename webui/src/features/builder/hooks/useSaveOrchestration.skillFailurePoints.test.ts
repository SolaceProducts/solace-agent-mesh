// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { BuildOperationResult } from "@/api/builder/applyTypes";

import { isAgentFixableError } from "../builderUtils";

import type { BuildManifest } from "./useBuilderManifest";
import { deployCandidates } from "./useManifestDeployment";
import { deployAfterSave, type OverwriteGuard, runPerKindSaves, runServerApply, savedOrPartial, type SaveResult, type SaveRun } from "./useSaveOrchestration";

const rememberedIds = (remember: { mock: { calls: unknown[][] } }): string[] => remember.mock.calls.flatMap(([written]) => (written as Array<{ id: string }>).map(w => w.id));

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string, extra: Record<string, unknown> = {}): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session", ...extra }) as Comp;
const summary = { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 };
const updated = (name: string): BuildOperationResult => ({ kind: "agent", name, action: "update", status: "succeeded", resourceId: `id-${name}` }) as BuildOperationResult;

const agentYaml = (skills: string[]) => `
apps:
  - name: X
    app_config:
      agent_name: X
      description: An agent with a sufficiently long description.
      instruction: |
        ${"x".repeat(120)}
      skills:
${skills.map(name => `        - name: ${name}`).join("\n")}
`;
const SKILL_YAML = `name: S1
description: A skill with a sufficiently long description.
instruction_content: |
  Do the thing.
`;

type Style = "depends_on" | "the manifest's skills list" | "its own config";
const dependent = (style: Style): Comp => {
    if (style === "depends_on") return comp("A", "agent", { depends_on: ["S1"] });
    if (style === "the manifest's skills list") return comp("A", "agent", { skills: ["S1"] });
    return comp("A", "agent");
};
const configFor = (c: Comp, style: Style): string => {
    if (c.type === "skill") return SKILL_YAML;
    return c.name === "A" && style === "its own config" ? agentYaml(["S1"]) : agentYaml(["Other"]);
};

const failure = (message: string, extra: object = {}) => Object.assign(new Error(message), extra);
type Failure = { label: string; message: RegExp; save: () => Promise<SaveResult | null> };
const FAILURES: Failure[] = [
    { label: "the skill lookup answers 500", message: /Could not check whether skill/, save: () => Promise.reject(failure('Could not check whether skill "S1" already exists: boom', { status: 500 })) },
    { label: "the skill lookup answers 401", message: /expired/, save: () => Promise.reject(failure("expired", { status: 401 })) },
    { label: "an artifact the skill names cannot be loaded", message: /could not be loaded from the session/, save: () => Promise.reject(failure('Skill "S1" references artifact "x" which could not be loaded from the session')) },
    { label: "the bundle cannot be assembled", message: /neither inline content nor an artifact/, save: () => Promise.reject(failure('Skill "S1" reference "a.md" has neither inline content nor an artifact.')) },
    { label: "the upload fails", message: /Could not update skill/, save: () => Promise.resolve({ name: "S1", type: "skill", id: "id-S1", commit: () => Promise.reject(failure("Could not update skill", { status: 403 })) } as SaveResult) },
];

function setup(style: Style, saveSkill: Failure["save"] | null) {
    const components = [comp("S1", "skill"), dependent(style), comp("B", "agent", { depends_on: ["Other"] })];
    const results = new Map<string, SaveResult>();
    const remember = vi.fn().mockResolvedValue(undefined);
    const guard: OverwriteGuard = { owns: () => false, wrote: () => false, remember, confirm: vi.fn().mockResolvedValue(undefined) };
    const run: SaveRun = {
        changed: [],
        declined: [],
        notWritten: [],
        ordered: components,
        results,
        skip: () => false,
        attribute: () => {},
        fetchConfigByFilename: vi.fn(async (filename: string) =>
            configFor(
                components.find(c => c.filename === filename)!,
                style
            )
        ),
        saveOne: async (_yaml, c) => (c.type === "skill" ? (saveSkill ? saveSkill() : { name: "S1", type: "skill", id: "id-S1", action: "created" }) : null),
    };
    const apply = vi.fn(async (vars: { dryRun?: boolean }) => ({ operations: [vars.dryRun ? ({ ...updated("A"), status: "planned" } as BuildOperationResult) : updated("A"), updated("B")], summary }));
    return { components, run, guard, remember, apply, results };
}

const buildOf = (components: Comp[]): BuildManifest => ({ name: "build", components }) as BuildManifest;

const SWEEP = "(every component that is not running)";
const deployedAfter = (changed: string[] | undefined, notWritten: string[]): string[] => {
    const next = deployAfterSave(savedOrPartial(true, changed, notWritten));
    if (!next.deploy) return [];
    if (next.changedOnPlatform === undefined) return [SWEEP];
    return (next.changedOnPlatform ?? []).filter(name => !(next.options.notWritten ?? []).includes(name));
};

const STYLES: Style[] = ["depends_on", "the manifest's skills list", "its own config"];

describe("builder_server_apply on — a skill that fails to save", () => {
    const rows = FAILURES.flatMap(f => STYLES.map(style => [f.label, style, f] as const));

    test.each(rows)("when %s, an agent that uses it through %s is not deployed, and one that does not is", async (_label, style, failing) => {
        const s = setup(style, failing.save);

        const outcome = await runServerApply(s.run, buildOf(s.components), s.apply as never, s.guard);

        expect(outcome.partialFailures?.map(f => f.name)).toEqual(["S1"]);
        expect(s.results.has("S1")).toBe(false);
        expect(s.run.notWritten).toEqual(["S1", "A"]);
        expect(outcome.changedOnPlatform).toEqual(["A", "B"]);
        expect(deployedAfter(outcome.changedOnPlatform, s.run.notWritten)).toEqual(["B"]);
        expect(rememberedIds(s.remember)).not.toContain("id-S1");
    });

    test("what uses the skill is looked for in the config as the build has it, not in what an earlier read left behind", async () => {
        const s = setup("its own config", FAILURES[0].save);
        const asBuilt = s.run.fetchConfigByFilename;
        s.run.fetchConfigByFilename = vi.fn(async (filename: string, options) => (options?.skipAccumulated ? asBuilt(filename, options) : agentYaml(["Other"])));

        await runServerApply(s.run, buildOf(s.components), s.apply as never, s.guard);

        expect(s.run.notWritten).toEqual(["S1", "A"]);
    });

    test.each(STYLES)("after the failure, Retry writes the skill and then everything is deployed (%s)", async style => {
        const first = setup(style, FAILURES[0].save);
        await runServerApply(first.run, buildOf(first.components), first.apply as never, first.guard);

        const retry = setup(style, null);
        retry.results.clear();
        const outcome = await runServerApply(retry.run, buildOf(retry.components), retry.apply as never, retry.guard);

        expect(retry.results.get("S1")).toMatchObject({ id: "id-S1", action: "created" });
        expect(retry.run.notWritten).toEqual([]);
        expect(outcome.partialFailures).toBeUndefined();
        expect(deployedAfter(outcome.changedOnPlatform, retry.run.notWritten)).toEqual(["A", "B"]);
    });

    test.each(STYLES)("when the build request then rejects, what uses the failed skill through %s is still withheld", async style => {
        const s = setup(style, FAILURES[0].save);
        s.apply.mockImplementation(async (vars: { dryRun?: boolean }) => {
            if (vars.dryRun) return { operations: [{ ...updated("A"), status: "planned" } as BuildOperationResult], summary };
            throw failure("build request timed out", { status: 504 });
        });

        await expect(runServerApply(s.run, buildOf(s.components), s.apply as never, s.guard)).rejects.toThrow(/build request timed out[\s\S]*Also failed — S1/);

        expect(s.run.notWritten).toEqual(["S1", "A"]);
    });

    const INFRASTRUCTURE: Array<[string, Failure["save"], number | undefined]> = [
        ["the lookup answers 500", FAILURES[0].save, 500],
        ["the lookup answers 401", FAILURES[1].save, 401],
        ["the upload is refused with 403", FAILURES[4].save, 403],
        ["the platform never answers", () => Promise.reject(failure("offline", { code: "ERR_NETWORK" })), undefined],
    ];
    const malformedBundle = () => Promise.reject(failure('Skill "S2" reference "a.md" has neither inline content nor an artifact.'));
    const REJECTED: Array<[string, string, boolean]> = [
        ["a component that uses only the skill that failed for infrastructure", "A", false],
        ["a component that uses only the skill whose bundle is malformed", "C", true],
        ["a component that uses both", "D", false],
        ["a component that uses neither", "B", true],
    ];
    const BUILD: Array<[number, boolean]> = [
        [422, true],
        [400, true],
        [500, false],
    ];

    const cells = INFRASTRUCTURE.flatMap(([skillLabel, save, skillStatus]) =>
        REJECTED.flatMap(([relation, rejected, fixableByRelation]) => BUILD.map(([buildStatus, fixableByBuild]) => [skillLabel, relation, rejected, buildStatus, fixableByRelation && fixableByBuild, skillStatus, save] as const))
    );

    test.each(cells)("when %s, and the build then rejects %s with %s: fixable by the builder is %s", async (_skill, _relation, rejected, buildStatus, fixable, skillStatus, save) => {
        const components = [comp("S1", "skill"), comp("S2", "skill"), comp("A", "agent", { depends_on: ["S1"] }), comp("C", "agent", { depends_on: ["S2"] }), comp("D", "agent", { depends_on: ["S1", "S2"] }), comp("B", "agent")];
        const s = setup("depends_on", null);
        s.run.ordered = components;
        s.run.fetchConfigByFilename = vi.fn(async () => agentYaml([]));
        s.run.saveOne = async (_yaml, c) => (c.name === "S1" ? save() : c.name === "S2" ? malformedBundle() : null);
        s.apply.mockImplementation(async (vars: { dryRun?: boolean }) => ({
            operations: [
                vars.dryRun
                    ? ({ ...updated(rejected), status: "planned" } as BuildOperationResult)
                    : ({ ...updated(rejected), status: "failed", error: { message: 'no skill named "S1" exists on the platform or in this build', errorId: "e", httpStatus: buildStatus } } as BuildOperationResult),
            ],
            summary,
        }));

        const thrown = await runServerApply(s.run, buildOf(components), s.apply as never, s.guard).catch((error: unknown) => error);

        expect(thrown).toBeInstanceOf(Error);
        expect(isAgentFixableError(thrown)).toBe(fixable);
        const downgraded = (rejected === "A" || rejected === "D") && buildStatus !== 500;
        expect((thrown as { status?: number }).status).toBe(downgraded ? (skillStatus ?? 503) : buildStatus);
    });

    test("only a skill's dependants are withheld: a failed agent's are left to the server's own skipping", async () => {
        const components = [comp("A", "agent"), comp("W", "workflow", { depends_on: ["A"] })];
        const s = setup("depends_on", null);
        s.run.ordered = components;
        s.run.saveOne = async () => null;
        s.run.fetchConfigByFilename = async () => agentYaml([]);
        s.apply.mockImplementation(async () => ({ operations: [{ ...updated("A"), status: "failed", error: { message: "bad", errorId: "e", httpStatus: 422 } } as BuildOperationResult], summary }));

        await expect(runServerApply(s.run, buildOf(components), s.apply as never)).rejects.toThrow("bad");

        expect(s.run.notWritten).toEqual(["A"]);
    });

    test("a skill that was declined is not an unwritten skill: nothing is withheld, and the save is cancelled", async () => {
        const s = setup("depends_on", () => Promise.resolve({ name: "S1", type: "skill", id: "id-S1", commit: async () => {} } as SaveResult));
        s.guard.confirm = vi.fn().mockRejectedValue(new Error("User cancelled"));

        await expect(runServerApply(s.run, buildOf(s.components), s.apply as never, s.guard)).rejects.toThrow("User cancelled");

        expect(s.run.notWritten).toEqual([]);
    });
});

describe("a config that cannot be re-read while looking for what uses a failed skill", () => {
    test.each([
        ["comes back empty", async () => null],
        ["throws", async () => Promise.reject(failure("expired"))],
    ] as [string, () => Promise<string | null>][])("is reported, and the agent whose config it was is withheld, as what it binds is not known: it %s", async (_label, reread) => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
        try {
            const s = setup("its own config", FAILURES[0].save);
            const read = s.run.fetchConfigByFilename;
            let reading = false;
            s.run.fetchConfigByFilename = vi.fn(async (filename: string, options) => (reading && filename === "A.yaml" ? reread() : read(filename, options)));
            s.apply.mockImplementation(async (vars: { dryRun?: boolean }) => {
                reading = !vars.dryRun;
                return { operations: [vars.dryRun ? ({ ...updated("A"), status: "planned" } as BuildOperationResult) : updated("A")], summary };
            });

            await runServerApply(s.run, buildOf(s.components), s.apply as never, s.guard);

            expect(warn).toHaveBeenCalledWith(expect.stringContaining("[builder]"), expect.objectContaining({ component: "A" }));
            expect(s.run.notWritten).toEqual(["S1", "A"]);
        } finally {
            warn.mockRestore();
        }
    });
});

describe("a save that ends in a cancellation", () => {
    test("has no deploy after it, so what uses a skill that failed is not looked up", async () => {
        const components = [comp("S1", "skill"), comp("S2", "skill"), comp("A", "agent", { depends_on: ["S1"] })];
        const s = setup("depends_on", null);
        s.run.ordered = components;
        s.run.fetchConfigByFilename = vi.fn(async (filename: string) => (filename.startsWith("A") ? agentYaml(["Other"]) : SKILL_YAML));
        s.run.saveOne = async (_yaml, c) => {
            if (c.name === "S1") throw failure("lookup failed", { status: 500 });
            return c.type === "skill" ? ({ name: c.name, type: "skill", id: `id-${c.name}`, commit: async () => {} } as SaveResult) : null;
        };
        s.guard.confirm = vi.fn().mockRejectedValue(new Error("User cancelled"));
        const reads = s.run.fetchConfigByFilename as ReturnType<typeof vi.fn>;

        await expect(runServerApply(s.run, buildOf(components), s.apply as never, s.guard)).rejects.toThrow("User cancelled");

        expect(s.run.notWritten).toEqual(["S1"]);
        expect(reads).toHaveBeenCalledTimes(components.length);
    });
});

describe("builder_server_apply off — a skill that fails to save", () => {
    test.each(rows())("when %s, the save fails with that failure and the skill is not counted as saved (%s)", async (_label, style, failing) => {
        const s = setup(style, failing.save);
        s.run.saveOne = async (_yaml, c) => {
            if (c.type !== "skill") return null;
            const result = await failing.save();
            if (result?.commit) await result.commit();
            return result;
        };

        await expect(runPerKindSaves(s.run, s.components, new Map(), s.guard)).rejects.toThrow(failing.message);

        expect(s.results.has("S1")).toBe(false);
    });

    test("what deploys next is the sweep, which still resolves every component that is not a skill or connector", () => {
        const next = deployAfterSave(savedOrPartial(true, undefined, ["S1"]));

        expect(next).toEqual({ deploy: true, changedOnPlatform: undefined, options: { notWritten: ["S1"] } });
        expect(deployCandidates([comp("S1", "skill"), comp("A", "agent"), comp("W", "workflow")], new Set(), false).map(c => c.name)).toEqual(["A", "W"]);
        expect(deployedAfter(undefined, ["S1"])).toEqual([SWEEP]);
    });

    test("a skill config that cannot be read is not counted as saved either, and the other components are still saved", async () => {
        const s = setup("depends_on", null);
        const read = s.run.fetchConfigByFilename;
        s.run.fetchConfigByFilename = async (filename, options) => {
            if (filename === "S1.yaml") throw failure("artifact expired");
            return read(filename, options);
        };
        s.run.saveOne = async (_yaml, c) => ({ name: c.name, type: "agent", id: `id-${c.name}` });

        const outcome = await runPerKindSaves(s.run, s.components, new Map(), s.guard);

        expect(outcome.partialFailures?.map(f => f.name)).toEqual(["S1"]);
        expect([...s.results.keys()]).toEqual(["A", "B"]);
    });
});

function rows() {
    return FAILURES.flatMap(f => STYLES.map(style => [f.label, style, f] as const));
}
