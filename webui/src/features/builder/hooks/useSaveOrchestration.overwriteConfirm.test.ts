// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import type { BuildOperationResult } from "@/api/builder/applyTypes";

import type { BuildManifest } from "./useBuilderManifest";
import { deployAfterSave, joinFailures, type OverwriteGuard, runServerApply, type SaveRun } from "./useSaveOrchestration";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, extra: Partial<Comp> = {}): Comp => ({ filename: `${name}.yaml`, type: "agent", name, status: "validated", origin: "session", ...extra }) as Comp;
const manifest = (components: Comp[]): BuildManifest => ({ name: "build", components }) as BuildManifest;

function makeRun(components: Comp[]): SaveRun {
    return {
        changed: [],
        declined: [],
        notWritten: [],
        ordered: components,
        results: new Map(),
        skip: () => false,
        attribute: () => {},
        fetchConfigByFilename: vi.fn().mockResolvedValue("apps: []"),
        saveOne: vi.fn().mockResolvedValue(null),
    };
}

const op = (name: string, action: BuildOperationResult["action"], status: BuildOperationResult["status"], extra: Partial<BuildOperationResult> = {}): BuildOperationResult =>
    ({ kind: "agent", name, action, status, resourceId: `id-${name}`, ...extra }) as BuildOperationResult;

const summary = { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 };

function twoStepApply(plan: BuildOperationResult[], applied: BuildOperationResult[]) {
    return vi.fn(async (vars: { dryRun?: boolean }) => (vars.dryRun ? { operations: plan, summary } : { operations: applied, summary }));
}

function makeGuard(written: string[] = [], confirm: OverwriteGuard["confirm"] = vi.fn().mockResolvedValue(undefined)) {
    const remember = vi.fn().mockResolvedValue(undefined);
    const guard: OverwriteGuard = { owns: ({ id }) => written.includes(id), wrote: id => written.includes(id), remember, confirm };
    return { guard, remember, confirm: confirm as ReturnType<typeof vi.fn> };
}

