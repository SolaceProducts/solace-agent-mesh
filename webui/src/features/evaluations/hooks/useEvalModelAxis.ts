// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import type { EvalExperiment, EvalRunGroup } from "@/types/eval";

import { deriveExperimentModels, sortModelsByExperimentOrder } from "../utils";

export function useEvalModelAxis(experiment: EvalExperiment | null, group: Pick<EvalRunGroup, "runs"> | null) {
    const effectiveModels = useMemo(() => deriveExperimentModels(experiment?.models, group?.runs), [experiment, group]);
    const allModels = useMemo(() => sortModelsByExperimentOrder(group?.runs.map(r => r.model) ?? [], effectiveModels), [group, effectiveModels]);
    return { effectiveModels, allModels };
}
