// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface EvalDataset {
    id: string;
    name: string;
    description?: string;
    exampleCount: number;
    inputDataExampleCount?: number;
    experimentCount: number;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export interface ImportExamplesResult {
    created: number;
    skipped: number;
    dropped?: number;
    limit?: number;
}

export type EvalArtifactRole = "input" | "expected";

export interface EvalExampleArtifact {
    filename: string;
    mimeType?: string;
    size?: number;
    version: number;
    contentHash?: string;
}

export interface EvalExample {
    id: string;
    datasetId: string;
    sequenceNumber: number;
    prompt: string;
    expectedResponse?: string;
    inputData?: Record<string, unknown> | null;
    expectedPath?: ExpectedPath | null;
    expectedFacts?: ExpectedFacts | null;
    inputArtifact?: EvalExampleArtifact | null;
    expectedArtifact?: EvalExampleArtifact | null;
    createdAt: string;
    updatedAt: string;
}

export type EvalEvaluatorType = "llm_judge" | "heuristic" | "decision";

export type EvaluatorQuestionType = "boolean" | "choice" | "score";

export interface EvaluatorCriterion {
    key: string;
    description?: string;
}

export interface EvaluatorQuestion {
    type: EvaluatorQuestionType;
    instructions: string;
    criteria?: EvaluatorCriterion[];
}

export type EvaluatorScoring = "chosen" | "expected";

export type EvaluatorRead = "prompt" | "response" | "expected_response" | "transcript" | "artifacts";

