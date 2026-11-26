// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type {
    AgentSnapshot,
    BeginToolAuthResponse,
    ConfigSnapshot,
    ConfigSnapshotExampleArtifact,
    CreateDatasetRequest,
    CreateEvaluatorRequest,
    CreateExampleRequest,
    CreateExperimentRequest,
    DecisionAnswer,
    EvalArtifact,
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
    EvalRunScore,
    EvalRunStatus,
    EvalScoreDecidedBy,
    EvalScoreDetails,
    EvalScoreEscalationReason,
    EvalTargetType,
    EvalTokenBreakdown,
    EvalTokenCounts,
    EvaluatorQuestion,
    EvaluatorRead,
    EvaluatorRunStats,
    EvaluatorScoring,
    EvaluatorSnapshotEntry,
    ExpectedFacts,
    ExpectedNodeOutcome,
    ExpectedPath,
    ExpectedToolUse,
    ExperimentToolAuth,
    GenerateDatasetRequest,
    GenerateExamplesRequest,
    ImportExamplesResult,
    ModelConfigSnapshot,
    TriggerRunRequest,
    UpdateDatasetRequest,
    UpdateEvaluatorRequest,
    UpdateExampleRequest,
    UpdateExperimentRequest,
    WatchlistAgentChart,
    WatchlistEntry,
} from "@/types/eval";
import type { A2AEventSSEPayload } from "@/types/sse";

import { ApiV2Error, fetchAllPages, instantToIso, pageQuery, type PaginatedResponse, platformV2, withoutNulls } from "../common";

import { artifactVersionQuery, buildQs, EXAMPLE_ARTIFACT_GONE_MESSAGE, exampleArtifactSegment } from "./service";

const EVALUATIONS = "/evaluations";

interface ExperimentModelV2 {
    modelConfigId: string | null;
    model: string;
    alias: string;
}

