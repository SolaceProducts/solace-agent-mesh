// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { EvaluationsApi } from "@/api/evaluations";

import { downloadBlob } from "@/utils/download";
import { uuid } from "@/utils/uuid";

import type { EvalArtifact, EvalArtifactRole, EvalDataset, EvalEvaluator, EvalExample, EvalExperiment, EvalFailureKind, EvalRun, EvalRunResult, ExpectedFacts, ExpectedPath, ExperimentModelConfig } from "@/types/eval";

import { getRenderType } from "@/features/chat/preview";

const UNTITLED_PLACEHOLDER_RE = /^Untitled Dataset(\s*\([0-9a-f]{8}\))?$/;

export function isUntitledPlaceholder(name: string): boolean {
    return UNTITLED_PLACEHOLDER_RE.test(name);
}

export function createUntitledDataset(evaluations: EvaluationsApi): Promise<EvalDataset> {
    return evaluations.createDataset({
        name: `Untitled Dataset (${uuid().slice(0, 8)})`,
        description: "",
    });
}

export function validateRunsPerExample(value: number): { error: string | null; warning: string | null } {
    let error: string | null = null;
    if (value < 1) {
        error = "At least 1 run is required per example";
    } else if (value >= 5) {
        error = "There is a maximum of 5 runs to avoid long waits.";
    }
    const warning = !error && value === 1 ? "We recommend a minimum of 2 for the most reliable results" : null;
    return { error, warning };
}

export const DEFAULT_MAX_WORKERS = 4;
export const MIN_MAX_WORKERS = 1;
export const MAX_MAX_WORKERS = 20;
export const MAX_WORKERS_ERROR = `Max Workers must be a whole number from ${MIN_MAX_WORKERS} to ${MAX_MAX_WORKERS}.`;

export function validateMaxWorkers(value: number): string | null {
    return Number.isInteger(value) && value >= MIN_MAX_WORKERS && value <= MAX_MAX_WORKERS ? null : MAX_WORKERS_ERROR;
}

export function getTypeLabel(evaluator: EvalEvaluator): string {
    if (evaluator.type === "llm_judge") return "LLM as a Judge";
    if (evaluator.type === "heuristic") return "Heuristic";
    if (evaluator.type === "decision") return "Decision Model as a Judge";
    return evaluator.type;
}

export const OWN_MODEL_LABEL = "Own model";

export function createExperimentModelLookup(experimentModels?: ExperimentModelConfig[]) {
    return {
        aliasFor: (id: string) => {
            const m = experimentModels?.find(m => m.modelConfigId === id);
            if (!m && !id) return OWN_MODEL_LABEL;
            const alias = m?.alias?.trim();
            if (alias) return alias;
            const model = m?.model?.trim();
            if (model) return model;
            return id;
        },
        rawModelFor: (id: string) => experimentModels?.find(m => m.modelConfigId === id)?.model,
    };
}

export function sortModelsByExperimentOrder(modelIds: string[], experimentModels?: ExperimentModelConfig[]): string[] {
    const deduped = Array.from(new Set(modelIds));
    if (!experimentModels?.length) return deduped;
    const orderById = new Map(experimentModels.map((m, i) => [m.modelConfigId, i]));
    return deduped.sort((a, b) => {
        const ai = orderById.get(a) ?? Number.MAX_SAFE_INTEGER;
        const bi = orderById.get(b) ?? Number.MAX_SAFE_INTEGER;
        return ai - bi;
    });
}

export function deriveExperimentModels(experimentModels: ExperimentModelConfig[] | undefined, runs: EvalRun[] | undefined): ExperimentModelConfig[] | undefined {
    const base = experimentModels ?? [];
    if (!runs?.length) return experimentModels;
    const covered = new Set(base.map(m => m.modelConfigId));
    const derived: ExperimentModelConfig[] = [];
    for (const r of runs) {
        const key = r.model ?? "";
        if (covered.has(key)) continue;
        covered.add(key);
        const mc = r.configSnapshot?.agent?.modelConfig;
        if (!mc) continue;
        derived.push({ modelConfigId: key, alias: mc.alias ?? "", model: mc.modelName ?? "" });
    }
    return derived.length ? [...base, ...derived] : experimentModels;
}

export function scoreColorClass(score: number): string {
    if (score >= 0.75) return "text-(--success-wMain)";
    if (score >= 0.5) return "text-(--warning-w100)";
    return "text-(--error-wMain)";
}

