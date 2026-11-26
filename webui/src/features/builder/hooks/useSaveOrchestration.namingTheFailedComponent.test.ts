// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test, vi } from "vitest";

import { isAgentFixableError } from "../builderUtils";

import type { BuildManifest } from "./useBuilderManifest";
import { savedAndReported } from "./useManifestDeployment";
import { runPerKindSaves, type SaveRun } from "./useSaveOrchestration";

type Comp = NonNullable<BuildManifest["components"]>[number];

const comp = (name: string, type: string): Comp => ({ filename: `${name}.yaml`, type, name, status: "validated", origin: "session" }) as Comp;

async function thrownBy(rejection: unknown): Promise<unknown> {
    const components = [comp("Acme", "agent")];
    const run: SaveRun = {
        changed: [],
        declined: [],
        notWritten: [],
        ordered: components,
        results: new Map(),
        skip: () => false,
        attribute: () => {},
        fetchConfigByFilename: vi.fn().mockResolvedValue("apps: []"),
        saveOne: vi.fn().mockRejectedValue(rejection) as unknown as SaveRun["saveOne"],
    };
    return runPerKindSaves(run, components, new Map()).then(
        () => {
            throw new Error("the save was expected to fail");
        },
        err => err
    );
}

const flat = (message: string, status: number, extra: Record<string, unknown> = {}) => Object.assign(new Error(message), { status, ...extra });
const axiosLike = (message: string, status: number) => Object.assign(new Error(message), { response: { status } });

describe("the per-kind save names the component it stopped on", () => {
    test.each([
        ["a name that only appears inside another name", flat('connector "AcmeDB" is unreachable', 502), 'Failed to save "Acme": connector "AcmeDB" is unreachable', false],
        ["a name that only appears inside a word", new Error("config is invalid, Acmeish"), 'Failed to save "Acme": config is invalid, Acmeish', true],
        ["a 500 that names nothing", flat("internal error", 500, { errorId: "e1", code: "X" }), 'Failed to save "Acme": internal error', false],
        ["a 401", flat("unauthorized", 401), 'Failed to save "Acme": unauthorized', false],
        ["a 404 for a missing route", flat("not found", 404), 'Failed to save "Acme": not found', false],
        ["a 422 rejection of the config", flat("config is invalid", 422), 'Failed to save "Acme": config is invalid', true],
        ["an axios-shaped 500", axiosLike("internal error", 500), 'Failed to save "Acme": internal error', false],
        ["an axios-shaped 422", axiosLike("config is invalid", 422), 'Failed to save "Acme": config is invalid', true],
        ["a client-side throw with no status", new Error("YAML parse failed"), 'Failed to save "Acme": YAML parse failed', true],
    ])("%s", async (_label, rejection, wantMessage, wantFixable) => {
        const err = (await thrownBy(rejection)) as Error & { status?: number; code?: string; errorId?: string };

        expect(err.message).toBe(wantMessage);
        expect(isAgentFixableError(err)).toBe(wantFixable);
        expect(isAgentFixableError(err)).toBe(isAgentFixableError(rejection));
        const original = rejection as { status?: number; code?: string; errorId?: string };
        expect([err.status, err.code, err.errorId]).toEqual([original.status, original.code, original.errorId]);
    });

    test.each([
        ["an error that already names the component", flat('agent "Acme" violates the schema', 422)],
        ["a rejection that is not an Error", "boom"],
        ["a null rejection", null],
        ["the user declining a prompt", new Error("User cancelled")],
    ])("%s is rethrown as it was", async (_label, rejection) => {
        expect(await thrownBy(rejection)).toBe(rejection);
    });
});

describe("savedAndReported: which missing component the save already reported", () => {
    const manifest = [comp("A", "agent"), comp("Flow", "workflow"), comp("weather", "skill"), comp("Weather", "agent"), comp("db", "connector"), comp("Twin", "skill"), comp("Twin", "agent")];

    test.each([
        ["an agent the save stopped on", "A", ["A"], true],
        ["a component the save did not stop on", "A", ["B"], false],
        ["nothing was recorded", "A", [], false],
        ["a name that differs only in case", "A", ["a"], false],
        ["a skill's name matches only exactly, not the agent named Weather", "Weather", ["weather"], false],
        ["an agent that shares its name with a skill", "Twin", ["Twin"], false],
        ["a workflow the save stopped on", "Flow", ["Flow"], true],
        ["an agent named like a connector that failed", "db", ["db"], false],
    ])("%s", (_label, missing, notWritten, want) => {
        expect(savedAndReported(missing, notWritten, manifest)).toBe(want);
    });
});