interface ExperimentV2 {
    id: string;
    name: string;
    description: string | null;
    datasetId: string | null;
    datasetName: string | null;
    datasetUpdatedTime: string | null;
    targetType: EvalTargetType;
    targetAgent: string;
    targetWorkflow: string | null;
    models: ExperimentModelV2[];
    runsPerExample: number;
    maxWorkers: number;
    evaluatorIds: string[];
    primaryEvaluatorId: string | null;
    isDefault: boolean;
    runAs: string | null;
    latestRunStatus: EvalRunStatus | null;
    latestRunTime: string | null;
    createdBy: string;
    updatedBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

interface ModelConfigSnapshotV2 {
    id: string | null;
    alias: string | null;
    provider: string | null;
    modelName: string | null;
    apiBase: string | null;
    modelParams: Record<string, unknown> | null;
    maxInputTokens: number | null;
    description: string | null;
}

interface ExampleArtifactSnapshotV2 {
    filename: string;
    version: number;
    mimeType: string | null;
    size: number | null;
    contentHash: string | null;
}

interface ConfigSnapshotV2 {
    experiment: {
        id: string;
        name: string;
        description: string | null;
        models: (ExperimentModelV2 & { resolvedModel: string | null; modelConfig: ModelConfigSnapshotV2 | null })[];
    } | null;
    dataset: {
        id: string;
        name: string;
        exampleCount: number;
        examples: {
            id: string;
            sequenceNumber: number;
            prompt: string;
            expectedResponse: string | null;
            inputData: Record<string, unknown> | null;
            expectedPath: ExpectedPathV2 | null;
            expectedFacts: ExpectedFacts | null;
            inputArtifact: ExampleArtifactSnapshotV2 | null;
            expectedArtifact: ExampleArtifactSnapshotV2 | null;
        }[];
    } | null;
    workflow: {
        name: string;
        instanceName: string | null;
        inputSchema: Record<string, unknown> | null;
    } | null;
    agent: {
        name: string;
        description: string | null;
        agentType: string | null;
        systemPrompt: string | null;
        toolsets: string[];
        tools: { key: string | null; name: string; description: string | null }[];
        connectors: { id: string; name: string; connectorType: string | null; subtype: string | null }[];
        skills: { id: string | null; name: string; description: string | null }[];
        inputModes: string[];
        outputModes: string[];
        modelConfig: ModelConfigSnapshotV2 | null;
        origin: string | null;
    } | null;
}

interface EvaluatorSnapshotV2 {
    id: string;
    name: string;
    description: string | null;
    evaluatorType: string;
    heuristicType: string | null;
    isDefault: boolean;
    model: string | null;
    promptTemplate: string | null;
    choiceScores: Record<string, number> | null;
    resolvedModelName: string | null;
    modelConfig: ModelConfigSnapshotV2 | null;
}

interface RunV2 {
    id: string;
    experimentId: string;
    runGroupId: string;
    status: EvalRunStatus;
    progressCurrent: number;
    progressTotal: number;
    errorMessage: string | null;
    summaryScores: { evaluatorId: string; score: number }[];
    evaluatorStats: RunEvaluatorStatsV2[];
    model: string | null;
    configSnapshot: ConfigSnapshotV2 | null;
    evaluatorSnapshot: EvaluatorSnapshotV2[];
    runName: string | null;
    source: string;
    runAs: string | null;
    createdBy: string;
    createdTime: string | null;
    completedTime: string | null;
}

interface JudgeTokenUsageV2 {
    inputTokens: number;
    outputTokens: number;
}

interface RunEvaluatorStatsV2 {
    evaluatorId: string;
    scored: number;
    escalated: number;
    withConfidence: number;
    escalatedPercent: number | null;
    meanConfidence: number | null;
    judgeUsage: { decision: JudgeTokenUsageV2 | null; llmJudge: JudgeTokenUsageV2 | null };
}

interface RunGroupV2 {
    id: string;
    experimentId: string;
    runName: string | null;
    status: EvalRunStatus;
    progressCurrent: number;
    progressTotal: number;
    createdTime: string | null;
    completedTime: string | null;
    runs: RunV2[];
    configOutdated: boolean;
}

interface RunGroupSummaryV2 {
    id: string;
    experimentId: string;
    experimentName: string;
    targetType: EvalTargetType;
    targetAgent: string;
    runName: string | null;
    status: EvalRunStatus;
    createdTime: string | null;
    completedTime: string | null;
    durationSeconds: number | null;
    runCount: number;
}

interface RunResultArtifactV2 {
    filename: string;
    mimeType: string | null;
    version: number;
    size: number | null;
}

interface DecisionAnswerV2 {
    choice: string | null;
    probability: number | null;
    score: number | null;
    confidence: number | null;
    probabilities: { option: string; probability: number }[];
    scoringApplied: EvaluatorScoring | null;
}

interface RunScoreDetailsV2 {
    decidedBy: EvalScoreDecidedBy | null;
    model: string | null;
    answer: DecisionAnswerV2 | null;
    escalated: boolean;
    escalationReason: EvalScoreEscalationReason | null;
    providerError: string | null;
    usage: { decision: JudgeTokenUsageV2 | null; llmJudge: JudgeTokenUsageV2 | null } | null;
    durationMs: number | null;
}

interface RunResultScoreV2 {
    key: string;
    evaluatorId: string;
    score: number;
    choice: string | null;
    passed: boolean | null;
    reasoning: string | null;
    details: RunScoreDetailsV2 | null;
    createdTime: string | null;
}

interface RunResultV2 {
    id: string;
    runId: string;
    exampleId: string;
    trialIndex: number;
    inputPrompt: string | null;
    expectedResponse: string | null;
    targetAgent: string | null;
    agentResponse: string | null;
    durationSeconds: number | null;
    errorMessage: string | null;
    failureKind: string | null;
    targetType: EvalTargetType;
    tokenBreakdown: TokenBreakdownV2 | null;
    cost: string | null;
    scores: RunResultScoreV2[];
    artifacts: RunResultArtifactV2[];
    inputArtifact: RunResultArtifactV2 | null;
    expectedArtifact: RunResultArtifactV2 | null;
    createdTime: string | null;
}

interface TokenBreakdownV2 {
    traceId: string | null;
    incomplete: boolean;
    byNode: { nodeId: string; models: string[]; tokens: EvalTokenCounts }[];
    unattributed: EvalTokenCounts;
    byModel: { model: string | null; tokens: EvalTokenCounts }[];
}

interface OperationV2 {
    id: string;
    resourceId: string | null;
    status: string;
    error: { message: string; errorId: string } | null;
}

interface DatasetV2 {
    id: string;
    type: "dataset";
    name: string;
    description: string | null;
    isDefault: boolean;
    exampleCount: number;
    inputDataExampleCount: number;
    experimentCount: number;
    createdBy: string;
    updatedBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

interface ExampleArtifactV2 {
    filename: string;
    mimeType: string;
    size: number;
    version: number;
    contentHash: string;
}

interface ExampleV2 {
    id: string;
    type: "example";
    datasetId: string;
    sequenceNumber: number;
    prompt: string;
    expectedResponse: string | null;
    inputData: Record<string, unknown> | null;
    expectedPath: ExpectedPathV2 | null;
    expectedFacts: ExpectedFacts | null;
    inputArtifact: ExampleArtifactV2 | null;
    expectedArtifact: ExampleArtifactV2 | null;
    createdTime: string | null;
    updatedTime: string | null;
}

interface ExpectedPathV2 {
    nodes: { nodeId: string; expectation: ExpectedNodeOutcome }[];
    tools: { toolName: string; expectation: ExpectedToolUse }[];
}

interface EvaluatorV2 {
    id: string;
    type: "evaluator";
    name: string;
    description: string | null;
    evaluatorType: EvalEvaluator["type"];
    heuristicType: string | null;
    isDefault: boolean;
    model: string | null;
    promptTemplate: string | null;
    choiceScores: Record<string, number> | null;
    question: QuestionV2 | null;
    scoring: EvaluatorScoring | null;
    reads: EvaluatorRead[] | null;
    minConfidence: number | null;
    escalateToEvaluatorId: string | null;
    experimentCount: number;
    createdBy: string;
    updatedBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

interface QuestionV2 {
    type: EvaluatorQuestion["type"];
    instructions: string;
    criteria: { key: string; description: string | null }[] | null;
}

interface ToolAuthV2 {
    runAs: string;
    agents: {
        agentName: string;
        isTarget: boolean;
        addedBy: string | null;
        reachable: boolean;
        tools: {
            credentialKey: string;
            displayName: string | null;
            authType: string | null;
            interactive: boolean;
            status: "connected" | "not_connected";
            expiresTime: string | null;
            lastAuthorizedBy: string | null;
            lastAuthorizedTime: string | null;
        }[];
    }[];
    allowListIsWildcard: boolean;
    totalAgents: number;
    totalRunningAgents: number;
    agentScanLimit: number;
}

interface WatchlistChartSeriesV2 extends WatchlistEntry {
    experiments: {
        experimentId: string;
        experimentName: string;
        primaryEvaluatorId: string;
        primaryEvaluatorName: string | null;
        points: { runGroupId: string; startedTime: string | null; score: number | null }[];
    }[];
}

const optionalIso = (instant: string | null) => instantToIso(instant) || undefined;

function toExperiment(e: ExperimentV2): EvalExperiment {
    return {
        ...withoutNulls({
            id: e.id,
            name: e.name,
            description: e.description,
            datasetName: e.datasetName,
            targetType: e.targetType,
            targetAgent: e.targetAgent,
            targetWorkflow: e.targetWorkflow,
            runsPerExample: e.runsPerExample,
            maxWorkers: e.maxWorkers,
            evaluatorIds: e.evaluatorIds,
            primaryEvaluatorId: e.primaryEvaluatorId,
            runAs: e.runAs,
            latestRunStatus: e.latestRunStatus,
            createdBy: e.createdBy,
            updatedBy: e.updatedBy,
        }),
        datasetId: e.datasetId,
        models: e.models.map(m => withoutNulls(m)),
        latestRunTime: optionalIso(e.latestRunTime),
        datasetUpdatedTime: optionalIso(e.datasetUpdatedTime),
        createdAt: instantToIso(e.createdTime),
        updatedAt: instantToIso(e.updatedTime),
    };
}

const toModelConfigSnapshot = (m: ModelConfigSnapshotV2 | null): ModelConfigSnapshot | null => (m ? withoutNulls(m) : null);

function toExampleArtifactSnapshot(a: ExampleArtifactSnapshotV2 | null): ConfigSnapshotExampleArtifact | null {
    if (!a) return null;
    return withoutNulls({ filename: a.filename, version: a.version, mime_type: a.mimeType, size_bytes: a.size, content_hash: a.contentHash });
}

function toAgentSnapshot(a: NonNullable<ConfigSnapshotV2["agent"]>): AgentSnapshot {
    return withoutNulls({
        name: a.name,
        description: a.description,
        type: a.agentType,
        systemPrompt: a.systemPrompt,
        toolsets: a.toolsets,
        tools: a.tools.map(({ key, ...t }) => withoutNulls({ id: key, ...t })),
        connectors: a.connectors.map(c => withoutNulls({ id: c.id, name: c.name, type: c.connectorType, subtype: c.subtype })),
        skills: a.skills.map(s => withoutNulls(s)),
        inputModes: a.inputModes,
        outputModes: a.outputModes,
        modelConfig: toModelConfigSnapshot(a.modelConfig),
        origin: a.origin,
    });
}

function toConfigSnapshot({ experiment, dataset, agent, workflow }: ConfigSnapshotV2): ConfigSnapshot {
    return withoutNulls({
        experiment: experiment && {
            ...withoutNulls(experiment),
            models: experiment.models.map(m => ({ ...withoutNulls(m), modelConfig: toModelConfigSnapshot(m.modelConfig) })),
        },
        dataset: dataset && {
            id: dataset.id,
            name: dataset.name,
            example_count: dataset.exampleCount,
            examples: dataset.examples.map(ex => ({
                id: ex.id,
                sequence_number: ex.sequenceNumber,
                prompt: ex.prompt,
                expected_response: ex.expectedResponse,
                input_data: ex.inputData,
                expected_path: ex.expectedPath && expectedPathFromWire(ex.expectedPath),
                expected_facts: ex.expectedFacts,
                artifact: toExampleArtifactSnapshot(ex.inputArtifact),
                expected_artifact: toExampleArtifactSnapshot(ex.expectedArtifact),
            })),
        },
        agent: agent && toAgentSnapshot(agent),
        workflow: workflow && withoutNulls({ name: workflow.name, instance_name: workflow.instanceName, input_schema: workflow.inputSchema }),
    });
}

function toEvaluatorSnapshot(e: EvaluatorSnapshotV2): EvaluatorSnapshotEntry {
    return withoutNulls({
        id: e.id,
        name: e.name,
        description: e.description,
        type: e.evaluatorType,
        heuristic_type: e.heuristicType,
        is_default: e.isDefault,
        model: e.model,
        prompt_template: e.promptTemplate,
        choice_scores: e.choiceScores,
        resolved_model_name: e.resolvedModelName,
        modelConfig: toModelConfigSnapshot(e.modelConfig),
    });
}

function toEvaluatorRunStats(s: RunEvaluatorStatsV2): EvaluatorRunStats {
    return withoutNulls({
        scored: s.scored,
        escalated: s.escalated === 0 ? null : s.escalated,
        withConfidence: s.withConfidence === 0 ? null : s.withConfidence,
        escalatedPercent: s.escalatedPercent,
        meanConfidence: s.meanConfidence,
        judgeUsage: withoutNulls(s.judgeUsage),
    });
}

function toRun(r: RunV2): EvalRun {
    return withoutNulls({
        id: r.id,
        experimentId: r.experimentId,
        runGroupId: r.runGroupId,
        status: r.status,
        progressCurrent: r.progressCurrent,
        progressTotal: r.progressTotal,
        errorMessage: r.errorMessage,
        summaryScores: Object.fromEntries(r.summaryScores.map(s => [s.evaluatorId, s.score])),
        evaluatorStats: Object.fromEntries(r.evaluatorStats.map(s => [s.evaluatorId, toEvaluatorRunStats(s)])),
        configSnapshot: r.configSnapshot && toConfigSnapshot(r.configSnapshot),
        evaluatorSnapshot: r.evaluatorSnapshot.map(toEvaluatorSnapshot),
        model: r.model ?? "",
        runName: r.runName,
        source: r.source,
        runAs: r.runAs,
        startedBy: r.createdBy,
        startedAt: instantToIso(r.createdTime),
        completedAt: optionalIso(r.completedTime),
    });
}

function toRunGroup(g: RunGroupV2): EvalRunGroup {
    return withoutNulls({
        id: g.id,
        experimentId: g.experimentId,
        runName: g.runName,
        status: g.status,
        progressCurrent: g.progressCurrent,
        progressTotal: g.progressTotal,
        startedAt: instantToIso(g.createdTime),
        completedAt: optionalIso(g.completedTime),
        runs: g.runs.map(toRun),
        configOutdated: g.configOutdated,
    });
}

function toRunGroupSummary(s: RunGroupSummaryV2): EvalRunGroupSummary {
    return withoutNulls({
        runGroupId: s.id,
        experimentId: s.experimentId,
        experimentName: s.experimentName,
        targetType: s.targetType,
        targetAgent: s.targetAgent,
        runName: s.runName,
        status: s.status,
        startedAt: instantToIso(s.createdTime),
        completedAt: optionalIso(s.completedTime),
        durationSeconds: s.durationSeconds,
        runCount: s.runCount,
    });
}

const toArtifact = (a: RunResultArtifactV2): EvalArtifact => withoutNulls(a);

function toTokenBreakdown(b: TokenBreakdownV2): EvalTokenBreakdown {
    return withoutNulls({
        traceId: b.traceId,
        incomplete: b.incomplete,
        byNode: b.byNode.map(({ nodeId, models, tokens }) => ({ nodeId, models, ...tokens })),
        unattributed: b.unattributed,
        byModel: Object.fromEntries(b.byModel.map(({ model, tokens }) => [model ?? "", tokens])),
    });
}

function expectedPathFromWire(p: ExpectedPathV2): ExpectedPath {
    return withoutNulls({
        nodes: p.nodes.length > 0 ? Object.fromEntries(p.nodes.map(n => [n.nodeId, n.expectation])) : null,
        tools: p.tools.length > 0 ? Object.fromEntries(p.tools.map(t => [t.toolName, t.expectation])) : null,
    });
}

function expectedPathToWire(p: ExpectedPath): ExpectedPathV2 {
    return {
        nodes: Object.entries(p.nodes ?? {}).map(([nodeId, expectation]) => ({ nodeId, expectation })),
        tools: Object.entries(p.tools ?? {}).map(([toolName, expectation]) => ({ toolName, expectation })),
    };
}

function withWireExpectedPath<T extends { expectedPath?: ExpectedPath | null }>(data: T): Omit<T, "expectedPath"> & { expectedPath?: ExpectedPathV2 | null } {
    const { expectedPath, ...rest } = data;
    if (expectedPath === undefined) return rest;
    return { ...rest, expectedPath: expectedPath && expectedPathToWire(expectedPath) };
}

function toDecisionAnswer({ probabilities, ...answer }: DecisionAnswerV2): DecisionAnswer {
    return withoutNulls({
        ...answer,
        probabilities: probabilities.length > 0 ? Object.fromEntries(probabilities.map(p => [p.option, p.probability])) : null,
    });
}

function toScoreDetails({ answer, usage, ...details }: RunScoreDetailsV2): EvalScoreDetails {
    return withoutNulls({
        ...details,
        answer: answer && toDecisionAnswer(answer),
        usage: usage && withoutNulls(usage),
    });
}

function toRunScore({ key, createdTime, details, ...score }: RunResultScoreV2): EvalRunScore {
    return withoutNulls({ ...score, details: details && toScoreDetails(details), id: key, createdAt: instantToIso(createdTime) });
}

function toRunResult(r: RunResultV2): EvalRunResult {
    return {
        ...withoutNulls({
            id: r.id,
            runId: r.runId,
            exampleId: r.exampleId,
            trialIndex: r.trialIndex,
            inputPrompt: r.inputPrompt,
            expectedResponse: r.expectedResponse,
            targetAgent: r.targetAgent,
            agentResponse: r.agentResponse,
            durationSeconds: r.durationSeconds,
            errorMessage: r.errorMessage,
            failureKind: r.failureKind,
            targetType: r.targetType,
        }),
        tokenBreakdown: r.tokenBreakdown && toTokenBreakdown(r.tokenBreakdown),
        cost: r.cost,
        scores: r.scores.map(toRunScore),
        artifacts: r.artifacts.map(toArtifact),
        inputArtifact: r.inputArtifact && toArtifact(r.inputArtifact),
        expectedArtifact: r.expectedArtifact && toArtifact(r.expectedArtifact),
        createdAt: instantToIso(r.createdTime),
    };
}

function toDataset(d: DatasetV2): EvalDataset {
    return withoutNulls({
        id: d.id,
        name: d.name,
        description: d.description,
        exampleCount: d.exampleCount,
        inputDataExampleCount: d.inputDataExampleCount,
        experimentCount: d.experimentCount,
        createdBy: d.createdBy,
        updatedBy: d.updatedBy,
        createdAt: instantToIso(d.createdTime),
        updatedAt: instantToIso(d.updatedTime),
    });
}

function toExample(e: ExampleV2): EvalExample {
    return withoutNulls({
        id: e.id,
        datasetId: e.datasetId,
        sequenceNumber: e.sequenceNumber,
        prompt: e.prompt,
        expectedResponse: e.expectedResponse,
        inputData: e.inputData,
        expectedPath: e.expectedPath && expectedPathFromWire(e.expectedPath),
        expectedFacts: e.expectedFacts,
        inputArtifact: e.inputArtifact,
        expectedArtifact: e.expectedArtifact,
        createdAt: instantToIso(e.createdTime),
        updatedAt: instantToIso(e.updatedTime),
    });
}

function toQuestion({ type, instructions, criteria }: QuestionV2): EvaluatorQuestion {
    return withoutNulls({ type, instructions, criteria: criteria?.map(c => withoutNulls(c)) });
}

function toEvaluator(e: EvaluatorV2): EvalEvaluator {
    return withoutNulls({
        id: e.id,
        name: e.name,
        description: e.description,
        type: e.evaluatorType,
        heuristicType: e.heuristicType,
        isDefault: e.isDefault,
        model: e.model,
        promptTemplate: e.promptTemplate,
        choiceScores: e.choiceScores,
        question: e.question && toQuestion(e.question),
        scoring: e.scoring,
        reads: e.reads,
        minConfidence: e.minConfidence,
        escalateToEvaluatorId: e.escalateToEvaluatorId,
        experimentCount: e.experimentCount,
        createdBy: e.createdBy,
        updatedBy: e.updatedBy,
        createdAt: instantToIso(e.createdTime),
        updatedAt: instantToIso(e.updatedTime),
    });
}

function toToolAuth(t: ToolAuthV2): ExperimentToolAuth {
    return {
        ...t,
        agents: t.agents.map(agent => ({
            ...withoutNulls(agent),
            tools: agent.tools.map(({ expiresTime, lastAuthorizedTime, ...tool }) => withoutNulls({ ...tool, expiresAt: optionalIso(expiresTime), lastAuthorizedAt: optionalIso(lastAuthorizedTime) })),
        })),
    };
}

function toWatchlistChart(s: WatchlistChartSeriesV2): WatchlistAgentChart {
    return {
        ...s,
        experiments: s.experiments.map(e => ({ ...e, points: e.points.map(p => ({ runGroupId: p.runGroupId, startedAt: instantToIso(p.startedTime), score: p.score })) })),
    };
}

async function downloadArtifact(path: string, version: number | undefined, goneMessage?: string): Promise<Blob> {
    try {
        return await platformV2.blob(`${path}${artifactVersionQuery(version)}`);
    } catch (error) {
        if (goneMessage && error instanceof ApiV2Error && error.status === 404) throw new Error(goneMessage, { cause: error });
        throw error;
    }
}

const GENERATION_POLL_MS = 2000;
const GENERATION_POLL_RETRIES = 5;

const wait = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

const RETRYABLE_POLL_STATUSES = new Set([408, 429]);

const isTransientPollError = (error: unknown) => {
    if (error instanceof ApiV2Error) return error.status >= 500 || RETRYABLE_POLL_STATUSES.has(error.status);
    return error instanceof TypeError;
};

async function awaitGeneration(operationPath: string): Promise<OperationV2> {
    let failedPolls = 0;
    for (;;) {
        let operation: OperationV2;
        try {
            operation = await platformV2.get<OperationV2>(operationPath);
        } catch (error) {
            if (!isTransientPollError(error) || failedPolls === GENERATION_POLL_RETRIES) throw error;
            failedPolls++;
            await wait(GENERATION_POLL_MS * 2 ** failedPolls);
            continue;
        }
        failedPolls = 0;
        if (operation.status === "succeeded") return operation;
        if (operation.status !== "pending" && operation.status !== "inProgress") throw new Error(operation.error?.message ?? "Generation failed.");
        await wait(GENERATION_POLL_MS);
    }
}

async function withReason<T>(request: Promise<T>, status: number, reason: string): Promise<T> {
    try {
        return await request;
    } catch (error) {
        if (error instanceof ApiV2Error && error.status === status) throw new Error(reason, { cause: error });
        throw error;
    }
}

const DATASET_NAME_TAKEN = "A dataset with this name already exists.";
const EVALUATOR_NAME_TAKEN = "An evaluator with this name already exists.";

const camelSort = (sort?: string) => sort?.replaceAll(/_([a-z])/g, (_, letter: string) => letter.toUpperCase());

export async function getDatasetsV2(): Promise<EvalDataset[]> {
    const all = await fetchAllPages(params => platformV2.list<DatasetV2>(`${EVALUATIONS}/datasets?${pageQuery(params)}`));
    return all.map(toDataset);
}

export async function getDatasetsPaginatedV2(params?: { pageNumber?: number; pageSize?: number; name?: string; sort?: string }): Promise<PaginatedResponse<EvalDataset>> {
    const page = await platformV2.list<DatasetV2>(`${EVALUATIONS}/datasets${buildQs({ ...params, sort: camelSort(params?.sort) })}`);
    return { ...page, data: page.data.map(toDataset) };
}

export async function getDatasetV2(id: string): Promise<EvalDataset> {
    return toDataset(await platformV2.get<DatasetV2>(`${EVALUATIONS}/datasets/${id}`));
}

export async function createDatasetV2(data: CreateDatasetRequest): Promise<EvalDataset> {
    return toDataset(await withReason(platformV2.post<DatasetV2>(`${EVALUATIONS}/datasets`, data), 409, DATASET_NAME_TAKEN));
}

export async function generateDatasetV2(data: GenerateDatasetRequest): Promise<EvalDataset> {
    const { id } = await platformV2.put<OperationV2>(`${EVALUATIONS}/datasets/generate`, data);
    const { resourceId } = await awaitGeneration(`${EVALUATIONS}/datasetOperations/${id}`);
    if (!resourceId) throw new Error("Generation finished without a dataset.");
    return getDatasetV2(resourceId);
}

export async function updateDatasetV2(id: string, data: UpdateDatasetRequest): Promise<EvalDataset> {
    return toDataset(await withReason(platformV2.patch<DatasetV2>(`${EVALUATIONS}/datasets/${id}`, data), 409, DATASET_NAME_TAKEN));
}

export async function deleteDatasetV2(id: string): Promise<void> {
    await withReason(platformV2.delete(`${EVALUATIONS}/datasets/${id}`), 422, "Cannot delete dataset while one or more of its experiments has active (pending/running) runs. Cancel or wait for runs to complete first.");
}

export async function getExamplesV2(datasetId: string): Promise<EvalExample[]> {
    const all = await fetchAllPages(params => platformV2.list<ExampleV2>(`${EVALUATIONS}/datasets/${datasetId}/examples?${pageQuery(params)}`));
    return all.map(toExample);
}

export async function getExamplesPaginatedV2(datasetId: string, params?: { pageNumber?: number; pageSize?: number; content?: string }): Promise<PaginatedResponse<EvalExample>> {
    const page = await platformV2.list<ExampleV2>(`${EVALUATIONS}/datasets/${datasetId}/examples${buildQs({ ...params })}`);
    return { ...page, data: page.data.map(toExample) };
}

export async function getExampleV2(exampleId: string): Promise<EvalExample> {
    return toExample(await platformV2.get<ExampleV2>(`${EVALUATIONS}/examples/${exampleId}`));
}

export async function createExampleV2(datasetId: string, data: CreateExampleRequest): Promise<EvalExample> {
    return toExample(await platformV2.post<ExampleV2>(`${EVALUATIONS}/datasets/${datasetId}/examples`, withWireExpectedPath(data)));
}

export async function generateExamplesV2(datasetId: string, data: GenerateExamplesRequest): Promise<void> {
    const { id } = await withReason(platformV2.put<OperationV2>(`${EVALUATIONS}/datasets/${datasetId}/examples/generate`, data), 409, "This dataset is already generating examples. Wait for it to finish.");
    await awaitGeneration(`${EVALUATIONS}/datasets/${datasetId}/operations/${id}`);
}

export async function updateExampleV2(exampleId: string, data: UpdateExampleRequest): Promise<EvalExample> {
    return toExample(await platformV2.patch<ExampleV2>(`${EVALUATIONS}/examples/${exampleId}`, withWireExpectedPath(data)));
}

export async function deleteExampleV2(exampleId: string): Promise<void> {
    await platformV2.delete(`${EVALUATIONS}/examples/${exampleId}`);
}

async function withFileReason<T>(request: Promise<T>): Promise<T> {
    try {
        return await request;
    } catch (error) {
        const reasons = error instanceof ApiV2Error ? error.validationDetails?.file : undefined;
        if (reasons?.length) throw new Error(error instanceof ApiV2Error && error.status === 413 ? `The file is too large: it ${reasons.join(" ")}.` : reasons.join(" "), { cause: error });
        throw error;
    }
}

export async function importExamplesV2(datasetId: string, body: string, contentType: string): Promise<ImportExamplesResult> {
    const form = new FormData();
    form.append("file", new File([body], contentType === "text/csv" ? "examples.csv" : "examples.json", { type: contentType }));
    return withFileReason(platformV2.put<ImportExamplesResult>(`${EVALUATIONS}/datasets/${datasetId}/examples/import`, form));
}

export async function exportExamplesV2(datasetId: string, format: "csv" | "json" = "csv"): Promise<Blob> {
    return platformV2.blob(`${EVALUATIONS}/datasets/${datasetId}/examples/export?format=${format}`);
}

export async function uploadExampleArtifactV2(exampleId: string, role: EvalArtifactRole, file: File, description?: string): Promise<EvalExampleArtifact> {
    const form = new FormData();
    form.append("file", file);
    if (description) form.append("description", description);
    return withFileReason(platformV2.put<EvalExampleArtifact>(`${EVALUATIONS}/examples/${exampleId}/${exampleArtifactSegment(role)}`, form));
}

export async function deleteExampleArtifactV2(exampleId: string, role: EvalArtifactRole): Promise<void> {
    await platformV2.delete(`${EVALUATIONS}/examples/${exampleId}/${exampleArtifactSegment(role)}`);
}

export async function getExampleArtifactBlobV2(exampleId: string, role: EvalArtifactRole, filename: string, version?: number): Promise<Blob> {
    return downloadArtifact(`${EVALUATIONS}/examples/${exampleId}/${exampleArtifactSegment(role)}/${encodeURIComponent(filename)}`, version);
}

export async function getEvaluatorsV2(): Promise<EvalEvaluator[]> {
    const all = await fetchAllPages(params => platformV2.list<EvaluatorV2>(`${EVALUATIONS}/evaluators?${pageQuery(params)}`));
    return all.map(toEvaluator);
}

export async function getEvaluatorV2(id: string): Promise<EvalEvaluator> {
    return toEvaluator(await platformV2.get<EvaluatorV2>(`${EVALUATIONS}/evaluators/${id}`));
}

export async function createEvaluatorV2({ type, ...data }: CreateEvaluatorRequest): Promise<EvalEvaluator> {
    return toEvaluator(await withReason(platformV2.post<EvaluatorV2>(`${EVALUATIONS}/evaluators`, { ...data, evaluatorType: type }), 409, EVALUATOR_NAME_TAKEN));
}

export async function updateEvaluatorV2(id: string, data: UpdateEvaluatorRequest): Promise<EvalEvaluator> {
    const request = platformV2.patch<EvaluatorV2>(`${EVALUATIONS}/evaluators/${id}`, data);
    return toEvaluator(await (data.name === undefined ? request : withReason(request, 409, EVALUATOR_NAME_TAKEN)));
}

export async function deleteEvaluatorV2(id: string): Promise<void> {
    await withReason(platformV2.delete(`${EVALUATIONS}/evaluators/${id}`), 422, "Another evaluator escalates to this one. Change or remove its escalation first.");
}

export async function getExperimentsV2(params?: { datasetId?: string; evaluatorId?: string; targetAgent?: string }): Promise<EvalExperiment[]> {
    const all = await fetchAllPages(page => platformV2.list<ExperimentV2>(`${EVALUATIONS}/experiments${buildQs({ ...page, datasetId: params?.datasetId, evaluatorId: params?.evaluatorId, targetAgent: params?.targetAgent })}`));
    return all.map(toExperiment);
}

export async function getExperimentV2(id: string): Promise<EvalExperiment> {
    return toExperiment(await platformV2.get<ExperimentV2>(`${EVALUATIONS}/experiments/${id}`));
}

export async function getSampleExperimentV2(): Promise<EvalExperiment | null> {
    try {
        return toExperiment(await platformV2.get<ExperimentV2>(`${EVALUATIONS}/sampleExperiment`));
    } catch (error) {
        if (error instanceof ApiV2Error && error.status === 404) return null;
        throw error;
    }
}

export async function createExperimentV2(data: CreateExperimentRequest): Promise<EvalExperiment> {
    return toExperiment(await platformV2.post<ExperimentV2>(`${EVALUATIONS}/experiments`, data));
}

export async function updateExperimentV2(id: string, data: UpdateExperimentRequest): Promise<EvalExperiment> {
    return toExperiment(await platformV2.patch<ExperimentV2>(`${EVALUATIONS}/experiments/${id}`, data));
}

export async function deleteExperimentV2(id: string): Promise<void> {
    await platformV2.delete(`${EVALUATIONS}/experiments/${id}`);
}

export async function getExperimentToolAuthV2(experimentId: string): Promise<ExperimentToolAuth> {
    return toToolAuth(await platformV2.get<ToolAuthV2>(`${EVALUATIONS}/experiments/${experimentId}/toolAuth`));
}

export async function addExperimentToolAuthAgentV2(experimentId: string, agentName: string): Promise<void> {
    await platformV2.post(`${EVALUATIONS}/experiments/${experimentId}/toolAuth/agents`, { agentName });
}

export async function removeExperimentToolAuthAgentV2(experimentId: string, agentName: string): Promise<void> {
    await platformV2.delete(`${EVALUATIONS}/experiments/${experimentId}/toolAuth/agents/${encodeURIComponent(agentName)}`);
}

export async function listToolAuthCandidateAgentsV2(experimentId: string): Promise<string[]> {
    const all = await fetchAllPages(params => platformV2.list<{ agentName: string }>(`${EVALUATIONS}/experiments/${experimentId}/toolAuth/candidateAgents?${pageQuery(params)}`));
    return all.map(candidate => candidate.agentName);
}

export async function beginExperimentToolAuthV2(experimentId: string, agentName: string, credentialKey: string): Promise<BeginToolAuthResponse> {
    const { expiresTime, ...authorization } = await platformV2.put<{ authUri: string; credentialKey: string; expiresTime: string | null }>(`${EVALUATIONS}/experiments/${experimentId}/toolAuth/begin`, {
        agentName,
        credentialKey,
    });
    return { ...authorization, expiresAt: instantToIso(expiresTime) };
}

export async function disconnectExperimentToolAuthV2(experimentId: string, agentName: string, credentialKey: string): Promise<void> {
    await platformV2.put(`${EVALUATIONS}/experiments/${experimentId}/toolAuth/disconnect`, { agentName, credentialKey });
}

export async function getRunsV2(experimentId: string): Promise<EvalRun[]> {
    const all = await fetchAllPages(params => platformV2.list<RunV2>(`${EVALUATIONS}/experiments/${experimentId}/runs?${pageQuery(params)}`));
    return all.map(toRun);
}

export async function getRunV2(runId: string): Promise<EvalRun> {
    return toRun(await platformV2.get<RunV2>(`${EVALUATIONS}/runs/${runId}`));
}

export async function triggerRunV2(experimentId: string, request: TriggerRunRequest = {}): Promise<{ runGroupId?: string }> {
    const operation = await platformV2.put<OperationV2>(`${EVALUATIONS}/experiments/${experimentId}/trigger`, request);
    return { runGroupId: operation.id };
}

export async function deleteRunV2(runId: string): Promise<void> {
    await withReason(platformV2.delete(`${EVALUATIONS}/runs/${runId}`), 422, "Cannot delete a run while it is pending or running. Cancel it first.");
}

export async function cancelRunV2(runId: string): Promise<EvalRun> {
    return toRun(await platformV2.put<RunV2>(`${EVALUATIONS}/runs/${runId}/cancel`));
}

export async function getRunResultsV2(runId: string): Promise<EvalRunResult[]> {
    const all = await fetchAllPages(params => platformV2.list<RunResultV2>(`${EVALUATIONS}/runs/${runId}/results?${pageQuery(params)}`));
    return [...new Map(all.map(r => [r.id, r])).values()].map(toRunResult);
}

export async function getRunResultV2(resultId: string): Promise<EvalRunResult> {
    return toRunResult(await platformV2.get<RunResultV2>(`${EVALUATIONS}/runResults/${resultId}`));
}

export async function getRunResultTaskEventsV2(resultId: string): Promise<A2AEventSSEPayload[] | null> {
    const { events } = await platformV2.get<{ events: A2AEventSSEPayload[] }>(`${EVALUATIONS}/runResults/${resultId}/taskEvents`);
    return events.length ? events : null;
}

export async function exportRunResultsV2(runId: string, format: "csv" | "json"): Promise<Blob> {
    return platformV2.blob(`${EVALUATIONS}/runs/${runId}/export?format=${format}`);
}

export async function getRunGroupV2(groupId: string): Promise<EvalRunGroup> {
    return toRunGroup(await platformV2.get<RunGroupV2>(`${EVALUATIONS}/runGroups/${groupId}`));
}

const runsByGroup = new Map<string, Promise<{ id: string; model: string }[]>>();

function groupRuns(groupId: string): Promise<{ id: string; model: string }[]> {
    let runs = runsByGroup.get(groupId);
    if (!runs) {
        runs = getRunGroupV2(groupId).then(group => group.runs.map(run => ({ id: run.id, model: run.model })));
        runs.catch(() => runsByGroup.delete(groupId));
        runsByGroup.set(groupId, runs);
    }
    return runs;
}

export async function getRunGroupResultsV2(groupId: string): Promise<EvalRunGroupResults> {
    const runs = await groupRuns(groupId);
    const outcomes = await Promise.allSettled(runs.map(run => getRunResultsV2(run.id)));
    const resultsByModel: EvalRunGroupResults["resultsByModel"] = {};
    runs.forEach((run, i) => {
        const outcome = outcomes[i];
        if (outcome.status === "fulfilled") {
            resultsByModel[run.model] = [...(resultsByModel[run.model] ?? []), ...outcome.value];
        } else if (!(outcome.reason instanceof ApiV2Error && outcome.reason.status === 404)) {
            throw outcome.reason;
        }
    });
    return { groupId, resultsByModel };
}

export async function deleteRunGroupV2(groupId: string): Promise<void> {
    await withReason(platformV2.delete(`${EVALUATIONS}/runGroups/${groupId}`), 422, "Cannot delete a report while its runs are pending or running. Cancel it first.");
}

export async function listRunGroupsPaginatedV2(params?: { pageNumber?: number; pageSize?: number; name?: string; agent?: string; targetType?: EvalTargetType }): Promise<PaginatedResponse<EvalRunGroupSummary>> {
    const page = await platformV2.list<RunGroupSummaryV2>(`${EVALUATIONS}/runGroups${buildQs({ pageNumber: params?.pageNumber, pageSize: params?.pageSize, name: params?.name, targetAgent: params?.agent, targetType: params?.targetType })}`);
    return { ...page, data: page.data.map(toRunGroupSummary) };
}

export async function listRunGroupAgentsV2(): Promise<EvalReportTarget[]> {
    const all = await fetchAllPages(params => platformV2.list<{ targetAgent: string; targetType: EvalTargetType }>(`${EVALUATIONS}/runGroupAgents?${pageQuery(params)}`));
    return all.map(({ targetAgent, targetType }) => ({ name: targetAgent, targetType }));
}

export async function getWatchlistV2(): Promise<WatchlistEntry[]> {
    return fetchAllPages(params => platformV2.list<WatchlistEntry>(`${EVALUATIONS}/watchlist?${pageQuery(params)}`));
}

export async function setWatchlistV2(agentNames: string[]): Promise<WatchlistEntry[]> {
    const page = await platformV2.listPut<WatchlistEntry>(`${EVALUATIONS}/watchlist?${pageQuery({ pageNumber: 1, pageSize: 100 })}`, { agentNames });
    return page.meta.pagination.nextPage == null ? page.data : getWatchlistV2();
}

export async function getWatchlistChartDataV2(): Promise<WatchlistAgentChart[]> {
    const all = await fetchAllPages(params => platformV2.list<WatchlistChartSeriesV2>(`${EVALUATIONS}/watchlist/chartData?${pageQuery(params)}`));
    return all.map(toWatchlistChart);
}

export async function getRunResultArtifactBlobV2(resultId: string, filename: string, version?: number): Promise<Blob> {
    return downloadArtifact(`${EVALUATIONS}/runResults/${resultId}/artifacts/${encodeURIComponent(filename)}`, version);
}

export async function getRunResultExampleArtifactBlobV2(resultId: string, role: EvalArtifactRole, filename: string, version?: number): Promise<Blob> {
    return downloadArtifact(`${EVALUATIONS}/runResults/${resultId}/${exampleArtifactSegment(role)}/${encodeURIComponent(filename)}`, version, EXAMPLE_ARTIFACT_GONE_MESSAGE);
}
