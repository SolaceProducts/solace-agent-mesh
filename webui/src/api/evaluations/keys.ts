// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const evaluationKeys = {
    all: ["evaluations"] as const,

    datasets: () => [...evaluationKeys.all, "datasets"] as const,
    datasetsList: (params?: Record<string, string | number | undefined>) => [...evaluationKeys.datasets(), "list", params ?? {}] as const,
    datasetDetails: () => [...evaluationKeys.datasets(), "detail"] as const,
    dataset: (id: string) => [...evaluationKeys.datasetDetails(), id] as const,

    examples: (datasetId: string) => [...evaluationKeys.dataset(datasetId), "examples"] as const,
    examplesList: (datasetId: string, params?: Record<string, string | number | undefined>) => [...evaluationKeys.examples(datasetId), "list", params ?? {}] as const,
    exampleDetails: () => [...evaluationKeys.all, "examples", "detail"] as const,
    example: (exampleId: string) => [...evaluationKeys.exampleDetails(), exampleId] as const,

    evaluators: () => [...evaluationKeys.all, "evaluators"] as const,
    evaluatorsList: () => [...evaluationKeys.evaluators(), "list"] as const,
    evaluatorDetails: () => [...evaluationKeys.evaluators(), "detail"] as const,
    evaluator: (id: string) => [...evaluationKeys.evaluatorDetails(), id] as const,

    experiments: () => [...evaluationKeys.all, "experiments"] as const,
    experimentsList: (params?: { datasetId?: string; evaluatorId?: string; targetAgent?: string }) => [...evaluationKeys.experiments(), "list", params ?? {}] as const,
    experimentDetails: () => [...evaluationKeys.experiments(), "detail"] as const,
    experiment: (id: string) => [...evaluationKeys.experimentDetails(), id] as const,
    experimentSample: () => [...evaluationKeys.experiments(), "sample"] as const,
    experimentToolAuth: (experimentId: string) => [...evaluationKeys.experiment(experimentId), "toolAuth"] as const,
    experimentToolAuthFor: (experimentId: string, principal: string) => [...evaluationKeys.experimentToolAuth(experimentId), principal] as const,
    experimentToolAuthCandidates: (experimentId: string) => [...evaluationKeys.experiment(experimentId), "toolAuthCandidates"] as const,

    experimentRuns: (experimentId: string) => [...evaluationKeys.experiment(experimentId), "runs"] as const,
    runDetails: () => [...evaluationKeys.all, "runs", "detail"] as const,
    run: (runId: string) => [...evaluationKeys.runDetails(), runId] as const,

    runResults: (runId: string) => [...evaluationKeys.run(runId), "results"] as const,
    runResultDetails: () => [...evaluationKeys.all, "runResults", "detail"] as const,
    runResult: (resultId: string) => [...evaluationKeys.runResultDetails(), resultId] as const,
    runResultTaskEvents: (resultId: string) => [...evaluationKeys.runResult(resultId), "taskEvents"] as const,
    runResultArtifactBlob: (resultId: string, filename: string, version?: number) => [...evaluationKeys.runResult(resultId), "artifactBlob", filename, version ?? null] as const,
    runResultExampleArtifactBlob: (resultId: string, role: string, filename: string, version?: number) => [...evaluationKeys.runResult(resultId), "exampleArtifactBlob", role, filename, version ?? null] as const,

    runGroups: () => [...evaluationKeys.all, "runGroups"] as const,
    runGroupsList: (params?: { pageNumber?: number; pageSize?: number; name?: string; agent?: string }) => [...evaluationKeys.runGroups(), "list", params ?? {}] as const,
    runGroupDetails: () => [...evaluationKeys.runGroups(), "detail"] as const,
    runGroup: (groupId: string) => [...evaluationKeys.runGroupDetails(), groupId] as const,
    runGroupResults: (groupId: string) => [...evaluationKeys.runGroup(groupId), "results"] as const,
    runGroupAgents: () => [...evaluationKeys.runGroups(), "agents"] as const,

    watchlist: () => [...evaluationKeys.all, "watchlist"] as const,
    watchlistList: () => [...evaluationKeys.watchlist(), "list"] as const,
    watchlistChart: () => [...evaluationKeys.watchlist(), "chart"] as const,
};