export function scoreBgClass(score: number): string {
    if (score >= 0.75) return "bg-(--success-wMain)/10";
    if (score >= 0.5) return "bg-(--warning-wMain)/10";
    return "bg-(--error-wMain)/10";
}

export function coerceArtifactVersion(version: number | string | undefined): number | undefined {
    if (typeof version === "number") return Number.isNaN(version) ? undefined : version;
    if (typeof version === "string" && version !== "") {
        const n = Number(version);
        return Number.isNaN(n) ? undefined : n;
    }
    return undefined;
}

export const MAX_EVALUATORS_PER_EXPERIMENT = 5;

export const MAX_MODELS_PER_EXPERIMENT = 3;

export function addEvaluatorRespectingCap(currentIds: string[], newId: string, cap: number = MAX_EVALUATORS_PER_EXPERIMENT): string[] {
    if (currentIds.includes(newId)) return currentIds;
    if (currentIds.length >= cap) return currentIds;
    return [...currentIds, newId];
}

export function buildModelColorIndex(models: ExperimentModelConfig[]): Map<string, number> {
    const index = new Map<string, number>();
    models.forEach((m, i) => {
        if (m.modelConfigId) index.set(m.modelConfigId, i);
    });
    return index;
}

export function isDatasetChangedSinceLastRun(experiment: EvalExperiment | null | undefined): boolean {
    if (!experiment?.datasetUpdatedTime || !experiment.latestRunTime) return false;
    return experiment.datasetUpdatedTime > experiment.latestRunTime;
}

export function hasActiveRun(runs: readonly Pick<EvalRun, "status">[]): boolean {
    return runs.some(r => r.status === "pending" || r.status === "running");
}

export function isRunGroupOutdated(runs: EvalRun[], experiment: EvalExperiment | null): boolean {
    if (!experiment) return false;
    const snap = runs.find(r => r.configSnapshot)?.configSnapshot?.experiment;
    if (!snap) return false;
    const snapModels = snap.models;
    if (Array.isArray(snapModels)) {
        const snapKeys = snapModels
            .map(m => m?.model)
            .filter((s): s is string => Boolean(s))
            .sort((a, b) => a.localeCompare(b));
        const currentKeys = experiment.models.map(m => m.model).sort((a, b) => a.localeCompare(b));
        if (snapKeys.length !== currentKeys.length || snapKeys.some((k, i) => k !== currentKeys[i])) return true;
    }
    if (snap.dataset_id !== undefined && snap.dataset_id !== experiment.datasetId) {
        return true;
    }
    return false;
}

export const MODEL_COLORS = ["#3C69E1", "#7841A8", "#7CD3F6", "#009193", "#F66651", "#FCA829", "#CB1AA5", "#3A4880"];

interface InputArtifactRow {
    readonly perModelTrials: Record<string, EvalRunResult[]>;
}

const RENDER_TYPE_NEVER_PREVIEWED_INLINE = "html";

export function evalArtifactRenderType(filename: string, mimeType?: string): string | null {
    const type = getRenderType(filename, mimeType);
    return type === RENDER_TYPE_NEVER_PREVIEWED_INLINE ? null : type;
}

export async function downloadExampleArtifact(evaluations: EvaluationsApi, example: EvalExample, role: EvalArtifactRole): Promise<void> {
    const artifact = role === "expected" ? example.expectedArtifact : example.inputArtifact;
    if (!artifact) return;
    const blob = await evaluations.getExampleArtifactBlob(example.id, role, artifact.filename, artifact.version);
    downloadBlob(blob, artifact.filename);
}

export interface ExampleArtifactEntry {
    readonly artifact: EvalArtifact;
    readonly role: EvalArtifactRole;
    readonly resultId: string;
}

export function exampleArtifactEntryKey(role: EvalArtifactRole, artifact: EvalArtifact): string {
    return `${role}:${artifact.filename}@${artifact.version ?? ""}`;
}

export function collectExampleArtifacts(row: InputArtifactRow, models: string[]): Map<string, ExampleArtifactEntry> {
    const byKey = new Map<string, ExampleArtifactEntry>();
    const record = (role: EvalArtifactRole, artifact: EvalArtifact | null | undefined, resultId: string) => {
        if (!artifact) return;
        const key = exampleArtifactEntryKey(role, artifact);
        if (!byKey.has(key)) byKey.set(key, { artifact, role, resultId });
    };
    for (const model of models) {
        for (const result of row.perModelTrials[model] ?? []) {
            record("input", result.inputArtifact, result.id);
            record("expected", result.expectedArtifact, result.id);
        }
    }
    return byKey;
}

