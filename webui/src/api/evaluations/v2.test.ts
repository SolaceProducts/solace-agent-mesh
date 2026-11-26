// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { decisionChip, decisionProbabilityBars } from "@/features/evaluations/components/decisionScore";

import { ApiV2Error } from "../common";

import { EXAMPLE_ARTIFACT_GONE_MESSAGE } from "./service";
import {
    addExperimentToolAuthAgentV2,
    beginExperimentToolAuthV2,
    cancelRunV2,
    createDatasetV2,
    createEvaluatorV2,
    createExampleV2,
    deleteDatasetV2,
    deleteEvaluatorV2,
    deleteExampleArtifactV2,
    deleteExampleV2,
    deleteRunGroupV2,
    deleteRunV2,
    disconnectExperimentToolAuthV2,
    exportExamplesV2,
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
    getRunResultArtifactBlobV2,
    getRunResultExampleArtifactBlobV2,
    getRunResultsV2,
    getRunResultTaskEventsV2,
    getRunV2,
    getSampleExperimentV2,
    getWatchlistChartDataV2,
    importExamplesV2,
    listRunGroupAgentsV2,
    listRunGroupsPaginatedV2,
    removeExperimentToolAuthAgentV2,
    setWatchlistV2,
    triggerRunV2,
    updateDatasetV2,
    updateEvaluatorV2,
    updateExampleV2,
    updateExperimentV2,
    uploadExampleArtifactV2,
} from "./v2";

const BASE = "/api/v2/platform/evaluations";