describe("runServerApply — replacing components the Builder did not write", () => {
    test("asks before an update to a resource it has not written, then saves once confirmed", async () => {
        const components = [comp("Taken")];
        const applyBuild = twoStepApply([op("Taken", "update", "planned", { changedFields: ["systemPrompt"] })], [op("Taken", "update", "succeeded")]);
        const { guard, confirm } = makeGuard();

        const outcome = await runServerApply(makeRun(components), manifest(components), applyBuild, guard);

        expect(confirm).toHaveBeenCalledWith([{ name: "Taken", kind: "agent", changedFields: ["systemPrompt"] }]);
        expect(applyBuild.mock.calls.map(([vars]) => Boolean(vars.dryRun))).toEqual([true, true, false]);
        expect(outcome.updatedInPlace).toEqual(["Taken"]);
    });

    test("writes nothing when the user declines", async () => {
        const components = [comp("Taken")];
        const applyBuild = twoStepApply([op("Taken", "update", "planned")], [op("Taken", "update", "succeeded")]);
        const { guard, remember } = makeGuard([], vi.fn().mockRejectedValue(new Error("User cancelled")));

        await expect(runServerApply(makeRun(components), manifest(components), applyBuild, guard)).rejects.toThrow("User cancelled");

        expect(applyBuild).toHaveBeenCalledTimes(1);
        expect(applyBuild.mock.calls[0][0].dryRun).toBe(true);
        expect(remember).not.toHaveBeenCalled();
    });

    test("a plan made again after an answer that now rejects a component stops the save before anything is written", async () => {
        const components = [comp("Taken"), comp("Broke")];
        const rejection = { message: "rejected", errorId: "e-1", httpStatus: 422 };
        const plans = [
            [op("Taken", "update", "planned"), op("Broke", "create", "planned")],
            [op("Taken", "update", "planned"), op("Broke", "create", "failed", { error: rejection })],
        ];
        const applyBuild = vi.fn(async (vars: { dryRun?: boolean }) => (vars.dryRun ? { operations: plans.shift() ?? [], summary } : { operations: [op("Taken", "update", "succeeded")], summary }));
        const { guard, confirm, remember } = makeGuard();

        await expect(runServerApply(makeRun(components), manifest(components), applyBuild, guard)).rejects.toThrow("rejected");

        expect(confirm).toHaveBeenCalledTimes(1);
        expect(applyBuild.mock.calls.filter(([vars]) => !vars.dryRun)).toHaveLength(0);
        expect(remember).not.toHaveBeenCalled();
    });

    test("a re-plan that rejects is not asked about, even when it plans a replacement the first plan did not", async () => {
        const components = [comp("Taken"), comp("Other"), comp("Broke")];
        const rejection = { message: "rejected", errorId: "e-1", httpStatus: 422 };
        const plans = [
            [op("Taken", "update", "planned"), op("Other", "unchanged", "planned"), op("Broke", "create", "planned")],
            [op("Taken", "update", "planned"), op("Other", "update", "planned"), op("Broke", "create", "failed", { error: rejection })],
        ];
        const applyBuild = vi.fn(async (vars: { dryRun?: boolean }) => (vars.dryRun ? { operations: plans.shift() ?? [], summary } : { operations: [], summary }));
        const { guard, confirm } = makeGuard();

        await expect(runServerApply(makeRun(components), manifest(components), applyBuild, guard)).rejects.toThrow("rejected");

        expect(confirm).toHaveBeenCalledTimes(1);
        expect(confirm).toHaveBeenCalledWith([{ name: "Taken", kind: "agent", changedFields: [] }]);
    });

    test("a build the first plan already rejects is not asked about at all", async () => {
        const components = [comp("Taken"), comp("Broke")];
        const rejection = { message: "rejected", errorId: "e-1", httpStatus: 422 };
        const applyBuild = twoStepApply([op("Taken", "update", "planned"), op("Broke", "create", "failed", { error: rejection })], []);
        const { guard, confirm } = makeGuard();

        await expect(runServerApply(makeRun(components), manifest(components), applyBuild, guard)).rejects.toThrow("rejected");

        expect(confirm).not.toHaveBeenCalled();
        expect(applyBuild).toHaveBeenCalledTimes(1);
    });

    test("does not ask about the resource this session wrote", async () => {
        const components = [comp("Mine")];
        const applyBuild = twoStepApply([op("Mine", "update", "planned")], [op("Mine", "update", "succeeded")]);
        const { guard, confirm } = makeGuard(["id-Mine"]);

        await runServerApply(makeRun(components), manifest(components), applyBuild, guard);

        expect(confirm).not.toHaveBeenCalled();
    });

    test("a deploy stamp is not ownership: a stamped component the Builder did not write still asks", async () => {
        const components = [comp("Taken", { deployed_version: 4 })];
        const applyBuild = twoStepApply([op("Taken", "update", "planned")], [op("Taken", "update", "succeeded")]);
        const { guard, confirm } = makeGuard();

        await runServerApply(makeRun(components), manifest(components), applyBuild, guard);

        expect(confirm).toHaveBeenCalledTimes(1);
    });

    test("asks when the update targets a different resource than the one the session wrote", async () => {
        const components = [comp("Name")];
        const applyBuild = twoStepApply([op("Name", "update", "planned", { resourceId: "someone-elses" })], [op("Name", "update", "succeeded")]);
        const { guard, confirm } = makeGuard(["id-Name"]);

        await runServerApply(makeRun(components), manifest(components), applyBuild, guard);

        expect(confirm).toHaveBeenCalledTimes(1);
    });

    test("does not ask when the build only creates or leaves things unchanged", async () => {
        const components = [comp("New"), comp("Same")];
        const applyBuild = twoStepApply([op("New", "create", "planned"), op("Same", "unchanged", "planned")], [op("New", "create", "succeeded"), op("Same", "unchanged", "succeeded")]);
        const { guard, confirm } = makeGuard();

        await runServerApply(makeRun(components), manifest(components), applyBuild, guard);

        expect(confirm).not.toHaveBeenCalled();
    });

    test("lists only the components it cannot place when the build mixes both", async () => {
        const components = [comp("Mine"), comp("Theirs")];
        const applyBuild = twoStepApply([op("Mine", "update", "planned"), op("Theirs", "update", "planned", { changedFields: ["description"] })], [op("Mine", "update", "succeeded"), op("Theirs", "update", "succeeded")]);
        const { guard, confirm } = makeGuard(["id-Mine"]);

        await runServerApply(makeRun(components), manifest(components), applyBuild, guard);

        expect(confirm.mock.calls[0][0].map((o: { name: string }) => o.name)).toEqual(["Theirs"]);
    });

    test("remembers what the save created or updated, so the next save does not ask", async () => {
        const components = [comp("New"), comp("Edited"), comp("Same")];
        const applyBuild = twoStepApply([], [op("New", "create", "succeeded"), op("Edited", "update", "succeeded"), op("Same", "unchanged", "succeeded")]);
        const { guard, remember } = makeGuard();

        await runServerApply(makeRun(components), manifest(components), applyBuild, guard);

        expect(remember).toHaveBeenCalledWith([
            { id: "id-New", kind: "agent" },
            { id: "id-Edited", kind: "agent" },
        ]);
    });

    test("remembers what landed before a failure, so a retry does not ask about it", async () => {
        const components = [comp("Landed"), comp("Broke")];
        const failed = op("Broke", "update", "failed", { error: { message: "rejected", errorId: "e-1", httpStatus: 422 } });
        const applyBuild = twoStepApply([], [op("Landed", "update", "succeeded"), failed]);
        const { guard, remember } = makeGuard();

        await expect(runServerApply(makeRun(components), manifest(components), applyBuild, guard)).rejects.toThrow();

        expect(remember).toHaveBeenCalledWith([{ id: "id-Landed", kind: "agent" }]);
    });

    test("skips the plan when no question is wired, and still remembers what it wrote", async () => {
        const components = [comp("Taken")];
        const applyBuild = twoStepApply([op("Taken", "update", "planned")], [op("Taken", "update", "succeeded")]);
        const remember = vi.fn().mockResolvedValue(undefined);

        await runServerApply(makeRun(components), manifest(components), applyBuild, { owns: () => false, wrote: () => false, remember });

        expect(applyBuild).toHaveBeenCalledTimes(1);
        expect(applyBuild.mock.calls[0][0].dryRun).toBeUndefined();
        expect(remember).toHaveBeenCalledWith([{ id: "id-Taken", kind: "agent" }]);
    });

    test("without a guard it behaves as before: one request", async () => {
        const components = [comp("Taken")];
        const applyBuild = twoStepApply([op("Taken", "update", "planned")], [op("Taken", "update", "succeeded")]);

        await runServerApply(makeRun(components), manifest(components), applyBuild);

        expect(applyBuild).toHaveBeenCalledTimes(1);
    });

    test("a plan the server rejects is thrown like a failed apply", async () => {
        const components = [comp("Bad")];
        const applyBuild = vi.fn().mockRejectedValue(Object.assign(new Error('agent "Bad": unknown toolset "foo"'), { status: 422 }));

        await expect(runServerApply(makeRun(components), manifest(components), applyBuild, makeGuard().guard)).rejects.toThrow('unknown toolset "foo"');
        expect(applyBuild).toHaveBeenCalledTimes(1);
    });

    describe("a plan that reports a component the save would reject", () => {
        const rejection = { message: "description must be at least 10 characters", errorId: "e-1", httpStatus: 422, validationDetails: { description: ["must be at least 10 characters"] } };

        test("blocks the save before anything is written or asked", async () => {
            const components = [comp("Good"), comp("Taken"), comp("Broke")];
            const plan = [op("Good", "create", "planned"), op("Taken", "update", "planned", { changedFields: ["description"] }), op("Broke", "create", "failed", { error: rejection })];
            const applyBuild = twoStepApply(plan, []);
            const { guard, confirm, remember } = makeGuard();

            await expect(runServerApply(makeRun(components), manifest(components), applyBuild, guard)).rejects.toThrow("description must be at least 10 characters");

            expect(applyBuild.mock.calls.map(([vars]) => Boolean(vars.dryRun))).toEqual([true]);
            expect(confirm).not.toHaveBeenCalled();
            expect(remember).not.toHaveBeenCalled();
        });

        test("is thrown with the server's status and field detail so the builder agent can repair it", async () => {
            const components = [comp("Broke")];
            const applyBuild = twoStepApply([op("Broke", "create", "failed", { error: rejection })], []);

            let err: unknown;
            await runServerApply(makeRun(components), manifest(components), applyBuild, makeGuard().guard).catch((e: unknown) => {
                err = e;
            });

            expect(err).toBeInstanceOf(Error);
            expect((err as Error).message).toContain("description: must be at least 10 characters");
            expect(err).toMatchObject({ status: 422, errorId: "e-1" });
        });

        test("carries the plan's warnings, which are the cause of some rejections", async () => {
            const components = [comp("Broke")];
            const plan = [op("Broke", "create", "failed", { error: rejection, warnings: ["the instruction key was dropped"] })];

            await expect(runServerApply(makeRun(components), manifest(components), twoStepApply(plan, []), makeGuard().guard)).rejects.toThrow("Warning — Broke: the instruction key was dropped");
        });

        test("is attributed to the component that would fail, and names every rejected one", async () => {
            const components = [comp("First"), comp("Second")];
            const attribute = vi.fn();
            const run = { ...makeRun(components), attribute };
            const plan = [op("First", "create", "failed", { error: { ...rejection, message: "first rejected" } }), op("Second", "update", "failed", { error: { ...rejection, message: "second rejected" } })];

            await expect(runServerApply(run, manifest(components), twoStepApply(plan, []), makeGuard().guard)).rejects.toThrow(/first rejected[\s\S]*Also failed — Second: second rejected/);

            expect(attribute).toHaveBeenLastCalledWith(components[0]);
        });
    });
});