export const DUPLICATE_NAME_ERROR = "An experiment with this name already exists.";
export function agentSectionTitle(group: { isTarget: boolean; nodeIds?: string[] }, label: string, targetIsWorkflow = false): string {
    if (group.nodeIds && group.nodeIds.length > 0) {
        const steps = group.nodeIds.join(", ");
        return group.nodeIds.length === 1 ? `Required by the ${steps} step, which calls the ${label} agent` : `Required by the ${steps} steps, which call the ${label} agent`;
    }
    if (group.isTarget) {
        return targetIsWorkflow ? `Required by tool steps in ${label}` : label;
    }
    return `Delegates to: ${label}`;
}

export interface ExampleProblem {
    readonly sequenceNumber: number;
    readonly position: number;
    readonly problems: string[];
}

const EXAMPLE_DETAIL_KEY = /^examples\[(\d+)\]/;
const SCHEMA_VIOLATION = /^Validation error at '[^']*': at '([^']*)': (.*)$/;
const MISSING_FIELDS = /^missing propert(?:y|ies) (.+)$/;
const EXTRA_FIELDS = /^additional properties (.+) not allowed$/;
const ALLOWED_VALUES = /^value must be (?:one of )?(.+)$/;
const WRONG_TYPE = /^got (\w+), want (.+)$/;
const ESCAPED_QUOTE = String.raw`\'`;
const LISTED_ITEM = /'((?:[^'\\]|\\.)*)'|([^,\s][^,]*)/g;

function listedItems(text: string): string[] {
    return [...text.matchAll(LISTED_ITEM)].map(m => (m[1] ?? m[2]).replaceAll(ESCAPED_QUOTE, "'").trim());
}

function listedValues(text: string): string[] {
    return [...text.matchAll(LISTED_ITEM)].map(m => (m[1] === undefined ? m[2].trim() : JSON.stringify(m[1].replaceAll(ESCAPED_QUOTE, "'"))));
}

const withArticle = (type: string) => `${/^[aeiou]/.test(type) ? "an" : "a"} ${type}`;
const fieldIn = (parent: string, name: string) => (parent ? `${parent}.${name}` : name);

function valueAt(input: Record<string, unknown> | null | undefined, field: string): unknown {
    let value: unknown = input ?? undefined;
    for (const key of field.split(".")) {
        if (value === null || typeof value !== "object") return undefined;
        value = (value as Record<string, unknown>)[key];
    }
    return value;
}

function shownValue(value: unknown): string {
    return typeof value === "string" ? `"${value}"` : JSON.stringify(value);
}

