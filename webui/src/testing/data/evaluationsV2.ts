// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { EvalDataset, EvalEvaluator, EvalExperiment, WatchlistAgentChart } from "@/types/eval";

import { toV2Instant } from "./platformWireV2";

export const datasetToV2 = (dataset: EvalDataset) => ({
    id: dataset.id,
    type: "dataset",
    name: dataset.name,
    description: dataset.description ?? null,
    isDefault: false,
    exampleCount: dataset.exampleCount,
    experimentCount: dataset.experimentCount,
    createdBy: dataset.createdBy,
    updatedBy: dataset.updatedBy,
    createdTime: toV2Instant(dataset.createdAt),
    updatedTime: toV2Instant(dataset.updatedAt),
});

export const evaluatorToV2 = ({ type, createdAt, updatedAt, question, ...evaluator }: EvalEvaluator) => ({
    id: evaluator.id,
    type: "evaluator",
    name: evaluator.name,
    description: evaluator.description ?? null,
    evaluatorType: type,
    heuristicType: evaluator.heuristicType ?? null,
    isDefault: evaluator.isDefault,
    model: evaluator.model ?? null,
    promptTemplate: evaluator.promptTemplate ?? null,
    choiceScores: evaluator.choiceScores ?? null,
    question: question ? { type: question.type, instructions: question.instructions, criteria: question.criteria?.map(c => ({ key: c.key, description: c.description ?? null })) ?? null } : null,
    scoring: evaluator.scoring ?? null,
    reads: evaluator.reads ?? null,
    minConfidence: evaluator.minConfidence ?? null,
    escalateToEvaluatorId: evaluator.escalateToEvaluatorId ?? null,
    experimentCount: evaluator.experimentCount,
    createdBy: evaluator.createdBy,
    updatedBy: evaluator.updatedBy,
    createdTime: toV2Instant(createdAt),
    updatedTime: toV2Instant(updatedAt),
});

export const experimentToV2 = (experiment: EvalExperiment) => ({
    id: experiment.id,
    type: "experiment",
    name: experiment.name,
    description: experiment.description ?? null,
    datasetId: experiment.datasetId,
    datasetName: experiment.datasetName ?? null,
    datasetUpdatedTime: toV2Instant(experiment.datasetUpdatedTime),
    targetType: experiment.targetType ?? "agent",
    targetAgent: experiment.targetAgent,
    targetWorkflow: experiment.targetWorkflow ?? null,
    models: experiment.models.map(m => ({ modelConfigId: m.modelConfigId ?? null, model: m.model, alias: m.alias })),
    runsPerExample: experiment.runsPerExample,
    maxWorkers: experiment.maxWorkers,
    evaluatorIds: experiment.evaluatorIds,
    primaryEvaluatorId: experiment.primaryEvaluatorId ?? null,
    isDefault: false,
    runAs: experiment.runAs ?? null,
    latestRunStatus: experiment.latestRunStatus ?? null,
    latestRunTime: toV2Instant(experiment.latestRunTime),
    createdBy: experiment.createdBy,
    updatedBy: experiment.updatedBy,
    createdTime: toV2Instant(experiment.createdAt),
    updatedTime: toV2Instant(experiment.updatedAt),
});

export const watchlistChartToV2 = (chart: WatchlistAgentChart) => ({
    ...chart,
    experiments: chart.experiments.map(e => ({
        ...e,
        primaryEvaluatorName: e.primaryEvaluatorName ?? null,
        points: e.points.map(p => ({ runGroupId: p.runGroupId, startedTime: toV2Instant(p.startedAt), score: p.score })),
    })),
});