const experiment = (overrides: Record<string, unknown> = {}) => ({
    id: "exp-1",
    type: "experiment",
    name: "Smoke",
    description: null,
    datasetId: null,
    datasetName: null,
    datasetUpdatedTime: null,
    targetType: "agent",
    targetAgent: "Orchestrator",
    targetWorkflow: null,
    models: [{ modelConfigId: null, model: "general", alias: "general" }],
    runsPerExample: 1,
    maxWorkers: 4,
    evaluatorIds: ["ev-1"],
    primaryEvaluatorId: "ev-1",
    isDefault: false,
    runAs: null,
    latestRunStatus: null,
    latestRunTime: null,
    createdBy: "u1",
    updatedBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const run = (overrides: Record<string, unknown> = {}) => ({
    id: "run-1",
    type: "run",
    experimentId: "exp-1",
    runGroupId: "group-1",
    status: "completed",
    progressCurrent: 1,
    progressTotal: 1,
    errorMessage: null,
    summaryScores: [{ evaluatorId: "ev-1", score: 0.5 }],
    evaluatorStats: [],
    model: null,
    configSnapshot: {
        experiment: { id: "exp-1", name: "Smoke", description: null, models: [] },
        dataset: {
            id: "ds-1",
            name: "Golden",
            exampleCount: 1,
            examples: [
                {
                    id: "ex-1",
                    sequenceNumber: 1,
                    prompt: "hi",
                    expectedResponse: null,
                    inputData: null,
                    expectedPath: null,
                    expectedFacts: null,
                    inputArtifact: { filename: "in.txt", version: 2, mimeType: "text/plain", size: 3, contentHash: null },
                    expectedArtifact: null,
                },
            ],
        },
        agent: null,
        workflow: null,
    },
    evaluatorSnapshot: [
        {
            id: "ev-1",
            name: "Judge",
            description: null,
            evaluatorType: "llm_judge",
            heuristicType: null,
            isDefault: true,
            model: null,
            promptTemplate: "p",
            choiceScores: { A: 1 },
            resolvedModelName: "gpt",
            modelConfig: null,
        },
    ],
    runName: "Smoke run",
    source: "manual",
    runAs: null,
    createdBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    completedTime: null,
    ...overrides,
});

const runGroup = (runs: unknown[]) => ({
    id: "group-1",
    type: "runGroup",
    experimentId: "exp-1",
    runName: "Smoke run",
    status: "completed",
    progressCurrent: 1,
    progressTotal: 1,
    createdTime: "2026-01-02T03:04:05.678Z",
    completedTime: null,
    runs,
    configOutdated: false,
});

const result = (id: string, runId: string) => ({
    id,
    type: "runResult",
    runId,
    exampleId: "ex-1",
    trialIndex: 0,
    inputPrompt: "hi",
    expectedResponse: null,
    targetAgent: "Orchestrator",
    agentResponse: "hello",
    durationSeconds: 1,
    errorMessage: null,
    failureKind: null,
    targetType: "agent",
    tokenBreakdown: null,
    cost: null,
    scores: [{ key: "s1", evaluatorId: "ev-1", score: 1, choice: null, passed: true, reasoning: null, details: null, createdTime: "2026-01-02T03:04:05.678Z" }],
    artifacts: [],
    inputArtifact: null,
    expectedArtifact: null,
    createdTime: "2026-01-02T03:04:05.678Z",
});

const dataset = (overrides: Record<string, unknown> = {}) => ({
    id: "ds-1",
    type: "dataset",
    name: "Golden",
    description: null,
    exampleCount: 2,
    inputDataExampleCount: 0,
    experimentCount: 1,
    isDefault: false,
    createdBy: "u1",
    updatedBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const exampleFile = { filename: "in.txt", mimeType: "text/plain", size: 3, version: 2, contentHash: "abc" };

const example = (overrides: Record<string, unknown> = {}) => ({
    id: "ex-1",
    type: "example",
    datasetId: "ds-1",
    sequenceNumber: 1,
    prompt: "hi",
    expectedResponse: null,
    inputData: null,
    expectedPath: null,
    expectedFacts: null,
    inputArtifact: exampleFile,
    expectedArtifact: null,
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: "2026-01-03T03:04:05.678Z",
    ...overrides,
});

const evaluator = (overrides: Record<string, unknown> = {}) => ({
    id: "ev-1",
    type: "evaluator",
    name: "Judge",
    description: null,
    evaluatorType: "llm_judge",
    heuristicType: null,
    isDefault: false,
    model: "general",
    promptTemplate: "Grade {{ Response }}",
    choiceScores: { A: 1, B: 0 },
    question: null,
    scoring: null,
    reads: null,
    minConfidence: null,
    escalateToEvaluatorId: null,
    experimentCount: 0,
    createdBy: "u1",
    updatedBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const operation = (status: string, overrides: Record<string, unknown> = {}) => ({ data: { id: "op-1", type: "operation", resourceId: "ds-9", status, error: null, ...overrides } });

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: nextPage ? 2 : 1 } } });

describe("evaluations on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status }));
    const respondBlob = (text: string) => fetchMock.mockResolvedValueOnce(new Response(text, { status: 200 }));
    const call = (n: number) => ({ url: fetchMock.mock.calls[n][0], method: fetchMock.mock.calls[n][1]?.method ?? "GET", body: JSON.parse((fetchMock.mock.calls[n][1]?.body as string) ?? "null") });

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("maps an experiment onto the shape the pages render", async () => {
        respond(200, { data: experiment({ latestRunStatus: "completed", latestRunTime: "2026-01-03T00:00:00.000Z" }) });

        const exp = await getExperimentV2("exp-1");

        expect(call(0).url).toBe(`${BASE}/experiments/exp-1`);
        expect(exp).toMatchObject({ id: "exp-1", datasetId: null, createdAt: "2026-01-02T03:04:05.678Z", updatedAt: "", latestRunStatus: "completed", latestRunTime: "2026-01-03T00:00:00.000Z", datasetUpdatedTime: undefined });
        expect(exp).not.toHaveProperty("description");
        expect(exp.models).toEqual([{ model: "general", alias: "general" }]);
    });

    test("maps a workflow experiment's target", async () => {
        respond(200, { data: experiment({ targetType: "workflow", targetAgent: "", targetWorkflow: "order_triage", models: [] }) });

        const exp = await getExperimentV2("exp-1");

        expect(exp).toMatchObject({ targetType: "workflow", targetAgent: "", targetWorkflow: "order_triage", models: [] });
    });

    test("maps a workflow trial's target type, cost and token breakdown, flattening each count", async () => {
        const tokens = (total: number) => ({ inputTokens: total - 10, cachedInputTokens: 0, outputTokens: 10, reasoningTokens: 0, totalTokens: total });
        respond(
            200,
            page([
                {
                    ...result("res-1", "run-1"),
                    targetType: "workflow",
                    failureKind: "partial_failure",
                    cost: "0.0142000000",
                    tokenBreakdown: {
                        traceId: null,
                        incomplete: true,
                        byNode: [{ nodeId: "classify", models: ["claude"], tokens: tokens(100) }],
                        unattributed: tokens(20),
                        byModel: [
                            { model: "claude", tokens: tokens(100) },
                            { model: null, tokens: tokens(20) },
                        ],
                    },
                },
            ])
        );

        const [res] = await getRunResultsV2("run-1");

        expect(res).toMatchObject({ targetType: "workflow", failureKind: "partial_failure", cost: "0.0142000000" });
        expect(res.tokenBreakdown).toEqual({
            incomplete: true,
            byNode: [{ nodeId: "classify", models: ["claude"], ...tokens(100) }],
            unattributed: tokens(20),
            byModel: { claude: tokens(100), "": tokens(20) },
        });
    });

    test("lists every page of experiments with the filters", async () => {
        respond(200, page([experiment()], 2));
        respond(200, page([experiment({ id: "exp-2" })]));

        const list = await getExperimentsV2({ datasetId: "ds-1", targetAgent: "" });

        expect(list.map(e => e.id)).toEqual(["exp-1", "exp-2"]);
        expect(call(0).url).toBe(`${BASE}/experiments?pageNumber=1&pageSize=100&datasetId=ds-1`);
        expect(call(1).url).toBe(`${BASE}/experiments?pageNumber=2&pageSize=100&datasetId=ds-1`);
    });

    test("reads a missing sample experiment as null and rethrows anything else", async () => {
        respond(404, { message: "Not found", errorId: "e1" });
        await expect(getSampleExperimentV2()).resolves.toBeNull();
        expect(call(0).url).toBe(`${BASE}/sampleExperiment`);

        respond(500, { message: "boom", errorId: "e2" });
        await expect(getSampleExperimentV2()).rejects.toBeInstanceOf(ApiV2Error);
    });

    test("updates an experiment with PATCH", async () => {
        respond(200, { data: experiment({ name: "Renamed" }) });

        await updateExperimentV2("exp-1", { name: "Renamed" });

        expect(call(0)).toEqual({ url: `${BASE}/experiments/exp-1`, method: "PATCH", body: { name: "Renamed" } });
    });

    test("triggers a run and names the run group the Operation started", async () => {
        respond(202, { data: { id: "group-1", type: "operation", status: "running" } });

        const started = await triggerRunV2("exp-1", { runName: "Smoke run" });

        expect(call(0)).toEqual({ url: `${BASE}/experiments/exp-1/trigger`, method: "PUT", body: { runName: "Smoke run" } });
        expect(fetchMock).toHaveBeenCalledTimes(1);
        expect(started).toEqual({ runGroupId: "group-1" });
    });

    test("maps an agent snapshot's tool key back to v1's id", async () => {
        const agent = {
            name: "Orchestrator",
            description: null,
            agentType: null,
            systemPrompt: null,
            toolsets: [],
            tools: [{ key: "search_web", name: "Search", description: null }],
            connectors: [],
            skills: [{ id: "summarize", name: "Summarize", description: null }],
            inputModes: [],
            outputModes: [],
            modelConfig: null,
            origin: null,
        };
        respond(200, { data: run({ configSnapshot: { ...run().configSnapshot, agent } }) });

        const r = await getRunV2("run-1");

        expect(r.configSnapshot?.agent).toMatchObject({ tools: [{ id: "search_web", name: "Search" }], skills: [{ id: "summarize", name: "Summarize" }] });
    });

    test("maps a run, and its snapshots back to the stored snake_case keys, as v1 sent them", async () => {
        respond(200, { data: run() });

        const r = await getRunV2("run-1");

        expect(call(0).url).toBe(`${BASE}/runs/run-1`);
        expect(r).toMatchObject({ id: "run-1", runGroupId: "group-1", model: "", summaryScores: { "ev-1": 0.5 }, startedBy: "u1", startedAt: "2026-01-02T03:04:05.678Z" });
        expect(r.completedAt).toBeUndefined();
        expect(r.configSnapshot?.dataset).toEqual({
            id: "ds-1",
            name: "Golden",
            example_count: 1,
            examples: [
                {
                    id: "ex-1",
                    sequence_number: 1,
                    prompt: "hi",
                    expected_response: null,
                    input_data: null,
                    expected_path: null,
                    expected_facts: null,
                    artifact: { filename: "in.txt", version: 2, mime_type: "text/plain", size_bytes: 3 },
                    expected_artifact: null,
                },
            ],
        });
        expect(r.configSnapshot?.experiment).not.toHaveProperty("dataset_id");
        expect(r.configSnapshot).not.toHaveProperty("workflow");
        expect(r.configSnapshot).not.toHaveProperty("agent");
        expect(r.evaluatorSnapshot?.[0]).toEqual({ id: "ev-1", name: "Judge", type: "llm_judge", is_default: true, prompt_template: "p", choice_scores: { A: 1 }, resolved_model_name: "gpt" });
    });

    test("maps a workflow run's snapshot: the workflow in place of the agent, and each example's input, path and facts", async () => {
        const snapshot = run().configSnapshot;
        const inputSchema = { type: "object", properties: { orderId: { type: "string" } }, required: ["orderId"] };
        const configSnapshot = {
            ...snapshot,
            dataset: {
                ...snapshot.dataset,
                examples: [
                    {
                        ...snapshot.dataset.examples[0],
                        prompt: "",
                        inputData: { orderId: "O-5003" },
                        expectedPath: { nodes: [{ nodeId: "approve", expectation: "succeeded" }], tools: [{ toolName: "issue_refund", expectation: "called" }] },
                        expectedFacts: { required: ["O-5003"], forbidden: [] },
                    },
                ],
            },
            agent: null,
            workflow: { name: "order_flow", instanceName: "workflow_0190f5ee", inputSchema },
        };
        respond(200, { data: run({ configSnapshot }) });

        const r = await getRunV2("run-1");

        expect(r.configSnapshot?.workflow).toEqual({ name: "order_flow", instance_name: "workflow_0190f5ee", input_schema: inputSchema });
        expect(r.configSnapshot).not.toHaveProperty("agent");
        expect(r.configSnapshot?.dataset?.examples?.[0]).toMatchObject({
            input_data: { orderId: "O-5003" },
            expected_path: { nodes: { approve: "succeeded" }, tools: { issue_refund: "called" } },
            expected_facts: { required: ["O-5003"], forbidden: [] },
        });
    });

    test("leaves out a workflow snapshot's instance name and input schema when they were not recorded", async () => {
        respond(200, { data: run({ configSnapshot: { ...run().configSnapshot, workflow: { name: "order_flow", instanceName: null, inputSchema: null } } }) });

        const r = await getRunV2("run-1");

        expect(r.configSnapshot?.workflow).toEqual({ name: "order_flow" });
    });

    test("keys a run's evaluator stats by evaluator, leaving out the figures an LLM judge has none of", async () => {
        respond(200, {
            data: run({
                evaluatorStats: [
                    {
                        evaluatorId: "ev-decision",
                        scored: 4,
                        escalated: 1,
                        withConfidence: 3,
                        escalatedPercent: 25,
                        meanConfidence: 0.9,
                        judgeUsage: { decision: { inputTokens: 10, outputTokens: 2 }, llmJudge: null },
                    },
                    {
                        evaluatorId: "ev-judge",
                        scored: 2,
                        escalated: 0,
                        withConfidence: 0,
                        escalatedPercent: null,
                        meanConfidence: null,
                        judgeUsage: { decision: null, llmJudge: { inputTokens: 5, outputTokens: 1 } },
                    },
                ],
            }),
        });

        const r = await getRunV2("run-1");

        expect(r.evaluatorStats).toEqual({
            "ev-decision": {
                scored: 4,
                escalated: 1,
                withConfidence: 3,
                escalatedPercent: 25,
                meanConfidence: 0.9,
                judgeUsage: { decision: { inputTokens: 10, outputTokens: 2 } },
            },
            "ev-judge": { scored: 2, judgeUsage: { llmJudge: { inputTokens: 5, outputTokens: 1 } } },
        });
    });

    test("maps a fully populated run and result field by field", async () => {
        const modelConfig = { id: "cfg-1", alias: "gpt", provider: "openai", modelName: "gpt-4o", apiBase: null, modelParams: { temperature: 0 }, maxInputTokens: 1000, description: null };
        const agent = {
            name: "Orchestrator",
            description: "Routes work",
            agentType: "orchestrator",
            systemPrompt: "Be brief",
            toolsets: ["web"],
            tools: [{ key: null, name: "Search", description: "Searches" }],
            connectors: [{ id: "c1", name: "Docs", connectorType: "sql", subtype: null }],
            skills: [{ id: null, name: "Summarize", description: null }],
            inputModes: ["text"],
            outputModes: ["text"],
            modelConfig,
            origin: "platform",
        };
        const snapshot = run().configSnapshot;
        const configSnapshot = {
            experiment: { ...snapshot.experiment, models: [{ modelConfigId: "cfg-1", model: "gpt", alias: "gpt", resolvedModel: "gpt-4o", modelConfig }] },
            dataset: { ...snapshot.dataset, examples: [{ ...snapshot.dataset.examples[0], expectedArtifact: { filename: "want.txt", version: 0, mimeType: null, size: 0, contentHash: "h" } }] },
            agent,
        };
        respond(200, { data: run({ configSnapshot }) });

        const r = await getRunV2("run-1");

        expect(r.configSnapshot?.experiment?.models).toEqual([
            { modelConfigId: "cfg-1", model: "gpt", alias: "gpt", resolvedModel: "gpt-4o", modelConfig: { id: "cfg-1", alias: "gpt", provider: "openai", modelName: "gpt-4o", modelParams: { temperature: 0 }, maxInputTokens: 1000 } },
        ]);
        expect(r.configSnapshot?.dataset?.examples?.[0].expected_artifact).toEqual({ filename: "want.txt", version: 0, size_bytes: 0, content_hash: "h" });
        expect(r.configSnapshot?.agent).toEqual({
            name: "Orchestrator",
            description: "Routes work",
            type: "orchestrator",
            systemPrompt: "Be brief",
            toolsets: ["web"],
            tools: [{ name: "Search", description: "Searches" }],
            connectors: [{ id: "c1", name: "Docs", type: "sql" }],
            skills: [{ name: "Summarize" }],
            inputModes: ["text"],
            outputModes: ["text"],
            modelConfig: { id: "cfg-1", alias: "gpt", provider: "openai", modelName: "gpt-4o", modelParams: { temperature: 0 }, maxInputTokens: 1000 },
            origin: "platform",
        });

        const artifact = { filename: "out.txt", mimeType: "text/plain", version: 1, size: 5 };
        respond(200, page([{ ...result("res-1", "run-1"), failureKind: "timeout", artifacts: [artifact], inputArtifact: { ...artifact, filename: "in.txt", mimeType: null }, expectedArtifact: artifact }]));

        const [res] = await getRunResultsV2("run-1");

        expect(res).toEqual({
            id: "res-1",
            runId: "run-1",
            exampleId: "ex-1",
            trialIndex: 0,
            inputPrompt: "hi",
            targetAgent: "Orchestrator",
            agentResponse: "hello",
            durationSeconds: 1,
            failureKind: "timeout",
            targetType: "agent",
            tokenBreakdown: null,
            cost: null,
            scores: [{ id: "s1", evaluatorId: "ev-1", score: 1, passed: true, createdAt: "2026-01-02T03:04:05.678Z" }],
            artifacts: [artifact],
            inputArtifact: { filename: "in.txt", version: 1, size: 5 },
            expectedArtifact: artifact,
            createdAt: "2026-01-02T03:04:05.678Z",
        });
    });

    test("maps a decision score's details from the wire, probabilities keyed by option", async () => {
        const choiceDetails = {
            decidedBy: "decision",
            model: "jev-1.13.0",
            answer: {
                choice: "grounded",
                probability: null,
                score: null,
                confidence: 0.93,
                probabilities: [
                    { option: "grounded", probability: 0.93 },
                    { option: "made_up", probability: 0.07 },
                ],
                scoringApplied: "chosen",
            },
            escalated: false,
            escalationReason: null,
            providerError: null,
            usage: { decision: { inputTokens: 120, outputTokens: 4 }, llmJudge: null },
            durationMs: 850,
        };
        const booleanDetails = {
            ...choiceDetails,
            answer: { choice: "true", probability: 0.8, score: null, confidence: 0.8, probabilities: [], scoringApplied: null },
            usage: null,
            durationMs: null,
        };
        const erroredDetails = { ...choiceDetails, decidedBy: null, model: null, answer: null, providerError: "rate_limited", usage: null, durationMs: null };
        const escalatedDetails = {
            ...choiceDetails,
            decidedBy: "llm_judge",
            escalated: true,
            escalationReason: "low_confidence",
            usage: { decision: { inputTokens: 120, outputTokens: 4 }, llmJudge: { inputTokens: 900, outputTokens: 60 } },
        };
        const scoreLevelDetails = {
            ...choiceDetails,
            answer: {
                choice: "poor",
                probability: null,
                score: 0,
                confidence: 0.7,
                probabilities: [
                    { option: "good", probability: 0 },
                    { option: "fair", probability: 0.3 },
                    { option: "poor", probability: 0.7 },
                ],
                scoringApplied: "expected",
            },
            usage: null,
            durationMs: null,
        };
        const falseBooleanDetails = { ...booleanDetails, answer: { ...booleanDetails.answer, choice: "false", probability: 0, confidence: 0 } };
        const score = result("res-1", "run-1").scores[0];
        respond(
            200,
            page([
                {
                    ...result("res-1", "run-1"),
                    scores: [
                        { ...score, key: "s1", details: choiceDetails },
                        { ...score, key: "s2", details: booleanDetails },
                        { ...score, key: "s3", details: erroredDetails },
                        { ...score, key: "s4", details: null },
                        { ...score, key: "s5", details: escalatedDetails },
                        { ...score, key: "s6", details: scoreLevelDetails },
                        { ...score, key: "s7", details: falseBooleanDetails },
                    ],
                },
            ])
        );

        const [{ scores }] = await getRunResultsV2("run-1");

        const choiceAnswer = { choice: "grounded", confidence: 0.93, probabilities: { grounded: 0.93, made_up: 0.07 }, scoringApplied: "chosen" };
        expect(scores.map(s => s.details)).toStrictEqual([
            {
                decidedBy: "decision",
                model: "jev-1.13.0",
                answer: choiceAnswer,
                escalated: false,
                usage: { decision: { inputTokens: 120, outputTokens: 4 } },
                durationMs: 850,
            },
            { decidedBy: "decision", model: "jev-1.13.0", answer: { choice: "true", probability: 0.8, confidence: 0.8 }, escalated: false },
            { escalated: false, providerError: "rate_limited" },
            undefined,
            {
                decidedBy: "llm_judge",
                model: "jev-1.13.0",
                answer: choiceAnswer,
                escalated: true,
                escalationReason: "low_confidence",
                usage: { decision: { inputTokens: 120, outputTokens: 4 }, llmJudge: { inputTokens: 900, outputTokens: 60 } },
                durationMs: 850,
            },
            {
                decidedBy: "decision",
                model: "jev-1.13.0",
                answer: { choice: "poor", score: 0, confidence: 0.7, probabilities: { good: 0, fair: 0.3, poor: 0.7 }, scoringApplied: "expected" },
                escalated: false,
            },
            { decidedBy: "decision", model: "jev-1.13.0", answer: { choice: "false", probability: 0, confidence: 0 }, escalated: false },
        ]);
        expect(scores[3]).not.toHaveProperty("details");
        expect(decisionChip(scores[4].details)).toEqual({ label: "Escalated · low confidence", tone: "escalated" });
    });

    test("a choice answer from the wire draws a labelled bar per option, the chosen one marked", async () => {
        const answer = {
            choice: "partial",
            probability: null,
            score: null,
            confidence: 0.6,
            probabilities: [
                { option: "correct", probability: 0.3 },
                { option: "partial", probability: 0.6 },
                { option: "wrong", probability: 0.1 },
            ],
            scoringApplied: "chosen",
        };
        const details = { decidedBy: "decision", model: "jev-1.13.0", answer, escalated: false, escalationReason: null, providerError: null, usage: null, durationMs: null };
        respond(200, page([{ ...result("res-1", "run-1"), scores: [{ ...result("res-1", "run-1").scores[0], details }] }]));

        const [{ scores }] = await getRunResultsV2("run-1");

        expect(decisionProbabilityBars(scores[0].details?.answer)).toEqual([
            { key: "partial", percent: 60, chosen: true },
            { key: "correct", percent: 30, chosen: false },
            { key: "wrong", percent: 10, chosen: false },
        ]);
    });

    test("keeps a result once when a trial finishing mid-read repeats it on the next page", async () => {
        respond(200, page([result("res-1", "run-1"), result("res-2", "run-1")], 2));
        respond(200, page([result("res-2", "run-1"), result("res-3", "run-1")]));

        const results = await getRunResultsV2("run-1");

        expect(results.map(r => r.id)).toEqual(["res-1", "res-2", "res-3"]);
    });

    test("deletes a run", async () => {
        respond(204);

        await deleteRunV2("run-1");

        expect(call(0)).toMatchObject({ url: `${BASE}/runs/run-1`, method: "DELETE" });
    });

    test("a refused run or report delete says to cancel first", async () => {
        respond(422, { message: "Unable to delete run", errorId: "e1" });
        respond(422, { message: "Unable to delete run group", errorId: "e2" });

        await expect(deleteRunV2("run-1")).rejects.toThrow("Cannot delete a run while it is pending or running. Cancel it first.");
        await expect(deleteRunGroupV2("group-1")).rejects.toThrow("Cannot delete a report while its runs are pending or running. Cancel it first.");
    });

    test("cancels a run with PUT", async () => {
        respond(200, { data: run({ status: "cancelled" }) });

        const cancelled = await cancelRunV2("run-1");

        expect(call(0)).toMatchObject({ url: `${BASE}/runs/run-1/cancel`, method: "PUT" });
        expect(cancelled.status).toBe("cancelled");
    });

    test("reads no recorded task events as null", async () => {
        respond(200, { data: { events: [] } });
        await expect(getRunResultTaskEventsV2("res-1")).resolves.toBeNull();
        expect(call(0).url).toBe(`${BASE}/runResults/res-1/taskEvents`);

        respond(200, { data: { events: [{ task_id: "t1" }] } });
        await expect(getRunResultTaskEventsV2("res-1")).resolves.toEqual([{ task_id: "t1" }]);
    });

    test("keys a group's results by each run's model", async () => {
        respond(200, { data: runGroup([run({ id: "run-1", model: "gpt" }), run({ id: "run-2", model: null }), run({ id: "run-3", model: "gpt" })]) });
        respond(200, page([result("res-1", "run-1")]));
        respond(200, page([result("res-2", "run-2")]));
        respond(200, page([result("res-3", "run-3")]));

        const results = await getRunGroupResultsV2("group-1");

        expect(results.groupId).toBe("group-1");
        expect(Object.keys(results.resultsByModel).sort()).toEqual(["", "gpt"]);
        expect(results.resultsByModel.gpt.map(r => r.id)).toEqual(["res-1", "res-3"]);
        expect(results.resultsByModel.gpt[0]).toMatchObject({ id: "res-1", createdAt: "2026-01-02T03:04:05.678Z", scores: [{ id: "s1", createdAt: "2026-01-02T03:04:05.678Z" }] });
        expect(fetchMock.mock.calls.map(c => c[0])).toContain(`${BASE}/runs/run-2/results?pageNumber=1&pageSize=100`);
    });

    test("reads a group's runs once across polls and pages only the results", async () => {
        respond(200, { data: runGroup([run({ id: "run-1", model: "gpt" })]) });
        respond(200, page([result("res-1", "run-1")]));
        await getRunGroupResultsV2("group-poll");
        respond(200, page([result("res-1", "run-1"), result("res-2", "run-1")]));
        const second = await getRunGroupResultsV2("group-poll");

        expect(second.resultsByModel.gpt.map(r => r.id)).toEqual(["res-1", "res-2"]);
        expect(fetchMock.mock.calls.filter(c => c[0] === `${BASE}/runGroups/group-poll`)).toHaveLength(1);
    });

    test("skips a run deleted while its group is read, and fails on any other error", async () => {
        respond(200, { data: runGroup([run({ id: "run-1", model: "gpt" }), run({ id: "run-2", model: "claude" })]) });
        respond(200, page([result("res-1", "run-1")]));
        respond(404, { message: "gone" });
        const results = await getRunGroupResultsV2("group-gone");
        expect(Object.keys(results.resultsByModel)).toEqual(["gpt"]);

        respond(200, page([result("res-1", "run-1")]));
        respond(500, { message: "boom" });
        await expect(getRunGroupResultsV2("group-gone")).rejects.toMatchObject({ status: 500 });
    });

    test("filters report pages by targetAgent and names each row by its run group", async () => {
        respond(
            200,
            page([
                {
                    id: "group-1",
                    type: "runGroupSummary",
                    experimentId: "exp-1",
                    experimentName: "Smoke",
                    targetType: "agent",
                    targetAgent: "Orchestrator",
                    runName: null,
                    status: "completed",
                    createdTime: "2026-01-02T03:04:05.678Z",
                    completedTime: null,
                    durationSeconds: null,
                    runCount: 1,
                },
            ])
        );

        const reports = await listRunGroupsPaginatedV2({ pageNumber: 2, pageSize: 20, agent: "Orchestrator" });

        expect(call(0).url).toBe(`${BASE}/runGroups?pageNumber=2&pageSize=20&targetAgent=Orchestrator`);
        expect(reports.data[0]).toEqual({ runGroupId: "group-1", experimentId: "exp-1", experimentName: "Smoke", targetType: "agent", targetAgent: "Orchestrator", status: "completed", startedAt: "2026-01-02T03:04:05.678Z", runCount: 1 });
        expect(reports.meta.pagination.pageSize).toBe(100);
    });

    test("filters report pages by target type", async () => {
        respond(
            200,
            page([
                {
                    id: "group-2",
                    type: "runGroupSummary",
                    experimentId: "exp-2",
                    experimentName: "Flow",
                    targetType: "workflow",
                    targetAgent: "order_flow",
                    runName: null,
                    status: "completed",
                    createdTime: "2026-01-02T03:04:05.678Z",
                    completedTime: null,
                    durationSeconds: null,
                    runCount: 1,
                },
            ])
        );
        const reports = await listRunGroupsPaginatedV2({ targetType: "workflow" });
        expect(call(0).url).toBe(`${BASE}/runGroups?targetType=workflow`);
        expect(reports.data[0]).toMatchObject({ runGroupId: "group-2", targetType: "workflow", targetAgent: "order_flow" });
    });

    test("lists every page of report targets with the type the server gives each", async () => {
        respond(
            200,
            page(
                [
                    { targetAgent: "order_flow", targetType: "agent" },
                    { targetAgent: "order_flow", targetType: "workflow" },
                ],
                2
            )
        );
        respond(200, page([{ targetAgent: "support_agent", targetType: "agent" }]));

        const targets = await listRunGroupAgentsV2();

        expect(call(0).url).toBe(`${BASE}/runGroupAgents?pageNumber=1&pageSize=100`);
        expect(call(1).url).toBe(`${BASE}/runGroupAgents?pageNumber=2&pageSize=100`);
        expect(targets).toEqual([
            { name: "order_flow", targetType: "agent" },
            { name: "order_flow", targetType: "workflow" },
            { name: "support_agent", targetType: "agent" },
        ]);
    });

    test("maps tool authorization times and begins with PUT", async () => {
        respond(200, {
            data: {
                runAs: "svc",
                agents: [
                    {
                        agentName: "A",
                        isTarget: true,
                        addedBy: null,
                        reachable: true,
                        tools: [{ credentialKey: "k", displayName: null, authType: "oauth2", interactive: true, status: "connected", expiresTime: "2026-01-05T00:00:00.000Z", lastAuthorizedBy: "u1", lastAuthorizedTime: null }],
                    },
                ],
                allowListIsWildcard: false,
                totalAgents: 1,
                totalRunningAgents: 1,
                agentScanLimit: 50,
            },
        });
        const auth = await getExperimentToolAuthV2("exp-1");
        expect(auth.agents[0]).toEqual({ agentName: "A", isTarget: true, reachable: true, tools: [{ credentialKey: "k", authType: "oauth2", interactive: true, status: "connected", expiresAt: "2026-01-05T00:00:00.000Z", lastAuthorizedBy: "u1" }] });

        respond(200, { data: { authUri: "https://idp/authorize", credentialKey: "k", expiresTime: "2026-01-05T00:00:00.000Z" } });
        const begun = await beginExperimentToolAuthV2("exp-1", "A", "k");
        expect(call(1)).toEqual({ url: `${BASE}/experiments/exp-1/toolAuth/begin`, method: "PUT", body: { agentName: "A", credentialKey: "k" } });
        expect(begun).toEqual({ authUri: "https://idp/authorize", credentialKey: "k", expiresAt: "2026-01-05T00:00:00.000Z" });
    });

    test("adds and removes a tool authorization agent and disconnects with PUT", async () => {
        respond(201, { data: { agentName: "A B" } });
        respond(204);
        respond(204);

        await addExperimentToolAuthAgentV2("exp-1", "A B");
        await removeExperimentToolAuthAgentV2("exp-1", "A B");
        await disconnectExperimentToolAuthV2("exp-1", "A", "k");

        expect(call(0)).toEqual({ url: `${BASE}/experiments/exp-1/toolAuth/agents`, method: "POST", body: { agentName: "A B" } });
        expect(call(1)).toEqual({ url: `${BASE}/experiments/exp-1/toolAuth/agents/A%20B`, method: "DELETE", body: null });
        expect(call(2)).toEqual({ url: `${BASE}/experiments/exp-1/toolAuth/disconnect`, method: "PUT", body: { agentName: "A", credentialKey: "k" } });
    });

    test("sets the watchlist with PUT and maps chart points to startedAt", async () => {
        respond(200, page([{ agentName: "A", resolvedModelProvider: "general", isBuiltIn: false, position: 0 }]));
        await setWatchlistV2(["A"]);
        expect(call(0)).toEqual({ url: `${BASE}/watchlist?pageNumber=1&pageSize=100`, method: "PUT", body: { agentNames: ["A"] } });

        respond(
            200,
            page([
                {
                    agentName: "A",
                    resolvedModelProvider: "general",
                    isBuiltIn: false,
                    position: 0,
                    experiments: [{ experimentId: "exp-1", experimentName: "Smoke", primaryEvaluatorId: "ev-1", primaryEvaluatorName: null, points: [{ runGroupId: "group-1", startedTime: "2026-01-02T03:04:05.678Z", score: 0.5 }] }],
                },
            ])
        );
        const [chart] = await getWatchlistChartDataV2();
        expect(chart.experiments[0].points).toEqual([{ runGroupId: "group-1", startedAt: "2026-01-02T03:04:05.678Z", score: 0.5 }]);
    });

    test("downloads artifacts at their version and reports a replaced example file as gone", async () => {
        respondBlob("bytes");
        const blob = await getRunResultArtifactBlobV2("res-1", "out put.txt", 3);
        expect(await blob.text()).toBe("bytes");
        expect(call(0).url).toBe(`${BASE}/runResults/res-1/artifacts/out%20put.txt?version=3`);

        respond(404, { message: "Not found", errorId: "e1" });
        await expect(getRunResultExampleArtifactBlobV2("res-1", "expected", "gold.txt")).rejects.toThrow(EXAMPLE_ARTIFACT_GONE_MESSAGE);
        expect(call(1).url).toBe(`${BASE}/runResults/res-1/expectedArtifact/gold.txt`);

        respond(404, { message: "Not found", errorId: "e2" });
        await expect(getRunResultArtifactBlobV2("res-1", "out.txt")).rejects.toBeInstanceOf(ApiV2Error);
    });

    test.each([
        ["a run result artifact", () => getRunResultArtifactBlobV2("res-1", "out.txt", 0), `${BASE}/runResults/res-1/artifacts/out.txt?version=0`],
        ["a run result's example file", () => getRunResultExampleArtifactBlobV2("res-1", "expected", "gold.txt", 0), `${BASE}/runResults/res-1/expectedArtifact/gold.txt?version=0`],
        ["an example file", () => getExampleArtifactBlobV2("ex-1", "input", "in.txt", 0), `${BASE}/examples/ex-1/inputArtifact/in.txt?version=0`],
    ])("downloads version 0 of %s", async (_name, download, url) => {
        respondBlob("bytes");
        await download();
        expect(call(0).url).toBe(url);
    });

    test("maps a dataset and sorts the paged list by the camelCase field", async () => {
        respond(200, page([dataset()]));

        const res = await getDatasetsPaginatedV2({ pageNumber: 2, pageSize: 20, name: "Gold", sort: "experiment_count:desc" });

        expect(call(0).url).toBe(`${BASE}/datasets?pageNumber=2&pageSize=20&name=Gold&sort=experimentCount%3Adesc`);
        expect(res.data).toEqual([{ id: "ds-1", name: "Golden", exampleCount: 2, inputDataExampleCount: 0, experimentCount: 1, createdBy: "u1", updatedBy: "u1", createdAt: "2026-01-02T03:04:05.678Z", updatedAt: "" }]);
        expect(res.meta.pagination.totalPages).toBe(1);
    });

    test("carries how many of a dataset's examples have input data", async () => {
        respond(200, { data: dataset({ exampleCount: 40, inputDataExampleCount: 25 }) });

        const ds = await getDatasetV2("ds-1");

        expect(ds).toMatchObject({ exampleCount: 40, inputDataExampleCount: 25 });
    });

    test("lists every page of datasets", async () => {
        respond(200, page([dataset()], 2));
        respond(200, page([dataset({ id: "ds-2" })]));

        const list = await getDatasetsV2();

        expect(list.map(d => d.id)).toEqual(["ds-1", "ds-2"]);
        expect(call(1).url).toBe(`${BASE}/datasets?pageNumber=2&pageSize=100`);
    });

    test("creates a dataset with POST and updates it with PATCH", async () => {
        respond(201, { data: dataset() });
        respond(200, { data: dataset({ description: "Kept" }) });

        await createDatasetV2({ name: "Golden", description: "" });
        const updated = await updateDatasetV2("ds-1", { description: "Kept" });

        expect(call(0)).toEqual({ url: `${BASE}/datasets`, method: "POST", body: { name: "Golden", description: "" } });
        expect(call(1)).toEqual({ url: `${BASE}/datasets/ds-1`, method: "PATCH", body: { description: "Kept" } });
        expect(updated.description).toBe("Kept");
    });

    test("generates a dataset and reads the one its operation made", async () => {
        respond(202, operation("inProgress", { resourceId: null }));
        respond(200, operation("succeeded"));
        respond(200, { data: dataset({ id: "ds-9" }) });

        const created = await generateDatasetV2({ targetAgent: "Orchestrator", numExamples: 3 });

        expect(call(0)).toEqual({ url: `${BASE}/datasets/generate`, method: "PUT", body: { targetAgent: "Orchestrator", numExamples: 3 } });
        expect(call(1).url).toBe(`${BASE}/datasetOperations/op-1`);
        expect(call(2).url).toBe(`${BASE}/datasets/ds-9`);
        expect(created.id).toBe("ds-9");
    });

    test("polls generation until it finishes", async () => {
        vi.useFakeTimers();
        try {
            respond(202, operation("inProgress"));
            respond(200, operation("inProgress"));
            respond(200, operation("succeeded"));

            const done = generateExamplesV2("ds-1", { targetAgent: "Orchestrator", numExamples: 2 });
            await vi.runAllTimersAsync();
            await done;

            expect(call(0)).toEqual({ url: `${BASE}/datasets/ds-1/examples/generate`, method: "PUT", body: { targetAgent: "Orchestrator", numExamples: 2 } });
            expect(call(1).url).toBe(`${BASE}/datasets/ds-1/operations/op-1`);
            expect(fetchMock).toHaveBeenCalledTimes(3);
        } finally {
            vi.useRealTimers();
        }
    });

    test("fails generation with the message SAM recorded", async () => {
        respond(202, operation("inProgress"));
        respond(200, operation("failed", { error: { message: "Generation failed: the agent did not answer.", errorId: "e1" } }));

        await expect(generateExamplesV2("ds-1", { targetAgent: "Nobody", numExamples: 2 })).rejects.toThrow("Generation failed: the agent did not answer.");
    });

    test("fails a generation SAM recorded no reason for", async () => {
        respond(202, operation("pending"));
        respond(200, operation("failed"));

        await expect(generateExamplesV2("ds-1", { targetAgent: "Orchestrator", numExamples: 2 })).rejects.toThrow("Generation failed.");
        expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    test("fails a dataset generation that names no dataset", async () => {
        respond(202, operation("inProgress", { resourceId: null }));
        respond(200, operation("succeeded", { resourceId: null }));

        await expect(generateDatasetV2({ targetAgent: "Orchestrator", numExamples: 3 })).rejects.toThrow("Generation finished without a dataset.");
        expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    test("keeps polling through a dropped connection or a 5xx, and stops at anything else", async () => {
        vi.useFakeTimers();
        try {
            respond(202, operation("inProgress"));
            fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
            respond(503, { message: "Unable to get operation", errorId: "e1" });
            respond(200, operation("succeeded"));
            const recovered = generateExamplesV2("ds-1", { targetAgent: "Orchestrator", numExamples: 2 });
            await vi.runAllTimersAsync();
            await expect(recovered).resolves.toBeUndefined();
            expect(fetchMock).toHaveBeenCalledTimes(4);

            fetchMock.mockReset();
            respond(202, operation("inProgress"));
            respond(404, { message: "Unable to get operation", errorId: "e2" });
            await expect(generateExamplesV2("ds-1", { targetAgent: "Orchestrator", numExamples: 2 })).rejects.toBeInstanceOf(ApiV2Error);
            expect(fetchMock).toHaveBeenCalledTimes(2);
        } finally {
            vi.useRealTimers();
        }
    });

    test("retries a 408 or a 429, but not a body that will not parse or a refused token", async () => {
        vi.useFakeTimers();
        try {
            respond(202, operation("inProgress"));
            respond(408, { message: "Unable to get operation", errorId: "e1" });
            respond(429, { message: "Unable to get operation", errorId: "e2" });
            respond(200, operation("succeeded"));
            const recovered = generateExamplesV2("ds-1", { targetAgent: "Orchestrator", numExamples: 2 });
            await vi.runAllTimersAsync();
            await expect(recovered).resolves.toBeUndefined();
            expect(fetchMock).toHaveBeenCalledTimes(4);

            fetchMock.mockReset();
            respond(202, operation("inProgress"));
            fetchMock.mockResolvedValueOnce(new Response("<html>not json</html>", { status: 200 }));
            await expect(generateExamplesV2("ds-1", { targetAgent: "Orchestrator", numExamples: 2 })).rejects.toBeInstanceOf(SyntaxError);
            expect(fetchMock).toHaveBeenCalledTimes(2);
        } finally {
            vi.useRealTimers();
        }
    });

    test("gives up polling after five failed polls in a row", async () => {
        vi.useFakeTimers();
        try {
            respond(202, operation("inProgress"));
            fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
            const done = generateExamplesV2("ds-1", { targetAgent: "Orchestrator", numExamples: 2 });
            const settled = expect(done).rejects.toThrow("Failed to fetch");
            await vi.runAllTimersAsync();
            await settled;
            expect(fetchMock).toHaveBeenCalledTimes(7);
        } finally {
            vi.useRealTimers();
        }
    });

    test("says why a dataset call was refused", async () => {
        respond(409, { message: "Unable to create dataset", errorId: "e1" });
        respond(409, { message: "Unable to update dataset", errorId: "e2" });
        respond(422, { message: "Unable to delete dataset", errorId: "e3" });
        respond(409, { message: "Unable to generate examples", errorId: "e4" });
        respond(404, { message: "Unable to delete dataset", errorId: "e5" });

        await expect(createDatasetV2({ name: "Golden", description: "" })).rejects.toThrow("A dataset with this name already exists.");
        await expect(updateDatasetV2("ds-1", { name: "Golden" })).rejects.toThrow("A dataset with this name already exists.");
        await expect(deleteDatasetV2("ds-1")).rejects.toThrow("Cannot delete dataset while one or more of its experiments has active (pending/running) runs.");
        await expect(generateExamplesV2("ds-1", { targetAgent: "Orchestrator", numExamples: 2 })).rejects.toThrow("This dataset is already generating examples.");
        await expect(deleteDatasetV2("ds-1")).rejects.toBeInstanceOf(ApiV2Error);
    });

    test("deletes a dataset", async () => {
        respond(204);

        await deleteDatasetV2("ds-1");

        expect(call(0)).toMatchObject({ url: `${BASE}/datasets/ds-1`, method: "DELETE" });
    });

    test("lists every page of a dataset's examples, and creates and deletes one", async () => {
        respond(200, page([example()], 2));
        respond(200, page([example({ id: "ex-2", sequenceNumber: 2 })]));
        respond(201, { data: example({ id: "ex-3", prompt: "new" }) });
        respond(204);

        const list = await getExamplesV2("ds-1");
        const created = await createExampleV2("ds-1", { prompt: "new" });
        await deleteExampleV2("ex-3");

        expect(list.map(e => e.id)).toEqual(["ex-1", "ex-2"]);
        expect(call(0).url).toBe(`${BASE}/datasets/ds-1/examples?pageNumber=1&pageSize=100`);
        expect(call(1).url).toBe(`${BASE}/datasets/ds-1/examples?pageNumber=2&pageSize=100`);
        expect(call(2)).toEqual({ url: `${BASE}/datasets/ds-1/examples`, method: "POST", body: { prompt: "new" } });
        expect(created).toMatchObject({ id: "ex-3", prompt: "new" });
        expect(call(3)).toMatchObject({ url: `${BASE}/examples/ex-3`, method: "DELETE" });
    });

    test("maps an example, keeping its file and dropping null fields", async () => {
        respond(200, { data: example() });

        const ex = await getExampleV2("ex-1");

        expect(call(0).url).toBe(`${BASE}/examples/ex-1`);
        expect(ex).toEqual({ id: "ex-1", datasetId: "ds-1", sequenceNumber: 1, prompt: "hi", inputArtifact: exampleFile, createdAt: "2026-01-02T03:04:05.678Z", updatedAt: "2026-01-03T03:04:05.678Z" });
    });

    test("maps a workflow example's input data, expected values and expected steps keyed by id", async () => {
        respond(200, {
            data: example({
                prompt: "",
                inputArtifact: null,
                inputData: { orderId: "O-1", rush: true },
                expectedFacts: { required: ["refund"], forbidden: [] },
                expectedPath: { nodes: [{ nodeId: "approve", expectation: "succeeded" }], tools: [{ toolName: "issue_refund", expectation: "called" }] },
            }),
        });

        const ex = await getExampleV2("ex-1");

        expect(ex).toMatchObject({
            prompt: "",
            inputData: { orderId: "O-1", rush: true },
            expectedFacts: { required: ["refund"], forbidden: [] },
            expectedPath: { nodes: { approve: "succeeded" }, tools: { issue_refund: "called" } },
        });
    });

    test("reads an expected path with no tools as nodes only", async () => {
        respond(200, { data: example({ expectedPath: { nodes: [{ nodeId: "approve", expectation: "ran" }], tools: [] } }) });

        const ex = await getExampleV2("ex-1");

        expect(ex.expectedPath).toEqual({ nodes: { approve: "ran" } });
    });

    test("sends expected steps as the lists v2 takes, on create and on update, and null to clear", async () => {
        respond(201, { data: example() });
        respond(200, { data: example() });
        respond(200, { data: example() });

        await createExampleV2("ds-1", { prompt: "", inputData: { orderId: "O-1" }, expectedPath: { nodes: { approve: "succeeded" }, tools: { issue_refund: "not_called" } } });
        await updateExampleV2("ex-1", { expectedPath: { tools: { issue_refund: "called" } } });
        await updateExampleV2("ex-1", { expectedPath: null, inputData: null });

        expect(call(0).body).toEqual({
            prompt: "",
            inputData: { orderId: "O-1" },
            expectedPath: { nodes: [{ nodeId: "approve", expectation: "succeeded" }], tools: [{ toolName: "issue_refund", expectation: "not_called" }] },
        });
        expect(call(1).body).toEqual({ expectedPath: { nodes: [], tools: [{ toolName: "issue_refund", expectation: "called" }] } });
        expect(call(2).body).toEqual({ expectedPath: null, inputData: null });
    });

    test("sends expected values as they are, on create and on update, and null to clear", async () => {
        respond(201, { data: example() });
        respond(200, { data: example() });
        respond(200, { data: example() });

        await createExampleV2("ds-1", { prompt: "", inputData: { orderId: "O-1" }, expectedFacts: { required: ["refund"], forbidden: ["escalated"] } });
        await updateExampleV2("ex-1", { expectedFacts: { required: ["approved"] } });
        await updateExampleV2("ex-1", { expectedFacts: null });

        expect(call(0).body).toEqual({ prompt: "", inputData: { orderId: "O-1" }, expectedFacts: { required: ["refund"], forbidden: ["escalated"] } });
        expect(call(1).body).toEqual({ expectedFacts: { required: ["approved"] } });
        expect(call(2).body).toEqual({ expectedFacts: null });
    });

    test("pages examples by content and updates one with PATCH", async () => {
        respond(200, page([example()]));
        respond(200, { data: example({ prompt: "edited" }) });

        await getExamplesPaginatedV2("ds-1", { pageNumber: 1, pageSize: 10, content: "hi" });
        await updateExampleV2("ex-1", { prompt: "edited" });

        expect(call(0).url).toBe(`${BASE}/datasets/ds-1/examples?pageNumber=1&pageSize=10&content=hi`);
        expect(call(1)).toEqual({ url: `${BASE}/examples/ex-1`, method: "PATCH", body: { prompt: "edited" } });
    });

    test("imports examples as a named file and exports them as a download", async () => {
        respond(200, { data: { created: 2, skipped: 0, dropped: 0, limit: 5000 } });
        respondBlob("prompt\nhi\n");

        const imported = await importExamplesV2("ds-1", "prompt\nhi\n", "text/csv");
        const exported = await exportExamplesV2("ds-1", "json");

        const [url, init] = fetchMock.mock.calls[0];
        expect(url).toBe(`${BASE}/datasets/ds-1/examples/import`);
        expect(init?.method).toBe("PUT");
        const file = (init?.body as FormData).get("file") as File;
        expect(file.name).toBe("examples.csv");
        expect(file.type).toBe("text/csv");
        expect(file.size).toBe("prompt\nhi\n".length);
        expect(imported).toEqual({ created: 2, skipped: 0, dropped: 0, limit: 5000 });
        expect(call(1).url).toBe(`${BASE}/datasets/ds-1/examples/export?format=json`);
        expect(await exported.text()).toBe("prompt\nhi\n");
    });

    test("says why a file was refused", async () => {
        respond(422, { message: "Unable to import examples", errorId: "e1", validationDetails: { file: ["row 2 is not valid CSV"] } });
        respond(422, { message: "Unable to upload expected file", errorId: "e2", validationDetails: { file: ["expected files must be text"] } });
        respond(500, { message: "Unable to import examples", errorId: "e3" });

        await expect(importExamplesV2("ds-1", "bad", "text/csv")).rejects.toThrow("row 2 is not valid CSV");
        await expect(uploadExampleArtifactV2("ex-1", "expected", new File(["x"], "pic.png"))).rejects.toThrow("expected files must be text");
        await expect(importExamplesV2("ds-1", "bad", "text/csv")).rejects.toBeInstanceOf(ApiV2Error);
    });

    test("says a file is too large and by how much", async () => {
        respond(413, { message: "The file is too large", errorId: "e1", validationDetails: { file: ["must be at most 1048576 bytes"] } });

        await expect(importExamplesV2("ds-1", "big", "text/csv")).rejects.toThrow("The file is too large: it must be at most 1048576 bytes.");
    });

    test("names a JSON import examples.json", async () => {
        respond(200, { data: { created: 1, skipped: 0, dropped: 0, limit: 5000 } });

        await importExamplesV2("ds-1", "[]", "application/json");

        expect(((fetchMock.mock.calls[0][1]?.body as FormData).get("file") as File).name).toBe("examples.json");
    });

    test("uploads, downloads and removes an example file", async () => {
        respond(200, { data: exampleFile });
        respondBlob("bytes");
        respond(204);

        const file = new File(["abc"], "in.txt", { type: "text/plain" });
        const uploaded = await uploadExampleArtifactV2("ex-1", "input", file, "notes");
        const blob = await getExampleArtifactBlobV2("ex-1", "expected", "gold file.txt", 2);
        await deleteExampleArtifactV2("ex-1", "input");

        const form = fetchMock.mock.calls[0][1]?.body as FormData;
        expect(fetchMock.mock.calls[0][0]).toBe(`${BASE}/examples/ex-1/inputArtifact`);
        expect(fetchMock.mock.calls[0][1]?.method).toBe("PUT");
        expect((form.get("file") as File).name).toBe("in.txt");
        expect(form.get("description")).toBe("notes");
        expect(uploaded).toEqual(exampleFile);
        expect(call(1).url).toBe(`${BASE}/examples/ex-1/expectedArtifact/gold%20file.txt?version=2`);
        expect(await blob.text()).toBe("bytes");
        expect(call(2)).toMatchObject({ url: `${BASE}/examples/ex-1/inputArtifact`, method: "DELETE" });
    });

    test("maps an evaluator's evaluatorType to type and sends it back on create", async () => {
        respond(200, { data: evaluator() });
        respond(201, { data: evaluator({ id: "ev-2" }) });

        const ev = await getEvaluatorV2("ev-1");
        await createEvaluatorV2({ name: "Judge", type: "llm_judge", model: "general", promptTemplate: "Grade {{ Response }}" });

        expect(ev).toEqual({
            id: "ev-1",
            name: "Judge",
            type: "llm_judge",
            isDefault: false,
            model: "general",
            promptTemplate: "Grade {{ Response }}",
            choiceScores: { A: 1, B: 0 },
            experimentCount: 0,
            createdBy: "u1",
            updatedBy: "u1",
            createdAt: "2026-01-02T03:04:05.678Z",
            updatedAt: "",
        });
        expect(call(1)).toEqual({ url: `${BASE}/evaluators`, method: "POST", body: { name: "Judge", evaluatorType: "llm_judge", model: "general", promptTemplate: "Grade {{ Response }}" } });
    });

    test("maps a decision evaluator's question, scoring and escalation", async () => {
        respond(200, {
            data: evaluator({
                evaluatorType: "decision",
                promptTemplate: null,
                question: {
                    type: "choice",
                    instructions: "Is it relevant?",
                    criteria: [
                        { key: "yes", description: null },
                        { key: "no", description: "Off topic" },
                    ],
                },
                scoring: "expected",
                reads: ["prompt", "response"],
                minConfidence: 0.7,
                escalateToEvaluatorId: "ev-9",
            }),
        });

        expect(await getEvaluatorV2("ev-1")).toMatchObject({
            type: "decision",
            question: { type: "choice", instructions: "Is it relevant?", criteria: [{ key: "yes" }, { key: "no", description: "Off topic" }] },
            scoring: "expected",
            reads: ["prompt", "response"],
            minConfidence: 0.7,
            escalateToEvaluatorId: "ev-9",
        });
    });

    test("updates an evaluator with PATCH", async () => {
        respond(200, { data: evaluator({ description: "Judges live runs" }) });

        await updateEvaluatorV2("ev-1", { description: "Judges live runs" });

        expect(call(0)).toEqual({ url: `${BASE}/evaluators/ev-1`, method: "PATCH", body: { description: "Judges live runs" } });
    });

    test("says an evaluator name is taken", async () => {
        respond(409, { message: "Unable to create evaluator", errorId: "e1" });
        respond(409, { message: "Unable to update evaluator", errorId: "e2" });
        respond(409, { message: "Unable to update evaluator", errorId: "e3" });

        await expect(createEvaluatorV2({ name: "Judge", type: "llm_judge" })).rejects.toThrow("An evaluator with this name already exists.");
        await expect(updateEvaluatorV2("ev-1", { name: "Judge" })).rejects.toThrow("An evaluator with this name already exists.");
        await expect(updateEvaluatorV2("ev-1", { description: "Built in" })).rejects.toBeInstanceOf(ApiV2Error);
    });

    test("lists every page of evaluators", async () => {
        respond(200, page([evaluator()], 2));
        respond(200, page([evaluator({ id: "ev-2" })]));

        const list = await getEvaluatorsV2();

        expect(list.map(e => e.id)).toEqual(["ev-1", "ev-2"]);
        expect(call(0).url).toBe(`${BASE}/evaluators?pageNumber=1&pageSize=100`);
        expect(call(1).url).toBe(`${BASE}/evaluators?pageNumber=2&pageSize=100`);
    });

    test("deletes an evaluator, and says why one another evaluator escalates to is kept", async () => {
        respond(204);
        respond(422, { message: "Unable to delete evaluator", errorId: "e1" });

        await deleteEvaluatorV2("ev-1");
        await expect(deleteEvaluatorV2("ev-2")).rejects.toThrow("Another evaluator escalates to this one. Change or remove its escalation first.");

        expect(call(0)).toMatchObject({ url: `${BASE}/evaluators/ev-1`, method: "DELETE" });
    });
});