export interface EvalEvaluator {
    id: string;
    name: string;
    description?: string;
    type: EvalEvaluatorType;
    heuristicType?: string;
    isDefault: boolean;
    model?: string;
    promptTemplate?: string;
    choiceScores?: Record<string, number>;
    question?: EvaluatorQuestion;
    scoring?: EvaluatorScoring;
    reads?: EvaluatorRead[];
    minConfidence?: number;
    escalateToEvaluatorId?: string;
    experimentCount: number;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export interface ExperimentModelConfig {
    modelConfigId?: string;
    model: string;
    alias: string;
}

export interface ModelConfigSnapshot {
    id?: string;
    alias?: string;
    provider?: string | null;
    modelName?: string | null;
    apiBase?: string | null;
    modelParams?: Record<string, unknown>;
    maxInputTokens?: number | null;
    description?: string | null;
}

export interface AgentConnectorSnapshot {
    id: string;
    name: string;
    type?: string;
    subtype?: string;
}

export interface AgentSkillSnapshot {
    id?: string;
    name: string;
    description?: string;
}

export interface AgentToolSnapshot {
    id?: string;
    name: string;
    description?: string;
}

export interface AgentSnapshot {
    id?: string;
    name: string;
    description?: string;
    type?: string;
    systemPrompt?: string | null;
    toolsets?: string[];
    tools?: AgentToolSnapshot[];
    connectors?: AgentConnectorSnapshot[];
    skills?: AgentSkillSnapshot[];
    inputModes?: string[];
    outputModes?: string[];
    modelConfig?: ModelConfigSnapshot | null;
    origin?: "discovered" | string;
    createdAt?: number;
    updatedAt?: number;
    createdBy?: string;
    updatedBy?: string;
}

export interface ExperimentModelSnapshotItem extends ExperimentModelConfig {
    resolvedModel?: string;
    modelConfig?: ModelConfigSnapshot | null;
}

export interface ConfigSnapshotExperiment {
    id?: string;
    name?: string;
    description?: string;
    dataset_id?: string;
    models?: ExperimentModelSnapshotItem[];
    [key: string]: unknown;
}

export interface ConfigSnapshotExampleArtifact {
    filename: string;
    version: number;
    mime_type?: string;
    size_bytes?: number;
    content_hash?: string;
    artifact_session_id?: string;
}

export interface ConfigSnapshotDataset {
    id?: string;
    name?: string;
    example_count?: number;
    examples?: {
        id: string;
        sequence_number: number;
        prompt: string;
        expected_response?: string | null;
        input_data?: Record<string, unknown> | null;
        expected_path?: ExpectedPath | null;
        expected_facts?: ExpectedFacts | null;
        artifact?: ConfigSnapshotExampleArtifact | null;
        expected_artifact?: ConfigSnapshotExampleArtifact | null;
    }[];
}

export interface ConfigSnapshotWorkflow {
    name: string;
    instance_name?: string;
    input_schema?: Record<string, unknown>;
}

export interface ConfigSnapshot {
    experiment?: ConfigSnapshotExperiment;
    dataset?: ConfigSnapshotDataset;
    agent?: AgentSnapshot;
    workflow?: ConfigSnapshotWorkflow;
}

export interface DecisionModelSnapshot {
    config_id?: string;
    ref: string;
    provider: string;
    model?: string;
    api_base?: string;
    max_state_bytes?: number;
    timeout_seconds?: number;
}

export interface EvaluatorDecisionConfigSnapshot {
    question: EvaluatorQuestion;
    scoring: EvaluatorScoring;
    reads: EvaluatorRead[];
    min_confidence?: number;
    escalate_to_evaluator_id?: string;
    resolved_model?: DecisionModelSnapshot;
    escalate_to?: EvaluatorSnapshotEntry;
}

export interface EvaluatorSnapshotEntry {
    id: string;
    name: string;
    description?: string;
    type?: string;
    heuristic_type?: string;
    is_default?: boolean;
    model?: string;
    prompt_template?: string;
    choice_scores?: Record<string, number>;
    resolved_model_name?: string;
    modelConfig?: ModelConfigSnapshot | null;
    decision_config?: EvaluatorDecisionConfigSnapshot;
    created_time?: number;
    updated_time?: number;
}

export interface EvalExperiment {
    id: string;
    name: string;
    description?: string;
    datasetId: string | null;
    datasetName?: string;
    targetType?: EvalTargetType;
    targetAgent: string;
    targetWorkflow?: string | null;
    models: ExperimentModelConfig[];
    runsPerExample: number;
    maxWorkers: number;
    primaryEvaluatorId?: string;
    evaluatorIds: string[];
    runAs?: string;
    latestRunStatus?: EvalRunStatus;
    latestRunTime?: string;
    datasetUpdatedTime?: string;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export interface ExperimentToolAuthItem {
    credentialKey: string;
    displayName?: string;
    authType?: string;
    interactive: boolean;
    status: "connected" | "not_connected";
    expiresAt?: string;
    lastAuthorizedBy?: string;
    lastAuthorizedAt?: string;
}

export interface ExperimentToolAuthAgent {
    agentName: string;
    isTarget: boolean;
    addedBy?: string;
    nodeIds?: string[];
    reachable: boolean;
    tools: ExperimentToolAuthItem[];
}

export interface ExperimentToolAuth {
    runAs: string;
    agents: ExperimentToolAuthAgent[];
    allowListIsWildcard: boolean;
    totalAgents: number;
    totalRunningAgents: number;
    agentScanLimit: number;
}

export interface BeginToolAuthResponse {
    authUri: string;
    credentialKey: string;
    expiresAt: string;
}

export interface JudgeTokenUsage {
    inputTokens: number;
    outputTokens: number;
}

export interface JudgeUsage {
    decision?: JudgeTokenUsage;
    llmJudge?: JudgeTokenUsage;
}

export interface EvaluatorRunStats {
    scored: number;
    escalated?: number;
    withConfidence?: number;
    escalatedPercent?: number;
    meanConfidence?: number;
    judgeUsage: JudgeUsage;
}

export type EvalRunStatus = "pending" | "running" | "completed" | "completed_with_warnings" | "failed" | "cancelled";

export type EvalTargetType = "agent" | "workflow";

export interface EvalReportTarget {
    name: string;
    targetType: EvalTargetType;
}

export type EvalFailureKind = "auth_required" | "node_failed" | "paused_for_input" | "timeout" | "cancelled" | "invalid_input" | "rejected" | "partial_failure";

export interface EvalTokenCounts {
    inputTokens: number;
    cachedInputTokens: number;
    outputTokens: number;
    reasoningTokens: number;
    totalTokens: number;
}

export interface EvalNodeTokenUsage extends EvalTokenCounts {
    nodeId: string;
    models: string[];
}

export interface EvalTokenBreakdown {
    traceId?: string;
    incomplete: boolean;
    byNode: EvalNodeTokenUsage[];
    unattributed?: EvalTokenCounts;
    byModel: Record<string, EvalTokenCounts>;
}

export interface EvalRun {
    id: string;
    experimentId: string;
    runGroupId: string;
    status: EvalRunStatus;
    progressCurrent: number;
    progressTotal: number;
    errorMessage?: string;
    summaryScores?: Record<string, number>;
    evaluatorStats?: Record<string, EvaluatorRunStats>;
    configSnapshot?: ConfigSnapshot;
    evaluatorSnapshot?: EvaluatorSnapshotEntry[];
    model: string;
    runName?: string;
    source: string;
    runAs?: string;
    startedBy: string;
    startedAt: string;
    completedAt?: string;
}

export interface EvalRunGroup {
    id: string;
    experimentId: string;
    runName?: string;
    status: EvalRunStatus;
    progressCurrent: number;
    progressTotal: number;
    startedAt: string;
    completedAt?: string;
    runs: EvalRun[];
    configOutdated: boolean;
}

export interface EvalRunGroupResults {
    groupId: string;
    resultsByModel: Record<string, EvalRunResult[]>;
}

export interface EvalRunGroupSummary {
    runGroupId: string;
    experimentId: string;
    experimentName: string;
    targetAgent: string;
    targetType?: EvalTargetType;
    runName?: string;
    status: EvalRunStatus;
    startedAt: string;
    completedAt?: string;
    durationSeconds?: number;
    runCount: number;
}

export interface DecisionAnswer {
    choice?: string;
    probability?: number;
    score?: number;
    confidence?: number;
    probabilities?: Record<string, number>;
    scoringApplied?: EvaluatorScoring;
}

export type EvalScoreDecidedBy = "decision" | "llm_judge";
export type EvalScoreEscalationReason = "low_confidence" | "no_confidence" | "provider_error" | "model_lookup_failed";

export interface EvalScoreDetails {
    decidedBy?: EvalScoreDecidedBy;
    model?: string;
    answer?: DecisionAnswer;
    escalated?: boolean;
    escalationReason?: EvalScoreEscalationReason;
    providerError?: string;
    usage?: JudgeUsage;
    durationMs?: number;
}

export interface EvalRunScore {
    id: string;
    evaluatorId: string;
    score: number;
    choice?: string;
    passed?: boolean;
    reasoning?: string;
    details?: EvalScoreDetails;
    createdAt: string;
}

export interface EvalArtifact {
    filename: string;
    mimeType?: string;
    version?: number;
    size?: number;
    snapshotFailed?: boolean;
    snapshotFailureReason?: string;
}

export interface EvalRunResult {
    id: string;
    runId: string;
    exampleId: string;
    trialIndex: number;
    inputPrompt?: string;
    expectedResponse?: string;
    targetAgent?: string;
    targetType?: EvalTargetType;
    agentResponse?: string;
    durationSeconds?: number;
    errorMessage?: string;
    failureKind?: string;
    tokenBreakdown?: EvalTokenBreakdown | null;
    cost?: string | null;
    scores: EvalRunScore[];
    artifacts?: EvalArtifact[];
    inputArtifact?: EvalArtifact | null;
    expectedArtifact?: EvalArtifact | null;
    createdAt: string;
}

export interface CreateDatasetRequest {
    name: string;
    description: string;
}

export interface GenerateDatasetRequest {
    targetAgent: string;
    numExamples: number;
    focus?: string;
}

export interface GenerateExamplesRequest {
    targetAgent: string;
    numExamples: number;
    focus?: string;
}

export interface UpdateDatasetRequest {
    name?: string;
    description?: string;
}

export interface CreateExampleRequest {
    prompt: string;
    expectedResponse?: string;
    inputData?: Record<string, unknown>;
    expectedPath?: ExpectedPath;
    expectedFacts?: ExpectedFacts;
}

export interface UpdateExampleRequest {
    prompt?: string;
    expectedResponse?: string;
    inputData?: Record<string, unknown> | null;
    expectedPath?: ExpectedPath | null;
    expectedFacts?: ExpectedFacts | null;
}

export interface ExpectedFacts {
    required?: string[];
    forbidden?: string[];
}

export type ExpectedNodeOutcome = "ran" | "not_ran" | "succeeded" | "failed" | "skipped";

export type ExpectedToolUse = "called" | "not_called";

export interface ExpectedPath {
    nodes?: Record<string, ExpectedNodeOutcome>;
    tools?: Record<string, ExpectedToolUse>;
}

export interface CreateEvaluatorRequest {
    name: string;
    description?: string;
    type?: Exclude<EvalEvaluatorType, "heuristic">;
    model?: string;
    promptTemplate?: string;
    choiceScores?: Record<string, number>;
    question?: EvaluatorQuestion;
    scoring?: EvaluatorScoring;
    reads?: EvaluatorRead[];
    minConfidence?: number;
    escalateToEvaluatorId?: string;
}

export interface UpdateEvaluatorRequest {
    name?: string;
    description?: string;
    model?: string | null;
    promptTemplate?: string;
    choiceScores?: Record<string, number> | null;
    question?: EvaluatorQuestion;
    scoring?: EvaluatorScoring;
    reads?: EvaluatorRead[];
    minConfidence?: number | null;
    escalateToEvaluatorId?: string | null;
}

export interface CreateExperimentRequest {
    name: string;
    description?: string;
    datasetId: string;
    targetAgent?: string;
    targetWorkflow?: string;
    models: ExperimentModelConfig[];
    runsPerExample: number;
    maxWorkers?: number;
    evaluatorIds: string[];
    primaryEvaluatorId?: string;
}

export type UpdateExperimentRequest = Partial<CreateExperimentRequest>;

export interface TriggerRunRequest {
    runName?: string;
}

export interface WatchlistEntry {
    agentName: string;
    targetType?: EvalTargetType;
    resolvedModelProvider: string;
    isBuiltIn: boolean;
    position: number;
}

export interface WatchlistChartPoint {
    runGroupId: string;
    startedAt: string;
    score: number | null;
}

export interface WatchlistExperimentSeries {
    experimentId: string;
    experimentName: string;
    primaryEvaluatorId: string;
    primaryEvaluatorName?: string | null;
    points: WatchlistChartPoint[];
}

export interface WatchlistAgentChart {
    agentName: string;
    targetType?: EvalTargetType;
    resolvedModelProvider: string;
    isBuiltIn: boolean;
    position: number;
    experiments: WatchlistExperimentSeries[];
}
