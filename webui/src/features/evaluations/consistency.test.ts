// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { EvalRunResult } from "@/types/eval";

import { consistencyLine, trialConsistency, trialsDisagree, trialVerdicts } from "./utils";

function trial(exampleId: string, trialIndex: number, passed: boolean | null): EvalRunResult {
    return { id: `${exampleId}-${trialIndex}`, exampleId, trialIndex, scores: passed === null ? [] : [{ evaluatorId: "ev", score: passed ? 1 : 0, passed }] } as unknown as EvalRunResult;
}

describe("consistency across trials", () => {
    const results = {
        "model-a": [trial("ex-1", 0, true), trial("ex-1", 1, true), trial("ex-1", 2, true), trial("ex-2", 0, true), trial("ex-2", 1, false), trial("ex-2", 2, true), trial("ex-3", 0, false), trial("ex-3", 1, false), trial("ex-3", 2, null)],
    };

    test("counts the trials that passed, of those judged", () => {
        expect(trialVerdicts(results["model-a"].slice(3, 6), "ev")).toEqual({ passed: 2, judged: 3 });
        expect(trialsDisagree({ passed: 2, judged: 3 })).toBe(true);
        expect(trialsDisagree({ passed: 0, judged: 2 })).toBe(false);
        expect(trialsDisagree({ passed: 1, judged: 1 })).toBe(false);
    });

    test("pass^k counts the examples that passed every trial and flags the ones that disagreed", () => {
        const c = trialConsistency(results, ["model-a"], "ev");
        expect(c).toEqual({ examples: 3, passedEvery: 1, inconsistent: 1, trials: 3 });
        expect(consistencyLine(c!)).toBe("1 of 3 examples passed all runs. 1 example partially failed.");
    });

    test("is absent when every example ran once", () => {
        expect(trialConsistency({ "model-a": [trial("ex-1", 0, true)] }, ["model-a"], "ev")).toBeNull();
    });
});