describe("deployAfterSave", () => {
    test("deploys what a completed save changed, and sweeps the rest", () => {
        expect(deployAfterSave({ kind: "saved", changedOnPlatform: ["A"] })).toEqual({ deploy: true, changedOnPlatform: ["A"], options: {} });
        expect(deployAfterSave({ kind: "saved", changedOnPlatform: undefined })).toEqual({ deploy: true, changedOnPlatform: undefined, options: {} });
    });

    test("never deploys after a decline or while another save runs", () => {
        expect(deployAfterSave({ kind: "cancelled" })).toEqual({ deploy: false });
        expect(deployAfterSave({ kind: "busy" })).toEqual({ deploy: false });
    });

    test("does not deploy after a failure that wrote nothing: the by-name sweep would reach components the save never touched", () => {
        expect(deployAfterSave({ kind: "failed", changedOnPlatform: [], notWritten: ["B"] })).toEqual({ deploy: false });
    });

    test("deploys only what landed before a failure, and not what it did not write", () => {
        expect(deployAfterSave({ kind: "failed", changedOnPlatform: ["A"], notWritten: ["B"] })).toEqual({ deploy: true, changedOnPlatform: ["A"], options: { onlyChanged: true, notWritten: ["B"] } });
    });

    test("keeps sweeping after a failure on a path that cannot say what it changed", () => {
        expect(deployAfterSave({ kind: "failed", changedOnPlatform: undefined, notWritten: [] })).toEqual({ deploy: true, changedOnPlatform: undefined, options: { notWritten: [] } });
    });
});

