// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { EvalRun } from "@/types/eval";

export const makeEvalRun = (overrides: Partial<EvalRun> = {}): EvalRun => {
    return {
        id: "run-1",
        experimentId: "exp-1",
        runGroupId: "group-1",
        status: "completed",
        progressCurrent: 10,
        progressTotal: 10,
        model: "model-config-1",
        source: "manual",
        startedBy: "user",
        startedAt: "2026-01-01T00:00:00Z",
        ...overrides,
    };
};
