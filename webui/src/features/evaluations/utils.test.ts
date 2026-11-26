// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { EvalExperiment, EvalRun, ExperimentModelConfig } from "@/types/eval";

import {
    buildModelColorIndex,
    createExperimentModelLookup,
    deriveExperimentModels,
    evalArtifactRenderType,
    hasActiveRun,
    isDatasetChangedSinceLastRun,
    isRunGroupOutdated,
    sortModelsByExperimentOrder,
    validateMaxWorkers,
    validateRunsPerExample,
} from "./utils";

const SAMPLE_MODELS: ExperimentModelConfig[] = [
    { modelConfigId: "11111111-1111-1111-1111-111111111111", model: "gpt-4o", alias: "Production" },
    { modelConfigId: "22222222-2222-2222-2222-222222222222", model: "claude-sonnet-4", alias: "Champion" },
];

describe("createExperimentModelLookup", () => {
    test("aliasFor resolves a modelConfigId to its alias", () => {
        const { aliasFor } = createExperimentModelLookup(SAMPLE_MODELS);
        expect(aliasFor("11111111-1111-1111-1111-111111111111")).toBe("Production");
        expect(aliasFor("22222222-2222-2222-2222-222222222222")).toBe("Champion");
    });

    test("aliasFor falls back to the input when no match is found", () => {
        const { aliasFor } = createExperimentModelLookup(SAMPLE_MODELS);
        const unknownId = "99999999-9999-9999-9999-999999999999";
        expect(aliasFor(unknownId)).toBe(unknownId);
    });

    test("aliasFor passed a model name (not a modelConfigId) does NOT match — regression guard", () => {
        const { aliasFor } = createExperimentModelLookup(SAMPLE_MODELS);
        expect(aliasFor("gpt-4o")).toBe("gpt-4o");
    });

    test("rawModelFor returns the model name when matched", () => {
        const { rawModelFor } = createExperimentModelLookup(SAMPLE_MODELS);
        expect(rawModelFor("11111111-1111-1111-1111-111111111111")).toBe("gpt-4o");
        expect(rawModelFor("22222222-2222-2222-2222-222222222222")).toBe("claude-sonnet-4");
    });

    test("rawModelFor returns undefined when no match is found", () => {
        const { rawModelFor } = createExperimentModelLookup(SAMPLE_MODELS);
        expect(rawModelFor("99999999-9999-9999-9999-999999999999")).toBeUndefined();
    });

    test("omitted experimentModels yields an aliasFor that just echoes the input", () => {
        const { aliasFor, rawModelFor } = createExperimentModelLookup();
        expect(aliasFor("anything")).toBe("anything");
        expect(rawModelFor("anything")).toBeUndefined();
    });

    test("aliasFor falls back to the raw model name when the alias is empty/whitespace", () => {
        const { aliasFor } = createExperimentModelLookup([
            { modelConfigId: "mc-blank", model: "openai/gemini-3.5-flash", alias: "" },
            { modelConfigId: "mc-space", model: "gpt-4o", alias: "   " },
        ]);
        expect(aliasFor("mc-blank")).toBe("openai/gemini-3.5-flash");
        expect(aliasFor("mc-space")).toBe("gpt-4o");
    });

    test("aliasFor falls back to the id only when alias AND model name are both empty", () => {
        const { aliasFor } = createExperimentModelLookup([{ modelConfigId: "mc-x", model: "", alias: "" }]);
        expect(aliasFor("mc-x")).toBe("mc-x");
    });
});

function baseRun(overrides: Partial<EvalRun> = {}): EvalRun {
    return {
        id: "run-1",
        experimentId: "exp-1",
        runGroupId: "g-1",
        status: "completed",
        progressCurrent: 1,
        progressTotal: 1,
        model: "mc-a",
        source: "manual",
        startedBy: "t",
        startedAt: "2026-04-29T00:00:00Z",
        ...overrides,
    };
}

function makeRunWithAgentModel(model: string, agentModelConfig?: { alias?: string; modelName?: string }): EvalRun {
    return baseRun({
        model,
        configSnapshot: agentModelConfig ? { agent: { name: "Orchestrator", modelConfig: agentModelConfig } } : undefined,
    });
}

