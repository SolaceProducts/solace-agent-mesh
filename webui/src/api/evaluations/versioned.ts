// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type {
    BeginToolAuthResponse,
    CreateDatasetRequest,
    CreateEvaluatorRequest,
    CreateExampleRequest,
    CreateExperimentRequest,
    EvalArtifactRole,
    EvalDataset,
    EvalEvaluator,
    EvalExample,
    EvalExampleArtifact,
    EvalExperiment,
    EvalReportTarget,
    EvalRun,
    EvalRunGroup,
    EvalRunGroupResults,
    EvalRunGroupSummary,
    EvalRunResult,
    EvalTargetType,
    ExperimentToolAuth,
    GenerateDatasetRequest,
    GenerateExamplesRequest,
    ImportExamplesResult,
    TriggerRunRequest,
    UpdateDatasetRequest,
    UpdateEvaluatorRequest,
    UpdateExampleRequest,
    UpdateExperimentRequest,
    WatchlistAgentChart,
    WatchlistEntry,
} from "@/types/eval";
import type { A2AEventSSEPayload } from "@/types/sse";

import type { PaginatedResponse } from "../common";

import {
    addExperimentToolAuthAgentV2,
    beginExperimentToolAuthV2,
    cancelRunV2,
    createDatasetV2,
    createEvaluatorV2,
    createExampleV2,
    createExperimentV2,
    deleteDatasetV2,
    deleteEvaluatorV2,
    deleteExampleArtifactV2,
    deleteExampleV2,
    deleteExperimentV2,
    deleteRunGroupV2,
    deleteRunV2,
    disconnectExperimentToolAuthV2,
    exportExamplesV2,
    exportRunResultsV2,
    generateDatasetV2,
    generateExamplesV2,
    getDatasetsPaginatedV2,
    getDatasetsV2,
    getDatasetV2,
    getEvaluatorsV2,
    getEvaluatorV2,
    getExampleArtifactBlobV2,
    getExamplesPaginatedV2,
    getExamplesV2,
    getExampleV2,
    getExperimentsV2,
    getExperimentToolAuthV2,
    getExperimentV2,
    getRunGroupResultsV2,
    getRunGroupV2,
    getRunResultArtifactBlobV2,
    getRunResultExampleArtifactBlobV2,
    getRunResultsV2,
    getRunResultTaskEventsV2,
    getRunResultV2,
    getRunsV2,
    getRunV2,
    getSampleExperimentV2,
    getWatchlistChartDataV2,
    getWatchlistV2,
    importExamplesV2,
    listRunGroupAgentsV2,
    listRunGroupsPaginatedV2,
    listToolAuthCandidateAgentsV2,
    removeExperimentToolAuthAgentV2,
    setWatchlistV2,
    triggerRunV2,
    updateDatasetV2,
    updateEvaluatorV2,
    updateExampleV2,
    updateExperimentV2,
    uploadExampleArtifactV2,
} from "./v2";

export interface EvaluationsApi {
    getDatasets: () => Promise<EvalDataset[]>;
    getDatasetsPaginated: (params?: { pageNumber?: number; pageSize?: number; name?: string; sort?: string }) => Promise<PaginatedResponse<EvalDataset>>;
    getDataset: (id: string) => Promise<EvalDataset>;
    createDataset: (data: CreateDatasetRequest) => Promise<EvalDataset>;
    generateDataset: (data: GenerateDatasetRequest) => Promise<EvalDataset>;
    updateDataset: (id: string, data: UpdateDatasetRequest) => Promise<EvalDataset>;
    deleteDataset: (id: string) => Promise<void>;
    getExamples: (datasetId: string) => Promise<EvalExample[]>;
    getExamplesPaginated: (datasetId: string, params?: { pageNumber?: number; pageSize?: number; content?: string }) => Promise<PaginatedResponse<EvalExample>>;
    getExample: (exampleId: string) => Promise<EvalExample>;
    createExample: (datasetId: string, data: CreateExampleRequest) => Promise<EvalExample>;
    generateExamples: (datasetId: string, data: GenerateExamplesRequest) => Promise<void>;
    updateExample: (exampleId: string, data: UpdateExampleRequest) => Promise<EvalExample>;
    deleteExample: (exampleId: string) => Promise<void>;
    importExamples: (datasetId: string, body: string, contentType: string) => Promise<ImportExamplesResult>;
    exportExamples: (datasetId: string, format?: "csv" | "json") => Promise<Blob>;
    uploadExampleArtifact: (exampleId: string, role: EvalArtifactRole, file: File, description?: string) => Promise<EvalExampleArtifact>;
    deleteExampleArtifact: (exampleId: string, role: EvalArtifactRole) => Promise<void>;
    getExampleArtifactBlob: (exampleId: string, role: EvalArtifactRole, filename: string, version?: number) => Promise<Blob>;
    getEvaluators: () => Promise<EvalEvaluator[]>;
    getEvaluator: (id: string) => Promise<EvalEvaluator>;
    createEvaluator: (data: CreateEvaluatorRequest) => Promise<EvalEvaluator>;
    updateEvaluator: (id: string, data: UpdateEvaluatorRequest) => Promise<EvalEvaluator>;
    deleteEvaluator: (id: string) => Promise<void>;
    getExperiments: (params?: { datasetId?: string; evaluatorId?: string; targetAgent?: string }) => Promise<EvalExperiment[]>;
    getExperiment: (id: string) => Promise<EvalExperiment>;
    getSampleExperiment: () => Promise<EvalExperiment | null>;
    createExperiment: (data: CreateExperimentRequest) => Promise<EvalExperiment>;
    updateExperiment: (id: string, data: UpdateExperimentRequest) => Promise<EvalExperiment>;
    deleteExperiment: (id: string) => Promise<void>;
    getExperimentToolAuth: (experimentId: string) => Promise<ExperimentToolAuth>;
    addExperimentToolAuthAgent: (experimentId: string, agentName: string) => Promise<void>;
    removeExperimentToolAuthAgent: (experimentId: string, agentName: string) => Promise<void>;
    listToolAuthCandidateAgents: (experimentId: string) => Promise<string[]>;
    beginExperimentToolAuth: (experimentId: string, agentName: string, credentialKey: string) => Promise<BeginToolAuthResponse>;
    disconnectExperimentToolAuth: (experimentId: string, agentName: string, credentialKey: string) => Promise<void>;
    getRuns: (experimentId: string) => Promise<EvalRun[]>;
    getRun: (runId: string) => Promise<EvalRun>;
    triggerRun: (experimentId: string, request?: TriggerRunRequest) => Promise<{ runGroupId?: string }>;
    deleteRun: (runId: string) => Promise<void>;
    cancelRun: (runId: string) => Promise<EvalRun>;
    getRunResults: (runId: string) => Promise<EvalRunResult[]>;
    getRunResult: (resultId: string) => Promise<EvalRunResult>;
    getRunResultTaskEvents: (resultId: string) => Promise<A2AEventSSEPayload[] | null>;
    exportRunResults: (runId: string, format: "csv" | "json") => Promise<Blob>;
    getRunGroup: (groupId: string) => Promise<EvalRunGroup>;
    getRunGroupResults: (groupId: string) => Promise<EvalRunGroupResults>;
    deleteRunGroup: (groupId: string) => Promise<void>;
    listRunGroupsPaginated: (params?: { pageNumber?: number; pageSize?: number; name?: string; agent?: string; targetType?: EvalTargetType }) => Promise<PaginatedResponse<EvalRunGroupSummary>>;
    listRunGroupAgents: () => Promise<EvalReportTarget[]>;
    getWatchlist: () => Promise<WatchlistEntry[]>;
    setWatchlist: (agentNames: string[]) => Promise<WatchlistEntry[]>;
    getWatchlistChartData: () => Promise<WatchlistAgentChart[]>;
    getRunResultArtifactBlob: (resultId: string, filename: string, version?: number) => Promise<Blob>;
    getRunResultExampleArtifactBlob: (resultId: string, role: EvalArtifactRole, filename: string, version?: number) => Promise<Blob>;
}