describe("joinFailures", () => {
    test("says each failure once, the save's first", () => {
        expect(joinFailures("save broke", "deploy broke")).toBe("save broke\ndeploy broke");
        expect(joinFailures("same", "same")).toBe("same");
    });

    test("says whichever there is", () => {
        expect(joinFailures("save broke", null)).toBe("save broke");
        expect(joinFailures(null, "deploy broke")).toBe("deploy broke");
        expect(joinFailures(null, null)).toBeNull();
    });
});

describe("runServerApply: which failures discard the platform check's remembered pass", () => {
    const refusal = { message: "rejected", errorId: "e-1", httpStatus: 422 };
    const outage = { message: "platform down", errorId: "e-2", httpStatus: 503 };
    const forbidden = { message: "not allowed", errorId: "e-3", httpStatus: 403 };

    async function saveFailingWith(error: BuildOperationResult["error"][], { planned }: { planned: boolean }) {
        const components = error.map((_, i) => comp(`C${i}`));
        const failed = error.map((e, i) => op(`C${i}`, "create", "failed", { error: e }));
        const onPlanRefused = vi.fn();
        const run = { ...makeRun(components), onPlanRefused };
        const applyBuild = twoStepApply(planned ? failed : [], planned ? [] : failed);
        await runServerApply(run, manifest(components), applyBuild, planned ? makeGuard().guard : undefined).catch(() => {});
        return onPlanRefused;
    }

    test.each([
        ["a plan the platform refuses", true],
        ["an apply the platform refuses", false],
    ])("%s asks the check again", async (_label, planned) => {
        expect(await saveFailingWith([refusal], { planned })).toHaveBeenCalledTimes(1);
    });

    test.each([
        ["a plan that fails with a 5xx", outage, true],
        ["an apply that fails with a 5xx", outage, false],
        ["a plan that fails with a 403", forbidden, true],
        ["an apply that fails with a 403", forbidden, false],
    ])("%s does not", async (_label, error, planned) => {
        expect(await saveFailingWith([error], { planned })).not.toHaveBeenCalled();
    });

    test("a refusal among other failures still asks", async () => {
        expect(await saveFailingWith([outage, refusal], { planned: false })).toHaveBeenCalledTimes(1);
    });
});