function readableProblem(problem: string, input?: Record<string, unknown> | null): string {
    const match = SCHEMA_VIOLATION.exec(problem);
    if (!match) return problem;
    const field = match[1].replace(/^\//, "").replaceAll("/", ".");
    const detail = match[2];
    const missing = MISSING_FIELDS.exec(detail);
    if (missing) {
        const names = listedItems(missing[1]).map(name => fieldIn(field, name));
        return names.length === 1 ? `The input data does not include the required field ${names[0]}. Add this field to the input data.` : `The input data does not include the required fields ${names.join(", ")}. Add these fields to the input data.`;
    }
    const extra = EXTRA_FIELDS.exec(detail);
    if (extra) {
        const names = listedItems(extra[1]).map(name => fieldIn(field, name));
        return names.length === 1
            ? `The input data includes the field ${names[0]}, which this workflow does not accept. Remove it from the input data.`
            : `The input data includes the fields ${names.join(", ")}, which this workflow does not accept. Remove them from the input data.`;
    }
    const allowed = ALLOWED_VALUES.exec(detail);
    if (allowed && field) return notAllowedValue(field, listedValues(allowed[1]), valueAt(input, field));
    const wrongType = WRONG_TYPE.exec(detail);
    if (wrongType) {
        const subject = field ? `The ${field} field in the input data` : "The input data";
        return `${subject} must be ${withArticle(wrongType[2])}, but the example sent ${withArticle(wrongType[1])}. Change the value in the input data.`;
    }
    return field ? `The ${field} field in the input data: ${detail}` : `The input data: ${detail}`;
}

function notAllowedValue(field: string, allowed: string[], sent: unknown): string {
    const change = allowed.length === 1 ? `to ${allowed[0]}.` : `to one of these values: ${allowed.join(", ")}.`;
    return sent === undefined
        ? `The ${field} field in the input data has a value that this workflow does not accept. Change the value ${change}`
        : `The ${field} field in the input data is ${shownValue(sent)}, which this workflow does not accept. Change it ${change}`;
}

export function exampleProblemsSummary(count: number, workflowName: string): string {
    const examples = count === 1 ? "1 example" : `${count} examples`;
    return `The input data in ${examples} does not match ${workflowName || "this workflow"}. Correct the examples listed.`;
}

export function exampleProblemsOf(details: Record<string, string[]> | undefined, examples: readonly Pick<EvalExample, "sequenceNumber" | "inputData">[] = []): ExampleProblem[] {
    const ordered = [...examples].sort((a, b) => a.sequenceNumber - b.sequenceNumber);
    const bySequence = new Map<number, string[]>();
    for (const [key, messages] of Object.entries(details ?? {})) {
        const match = EXAMPLE_DETAIL_KEY.exec(key);
        if (!match) continue;
        const sequenceNumber = Number(match[1]);
        const input = ordered.find(e => e.sequenceNumber === sequenceNumber)?.inputData;
        bySequence.set(sequenceNumber, [...(bySequence.get(sequenceNumber) ?? []), ...messages.map(m => readableProblem(m, input))]);
    }
    return [...bySequence.entries()]
        .sort(([a], [b]) => a - b)
        .map(([sequenceNumber, problems]) => {
            const row = ordered.findIndex(e => e.sequenceNumber === sequenceNumber);
            return { sequenceNumber, position: row >= 0 ? row + 1 : sequenceNumber, problems };
        });
}

interface TrialsOfExample {
    readonly prompt: string;
    readonly perModelTrials: Record<string, readonly Pick<EvalRunResult, "targetType">[]>;
}

export function isWorkflowRow(row: Pick<TrialsOfExample, "perModelTrials">): boolean {
    return Object.values(row.perModelTrials).some(trials => trials.some(r => r.targetType === "workflow"));
}

export function exampleLabel(row: TrialsOfExample): string {
    if (!isWorkflowRow(row)) return row.prompt;
    const { description, input } = splitWorkflowPrompt(row.prompt);
    return description || input || "";
}

const WORKFLOW_INPUT_SEPARATOR = "\n\nInput: ";

export function splitWorkflowPrompt(prompt: string): { description?: string; input?: string } {
    const at = prompt.indexOf(WORKFLOW_INPUT_SEPARATOR);
    if (at >= 0) return { description: prompt.slice(0, at), input: prompt.slice(at + WORKFLOW_INPUT_SEPARATOR.length) };
    return prompt.trimStart().startsWith("{") ? { input: prompt } : { description: prompt };
}

export function examplesDescription(withInputData: boolean): string {
    return withInputData ? "Test cases for your dataset — each has a prompt, or a JSON input for a workflow, and an optional expected response." : "Test cases for your dataset — each has a prompt (required) and an optional expected response.";
}

export const INPUT_DATA_PLACEHOLDER = '{"orderId": "O-5003"}';

export function formatInputData(inputData: Record<string, unknown> | null | undefined): string {
    return inputData ? JSON.stringify(inputData) : "";
}

export function isWorkflowNotDeployed(option: { deploymentStatus?: string } | undefined): boolean {
    return option?.deploymentStatus !== undefined && option.deploymentStatus !== "deployed";
}

export const WORKFLOW_EVALS_OFF = "Workflow evaluations are turned off for this deployment, so an experiment that evaluates a workflow cannot run or be copied. Ask your administrator to turn them on.";

export const WORKFLOW_DEPLOY_STATE_UNKNOWN = "The list of workflows has not loaded, so this page cannot check that the workflow is deployed. Wait a moment, or reload the page, and run the experiment again.";

export function workflowRunCheck(
    workflows: { options: { name: string; displayName: string; deploymentStatus?: string }[]; readable?: boolean; isLoading: boolean; isError: boolean },
    workflowName: string | null | undefined,
    workflowEvalsEnabled = true
): { state: "off" } | { state: "unknown" } | { state: "not_deployed"; displayName: string } | { state: "ok" } {
    if (!workflowEvalsEnabled) return { state: "off" };
    if (workflows.readable === false) return { state: "ok" };
    if (workflows.isLoading || workflows.isError) return { state: "unknown" };
    const option = workflows.options.find(o => o.name === workflowName);
    if (isWorkflowNotDeployed(option)) return { state: "not_deployed", displayName: option?.displayName ?? workflowName ?? "" };
    return { state: "ok" };
}

export function isWorkflowExperiment(experiment: Pick<EvalExperiment, "targetType"> | null | undefined): boolean {
    return experiment?.targetType === "workflow";
}

export function experimentTargetName(experiment: Pick<EvalExperiment, "targetType" | "targetAgent" | "targetWorkflow">): string {
    return isWorkflowExperiment(experiment) ? (experiment.targetWorkflow ?? "") : experiment.targetAgent;
}

const FAILURE_KIND_LABELS: Record<EvalFailureKind, string> = {
    auth_required: "Authorization required",
    node_failed: "Step failed",
    paused_for_input: "Stopped at approval step",
    timeout: "Timed out",
    cancelled: "Cancelled",
    invalid_input: "Input not accepted",
    rejected: "Request refused",
    partial_failure: "Completed with a failed step",
};

export interface TrialOutcomeText {
    readonly title: string;
    readonly body: string;
    readonly details?: string;
    readonly tone: "error" | "warning";
}

const GO_QUOTED = String.raw`"((?:[^"\\]|\\.)*)"`;
const FAILURE_HEAD = new RegExp(String.raw`^(?:Workflow "(?:[^"\\]|\\.)*" failed: )?((?:[Nn]ode "(?:[^"\\]|\\.)*" (?:failed(?: and was skipped)?: |asked for human input))*)(?:tool ${GO_QUOTED} (?:error|failed|dispatch))?`);
const HEAD_NODE = new RegExp(String.raw`[Nn]ode ${GO_QUOTED}`, "g");
const unquoteGo = (name: string) => name.replaceAll(/\\(.)/g, "$1");

function failurePoint(message: string): { step?: string; tool?: string } {
    const head = FAILURE_HEAD.exec(message);
    if (!head) return {};
    const step = [...head[1].matchAll(HEAD_NODE)].at(-1)?.[1];
    return { step: step === undefined ? undefined : unquoteGo(step), tool: head[2] === undefined ? undefined : unquoteGo(head[2]) };
}
const NOT_SCORED = "This run is not included in the scores.";

const toolErrorClause = (tool: string | undefined) => (tool ? " because the " + tool + " tool returned an error" : "");

export function trialOutcomeText(kind: string, message: string): TrialOutcomeText {
    const { step, tool } = failurePoint(message);
    const theStep = step ? `The ${step} step` : "A step";
    switch (kind) {
        case "node_failed":
            return {
                tone: "error",
                title: "Unable to complete run",
                body: tool ? `${theStep} failed because the ${tool} tool returned an error. ${NOT_SCORED} Check that the service this tool connects to is available.` : `${theStep} failed. ${NOT_SCORED} Open the details to see the error.`,
                details: message,
            };
        case "paused_for_input":
            return {
                tone: "error",
                title: "Run stopped at approval step",
                body: `${theStep} requires approval from a person, and experiment runs cannot wait for approval. ${NOT_SCORED} Use examples that do not reach this step.`,
                details: message,
            };
        case "partial_failure":
            return {
                tone: "warning",
                title: "Run completed with a failed step",
                body: `${theStep} failed${toolErrorClause(tool)}, so the workflow completed without that branch. The scores include this run.`,
                details: message || undefined,
            };
        default:
            return { tone: "error", title: `Unable to complete run: ${failureKindLabel(kind) ?? kind}`, body: message };
    }
}

export function trialProblem(result: Pick<EvalRunResult, "errorMessage" | "failureKind" | "targetType"> | undefined): "ended" | "error" | null {
    if (!result || result.failureKind === "partial_failure") return null;
    if (result.failureKind && result.targetType === "workflow") return "ended";
    return result.errorMessage ? "error" : null;
}

export type ScoreCellState = { state: "pending" } | { state: "err" } | { state: "ended" } | { state: "missing" } | { state: "score"; value: number };

export function cellStateOf(result: EvalRunResult | undefined, score: number | undefined, problem: "ended" | "error" | null): ScoreCellState {
    if (!result) return { state: "pending" };
    if (problem === "error") return { state: "err" };
    if (score != null) return { state: "score", value: score };
    return problem === "ended" ? { state: "ended" } : { state: "missing" };
}

export function failureKindLabel(kind: string | undefined | null): string | undefined {
    if (!kind) return undefined;
    return FAILURE_KIND_LABELS[kind as EvalFailureKind] ?? kind;
}

export const EXPECTED_PATH_PLACEHOLDER = '{"nodes": {"approve": "succeeded"}, "tools": {"issue_refund": "called"}}';

const NODE_OUTCOMES: readonly string[] = ["ran", "not_ran", "succeeded", "failed", "skipped"];
const TOOL_USES: readonly string[] = ["called", "not_called"];

export function formatExpectedPath(path: ExpectedPath | null | undefined): string {
    return path ? JSON.stringify(path) : "";
}

function pathAssertionError(group: unknown, kind: "nodes" | "tools", allowed: readonly string[]): string | undefined {
    if (group === undefined) return undefined;
    if (group === null || typeof group !== "object" || Array.isArray(group)) {
        return `"${kind}" must be an object.`;
    }
    for (const [name, expectation] of Object.entries(group)) {
        if (typeof expectation !== "string" || !allowed.includes(expectation)) {
            return `${kind === "nodes" ? "Node" : "Tool"} "${name}" must be one of ${allowed.join(", ")}.`;
        }
    }
    return undefined;
}

export function parseExpectedPathText(text: string): { value?: ExpectedPath; error?: string } {
    if (text.trim() === "") return {};
    let parsed: unknown;
    try {
        parsed = JSON.parse(text);
    } catch {
        return { error: "Expected steps are not valid JSON." };
    }
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
        return { error: `Expected steps must be a JSON object, like ${EXPECTED_PATH_PLACEHOLDER}.` };
    }
    const unknownKey = Object.keys(parsed).find(key => key !== "nodes" && key !== "tools");
    if (unknownKey) return { error: `Expected steps hold only "nodes" and "tools", not "${unknownKey}".` };
    const { nodes, tools } = parsed as { nodes?: unknown; tools?: unknown };
    const error = pathAssertionError(nodes, "nodes", NODE_OUTCOMES) ?? pathAssertionError(tools, "tools", TOOL_USES);
    if (error) return { error };
    if (Object.keys(nodes ?? {}).length + Object.keys(tools ?? {}).length === 0) {
        return { error: "Expected steps need at least one node or tool." };
    }
    return { value: parsed as ExpectedPath };
}