describe("deriveExperimentModels", () => {
    test("adds a synthetic entry from the run's agent model when the experiment defines no models", () => {
        const runs = [makeRunWithAgentModel("", { alias: "planning", modelName: "openai/gemini-3.5-flash" })];
        const derived = deriveExperimentModels([], runs);
        expect(derived).toEqual([{ modelConfigId: "", alias: "planning", model: "openai/gemini-3.5-flash" }]);
        expect(createExperimentModelLookup(derived).aliasFor("")).toBe("planning");
    });

    test("a run made with no model, and no agent snapshot to name one, is labelled as using its own model", () => {
        expect(createExperimentModelLookup([]).aliasFor("")).toBe("Own model");
        expect(createExperimentModelLookup([]).aliasFor("mc-x")).toBe("mc-x");
    });

    test("does not override models already defined by the experiment", () => {
        const experimentModels: ExperimentModelConfig[] = [{ modelConfigId: "mc-a", model: "gpt-4o", alias: "Production" }];
        const runs = [makeRunWithAgentModel("mc-a", { alias: "should-be-ignored", modelName: "ignored" })];
        expect(deriveExperimentModels(experimentModels, runs)).toBe(experimentModels);
    });

    test("dedupes multiple runs that share the same uncovered model key", () => {
        const runs = [makeRunWithAgentModel("", { alias: "planning", modelName: "gemini" }), makeRunWithAgentModel("", { alias: "planning", modelName: "gemini" })];
        const derived = deriveExperimentModels([], runs);
        expect(derived).toHaveLength(1);
    });

    test("returns the original list unchanged when no run carries an agent model snapshot", () => {
        const runs = [makeRunWithAgentModel("", undefined)];
        expect(deriveExperimentModels([], runs)).toEqual([]);
    });

    test("returns experimentModels untouched when there are no runs", () => {
        const experimentModels: ExperimentModelConfig[] = [{ modelConfigId: "mc-a", model: "gpt-4o", alias: "Production" }];
        expect(deriveExperimentModels(experimentModels, [])).toBe(experimentModels);
        expect(deriveExperimentModels(experimentModels, undefined)).toBe(experimentModels);
    });
});