export const evaluationsApi: EvaluationsApi = {
    createDataset: createDatasetV2,
    createEvaluator: createEvaluatorV2,
    createExample: createExampleV2,
    deleteDataset: deleteDatasetV2,
    deleteEvaluator: deleteEvaluatorV2,
    deleteExample: deleteExampleV2,
    deleteExampleArtifact: deleteExampleArtifactV2,
    exportExamples: exportExamplesV2,
    generateDataset: generateDatasetV2,
    generateExamples: generateExamplesV2,
    getDataset: getDatasetV2,
    getDatasets: getDatasetsV2,
    getDatasetsPaginated: getDatasetsPaginatedV2,
    getEvaluator: getEvaluatorV2,
    getEvaluators: getEvaluatorsV2,
    getExample: getExampleV2,
    getExampleArtifactBlob: getExampleArtifactBlobV2,
    getExamples: getExamplesV2,
    getExamplesPaginated: getExamplesPaginatedV2,
    importExamples: importExamplesV2,
    updateDataset: updateDatasetV2,
    updateEvaluator: updateEvaluatorV2,
    updateExample: updateExampleV2,
    uploadExampleArtifact: uploadExampleArtifactV2,
    getExperiments: getExperimentsV2,
    getExperiment: getExperimentV2,
    getSampleExperiment: getSampleExperimentV2,
    createExperiment: createExperimentV2,
    updateExperiment: updateExperimentV2,
    deleteExperiment: deleteExperimentV2,
    getExperimentToolAuth: getExperimentToolAuthV2,
    addExperimentToolAuthAgent: addExperimentToolAuthAgentV2,
    removeExperimentToolAuthAgent: removeExperimentToolAuthAgentV2,
    listToolAuthCandidateAgents: listToolAuthCandidateAgentsV2,
    beginExperimentToolAuth: beginExperimentToolAuthV2,
    disconnectExperimentToolAuth: disconnectExperimentToolAuthV2,
    getRuns: getRunsV2,
    getRun: getRunV2,
    triggerRun: triggerRunV2,
    deleteRun: deleteRunV2,
    cancelRun: cancelRunV2,
    getRunResults: getRunResultsV2,
    getRunResult: getRunResultV2,
    getRunResultTaskEvents: getRunResultTaskEventsV2,
    exportRunResults: exportRunResultsV2,
    getRunGroup: getRunGroupV2,
    getRunGroupResults: getRunGroupResultsV2,
    deleteRunGroup: deleteRunGroupV2,
    listRunGroupsPaginated: listRunGroupsPaginatedV2,
    listRunGroupAgents: listRunGroupAgentsV2,
    getWatchlist: getWatchlistV2,
    setWatchlist: setWatchlistV2,
    getWatchlistChartData: getWatchlistChartDataV2,
    getRunResultArtifactBlob: getRunResultArtifactBlobV2,
    getRunResultExampleArtifactBlob: getRunResultExampleArtifactBlobV2,
};