export interface TrialConsistency {
    readonly examples: number;
    readonly passedEvery: number;
    readonly inconsistent: number;
    readonly trials: number;
}

export function trialVerdicts(trials: readonly EvalRunResult[] | undefined, evaluatorId: string): { passed: number; judged: number } {
    let passed = 0;
    let judged = 0;
    for (const trial of trials ?? []) {
        const verdict = trial.scores?.find(s => s.evaluatorId === evaluatorId)?.passed;
        if (verdict == null) continue;
        judged++;
        if (verdict) passed++;
    }
    return { passed, judged };
}

export function trialsDisagree(verdicts: { passed: number; judged: number }): boolean {
    return verdicts.judged >= 2 && verdicts.passed > 0 && verdicts.passed < verdicts.judged;
}

export function trialConsistency(resultsByModel: Record<string, EvalRunResult[]>, models: readonly string[], evaluatorId: string): TrialConsistency | null {
    let examples = 0;
    let passedEvery = 0;
    let inconsistent = 0;
    let trials = 0;
    for (const model of models) {
        const byExample = new Map<string, EvalRunResult[]>();
        for (const r of resultsByModel[model] ?? []) {
            const key = r.exampleId ?? r.id;
            byExample.set(key, [...(byExample.get(key) ?? []), r]);
        }
        for (const exampleTrials of byExample.values()) {
            const verdicts = trialVerdicts(exampleTrials, evaluatorId);
            if (verdicts.judged < 2) continue;
            examples++;
            trials = Math.max(trials, verdicts.judged);
            if (verdicts.passed === verdicts.judged) passedEvery++;
            else if (verdicts.passed > 0) inconsistent++;
        }
    }
    return examples === 0 ? null : { examples, passedEvery, inconsistent, trials };
}