describe("sortModelsByExperimentOrder", () => {
    test("orders run-derived UUIDs by their position in experiment.models", () => {
        const out = sortModelsByExperimentOrder(["22222222-2222-2222-2222-222222222222", "11111111-1111-1111-1111-111111111111"], SAMPLE_MODELS);
        expect(out).toEqual(["11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"]);
    });

    test("dedupes input ids", () => {
        const out = sortModelsByExperimentOrder(["22222222-2222-2222-2222-222222222222", "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"], SAMPLE_MODELS);
        expect(out).toEqual(["11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"]);
    });

    test("ids missing from experimentModels sort to the end", () => {
        const out = sortModelsByExperimentOrder(["99999999-9999-9999-9999-999999999999", "22222222-2222-2222-2222-222222222222", "11111111-1111-1111-1111-111111111111"], SAMPLE_MODELS);
        expect(out).toEqual(["11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "99999999-9999-9999-9999-999999999999"]);
    });

    test("omitted / empty experimentModels preserves dedupe but not order", () => {
        const out = sortModelsByExperimentOrder(["a", "b", "a"]);
        expect(out).toEqual(["a", "b"]);
    });
});

describe("buildModelColorIndex", () => {
    test("assigns distinct indices to two model configs that share the same `model` — regression guard", () => {
        const models: ExperimentModelConfig[] = [
            { modelConfigId: "cfg-a", model: "gpt-4o", alias: "Production" },
            { modelConfigId: "cfg-b", model: "gpt-4o", alias: "Eval-key" },
        ];
        const idx = buildModelColorIndex(models);
        expect(idx.get("cfg-a")).toBe(0);
        expect(idx.get("cfg-b")).toBe(1);
        expect(idx.size).toBe(2);
    });

    test("returns undefined for ids not in the experiment (callsite uses `?? 0` fallback)", () => {
        const idx = buildModelColorIndex([{ modelConfigId: "cfg-a", model: "gpt-4o", alias: "A" }]);
        expect(idx.get("cfg-missing")).toBeUndefined();
    });
});

const EXPERIMENT_BASE: EvalExperiment = {
    id: "exp-1",
    name: "Exp",
    datasetId: "ds-current",
    targetAgent: "A",
    models: [
        { modelConfigId: "mc-a", model: "gpt-4o", alias: "gpt" },
        { modelConfigId: "mc-b", model: "claude-sonnet-4", alias: "claude" },
    ],
    runsPerExample: 1,
    maxWorkers: 1,
    evaluatorIds: [],
    createdBy: "t",
    updatedBy: "t",
    createdAt: "2026-04-29T00:00:00Z",
    updatedAt: "2026-04-29T00:00:00Z",
};

function makeRun(snapshotExperiment: Record<string, unknown> | undefined): EvalRun {
    return baseRun({
        configSnapshot: snapshotExperiment === undefined ? undefined : { experiment: snapshotExperiment },
    });
}

describe("isRunGroupOutdated", () => {
    test("returns false when experiment is null (loading state must not flag stale)", () => {
        const run = makeRun({ models: [{ model: "gpt-4o" }, { model: "claude-sonnet-4" }], dataset_id: "ds-current" });
        expect(isRunGroupOutdated([run], null)).toBe(false);
    });

    test("returns false when no run carries a config snapshot (best-effort: assume current)", () => {
        const run = makeRun(undefined);
        expect(isRunGroupOutdated([run], EXPERIMENT_BASE)).toBe(false);
    });

    test("returns false when snapshot models match the experiment regardless of order", () => {
        const run = makeRun({
            models: [{ model: "claude-sonnet-4" }, { model: "gpt-4o" }],
            dataset_id: "ds-current",
        });
        expect(isRunGroupOutdated([run], EXPERIMENT_BASE)).toBe(false);
    });

    test("returns true when the experiment has gained a model since the run", () => {
        const run = makeRun({ models: [{ model: "gpt-4o" }], dataset_id: "ds-current" });
        expect(isRunGroupOutdated([run], EXPERIMENT_BASE)).toBe(true);
    });

    test("returns true when the experiment has dropped a model since the run", () => {
        const run = makeRun({
            models: [{ model: "gpt-4o" }, { model: "claude-sonnet-4" }, { model: "gemini-2.5-pro" }],
            dataset_id: "ds-current",
        });
        expect(isRunGroupOutdated([run], EXPERIMENT_BASE)).toBe(true);
    });

    test("returns true when a model identity has changed since the run", () => {
        const run = makeRun({
            models: [{ model: "gpt-4o-mini" }, { model: "claude-sonnet-4" }],
            dataset_id: "ds-current",
        });
        expect(isRunGroupOutdated([run], EXPERIMENT_BASE)).toBe(true);
    });

    test("returns true when the experiment's dataset has been swapped since the run", () => {
        const run = makeRun({
            models: [{ model: "gpt-4o" }, { model: "claude-sonnet-4" }],
            dataset_id: "ds-old",
        });
        expect(isRunGroupOutdated([run], EXPERIMENT_BASE)).toBe(true);
    });

    test("must read snake_case `dataset_id` from the snapshot — regression guard", () => {
        const run = makeRun({
            models: [{ model: "gpt-4o" }, { model: "claude-sonnet-4" }],
            datasetId: "ds-old",
            dataset_id: "ds-current",
        });
        expect(isRunGroupOutdated([run], EXPERIMENT_BASE)).toBe(false);
    });

    test("must read snapshot fields from the nested `experiment` key — regression guard", () => {
        const run: EvalRun = {
            ...makeRun({ models: [{ model: "gpt-4o" }], dataset_id: "ds-current" }),
            configSnapshot: {
                models: [{ model: "gpt-4o" }, { model: "claude-sonnet-4" }],
                experiment: { models: [{ model: "gpt-4o" }], dataset_id: "ds-current" },
            } as unknown as EvalRun["configSnapshot"],
        };
        expect(isRunGroupOutdated([run], EXPERIMENT_BASE)).toBe(true);
    });

    test("uses the first run that has a snapshot when later runs are missing it", () => {
        const withSnap = makeRun({ models: [{ model: "gpt-4o" }], dataset_id: "ds-current" });
        const withoutSnap = makeRun(undefined);
        expect(isRunGroupOutdated([withoutSnap, withSnap], EXPERIMENT_BASE)).toBe(true);
    });
});

describe("hasActiveRun", () => {
    test("a pending or running run blocks the delete", () => {
        expect(hasActiveRun([{ status: "completed" }, { status: "running" }])).toBe(true);
        expect(hasActiveRun([{ status: "pending" }])).toBe(true);
    });

    test("finished runs do not", () => {
        expect(hasActiveRun([{ status: "completed" }, { status: "failed" }, { status: "cancelled" }])).toBe(false);
        expect(hasActiveRun([])).toBe(false);
    });
});

describe("validateRunsPerExample", () => {
    test.each([2, 3, 4])("no message for a value below the max (%i)", value => {
        expect(validateRunsPerExample(value)).toEqual({ error: null, warning: null });
    });

    test.each([5, 6])("shows the max-runs message once the cap is reached (%i)", value => {
        expect(validateRunsPerExample(value).error).toBe("There is a maximum of 5 runs to avoid long waits.");
    });

    test("errors when fewer than 1 run is requested", () => {
        expect(validateRunsPerExample(0).error).toBe("At least 1 run is required per example");
    });

    test("warns (without erroring) when only 1 run is requested", () => {
        const { error, warning } = validateRunsPerExample(1);
        expect(error).toBeNull();
        expect(warning).toBe("We recommend a minimum of 2 for the most reliable results");
    });
});

describe("validateMaxWorkers", () => {
    test.each([1, 4, 20])("accepts %i", value => {
        expect(validateMaxWorkers(value)).toBeNull();
    });

    test.each([0, 21, 2.5, Number.NaN])("rejects %s", value => {
        expect(validateMaxWorkers(value)).toBe("Max Workers must be a whole number from 1 to 20.");
    });
});

describe("evalArtifactRenderType", () => {
    test("refuses HTML by filename and by media type", () => {
        expect(evalArtifactRenderType("report.html")).toBeNull();
        expect(evalArtifactRenderType("report.htm")).toBeNull();
        expect(evalArtifactRenderType("page", "text/html")).toBeNull();
    });

    test("still resolves the types eval does preview", () => {
        expect(evalArtifactRenderType("notes.md")).toBe("markdown");
        expect(evalArtifactRenderType("data.csv")).toBe("csv");
        expect(evalArtifactRenderType("config.yaml")).toBe("yaml");
        expect(evalArtifactRenderType("shot.png")).toBe("image");
    });
});

describe("isDatasetChangedSinceLastRun", () => {
    const experiment = (over: Partial<EvalExperiment>): EvalExperiment => ({
        id: "e1",
        name: "E1",
        datasetId: "ds-1",
        targetAgent: "a",
        models: [],
        runsPerExample: 1,
        maxWorkers: 1,
        evaluatorIds: [],
        createdBy: "u",
        updatedBy: "u",
        createdAt: "",
        updatedAt: "",
        ...over,
    });

    test("true only when the dataset changed after the latest run", () => {
        expect(isDatasetChangedSinceLastRun(experiment({ datasetUpdatedTime: "2026-08-19T12:00:00.000Z", latestRunTime: "2026-08-19T11:00:00.000Z" }))).toBe(true);
        expect(isDatasetChangedSinceLastRun(experiment({ datasetUpdatedTime: "2026-08-19T10:00:00.000Z", latestRunTime: "2026-08-19T11:00:00.000Z" }))).toBe(false);
    });

    test("false without a prior run, a dataset timestamp, or an experiment", () => {
        expect(isDatasetChangedSinceLastRun(experiment({ datasetUpdatedTime: "2026-08-19T12:00:00.000Z" }))).toBe(false);
        expect(isDatasetChangedSinceLastRun(experiment({ latestRunTime: "2026-08-19T11:00:00.000Z" }))).toBe(false);
        expect(isDatasetChangedSinceLastRun(null)).toBe(false);
    });
});
