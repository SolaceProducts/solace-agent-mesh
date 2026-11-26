// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { DecisionQuestionConfig, WorkflowNodeConfig } from "./agentUtils";

export interface DecisionQuestion {
    key: string;
    type: DecisionQuestionConfig["type"];
    instructions: string;
    options: string[];
}

export function decisionQuestionTypeLabel(type: DecisionQuestion["type"]): string {
    return type.charAt(0).toUpperCase() + type.slice(1);
}

export function routedQuestionFirst(questions: DecisionQuestion[], routeOn: string | undefined): DecisionQuestion[] {
    const routed = questions.find(question => question.key === routeOn);
    return routed ? [routed, ...questions.filter(question => question !== routed)] : questions;
}

export type DecisionBranchKind = "route" | "default" | "escalate";

export interface DecisionBranch {
    kind: DecisionBranchKind;
    labels: string[];
    target: string;
}

function entryKeys(entry: unknown): string[] {
    if (typeof entry === "string") {
        return [entry];
    }
    return entry && typeof entry === "object" ? Object.keys(entry) : [];
}

function optionKeys(criteria: DecisionQuestionConfig["criteria"]): string[] {
    if (Array.isArray(criteria)) {
        return criteria.flatMap(entryKeys);
    }
    return criteria && typeof criteria === "object" ? Object.keys(criteria) : [];
}

export function decisionQuestions(node: Pick<WorkflowNodeConfig, "questions">): DecisionQuestion[] {
    const { questions } = node;
    if (!questions) return [];
    const entries: Array<[string, DecisionQuestionConfig]> = Array.isArray(questions) ? questions.map(q => [q.key ?? "", q]) : Object.entries(questions);
    return entries.filter(([key, q]) => key !== "" && q && typeof q === "object").map(([key, q]) => ({ key, type: q.type, instructions: q.instructions ?? "", options: optionKeys(q.criteria) }));
}

function escalateLabel(node: Pick<WorkflowNodeConfig, "min_confidence">): string {
    return node.min_confidence ? `escalate < ${confidenceThresholdLabel(node.min_confidence)}` : "escalate";
}

const BRANCH_KIND_PRECEDENCE: Record<DecisionBranchKind, number> = { default: 0, route: 1, escalate: 2 };

export function decisionBranches(node: Pick<WorkflowNodeConfig, "routes" | "default" | "escalate" | "min_confidence">): DecisionBranch[] {
    const byTarget = new Map<string, DecisionBranch>();
    const add = (kind: DecisionBranchKind, label: string, target: string | undefined) => {
        if (!target) return;
        const branch = byTarget.get(target);
        if (!branch) {
            byTarget.set(target, { kind, labels: [label], target });
            return;
        }
        branch.labels.push(label);
        if (BRANCH_KIND_PRECEDENCE[kind] > BRANCH_KIND_PRECEDENCE[branch.kind]) branch.kind = kind;
    };
    for (const [answer, target] of Object.entries(node.routes ?? {})) add("route", answer, target);
    add("default", "default", node.default);
    add("escalate", escalateLabel(node), node.escalate);
    return [...byTarget.values()];
}

type EscalationModeSource = Pick<WorkflowNodeConfig, "escalation_mode" | "escalationMode">;

export function escalationModeOf(node: EscalationModeSource): string | undefined {
    const raw: unknown = [node.escalation_mode, node.escalationMode].find(v => v != null && v !== "");
    return raw === undefined ? undefined : String(raw);
}

export function reroutesEscalation(node: Pick<WorkflowNodeConfig, "escalate"> & EscalationModeSource): boolean {
    const mode = escalationModeOf(node);
    return !!node.escalate && (mode === undefined || mode === "reroute");
}

export const REROUTE_ESCALATE_TARGET_TYPES: ReadonlySet<string> = new Set(["agent", "workflow", "tool", "decision"]);

export function decisionBranchLabel(branch: DecisionBranch): string {
    return branch.labels.join(" / ");
}

export function confidenceThresholdLabel(minConfidence: number): string {
    return `${Math.round(minConfidence * 100)}% confidence`;
}