const examplesCount = (n: number) => (n === 1 ? "1 example" : `${n} examples`);

export function consistencyLine(c: TrialConsistency): string {
    const line = `${c.passedEvery} of ${examplesCount(c.examples)} passed all runs.`;
    if (c.inconsistent === 0) return line;
    return `${line} ${examplesCount(c.inconsistent)} partially failed.`;
}

export const EXPECTED_FACTS_PLACEHOLDER = '{"required": ["purchase"], "forbidden": ["refund"]}';

export function formatExpectedFacts(facts: ExpectedFacts | null | undefined): string {
    return facts ? JSON.stringify(facts) : "";
}

function factListError(list: unknown, kind: "required" | "forbidden"): string | undefined {
    if (list === undefined) return undefined;
    if (!Array.isArray(list) || list.some(v => typeof v !== "string" || v.trim() === "")) {
        return `"${kind}" must be a list of non-blank strings.`;
    }
    return undefined;
}

export function parseExpectedFactsText(text: string): { value?: ExpectedFacts; error?: string } {
    if (text.trim() === "") return {};
    let parsed: unknown;
    try {
        parsed = JSON.parse(text);
    } catch {
        return { error: "Expected values are not valid JSON." };
    }
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
        return { error: `Expected values must be a JSON object, like ${EXPECTED_FACTS_PLACEHOLDER}.` };
    }
    const unknownKey = Object.keys(parsed).find(key => key !== "required" && key !== "forbidden");
    if (unknownKey) return { error: `Expected values hold only "required" and "forbidden", not "${unknownKey}".` };
    const { required, forbidden } = parsed as { required?: unknown; forbidden?: unknown };
    const error = factListError(required, "required") ?? factListError(forbidden, "forbidden");
    if (error) return { error };
    if (((required as string[] | undefined)?.length ?? 0) + ((forbidden as string[] | undefined)?.length ?? 0) === 0) {
        return { error: "Expected values need at least one required or forbidden value." };
    }
    return { value: parsed as ExpectedFacts };
}

const MAX_EXACT_SIGNIFICANT_DIGITS = 15;
const NUMBER_CHARS = new Set("-+.eE0123456789");

function endOfString(text: string, start: number): number {
    let i = start + 1;
    while (i < text.length && text[i] !== '"') i += text[i] === "\\" ? 2 : 1;
    return i + 1;
}

function endOfNumber(text: string, start: number): number {
    let i = start;
    while (i < text.length && NUMBER_CHARS.has(text[i])) i += 1;
    return i;
}

const startsNumber = (c: string) => c === "-" || (c >= "0" && c <= "9");

function jsonNumberTokens(text: string): string[] {
    const tokens: string[] = [];
    let i = 0;
    while (i < text.length) {
        if (text[i] === '"') {
            i = endOfString(text, i);
        } else if (startsNumber(text[i])) {
            const end = endOfNumber(text, i);
            tokens.push(text.slice(i, end));
            i = end;
        } else {
            i += 1;
        }
    }
    return tokens;
}

function significantDigits(token: string): string {
    const exponent = token.search(/[eE]/);
    const mantissa = (exponent === -1 ? token : token.slice(0, exponent)).replace("-", "").replace(".", "");
    let start = 0;
    let end = mantissa.length;
    while (start < end && mantissa[start] === "0") start += 1;
    while (end > start && mantissa[end - 1] === "0") end -= 1;
    return mantissa.slice(start, end);
}

function inexactNumber(text: string): string | undefined {
    return jsonNumberTokens(text).find(token => {
        const value = Number(token);
        if (!Number.isFinite(value)) return true;
        const integer = !token.includes(".") && !token.toLowerCase().includes("e");
        return integer ? !Number.isSafeInteger(value) : significantDigits(token).length > MAX_EXACT_SIGNIFICANT_DIGITS;
    });
}

export function parseInputDataText(text: string): { value?: Record<string, unknown>; error?: string } {
    if (text.trim() === "") return {};
    let parsed: unknown;
    try {
        parsed = JSON.parse(text);
    } catch {
        return { error: "Input is not valid JSON." };
    }
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
        return { error: 'Input must be a JSON object, like {"orderId": "O-1"}.' };
    }
    const inexact = inexactNumber(text);
    if (inexact) {
        return { error: `The number ${inexact} cannot be kept exactly. Send it as a string instead, like "${inexact}".` };
    }
    return { value: parsed as Record<string, unknown> };
}
